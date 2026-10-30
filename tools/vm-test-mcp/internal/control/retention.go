package control

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func directoryBytes(root string, excludeRuntime bool, seen map[[2]uint64]bool) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if excludeRuntime {
			relative, _ := filepath.Rel(root, p)
			parts := strings.Split(relative, string(filepath.Separator))
			for i, part := range parts {
				if part == "runtime" && i+1 < len(parts) {
					entry := parts[i+1]
					if entry != "router-console" && !strings.HasPrefix(entry, "capture-") {
						if d.IsDir() {
							return filepath.SkipDir
						}
						return nil
					}
					break
				}
			}
		}
		if d.IsDir() {
			return nil
		}
		if d.Type().IsRegular() {
			st, err := d.Info()
			if err != nil {
				return err
			}
			stat := st.Sys().(*syscall.Stat_t)
			key := [2]uint64{stat.Dev, stat.Ino}
			if !seen[key] {
				size += st.Size()
				seen[key] = true
			}
		}
		return nil
	})
	return size, err
}
func (s *Server) evidenceUsage() (int64, error) {
	seen := map[[2]uint64]bool{}
	size, err := directoryBytes(filepath.Join(s.root.Path, "tasks"), true, seen)
	if err != nil {
		return 0, err
	}
	bodies, err := directoryBytes(filepath.Join(s.root.Path, "bodies"), false, seen)
	if err != nil {
		return 0, err
	}
	size += bodies
	rows, err := s.db.QueryContext(s.ctx, `SELECT t.id FROM transfers t JOIN executions e ON t.task_id=e.id WHERE e.cleanup='complete'`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	e := rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if e != nil {
		return 0, e
	}
	for _, id := range ids {
		n, err := directoryBytes(filepath.Join(s.root.Path, "transfers", id), false, seen)
		if err != nil {
			return 0, err
		}
		size += n
	}
	if provider, ok := s.backend.(interface {
		EvidenceUsage(context.Context) (int64, error)
	}); ok {
		ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
		defer cancel()
		worker, err := provider.EvidenceUsage(ctx)
		if err != nil {
			return 0, err
		}
		size += worker
	}
	return size, nil
}
func (s *Server) inputUsage() int64 {
	var n int64
	for _, t := range s.transfers {
		if t.State != "retained" {
			n += max(t.Bytes, t.ReservedBytes)
		}
	}
	return n
}

// Admission reads the last completed storage sample. Scans and reclamation run
// independently of the control mutex, including while the executor is occupied.
func (s *Server) admission() error {
	if s.storageBlocked {
		return wire.Fail("storage_pressure", "Evidence storage is unavailable or above budget; existing execution and cleanup remain available")
	}
	return nil
}
func (s *Server) expireRetention() { s.refreshStorage(false) }
func (s *Server) refreshStorage(force bool) {
	if force {
		s.retentionMu.Lock()
	} else if !s.retentionMu.TryLock() {
		return
	}
	defer s.retentionMu.Unlock()
	s.mu.Lock()
	now := time.Now().UTC()
	if !force && now.Sub(s.lastRetention) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastRetention = now
	s.mu.Unlock()
	err := s.reclaim(now, false)
	used, usageErr := s.evidenceUsage()
	if err == nil {
		err = usageErr
	}
	if err == nil && used > s.cfg.Storage.EvidenceBytes {
		for used > s.cfg.Storage.LowWaterBytes {
			previous := used
			if err = s.reclaim(now, true); err != nil {
				break
			}
			used, err = s.evidenceUsage()
			if err != nil || used >= previous {
				break
			}
		}
	}
	s.mu.Lock()
	s.evidenceBytes = used
	s.storageBlocked = err != nil || used > s.cfg.Storage.EvidenceBytes
	s.mu.Unlock()
}
func (s *Server) reclaim(now time.Time, pressure bool) error {
	if pressure {
		return s.reclaimPressure()
	}
	// Bounded batches, sorted by successful result first and oldest terminal time.
	detailBefore := now.Add(-time.Duration(s.cfg.Storage.FailureDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	successBefore := now.Add(-time.Duration(s.cfg.Storage.SuccessDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	summaryBefore := now.Add(-time.Duration(s.cfg.Storage.SummaryDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(s.ctx, `SELECT id FROM executions e WHERE settled=1 AND ended<>'' AND (
 ((ended<=CASE WHEN json_extract(body,'$.conclusion')='passed' AND state='finished' THEN ? ELSE ? END)
 AND (COALESCE(json_extract(body,'$.purged'),0)=0 OR EXISTS(SELECT 1 FROM evidence a WHERE a.task_id=e.id AND a.pinned=0)))
 OR ended<=?)
 ORDER BY CASE WHEN json_extract(body,'$.conclusion')='passed' THEN 0 ELSE 1 END, ended LIMIT 128`, successBefore, detailBefore, summaryBefore)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	e := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if e != nil {
		return e
	}
	for _, id := range ids {
		s.mu.Lock()
		err := func() error {
			defer s.mu.Unlock()
			t, err := s.task(id)
			if err != nil {
				return err
			}
			if !t.Terminal() || t.Ended == nil {
				return nil
			}
			if !s.performancePending[id].IsZero() {
				return nil
			}
			days := s.cfg.Storage.FailureDays
			if t.Conclusion == "passed" && t.State == "finished" {
				days = s.cfg.Storage.SuccessDays
			}
			expired := !now.Before(t.Ended.Add(time.Duration(days) * 24 * time.Hour))
			if expired {
				if err := s.purge(t); err != nil {
					return err
				}
			}
			if !now.Before(t.Ended.Add(time.Duration(s.cfg.Storage.SummaryDays) * 24 * time.Hour)) {
				if err := s.db.DeleteExecution(s.ctx, id); err != nil {
					return err
				}
				s.forget(id)
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	err = func() error {
		defer s.mu.Unlock()
		for id, t := range s.transfers {
			updated := t.Updated
			if updated.IsZero() {
				updated = t.Created
			}
			if t.TaskID == "" && s.uploading[id] == nil && !now.Before(updated.Add(s.cfg.Storage.UploadIdle.Value())) {
				if err := s.removeTransfer(t); err != nil {
					return err
				}
			}
		}
		// Failed/unbound receipts also expire; otherwise rejected admissions grow forever.
		if _, err := s.db.ExecContext(s.ctx, `DELETE FROM operations WHERE state<>'pending' AND ended<?`, now.Add(-time.Duration(s.cfg.Storage.SummaryDays)*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
			return err
		}
		for id, o := range s.operations {
			if o.State != "pending" && o.Ended != nil && now.Sub(*o.Ended) > time.Duration(s.cfg.Storage.SummaryDays)*24*time.Hour {
				delete(s.operations, id)
				delete(s.requests, o.RequestID)
			}
		}
		// Historical summaries and receipts remain queryable in SQLite, not in the
		// live execution cache. Fault-window collection and pending dispatch stay live.
		for _, t := range append([]*Task(nil), s.tasks...) {
			if !t.settled() || !s.performancePending[t.ID].IsZero() {
				continue
			}
			pending := false
			for _, o := range s.operations {
				if o.TaskID == t.ID && o.State == "pending" {
					pending = true
					break
				}
			}
			if !pending {
				s.forget(t.ID)
			}
		}
		for id, o := range s.operations {
			if o.State != "pending" {
				delete(s.operations, id)
				delete(s.requests, o.RequestID)
			}
		}
		return nil
	}()
	if err != nil {
		return err
	}
	// Failed transfer creation can leave a directory before its index commit.
	entries, err := os.ReadDir(filepath.Join(s.root.Path, "transfers"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !wire.IDPattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) < time.Hour {
			continue
		}
		var references int
		if err = s.db.QueryRowContext(s.ctx, "SELECT count(*) FROM transfers WHERE id=?", entry.Name()).Scan(&references); err != nil {
			return err
		}
		if references == 0 {
			if err = s.root.RemoveAll(path.Join("transfers", entry.Name())); err != nil {
				return err
			}
		}
	}
	if err := s.reclaimEvidence(now, false); err != nil {
		return err
	}
	if err := s.db.SweepBodies(s.ctx); err != nil {
		return err
	}
	if err := s.sweepExecutionFiles(now); err != nil {
		return err
	}
	return s.db.Compact(s.ctx)
}
func (s *Server) forget(id string) {
	for i, t := range s.tasks {
		if t.ID == id {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			break
		}
	}
	delete(s.runtime, id)

}
func (s *Server) removeTransfer(t *Transfer) error {
	if s.uploading[t.ID] != nil {
		return wire.Fail("conflict", "Upload is still settling")
	}
	t.State = "deleting"
	s.persist(t)
	s.mu.Unlock()
	removeErr := s.root.RemoveAll(path.Join("transfers", t.ID))
	s.mu.Lock()
	if err := removeErr; err != nil {
		return err
	}
	if _, err := s.db.ExecContext(s.ctx, "DELETE FROM transfers WHERE id=?", t.ID); err != nil {
		return err
	}
	delete(s.transfers, t.ID)
	s.transferLocks.Delete(t.ID)
	return nil
}
func (s *Server) changeEvidence(name string, a EvidenceChange) (any, error) {
	s.evidenceMu.Lock()
	defer s.evidenceMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.db.evidence(s.ctx, a.ArtifactID)
	if err != nil {
		return nil, err
	}
	if name == "evidence_delete" {
		var active int
		if err = s.db.QueryRowContext(s.ctx, "SELECT COUNT(*) FROM executions WHERE id=? AND settled=0", v.ExecutionID).Scan(&active); err != nil {
			return nil, err
		}
		if active > 0 || v.Pinned {
			return nil, wire.Fail("protected", "Evidence is active, required for cleanup or pinned")
		}
		if err = s.root.Remove(v.Path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		_, err = s.db.ExecContext(s.ctx, "DELETE FROM evidence WHERE id=?", v.ID)
	} else {
		v.Pinned = name == "evidence_pin"
		_, err = s.db.ExecContext(s.ctx, "UPDATE evidence SET pinned=?,body=? WHERE id=?", v.Pinned, wire.JSON(v), v.ID)
	}
	if err != nil {
		return nil, err
	}
	for _, t := range s.tasks {
		for i, x := range t.Artifacts {
			if x.ID == v.ID {
				if name == "evidence_delete" {
					t.Artifacts = append(t.Artifacts[:i], t.Artifacts[i+1:]...)
				} else {
					*x = *v
				}
				break
			}
		}
	}
	return map[string]any{"id": v.ID, "pinned": v.Pinned, "deleted": name == "evidence_delete"}, nil
}
func (s *Server) uploadObjects(a UploadObjectsRequest) (any, error) {
	limit := a.Limit
	if limit == 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(s.ctx, "SELECT name,expected_size,expected_sha256,body IS NOT NULL FROM transfer_objects WHERE transfer_id=? AND name>? ORDER BY name LIMIT ?", a.TransferID, a.Cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []any{}
	next := a.Cursor
	for rows.Next() {
		var size int64
		var hash string
		var published bool
		if err = rows.Scan(&next, &size, &hash, &published); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"name": next, "size": size, "sha256": hash, "published": published})
	}
	return map[string]any{"items": items, "next_cursor": next}, rows.Err()
}

func (s *Server) listTasksKind(a TaskListRequest, name string) (any, error) {
	kind := "lab"
	if name == "nix_job_list" {
		kind = "nixos"
	}
	return s.listStoredTasks(a, kind)
}
func (s *Server) listStoredTasks(a TaskListRequest, kind string) (any, error) {
	if a.Phase == "" {
		a.Phase = "active"
	}
	if !oneOf(a.Phase, "active", "all", "queued", "preparing", "running", "ended") || a.Limit < 0 || a.Limit > 200 {
		return nil, wire.Fail("invalid_argument", "Invalid phase or limit")
	}
	if a.Limit == 0 {
		a.Limit = 50
	}
	after := int64(0)
	if a.Cursor != "" {
		var err error
		after, err = strconv.ParseInt(a.Cursor, 10, 64)
		if err != nil || after < 0 {
			return nil, wire.Fail("invalid_argument", "Invalid execution cursor")
		}
	}
	filter := ""
	switch a.Phase {
	case "active":
		filter = " AND settled=0"
	case "all":
	case "ended":
		filter = " AND state NOT IN ('queued','preparing','running')"
	default:
		filter = " AND state='" + a.Phase + "'"
	}
	rows, err := s.db.QueryContext(s.ctx, "SELECT sequence,body FROM executions WHERE sequence>? AND (?='' OR kind=?)"+filter+" ORDER BY sequence LIMIT ?", after, kind, kind, a.Limit)
	if err != nil {
		return nil, err
	}
	type row struct {
		sequence int64
		body     []byte
	}
	values := []row{}
	for rows.Next() {
		var v row
		if err = rows.Scan(&v.sequence, &v.body); err != nil {
			break
		}
		values = append(values, v)
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if scanErr != nil {
		return nil, scanErr
	}
	items := []any{}
	next := a.Cursor
	for _, value := range values {
		t := newTask(kind, 0)
		if err = json.Unmarshal(value.body, &t.executionRecord); err != nil {
			return nil, err
		}
		if t.Kind == "nixos" {
			var metadata []byte
			if err = s.db.QueryRowContext(s.ctx, "SELECT body FROM nix_jobs WHERE id=?", t.ID).Scan(&metadata); err != nil {
				return nil, err
			}
			if err = json.Unmarshal(metadata, t.nix()); err != nil {
				return nil, err
			}
		}
		view := t.summaryView()
		var commands, endpoints, captures int
		if err = s.db.QueryRowContext(s.ctx, "SELECT (SELECT COUNT(*) FROM commands WHERE task_id=? AND needs_poll=1),(SELECT COUNT(*) FROM endpoints WHERE task_id=? AND state='open'),(SELECT COUNT(*) FROM captures WHERE task_id=? AND state NOT IN ('stopped','released'))", t.ID, t.ID, t.ID).Scan(&commands, &endpoints, &captures); err != nil {
			return nil, err
		}
		view["activity"] = map[string]int{"commands_active": commands, "endpoints_open": endpoints, "captures_active": captures}
		s.queueView(t, view)
		items = append(items, view)
		next = strconv.FormatInt(value.sequence, 10)
	}

	return *clone(&map[string]any{"items": items, "next_cursor": next}), nil
}
