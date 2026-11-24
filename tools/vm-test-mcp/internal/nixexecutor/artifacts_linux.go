package nixexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type retainedArtifact struct {
	Artifact
	Relative string `json:"relative"`
}

// Retained build trees remain executor-owned until the worker's handoff receipt.
// Their bytes participate in admission pressure while keeping readiness probes non-blocking.
func (e *Executor) retainedBuildBytes(ctx context.Context) (int64, error) {
	seen := map[[2]uint64]bool{}
	var total int64
	err := fs.WalkDir(e.root.FS(), "builds", func(name string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		key := [2]uint64{stat.Dev, stat.Ino}
		if !seen[key] {
			total += info.Size()
			seen[key] = true
		}
		return nil
	})
	return total, err
}

func (e *Executor) startEvidenceScan() <-chan struct{} {
	e.mu.Lock()
	if e.closing || e.evidenceScanning {
		e.mu.Unlock()
		return nil
	}
	e.evidenceScanning = true
	parent := e.ctx
	if parent == nil {
		parent = context.Background()
	}
	e.wg.Add(1)
	e.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer e.wg.Done()
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		used, err := e.retainedBuildBytes(ctx)
		cancel()
		now := time.Now().UTC()
		e.mu.Lock()
		e.evidenceScanning = false
		if err == nil {
			e.evidenceBytes = used
			e.evidenceSampled = true
			e.evidenceObserved = now
			e.evidenceRefreshErr = nil
		} else {
			e.evidenceRefreshErr = wire.Safe(err)
		}
		e.mu.Unlock()
	}()
	return done
}

func (e *Executor) populateEvidenceUsage(v *Description) {
	e.mu.Lock()
	sampled := e.evidenceSampled
	stale := !sampled || time.Since(e.evidenceObserved) >= 5*time.Second
	scanning := e.evidenceScanning
	e.mu.Unlock()

	var done <-chan struct{}
	if stale && !scanning {
		done = e.startEvidenceScan()
	}
	if !sampled && done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	v.EvidenceBytes = e.evidenceBytes
	if !e.evidenceObserved.IsZero() {
		observed := e.evidenceObserved
		v.EvidenceObserved = &observed
	}
	if e.evidenceRefreshErr != nil {
		if !e.evidenceSampled && e.evidenceRefreshErr.Code != "backend_timeout" {
			v.EvidenceError = e.evidenceRefreshErr
		} else {
			v.EvidenceRefreshError = e.evidenceRefreshErr
		}
	} else if !e.evidenceSampled && e.evidenceScanning {
		v.EvidenceRefreshError = wire.Safe(errors.Join(context.DeadlineExceeded))
	}
}

// The resource receipt seals the build directory before inventory or reads.
// Requests identify a worker task and opaque object; no host path is accepted.
func (e *Executor) artifactSession(a Request) (*runtimeSession, Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !wire.IDPattern.MatchString(a.WorkerInstance) || !wire.IDPattern.MatchString(a.TaskID) || !wire.IDPattern.MatchString(a.SessionID) {
		return nil, Session{}, wire.Fail("invalid_argument", "Worker, task and session identities are required")
	}
	r, err := e.lookup(a)
	if err != nil {
		return nil, Session{}, err
	}
	if !r.session.Resources.Released() {
		return nil, Session{}, wire.Fail("resources_active", "Evidence is sealed after resource release")
	}
	return r, cloneSession(r.session), nil
}

