package worker

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// Authentication is shared with /call. Opening under the ledger mutex prevents
// handoff from removing roots before the stream has acquired its file descriptor.
func (w *Worker) serveArtifact(rw http.ResponseWriter, r *http.Request) {
	id, job := strings.TrimPrefix(r.URL.Path, "/files/"), r.URL.Query().Get("job_id")
	if !wire.IDPattern.MatchString(id) || !wire.IDPattern.MatchString(job) || r.URL.Query().Get("instance_id") != w.db.InstanceID {
		http.Error(rw, "invalid file reference", http.StatusBadRequest)
		return
	}
	w.mu.Lock()
	var f File
	err := w.db.QueryRowContext(r.Context(), "SELECT root,relative,size,sha256 FROM worker_files WHERE id=? AND task_id=?", id, job).Scan(&f.Root, &f.Relative, &f.Size, &f.SHA256)
	var root *os.Root
	var file *os.File
	if err == nil {
		root, err = os.OpenRoot(f.Root)
	}
	if err == nil {
		file, err = (&safefs.Root{Root: root}).Regular(f.Relative)
		root.Close()
	}
	if err == nil {
		w.streams[job]++
	}
	w.mu.Unlock()
	if err != nil {
		http.Error(rw, "file unavailable", http.StatusNotFound)
		return
	}
	defer file.Close()
	defer func() {
		w.mu.Lock()
		w.streams[job]--
		if w.streams[job] == 0 {
			delete(w.streams, job)
		}
		w.mu.Unlock()
	}()
	rw.Header().Set("Content-Type", "application/octet-stream")
	rw.Header().Set("Content-Length", strconv.FormatInt(f.Size, 10))
	rw.Header().Set("ETag", `"`+f.SHA256+`"`)
	_, _ = io.Copy(&streamWriter{rw}, io.LimitReader(file, f.Size))
}

type streamWriter struct{ http.ResponseWriter }

func (w *streamWriter) Write(p []byte) (int, error) {
	if err := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(p)
}
