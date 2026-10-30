package worker

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Indexing owns only this job's I/O mutex. Admission, resource observations and
// other jobs stay responsive while retained output is read and hashed.
func (w *Worker) collectLocked(j *Job) {
	e := &j.Execution.Evidence
	if w.closing || j.collecting || j.Cleanup != "complete" || e.State != "pending" || e.NextAttempt != nil && time.Now().Before(*e.NextAttempt) {
		return
	}
	if j.ManifestDigest != "" {
		e.State, e.Error = "transferring", nil
		_ = w.save(j)
		return
	}
	j.collecting = true
	e.State, e.Attempts, e.Error = "collecting", e.Attempts+1, nil
	now := time.Now().UTC()
	e.Updated, e.NextAttempt = &now, nil
	if err := w.save(j); err != nil {
		e.State = "blocked"
		e.Error = lifecycleError(j, "evidence", err, "administrator")
		j.collecting = false
		return
	}
	copy := &Job{Outputs: map[string]string{}}
	for name, value := range j.Outputs {
		copy.Outputs[name] = value
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		j.io.Lock()
		defer j.io.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		logRoot := filepath.Join(w.root.Path, j.Request.TaskID)
		var err error
		if info, statErr := os.Stat(filepath.Join(logRoot, "task.log")); statErr == nil {
			copy.Files = append(copy.Files, File{ID: wire.ID(), Name: "task.log", Root: logRoot, Relative: "task.log", Size: info.Size()})
			file, openErr := os.OpenFile(filepath.Join(logRoot, "task.log"), os.O_RDWR, 0)
			err = openErr
			if err == nil {
				err = file.Sync()
				file.Close()
			}
		} else if !os.IsNotExist(statErr) {
			err = statErr
		}
		if err == nil {
			err = w.indexFilesContext(ctx, copy)
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		j.collecting = false
		e := &j.Execution.Evidence
		now := time.Now().UTC()
		e.Updated = &now
		if err == nil {
			j.Files = copy.Files
			files := []wire.HandoffFile{}
			for _, file := range j.Files {
				files = append(files, wire.HandoffFile{ID: file.ID, Size: file.Size, SHA256: file.SHA256})
			}
			j.ManifestDigest = wire.ManifestDigest(files)
			e.State = "transferring"
			err = w.save(j)
		}
		if err != nil {
			e.State, e.Error = "blocked", lifecycleError(j, "evidence", err, "administrator")
			if wire.TransientEvidence(err) && e.Attempts < 3 {
				delay := time.Second
				if e.Attempts == 2 {
					delay = 5 * time.Second
				}
				next := now.Add(delay)
				e.State, e.NextAttempt = "pending", &next
			}
			_ = w.save(j)
		}
	}()
}