func (e *Executor) artifactInventory(s Session) ([]retainedArtifact, error) {
	manifest := path.Join("artifacts", s.ID+".json")
	if raw, err := e.root.Read(manifest, 8<<20); err == nil {
		var files []retainedArtifact
		err = json.Unmarshal(raw, &files)
		return files, err
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	files := []retainedArtifact{}
	base := path.Join("builds", s.ID)
	if _, err := e.root.Lstat(base); os.IsNotExist(err) {
		return files, nil
	}
	visited := 0
	err := fs.WalkDir(e.root.FS(), base, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		visited++
		if visited > 200000 {
			return wire.Fail("quota_exceeded", "Retained build directory exceeds the traversal limit")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		// Nix may retain large unpacked build trees from failed dependencies.
		// Prune any subtree outside <top>/vm-test-evidence or <top>/build/vm-test-evidence.
		if name != base && entry.IsDir() {
			parts := strings.Split(strings.TrimPrefix(name, base+"/"), "/")
			if !slices.Contains(parts, "vm-test-evidence") {
				if len(parts) == 2 && parts[1] != "vm-test-evidence" && parts[1] != "build" {
					return fs.SkipDir
				}
				if len(parts) >= 3 && parts[1] == "build" && parts[2] != "vm-test-evidence" {
					return fs.SkipDir
				}
			}
		}
		marker := "/vm-test-evidence/"
		index := strings.Index(name, marker)
		if entry.IsDir() || index < 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Nlink != 1 {
			return nil
		}
		if len(files) >= 10000 {
			return wire.Fail("quota_exceeded", "Retained evidence exceeds the object limit")
		}
		hash := sha256.Sum256([]byte(name))
		files = append(files, retainedArtifact{Artifact: Artifact{ID: hex.EncodeToString(hash[:16]), Name: name[index+len(marker):], Size: info.Size()}, Relative: name})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
	_, _, err = e.root.Write(manifest, strings.NewReader(string(wire.JSON(files))), 8<<20)
	return files, err
}

func (e *Executor) artifactCall(method string, a Request) (any, error) {
	r, _, err := e.artifactSession(a)
	if err != nil {
		return nil, err
	}
	r.artifacts.Lock()
	defer r.artifacts.Unlock()
	_, session, err := e.artifactSession(a)
	if err != nil {
		return nil, err
	}
	if method == "evidence_release" {
		if err = e.root.RemoveAll(path.Join("builds", session.ID)); err != nil {
			return nil, err
		}
		if err = e.root.Remove(path.Join("artifacts", session.ID+".json")); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		session.ArtifactsReleased = true
		if err = e.save(&session); err != nil {
			return nil, err
		}
		*r.session = session
		e.mu.Unlock()
		e.startEvidenceScan()
		e.mu.Lock()
		return session, nil
	}
	if session.ArtifactsReleased {
		return ArtifactPage{Files: []Artifact{}}, nil
	}
	files, err := e.artifactInventory(session)
	if err != nil {
		return nil, err
	}
	if a.Cursor < 0 || a.Cursor > len(files) {
		return nil, wire.Fail("invalid_argument", "Invalid evidence cursor")
	}
	end := min(a.Cursor+200, len(files))
	page := ArtifactPage{Files: []Artifact{}}
	for _, f := range files[a.Cursor:end] {
		page.Files = append(page.Files, f.Artifact)
	}
	if end < len(files) {
		page.NextCursor = end
	}
	return page, nil
}

func (e *Executor) openArtifact(a Request) (*os.File, int64, error) {
	r, _, err := e.artifactSession(a)
	if err != nil {
		return nil, 0, err
	}
	r.artifacts.Lock()
	defer r.artifacts.Unlock()
	_, session, err := e.artifactSession(a)
	if err != nil {
		return nil, 0, err
	}
	if session.ArtifactsReleased {
		return nil, 0, wire.Fail("evidence_released", "Retained evidence was released")
	}
	files, err := e.artifactInventory(session)
	if err != nil {
		return nil, 0, err
	}
	for _, item := range files {
		if item.ID != a.ArtifactID {
			continue
		}
		if safefs.Name(item.Relative) != nil || !strings.HasPrefix(item.Relative, path.Join("builds", session.ID)+"/") {
			return nil, 0, wire.Fail("integrity_mismatch", "Retained artifact is outside its session")
		}
		file, err := e.root.Regular(item.Relative)
		if err != nil {
			return nil, 0, err
		}
		info, err := file.Stat()
		if err == nil {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || stat.Nlink != 1 || info.Size() != item.Size {
				err = wire.Fail("integrity_mismatch", "Retained artifact changed after sealing")
			}
		}
		if err != nil {
			file.Close()
			return nil, 0, err
		}
		return file, item.Size, nil
	}
	return nil, 0, wire.Fail("not_found", "Unknown retained artifact")
}

func (e *Executor) serveArtifact(w http.ResponseWriter, request *http.Request) {
	q := request.URL.Query()
	a := Request{WorkerInstance: q.Get("worker_instance"), TaskID: q.Get("task_id"), SessionID: q.Get("session_id"), ArtifactID: q.Get("artifact_id")}
	f, size, err := e.openArtifact(a)
	if err != nil {
		http.Error(w, wire.Safe(err).Code, http.StatusBadRequest)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	_, _ = io.Copy(&artifactWriter{w}, io.LimitReader(f, size))
}

type artifactWriter struct{ http.ResponseWriter }

func (w *artifactWriter) Write(p []byte) (int, error) {
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}
