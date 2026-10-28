package worker

import (
	"context"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"time"
)

// An acknowledgement is accepted only after controller publication and durable
// indexing. Every retry finishes both filesystem and index cleanup.
func (w *Worker) handoff(j *Job, digest string) (any, error) {
	if w.streams[j.Request.TaskID] > 0 {
		return nil, wire.Fail("resource_busy", "Evidence transfer is still active")
	}
	if j.Cleanup != "complete" {
		return nil, wire.Fail("cleanup_required", "Resources must be released before evidence handoff")
	}
	if j.HandoffComplete {
		if j.ManifestDigest != digest {
			return nil, wire.Fail("request_conflict", "Handoff manifest changed")
		}
	} else {
		files := []wire.HandoffFile{}
		for _, f := range j.Files {
			files = append(files, wire.HandoffFile{ID: f.ID, Size: f.Size, SHA256: f.SHA256})
		}
		if wire.ManifestDigest(files) != digest {
			return nil, wire.Fail("evidence_integrity", "Handoff digest differs from retained evidence")
		}
		j.HandoffComplete = true
		j.ManifestDigest = digest
		if err := w.save(j); err != nil {
			j.HandoffComplete = false
			return nil, err
		}
	}
	if err := w.root.RemoveAll(j.Request.TaskID); err != nil {
		return nil, err
	}
	if _, err := w.db.ExecContext(context.Background(), "DELETE FROM worker_files WHERE task_id=?", j.Request.TaskID); err != nil {
		return nil, err
	}
	j.Files = nil
	j.Outputs = nil
	j.CopiesReleased = true
	if j.HandoffFinished == nil {
		now := time.Now().UTC()
		j.HandoffFinished = &now
	}
	if err := w.save(j); err != nil {
		return nil, err
	}
	return public(j), nil
}

// Worker metadata has its own bounded lifetime. Controller reclamation never
// waits on a worker RPC while holding the shared admission/control mutex.
func (w *Worker) expireCompleted(now time.Time) error {
	if now.Sub(w.lastRetention) < time.Minute {
		return nil
	}
	cutoff := now.Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := w.db.ExecContext(context.Background(), `DELETE FROM worker_jobs WHERE id IN (
 SELECT id FROM worker_jobs WHERE cleanup='complete' AND handoff_complete=1 AND copies_released=1 AND handoff_finished<? ORDER BY sequence LIMIT 128)`, cutoff); err != nil {
		return err
	}
	for id, j := range w.jobs {
		if j.CopiesReleased {
			delete(w.jobs, id)
		}
	}
	if err := w.sweepUnreferenced(now); err != nil {
		return err
	}
	w.lastRetention = now
	return w.db.Compact(context.Background())
}
