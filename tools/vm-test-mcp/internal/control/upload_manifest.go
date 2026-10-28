package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// The upload lock serializes the manifest with object publication. HTTP retries
// reassert immutable content; no business operation is needed to close an upload.
func (s *Server) uploadManifest(w http.ResponseWriter, r *http.Request, t *Transfer, right string) {
	if r.Method != "PUT" {
		s.serveFile(w, r, path.Join("transfers", t.ID, "manifest.json"))
		return
	}
	if right != "write" {
		httpError(w, 403, wire.Fail("forbidden", "Upload capability is read-only"))
		return
	}
	data, err := io.ReadAll(io.LimitReader(uploadReader{r.Context(), r.Body}, wire.MaxManifestBytes+1))
	var m wire.UploadManifest
	if err == nil {
		err = wire.DecodeLimit(data, &m, wire.MaxManifestBytes)
	}
	if err != nil {
		httpError(w, 400, err)
		return
	}
	if m.UploadID != t.ID || len(m.Objects) < 1 || len(m.Objects) > 100000 {
		httpError(w, 400, wire.Fail("invalid_manifest", "Expected this upload ID and 1..100000 objects"))
		return
	}
	if t.Kind == "file" && (len(m.Objects) != 1 || m.Objects[0].Name != "blob" || m.Derivation != "" || m.System != "") {
		httpError(w, 400, wire.Fail("invalid_manifest", "File input requires one blob object"))
		return
	}
	if t.Kind == "cache" && (!wire.StorePattern.MatchString(m.Derivation) || !strings.HasSuffix(m.Derivation, ".drv") || !oneOf(m.System, "x86_64-linux", "aarch64-linux")) {
		httpError(w, 400, wire.Fail("invalid_manifest", "Nix input requires its derivation and system"))
		return
	}
	slices.SortFunc(m.Objects, func(a, b wire.UploadObject) int { return strings.Compare(a.Name, b.Name) })
	var total int64
	previous := ""
	for _, o := range m.Objects {
		if !objectName(t.Kind, o.Name) || o.Name == previous || !sha256Pattern.MatchString(o.SHA256) || o.Size < 0 || o.Size > s.cfg.Storage.MaxUploadBytes-total {
			httpError(w, 400, wire.Fail("invalid_manifest", "Invalid object identity, checksum or byte budget"))
			return
		}
		previous = o.Name
		total += o.Size
	}
	if t.Kind == "cache" {
		drvInfo := strings.SplitN(path.Base(m.Derivation), "-", 2)[0] + ".narinfo"
		if !slices.ContainsFunc(m.Objects, func(o wire.UploadObject) bool { return o.Name == drvInfo }) || !slices.ContainsFunc(m.Objects, func(o wire.UploadObject) bool { return o.Name == "nix-cache-info" }) {
			httpError(w, 400, wire.Fail("invalid_manifest", "Nix manifest must contain its derivation and cache description"))
			return
		}
	}
	canonical := wire.JSON(m)
	hash := sha256.Sum256(canonical)
	digest := hex.EncodeToString(hash[:])
	s.mu.Lock()
	if t.ManifestDigest != "" {
		same := t.ManifestDigest == digest
		s.mu.Unlock()
		if !same {
			httpError(w, 409, wire.Fail("manifest_conflict", "Upload manifest is immutable"))
			return
		}
		w.WriteHeader(200)
		return
	}
	if err = s.uploadAllowed(t); err == nil && total > s.cfg.Storage.InputBytes-s.inputUsage() {
		err = wire.Fail("quota_exceeded", "Input reservation exceeds the staging budget")
	}
	if err != nil {
		s.mu.Unlock()
		httpError(w, 409, err)
		return
	}
	// Reserve before file IO; release the provisional reservation on any failure.
	t.ReservedBytes = total
	s.mu.Unlock()
	_, _, err = s.root.Write(path.Join("transfers", t.ID, "manifest.json"), strings.NewReader(string(canonical)), wire.MaxManifestBytes)
	s.mu.Lock()
	if err == nil {
		err = r.Context().Err()
	}
	if err == nil {
		err = s.uploadAllowed(t)
	}
	if err == nil {
		declared := *t
		declared.ManifestDigest = digest
		declared.Derivation = m.Derivation
		declared.System = m.System
		declared.Updated = time.Now().UTC()
		err = s.db.DeclareUpload(r.Context(), &declared, m.Objects)
		if err == nil {
			*t = declared
		}
	}
	if err != nil {
		t.ReservedBytes = 0
	}
	s.mu.Unlock()
	if err != nil {
		httpError(w, 409, err)
		return
	}
	w.WriteHeader(200)
}

func (s *Server) deleteUpload(id string) (any, error) {
	s.mu.Lock()
	t, err := s.transfer(id)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if t.TaskID != "" {
		s.mu.Unlock()
		return nil, wire.Fail("protected", "Bound input follows its execution lifecycle")
	}
	t.State = "deleting"
	s.persist(t)
	var done <-chan struct{}
	if u := s.uploading[id]; u != nil {
		u.cancel()
		done = u.done
	}
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
	}
	v, _ := s.transferLocks.LoadOrStore(id, &sync.Mutex{})
	lock := v.(*sync.Mutex)
	if err = lockContext(s.ctx, lock); err != nil {
		return nil, err
	}
	defer lock.Unlock()
	if err = s.root.RemoveAll(path.Join("transfers", id)); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err = s.db.ExecContext(context.Background(), "DELETE FROM transfers WHERE id=?", id); err != nil {
		return nil, err
	}
	delete(s.transfers, id)
	return map[string]any{"upload_id": id, "deleted": true}, nil
}
