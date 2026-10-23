package control

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type capability struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Right   string `json:"right"`
	Expires int64  `json:"expires"`
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
	if t.State == "open" {
		right = "write"
	}
	u, expires := s.capURL("transfer", t.ID, right)
	if t.Kind == "file" {
		u += "/blob"
	}
	return map[string]any{"id": t.ID, "kind": t.Kind, "state": t.State, "objects": clone(&t.Objects), "url": u, "expires": expires}
}
func (s *Server) createTransfer(a TransferRequest) (any, error) {
	if a.Kind != "file" && a.Kind != "cache" {
		return nil, wire.Fail("invalid_argument", "Transfer kind must be file or cache")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.TaskID != "" {
		t, _, e := s.running(a.TaskID)
		if e != nil {
			return nil, e
		}
		if t.Purged {
			return nil, wire.Fail("purged", "Task was purged")
		}
	} else if a.Kind == "file" {
		return nil, wire.Fail("invalid_argument", "File uploads require a task_id")
	}
	t := &Transfer{ID: wire.ID(), TaskID: a.TaskID, Kind: a.Kind, State: "open", Created: time.Now().UTC(), Objects: map[string]Object{}}
	if e := s.root.MkdirAll(path.Join("transfers", t.ID), 0700); e != nil {
		return nil, e
	}
	if a.Kind == "cache" {
		data := "StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 30\n"
		n, h, e := s.root.Write(path.Join("transfers", t.ID, "nix-cache-info"), strings.NewReader(data), 65536)
		if e != nil {
			return nil, e
		}
		t.Objects["nix-cache-info"] = Object{n, h}
	}
	if e := s.db.Save(context.Background(), state.Document{Kind: "transfer", ID: t.ID, Value: t}); e != nil {
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
func (s *Server) seal(t *Transfer) error {
	if t.State == "sealed" {
		return nil
	}
	if t.State != "open" || s.uploading[t.ID] {
		return wire.Fail("conflict", "Transfer is unavailable or upload is still in progress")
	}
	if t.Kind == "file" {
		if _, ok := t.Objects["blob"]; !ok {
			return wire.Fail("invalid_transfer", "Upload has no content")
		}
	} else {
		count := 0
		for name := range t.Objects {
			if !cacheKey.MatchString(name) {
				continue
			}
			count++
			data, e := s.root.Read(path.Join("transfers", t.ID, name), 65536)
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
	t.State = "sealed"
	return s.db.Save(context.Background(), state.Document{Kind: "transfer", ID: t.ID, Value: t})
}
func (s *Server) putGuest(a PutRequest) (any, error) {
	if !guestPath(a.Path) {
		return nil, wire.Fail("invalid_argument", "Invalid guest path")
	}
	s.mu.Lock()
	t := s.transfers[a.TransferID]
	if t == nil || t.Kind != "file" || t.TaskID != a.TaskID.TaskID {
		s.mu.Unlock()
		return nil, wire.Fail("not_found", "Transfer does not belong to task")
	}
	e := s.seal(t)
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	return s.action(a.NodeID, NodeAction{Action: "file_put", Path: a.Path, Local: path.Join(s.root.Path, "transfers", t.ID, "blob")})
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
		ids := strings.Split(cap.ID, "/")
		if len(ids) != 2 {
			httpError(w, 404, wire.Fail("not_found", "Artifact not found"))
			return
		}
		s.mu.Lock()
		t, e := s.task(ids[0])
		var file string
		if e == nil && !t.Purged {
			for _, a := range t.Artifacts {
				if a.ID == ids[1] {
					file = a.Path
				}
			}
		}
		s.mu.Unlock()
		if file == "" {
			httpError(w, 404, wire.Fail("not_found", "Artifact not found"))
			return
		}
		s.serveFile(w, r, file)
		return
	}
	if cap.Kind != "transfer" {
		httpError(w, 404, wire.Fail("not_found", "Object not found"))
		return
	}
	v, _ := s.transferLocks.LoadOrStore(cap.ID, &sync.Mutex{})
	lock := v.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	t := s.transfers[cap.ID]
	if t == nil || t.State == "purged" || !objectName(t.Kind, name) {
		s.mu.Unlock()
		httpError(w, 404, wire.Fail("not_found", "Object not found"))
		return
	}
	file := path.Join("transfers", t.ID, name)
	if r.Method != "PUT" {
		_, ok := t.Objects[name]
		s.mu.Unlock()
		if !ok {
			httpError(w, 404, wire.Fail("not_found", "Object not found"))
			return
		}
		s.serveFile(w, r, file)
		return
	}
	if cap.Right != "write" || t.State != "open" {
		s.mu.Unlock()
		httpError(w, 409, wire.Fail("sealed", "Transfer is sealed"))
		return
	}
	quota := s.cfg.Retention.MaxUploadBytes
	for key, obj := range t.Objects {
		if key != name {
			quota -= obj.Size
		}
	}
	if len(t.Objects) > 100000 {
		quota = 0
	}
	if strings.HasSuffix(name, ".narinfo") || name == "nix-cache-info" {
		quota = min(quota, 65536)
	}
	s.uploading[t.ID] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.uploading, t.ID); s.mu.Unlock() }()
	var reader io.Reader = r.Body
	if strings.HasSuffix(name, ".narinfo") || name == "nix-cache-info" {
		data, err := io.ReadAll(io.LimitReader(r.Body, quota+1))
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
	n, h, e := s.root.Write(file, reader, quota)
	if e != nil {
		httpError(w, 413, e)
		return
	}
	s.mu.Lock()
	t.Objects[name] = Object{Size: n, SHA256: h}
	e = s.db.Save(context.Background(), state.Document{Kind: "transfer", ID: t.ID, Value: t})
	s.mu.Unlock()
	if e != nil {
		httpError(w, 500, e)
		return
	}
	w.WriteHeader(200)
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
