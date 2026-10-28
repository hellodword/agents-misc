package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (w *Worker) save(j *Job) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	request := j.Request
	request.Cache = ""
	_, err = tx.ExecContext(ctx, `INSERT INTO worker_jobs(id,state,cleanup,request,execution,started,deadline,handoff_complete,copies_released,manifest_digest,handoff_finished,file_count)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,cleanup=excluded.cleanup,request=excluded.request,execution=excluded.execution,
 deadline=excluded.deadline,handoff_complete=excluded.handoff_complete,copies_released=excluded.copies_released,manifest_digest=excluded.manifest_digest,handoff_finished=excluded.handoff_finished,file_count=excluded.file_count`,
		j.Request.TaskID, j.State, j.Cleanup, wire.JSON(request), wire.JSON(j.Execution), j.Started.Format(time.RFC3339Nano), j.Deadline.Format(time.RFC3339Nano), j.HandoffComplete, j.CopiesReleased, j.ManifestDigest, timeText(j.HandoffFinished), len(j.Files))
	if err != nil {
		return err
	}
	for _, f := range j.Files[min(j.indexedFiles, len(j.Files)):] {
		if _, err = tx.ExecContext(ctx, `INSERT INTO worker_files(id,task_id,name,root,relative,size,sha256) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, f.ID, j.Request.TaskID, f.Name, f.Root, f.Relative, f.Size, f.SHA256); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	j.indexedFiles = len(j.Files)
	return nil
}
func timeText(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func (w *Worker) loadJob(id string) (*Job, error) {
	var j Job
	var request, execution []byte
	var started, deadline string
	var finished sql.NullString
	if err := w.db.QueryRow(`SELECT state,cleanup,request,execution,started,deadline,handoff_complete,copies_released,manifest_digest,handoff_finished FROM worker_jobs WHERE id=?`, id).Scan(&j.State, &j.Cleanup, &request, &execution, &started, &deadline, &j.HandoffComplete, &j.CopiesReleased, &j.ManifestDigest, &finished); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(request, &j.Request); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(execution, &j.Execution); err != nil {
		return nil, err
	}
	var err error
	if j.Started, err = time.Parse(time.RFC3339Nano, started); err != nil {
		return nil, err
	}
	if j.Deadline, err = time.Parse(time.RFC3339Nano, deadline); err != nil {
		return nil, err
	}
	if finished.Valid {
		t, e := time.Parse(time.RFC3339Nano, finished.String)
		if e != nil {
			return nil, e
		}
		j.HandoffFinished = &t
	}
	if !j.CopiesReleased {
		rows, err := w.db.Query(`SELECT id,name,root,relative,size,sha256 FROM worker_files WHERE task_id=? ORDER BY sequence`, id)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var f File
			if err = rows.Scan(&f.ID, &f.Name, &f.Root, &f.Relative, &f.Size, &f.SHA256); err != nil {
				return nil, err
			}
			j.Files = append(j.Files, f)
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	j.indexedFiles = len(j.Files)
	return &j, nil
}
func (w *Worker) activeJobs() ([]*Job, error) {
	rows, err := w.db.Query("SELECT id FROM worker_jobs WHERE cleanup<>'complete' OR copies_released=0 ORDER BY sequence")
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if scanErr != nil {
		return nil, scanErr
	}
	jobs := make([]*Job, 0, len(ids))
	for _, id := range ids {
		j, e := w.loadJob(id)
		if e != nil {
			return nil, e
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func (w *Worker) filePage(id string, after int64) (any, error) {
	rows, err := w.db.Query(`SELECT sequence,id,name,size,sha256 FROM worker_files WHERE task_id=? AND sequence>? ORDER BY sequence LIMIT 101`, id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []any{}
	next, eof := after, true
	for rows.Next() {
		var sequence int64
		var f File
		if err = rows.Scan(&sequence, &f.ID, &f.Name, &f.Size, &f.SHA256); err != nil {
			return nil, err
		}
		if len(items) == 100 {
			eof = false
			break
		}
		next = sequence
		items = append(items, map[string]any{"id": f.ID, "name": f.Name, "size": f.Size, "sha256": f.SHA256})
	}
	return map[string]any{"items": items, "next_cursor": next, "eof": eof}, rows.Err()
}
