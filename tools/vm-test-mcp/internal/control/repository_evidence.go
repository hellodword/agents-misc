package control

import (
	"context"
	"encoding/json"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"os"
	"time"
)

func (d *repository) evidence(ctx context.Context, id string) (*Artifact, error) {
	var body []byte
	if err := d.QueryRowContext(ctx, "SELECT body FROM evidence WHERE id=?", id).Scan(&body); err != nil {
		return nil, wire.Fail("not_found", "Evidence is no longer retained")
	}
	var a Artifact
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, err
	}
	return &a, nil
}
func (s *Server) evidenceList(a ArtifactListRequest) (any, error) {
	limit := a.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(s.ctx, "SELECT sequence,body FROM evidence WHERE (?='' OR task_id=?) AND sequence>? ORDER BY sequence LIMIT ?", a.ExecutionID, a.ExecutionID, a.Cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []any{}
	next := a.Cursor
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&next, &body); err != nil {
			return nil, err
		}
		var v Artifact
		if err = json.Unmarshal(body, &v); err != nil {
			return nil, err
		}
		items = append(items, artifactView(&v))
	}
	return map[string]any{"items": items, "next_cursor": next}, rows.Err()
}

func (s *Server) reclaimEvidence(now time.Time, pressure bool) error {
	s.evidenceMu.Lock()
	defer s.evidenceMu.Unlock()
	rows, err := s.db.QueryContext(s.ctx, "SELECT body FROM evidence WHERE protected=0 AND pinned=0 AND NOT EXISTS(SELECT 1 FROM executions e WHERE e.id=evidence.task_id) AND (? OR expires<=?) ORDER BY success DESC,expires,sequence LIMIT 128", pressure, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	values := []Artifact{}
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			break
		}
		var a Artifact
		if err = json.Unmarshal(body, &a); err != nil {
			break
		}
		values = append(values, a)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowErr != nil {
		return rowErr
	}
	for _, a := range values {
		if err = s.root.Remove(a.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		if _, err = s.db.ExecContext(s.ctx, "DELETE FROM evidence WHERE id=? AND pinned=0 AND protected=0", a.ID); err != nil {
			return err
		}
	}
	return nil
}
func (d *repository) PublishEvidence(ctx context.Context, a *Artifact) error {
	_, err := d.ExecContext(ctx, `INSERT INTO evidence(id,task_id,position,pinned,size,body) VALUES(?,?,0,?,?,?) ON CONFLICT(id) DO NOTHING`, a.ID, a.ExecutionID, a.Pinned, a.Size, wire.JSON(a))
	return err
}
