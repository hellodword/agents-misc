package control

import (
	"os"
)

// Executions and evidence whose summaries have expired participate in the same
// ordering. A pin protects its object without changing the age of other objects.
func (s *Server) reclaimPressure() error {
	rows, err := s.db.QueryContext(s.ctx, `SELECT id,kind FROM (
 SELECT id,'execution' AS kind,(state='finished' AND json_extract(body,'$.conclusion')='passed') AS success,julianday(ended) AS age
 FROM executions e WHERE cleanup='complete' AND ended IS NOT NULL
 AND (COALESCE(json_extract(body,'$.purged'),0)=0 OR EXISTS(SELECT 1 FROM evidence a WHERE a.task_id=e.id AND a.pinned=0))
 UNION ALL
 SELECT id,'evidence' AS kind,success,julianday(expires)-CASE WHEN success=1 THEN ? ELSE ? END AS age FROM evidence a
 WHERE protected=0 AND pinned=0 AND NOT EXISTS(SELECT 1 FROM executions e WHERE e.id=a.task_id)
 ) ORDER BY success DESC,age,id LIMIT 128`, s.cfg.Storage.SuccessDays, s.cfg.Storage.FailureDays)
	if err != nil {
		return err
	}
	type candidate struct{ id, kind string }
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.kind); err != nil {
			break
		}
		candidates = append(candidates, c)
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if scanErr != nil {
		return scanErr
	}
	for _, c := range candidates {
		if c.kind == "execution" {
			s.mu.Lock()
			t, loadErr := s.task(c.id)
			if loadErr == nil && s.performancePending[c.id].IsZero() {
				err = s.purge(t)
			}
			s.mu.Unlock()
			if loadErr != nil {
				return loadErr
			}
		} else {
			s.evidenceMu.Lock()
			a, loadErr := s.db.evidence(s.ctx, c.id)
			if loadErr == nil && !a.Pinned {
				err = s.root.Remove(a.Path)
				if os.IsNotExist(err) {
					err = nil
				}
				if err == nil {
					_, err = s.db.ExecContext(s.ctx, "DELETE FROM evidence WHERE id=? AND pinned=0 AND protected=0", c.id)
				}
			}
			s.evidenceMu.Unlock()
		}
		if err != nil {
			return err
		}
		used, err := s.evidenceUsage()
		if err != nil {
			return err
		}
		if used <= s.cfg.Storage.LowWaterBytes {
			return nil
		}
	}
	return nil
}
