package control

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type capability struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Right   string `json:"right"`
	Expires int64  `json:"expires"`
}

const uploadBudget = 5 * time.Minute
const uploadConcurrency = 4
const globalUploadConcurrency = 16

type Upload struct {
	cancel  context.CancelFunc
	done    chan struct{}
	writers map[string]context.CancelFunc
}
type uploadReader struct {
	ctx context.Context
	io.Reader
}

func (r uploadReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

// Called under s.mu. Completed inputs remain readable after task termination;
// pending writes are cancelled and cannot replace those retained bytes.
func (s *Server) retainTransfers(taskID string) []<-chan struct{} {
	done := []<-chan struct{}{}
	for id, t := range s.transfers {
		if t.TaskID != taskID {
			continue
		}
		if oneOf(t.State, "open", "ready") {
			t.State = "settling"
			s.persist(t)
		}
		if upload := s.uploading[id]; upload != nil {
			upload.cancel()
			done = append(done, upload.done)
		}
	}
	return done
}

func (s *Server) uploadAllowed(t *Transfer) error {
	if s.closed {
		return wire.Fail("unavailable", "Service is stopping")
	}
	if t.State != "open" {
		return wire.Fail("immutable", "Upload is read-only")
	}
	if t.TaskID != "" {
		task, err := s.task(t.TaskID)
		if err != nil {
			return err
		}
		if task.Terminal() || task.Purged {
			return wire.Fail("invalid_state", "Upload requires a queued, preparing or running task")
		}
	}
	return nil
}

func (s *Server) capURL(kind, id, right string) (string, time.Time) {
	expires := time.Now().UTC().Add(time.Hour)
	return s.capability(kind, id, right, expires), expires
}
func (s *Server) capability(kind, id, right string, expires time.Time) string {
	b := wire.JSON(capability{kind, id, right, expires.Unix()})
	data := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(data))
	return strings.TrimRight(s.cfg.Service.PublicURL, "/") + "/files/" + data + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Server) decodeCap(token string) (capability, error) {
	var c capability
	p := strings.Split(token, ".")
	if len(p) != 2 || len(token) > 2048 {
		return c, wire.Fail("unauthorized", "Invalid capability")
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil {
		return c, e
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(p[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return c, wire.Fail("unauthorized", "Invalid capability")
	}
	b, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return c, e
	}
	if e = wire.Decode(b, &c); e != nil {
		return c, e
	}
	if c.Expires <= time.Now().Unix() {
		return c, wire.Fail("expired", "Transfer capability expired")
	}
	return c, nil
}
func (s *Server) transferView(t *Transfer) map[string]any {
	right := "read"
	canUpload := s.uploadAllowed(t) == nil
	if canUpload || t.State == "ready" && t.TaskID == "" {
		right = "write"
	}
	u, expires := s.capURL("transfer", t.ID, right)
	return map[string]any{"id": t.ID, "execution_id": t.TaskID, "kind": t.Kind, "state": t.State, "can_upload": canUpload, "uploading": s.uploading[t.ID] != nil, "bytes": t.Bytes, "object_count": len(t.Objects), "reserved_bytes": t.ReservedBytes, "manifest_digest": t.ManifestDigest, "url": u, "expires": expires}
}
func (s *Server) createTransfer(a TransferRequest, id string) (any, error) {
	if a.Kind != "file" && a.Kind != "cache" {
		return nil, wire.Fail("invalid_argument", "Transfer kind must be file or cache")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.admission(); err != nil {
		return nil, err
	}
	t := &Transfer{ID: id, Kind: a.Kind, State: "open", Created: time.Now().UTC(), Updated: time.Now().UTC(), Objects: map[string]Object{}}
	if e := s.root.MkdirAll(path.Join("transfers", t.ID), 0700); e != nil {
		return nil, e
	}
	if e := s.db.CreateTransfer(context.Background(), t, t.Objects); e != nil {
		return nil, e
	}
	s.transfers[t.ID] = t
	return s.transferView(t), nil
}

var cacheKey = regexp.MustCompile(`^[0-9abcdfghijklmnpqrsvwxyz]{32}\.narinfo$`)
var narKey = regexp.MustCompile(`^nar/[A-Za-z0-9][A-Za-z0-9._-]{0,190}\.nar(\.(xz|bz2|zst))?$`)
var narHash = regexp.MustCompile(`^sha256:([0-9a-f]{64}|[01][0-9abcdfghijklmnpqrsvwxyz]{51})$`)

func objectName(kind, name string) bool {
	return kind == "file" && name == "blob" || kind == "cache" && (name == "nix-cache-info" || cacheKey.MatchString(name) || narKey.MatchString(name))
}
func hashFile(r io.Reader, limit int64) (int64, string, error) {
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(r, limit+1))
	if e != nil {
		return 0, "", e
	}
	if n > limit {
		return 0, "", wire.Fail("quota_exceeded", "File exceeds limit")
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}
func nixHash(hexHash string) string {
	b, _ := hex.DecodeString(hexHash)
	alphabet := "0123456789abcdfghijklmnpqrsvwxyz"
	var out strings.Builder
	for n := (len(b)*8 - 1) / 5; n >= 0; n-- {
		bit := n * 5
		i := bit / 8
		shift := uint(bit % 8)
		v := uint(b[i]) >> shift
		if i+1 < len(b) {
			v |= uint(b[i+1]) << (8 - shift)
		}
		out.WriteByte(alphabet[v&31])
	}
	return out.String()
}

type narInfo struct {
	Store, URL, Hash string
	Size             int64
	References       []string
}

func parseNarInfo(name string, data []byte) (narInfo, error) {
	var n narInfo
	bad := func() (narInfo, error) { return n, wire.Fail("invalid_cache", "Malformed or unsafe narinfo") }
	if !cacheKey.MatchString(name) || len(data) > 65536 {
		return bad()
	}
	fields := map[string]string{}
	scan := bufio.NewScanner(strings.NewReader(string(data)))
	for scan.Scan() {
		line := scan.Text()
		k, v, ok := strings.Cut(line, ": ")
		if !ok || strings.ContainsAny(v, "\r\x00") || !oneOf(k, "StorePath", "URL", "Compression", "FileHash", "FileSize", "NarHash", "NarSize", "References", "Deriver", "Sig", "CA", "System") {
			return bad()
		}
		if _, ok := fields[k]; ok && k != "Sig" {
			return bad()
		}
		fields[k] = v
	}
	if scan.Err() != nil {
		return bad()
	}
	n.Store = fields["StorePath"]
	n.URL = fields["URL"]
	n.Hash = fields["FileHash"]
	size, e := strconv.ParseInt(fields["FileSize"], 10, 64)
	n.Size = size
	if e != nil || size < 1 || !wire.StorePattern.MatchString(n.Store) || !strings.HasPrefix(path.Base(n.Store), strings.TrimSuffix(name, ".narinfo")+"-") || !narKey.MatchString(n.URL) || !oneOf(fields["Compression"], "none", "xz", "bzip2", "zstd") || !narHash.MatchString(fields["NarHash"]) || !narHash.MatchString(n.Hash) {
		return bad()
	}
	if size, e = strconv.ParseInt(fields["NarSize"], 10, 64); e != nil || size < 1 {
		return bad()
	}
	for _, ref := range strings.Fields(fields["References"]) {
		if !wire.StorePattern.MatchString("/nix/store/" + ref) {
			return bad()
		}
		n.References = append(n.References, strings.SplitN(ref, "-", 2)[0]+".narinfo")
	}
	if d := fields["Deriver"]; d != "" && d != "unknown-deriver" && (!wire.StorePattern.MatchString("/nix/store/"+d) || !strings.HasSuffix(d, ".drv")) {
		return bad()
	}
	return n, nil
}

// Only the final publication validates the closure. This lock serializes that
// validation with another completion attempt, independently of incoming bodies.
func (s *Server) completeUpload(t *Transfer) error {
	release, err := s.transferLocks.acquire(s.ctx, t.ID, 1)
	if err != nil {
		return err
	}
	defer release()
	return s.checkUploadLocked(t)
}
func (s *Server) checkUploadLocked(t *Transfer) error {
	s.mu.Lock()
	var remaining int
	err := s.db.QueryRowContext(s.ctx, "SELECT remaining FROM transfers WHERE id=?", t.ID).Scan(&remaining)
	if err != nil || remaining != 0 || t.State == "ready" {
		s.mu.Unlock()
		return err
	}
	snapshot := *t
	snapshot.Objects = maps.Clone(t.Objects)
	s.mu.Unlock()
	return s.verifyUpload(t, &snapshot)
}
func (s *Server) verifyUpload(target, t *Transfer) error {
	if t.State == "ready" {
		return nil
	}
	if t.State != "open" || t.ManifestDigest == "" {
		return wire.Fail("conflict", "Transfer is unavailable or upload is still in progress")
	}
	var missing int
	if err := s.db.QueryRowContext(s.ctx, "SELECT remaining FROM transfers WHERE id=?", t.ID).Scan(&missing); err != nil {
		return err
	}
	if missing != 0 {
		return nil
	}
	if t.Kind == "file" {
		if _, ok := t.Objects["blob"]; !ok {
			return wire.Fail("invalid_transfer", "Upload has no content")
		}
	} else {
		count := 0
		for name, object := range t.Objects {
			if !cacheKey.MatchString(name) {
				continue
			}
			count++
			data, e := s.root.Read(object.File, 65536)
			if e != nil {
				return e
			}
			info, e := parseNarInfo(name, data)
			if e != nil {
				return e
			}
			obj, ok := t.Objects[info.URL]
			if !ok || obj.Size != info.Size || (info.Hash != "sha256:"+obj.SHA256 && info.Hash != "sha256:"+nixHash(obj.SHA256)) {
				return wire.Fail("invalid_cache", "NAR object is missing or its size/checksum differs")
			}
			for _, ref := range info.References {
				if _, ok := t.Objects[ref]; !ok {
					return wire.Fail("invalid_cache", "Cache does not contain its declared closure")
				}
			}
		}
		if count == 0 {
			return wire.Fail("invalid_cache", "Cache contains no derivations")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.uploadAllowed(target); err != nil {
		return err
	}
	ready := *target
	ready.State = "ready"
	if err := s.db.Save(context.Background(), &ready); err != nil {
		return err
	}
	target.State = ready.State
	return nil
}
func (s *Server) putGuest(a PutRequest) (any, error) {
	if !guestPath(a.Path) {
		return nil, wire.Fail("invalid_argument", "Invalid guest path")
	}
	s.mu.Lock()
	t := s.transfers[a.TransferID]
	if t == nil || t.Kind != "file" || (t.TaskID != "" && t.TaskID != a.TaskID.TaskID) {
		s.mu.Unlock()
		return nil, wire.Fail("not_found", "Transfer does not belong to task")
	}
	if _, _, _, err := s.nodeAccess(a.NodeID, "file_put"); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	var e error
	if t.State != "ready" {
		e = wire.Fail("input_not_ready", "Upload must have its complete verified manifest")
	}
	if e == nil && t.TaskID == "" {
		bound := *t
		bound.TaskID = a.TaskID.TaskID
		e = s.db.Save(s.ctx, &bound)
		if e == nil {
			t.TaskID = bound.TaskID
		}
	}
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	return s.action(a.NodeID, NodeAction{Action: "file_put", Path: a.Path, Local: path.Join(s.root.Path, t.Objects["blob"].File)})
}
func httpError(w http.ResponseWriter, status int, e error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(wire.JSON(map[string]any{"error": wire.Safe(e)}))
}
func (s *Server) Files(w http.ResponseWriter, r *http.Request) {
	p := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/files/"), "/", 2)
	cap, e := s.decodeCap(p[0])
	if e != nil {
		httpError(w, 401, e)
		return
	}
	name := ""
	if len(p) == 2 {
		name = p[1]
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "PUT" {
		httpError(w, 405, wire.Fail("method_not_allowed", "Use GET, HEAD or PUT"))
		return
	}
	if cap.Kind == "artifact" {
		if r.Method == "PUT" || cap.Right != "read" || name != "" {
			httpError(w, 403, wire.Fail("forbidden", "Read-only artifact capability"))
			return
		}
		a, err := s.db.evidence(r.Context(), cap.ID)
		if err != nil {
			httpError(w, 404, err)
			return
		}
		file := a.Path
		s.serveFile(w, r, file)
		return
	}
	if cap.Kind != "transfer" {
		httpError(w, 404, wire.Fail("not_found", "Object not found"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), uploadBudget)
	defer cancel()
	stopService := context.AfterFunc(s.ctx, cancel)
	defer stopService()
	r = r.WithContext(ctx)
	if r.Method == "PUT" {
		release, err := s.transferSlots.acquire(ctx, cap.ID, uploadConcurrency)
		if err != nil {
			httpError(w, 408, err)
			return
		}
		defer release()
		select {
		case s.uploadSlots <- struct{}{}:
			defer func() { <-s.uploadSlots }()
		case <-ctx.Done():
			httpError(w, 408, ctx.Err())
			return
		}
	}
	s.mu.Lock()
	t, loadErr := s.transfer(cap.ID)
	if loadErr != nil || (name != "manifest" && name != "missing" && !objectName(t.Kind, name)) {
		s.mu.Unlock()
		httpError(w, 404, wire.Fail("not_found", "Object not found"))
		return
	}
	if r.Method == "PUT" {
		if cap.Right != "write" || s.closed || !oneOf(t.State, "open", "ready") || name == "missing" {
			s.mu.Unlock()
			httpError(w, 409, wire.Fail("immutable", "Upload is read-only"))
			return
		}
		upload := s.uploading[t.ID]
		if upload == nil {
			upload = &Upload{done: make(chan struct{}), writers: map[string]context.CancelFunc{}}
			upload.cancel = func() {
				for _, cancel := range upload.writers {
					cancel()
				}
			}
			s.uploading[t.ID] = upload
		}
		writerID := wire.ID()
		upload.writers[writerID] = cancel
		s.wg.Add(1)
		stopBody := context.AfterFunc(ctx, func() { _ = http.NewResponseController(w).SetReadDeadline(time.Now()); _ = r.Body.Close() })
		defer func() {
			stopBody()
			s.mu.Lock()
			delete(upload.writers, writerID)
			if len(upload.writers) == 0 {
				delete(s.uploading, t.ID)
				close(upload.done)
			}
			s.mu.Unlock()
			s.wg.Done()
		}()
	}
	if name == "manifest" {
		s.mu.Unlock()
		release, err := s.transferLocks.acquire(ctx, t.ID, 1)
		if err != nil {
			httpError(w, 408, err)
			return
		}
		defer release()
		s.uploadManifest(w, r, t, cap.Right)
		return
	}
	if name == "missing" {
		if r.Method != "GET" {
			s.mu.Unlock()
			httpError(w, 405, wire.Fail("method_not_allowed", "Use GET for missing objects"))
			return
		}
		cursor := r.URL.Query().Get("cursor")
		var err error
		if cursor == "" {
			err = s.reuseInputObjects(ctx, t)
		}
		var result any
		if err == nil {
			result, err = s.uploadObjects(UploadObjectsRequest{TransferGetRequest: TransferGetRequest{t.ID}, Cursor: cursor, Limit: 200, Missing: true})
		}
		s.mu.Unlock()
		if err == nil {
			err = s.completeUpload(t)
		}
		if err != nil {
			httpError(w, 409, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(wire.JSON(result))
		return
	}
	if r.Method != "PUT" {
		object, ok := t.Objects[name]
		s.mu.Unlock()
		if !ok {
			httpError(w, 404, wire.Fail("not_found", "Object not found"))
			return
		}
		s.serveFile(w, r, object.File)
		return
	}
	expectedSize, expectedHash, expectedErr := s.db.ExpectedObject(ctx, t.ID, name)
	if expectedErr != nil {
		s.mu.Unlock()
		httpError(w, 409, wire.Fail("manifest_required", "Object must be declared in the immutable manifest"))
		return
	}
	s.mu.Unlock()
	// Different digests stream concurrently. A digest has one writer even when
	// several manifests reference it, so temporary copies cannot multiply it.
	release, err := s.objectLocks.acquire(ctx, expectedHash, 1)
	if err != nil {
		httpError(w, 408, err)
		return
	}
	defer release()
	s.mu.Lock()
	if err := s.uploadAllowed(t); err != nil && t.State != "ready" {
		s.mu.Unlock()
		httpError(w, 409, err)
		return
	}
	var retained []byte
	e = s.db.QueryRowContext(ctx, "SELECT body FROM input_objects WHERE sha256=? AND size=?", expectedHash, expectedSize).Scan(&retained)
	s.mu.Unlock()
	if e != nil {
		httpError(w, 409, e)
		return
	}
	if len(retained) > 0 {
		var object Object
		if e = json.Unmarshal(retained, &object); e == nil {
			var n int64
			var h string
			n, h, e = hashFile(uploadReader{ctx, r.Body}, expectedSize)
			if e == nil && (n != object.Size || h != object.SHA256) {
				e = wire.Fail("object_conflict", "Published upload bytes are immutable")
			}
		}
		if e == nil {
			s.mu.Lock()
			if e = ctx.Err(); e == nil && t.State != "ready" {
				e = s.uploadAllowed(t)
			}
			if e == nil {
				e = s.publishTransferObject(ctx, t, name, object)
			}
			s.mu.Unlock()
		}
		if e == nil {
			e = s.completeUpload(t)
		}
		if e != nil {
			httpError(w, 409, e)
			return
		}
		w.WriteHeader(200)
		return
	}
	if r.ContentLength >= 0 && r.ContentLength != expectedSize {
		httpError(w, 400, wire.Fail("size_mismatch", "Object length differs from manifest"))
		return
	}
	var reader io.Reader = uploadReader{ctx, r.Body}
	if strings.HasSuffix(name, ".narinfo") || name == "nix-cache-info" {
		data, err := io.ReadAll(io.LimitReader(reader, min(expectedSize, 65536)+1))
		e = err
		if e == nil && name != "nix-cache-info" {
			_, e = parseNarInfo(name, data)
		}
		if e == nil && name == "nix-cache-info" && string(data) != "StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 30\n" {
			e = wire.Fail("invalid_cache", "Cache description is service-owned")
		}
		if e != nil {
			httpError(w, 400, e)
			return
		}
		reader = bytesReader(data)
	}
	staged := path.Join("input-objects", ".object-"+wire.ID())
	defer s.root.Remove(staged)
	n, h, e := s.root.Write(staged, reader, expectedSize)
	if e != nil {
		if ctx.Err() != nil {
			httpError(w, 408, wire.Fail("upload_cancelled", "Upload ended before publication"))
		} else {
			httpError(w, 413, e)
		}
		return
	}
	if n != expectedSize || h != expectedHash {
		httpError(w, 400, wire.Fail("integrity_mismatch", "Object size or SHA-256 differs from manifest"))
		return
	}
	object := Object{Size: n, SHA256: h, File: inputObjectFile(h)}
	s.mu.Lock()
	e = ctx.Err()
	if e == nil {
		e = s.uploadAllowed(t)
	}
	if e == nil {
		e = s.root.Publish(staged, object.File)
	}
	if e == nil {
		e = s.publishTransferObject(ctx, t, name, object)
	}
	s.mu.Unlock()
	if e == nil {
		e = s.completeUpload(t)
	}
	if e != nil {
		httpError(w, 409, e)
		return
	}
	w.WriteHeader(200)
}

// The caller holds s.mu. The index is the publication boundary.
func (s *Server) publishTransferObject(ctx context.Context, t *Transfer, name string, object Object) error {
	if old, ok := t.Objects[name]; ok {
		if old != object {
			return wire.Fail("object_conflict", "Upload object differs from publication")
		}
		return nil
	}
	if err := s.db.StoreTransferObject(ctx, t.ID, name, object); err != nil {
		return err
	}
	t.Bytes += object.Size
	t.Objects[name] = object
	t.Updated = time.Now().UTC()
	return s.db.Save(ctx, t)
}

var transferScratch = regexp.MustCompile(`^\.(object|upload)-[a-f0-9]{32}(\.[a-f0-9]{32}\.partial)?$`)

// The durable index is the sole publication boundary. Reclaim only recognized,
// unreferenced upload files, including a revision left before/after index commit.
// Retained objects and unrelated directory entries are never removed here.
func (s *Server) recoverTransfer(ctx context.Context, t *Transfer) error {
	if !wire.IDPattern.MatchString(t.ID) || !oneOf(t.Kind, "file", "cache") {
		return wire.Fail("invalid_transfer", "Invalid retained transfer identity")
	}
	referenced := make(map[string]bool, len(t.Objects))
	for name, object := range t.Objects {
		if !objectName(t.Kind, name) || (!sha256Pattern.MatchString(object.SHA256) || object.File != inputObjectFile(object.SHA256)) {
			return wire.Fail("invalid_transfer", "Invalid retained transfer object path")
		}
		referenced[object.File] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	for _, subdir := range []string{"", "nar"} {
		dir := path.Join("transfers", t.ID, subdir)
		st, err := s.root.Lstat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return wire.Fail("invalid_path", "Transfer storage must be an ordinary directory")
		}
		f, err := s.root.Open(dir)
		if err != nil {
			return err
		}
		err = func() error {
			defer f.Close()
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				entries, err := f.ReadDir(256)
				if err != nil && err != io.EOF {
					return err
				}
				for _, entry := range entries {
					name := path.Join(subdir, entry.Name())
					if referenced[name] || !entry.Type().IsRegular() {
						continue
					}
					if !transferScratch.MatchString(entry.Name()) {
						continue
					}
					if err := s.root.Remove(path.Join(dir, entry.Name())); err != nil {
						return err
					}
				}
				if err == io.EOF {
					return nil
				}
			}
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	f, e := s.root.Regular(name)
	if e != nil {
		httpError(w, 404, wire.Fail("not_found", "Object unavailable"))
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		httpError(w, 500, e)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "object", st.ModTime(), f)
}

// Input storage changes category only after the execution resource boundary.
func (s *Server) completeTransfers(id string) {
	for _, t := range s.transfers {
		if t.TaskID == id && t.State != "deleting" {
			t.State = "retained"
			s.persist(t)
		}
	}
}
