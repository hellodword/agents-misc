package control

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func directoryBytes(root string, excludeRuntime bool) (int64, error) {
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
			size += st.Size()
		}
		return nil
	})
	return size, err
}
func (s *Server) evidenceUsage() (int64, error) {
	size, err := directoryBytes(filepath.Join(s.root.Path, "tasks"), true)
	if err != nil {
		return 0, err
	}
	bodies, err := directoryBytes(filepath.Join(s.root.Path, "bodies"), false)
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
		n, err := directoryBytes(filepath.Join(s.root.Path, "transfers", id), false)
		if err != nil {
			return 0, err
		}
		size += n
	}
	return size, nil
}
func (s *Server) inputUsage() int64 {
	var n int64
	for _, t := range s.transfers {
		if t.State != "purged" && t.State != "retained" {
			n += t.Bytes
		}
	}
	for _, u := range s.uploading {
		n += u.reserved
	}
	return n
}

// Caller holds s.mu. Active work and unconfirmed cleanup are protected even
// when they alone exceed the budget. No control or cleanup operation is gated.
func (s *Server) admission() error {
	used, err := s.evidenceUsage()
	if err != nil {
		return err
	}
	for used > s.cfg.Storage.EvidenceBytes {
		previous := used
		if err = s.reclaim(time.Now().UTC(), true); err != nil {
			return err
		}
		used, err = s.evidenceUsage()
		if err != nil {
			return err
		}
		if used >= previous {
			break
		}
	}
	s.storageBlocked = used > s.cfg.Storage.EvidenceBytes
	if s.storageBlocked {
		return wire.Fail("storage_pressure", "Protected evidence exceeds the storage budget; unpin or delete eligible evidence")
	}
	return nil
}
func (s *Server) expireRetention() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if now.Sub(s.lastRetention) < time.Minute {
		return
	}
	s.lastRetention = now
	if err := s.reclaim(now, false); err != nil {
		return
	}
	used, err := s.evidenceUsage()
	if err == nil && used > s.cfg.Storage.EvidenceBytes {
		_ = s.reclaim(now, true)
		used, err = s.evidenceUsage()
	}
	s.storageBlocked = err != nil || used > s.cfg.Storage.EvidenceBytes
}
func (s *Server) reclaim(now time.Time, pressure bool) error {
	// Bounded batches, sorted by successful result first and oldest terminal time.
	detailBefore := now.Add(-time.Duration(s.cfg.Storage.FailureDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	successBefore := now.Add(-time.Duration(s.cfg.Storage.SuccessDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	summaryBefore := now.Add(-time.Duration(s.cfg.Storage.SummaryDays) * 24 * time.Hour).Format(time.RFC3339Nano)
	rows, err := s.db.QueryContext(s.ctx, `SELECT id FROM executions e WHERE cleanup='complete' AND ended<>'' AND (
 ((? OR ended<=CASE WHEN json_extract(body,'$.conclusion')='passed' AND state='finished' THEN ? ELSE ? END)
 AND (COALESCE(json_extract(body,'$.purged'),0)=0 OR EXISTS(SELECT 1 FROM evidence a WHERE a.task_id=e.id AND a.pinned=0)))
 OR (ended<=? AND NOT EXISTS(SELECT 1 FROM evidence a WHERE a.task_id=e.id AND a.pinned=1)))
 ORDER BY CASE WHEN json_extract(body,'$.conclusion')='passed' THEN 0 ELSE 1 END, ended LIMIT 128`, pressure, successBefore, detailBefore, summaryBefore)
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
		t, err := s.task(id)
		if err != nil {
			return err
		}
		if !t.Terminal() || t.Ended == nil {
			continue
		}
		if !s.performancePending[id].IsZero() {
			continue
		}
		days := s.cfg.Storage.FailureDays
		if t.Conclusion == "passed" && t.State == "finished" {
			days = s.cfg.Storage.SuccessDays
		}
		expired := !now.Before(t.Ended.Add(time.Duration(days) * 24 * time.Hour))
		if expired || pressure {
			if err := s.purge(t); err != nil {
				continue
			}
		}
		if !now.Before(t.Ended.Add(time.Duration(s.cfg.Storage.SummaryDays)*24*time.Hour)) && len(t.Artifacts) == 0 {
			if err := s.db.DeleteExecution(s.ctx, id); err != nil {
				return err
			}
			s.forget(id)
		}
		if pressure {
			used, err := s.evidenceUsage()
			if err != nil {
				return err
			}
			if used <= s.cfg.Storage.LowWaterBytes {
				break
			}
		}
	}
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
	if _, err := s.db.ExecContext(s.ctx, `DELETE FROM revisions WHERE key IN (SELECT 'operation/'||id FROM operations WHERE state<>'pending' AND created<? AND (task_id='' OR NOT EXISTS(SELECT 1 FROM executions e WHERE e.id=operations.task_id)))`, now.Add(-time.Duration(s.cfg.Storage.SummaryDays)*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(s.ctx, `DELETE FROM operations WHERE state<>'pending' AND created<? AND (task_id='' OR NOT EXISTS(SELECT 1 FROM executions e WHERE e.id=operations.task_id))`, now.Add(-time.Duration(s.cfg.Storage.SummaryDays)*24*time.Hour).Format(time.RFC3339Nano)); err != nil {
		return err
	}
	for id, o := range s.operations {
		if o.State != "pending" && now.Sub(o.Created) > time.Duration(s.cfg.Storage.SummaryDays)*24*time.Hour {
			delete(s.operations, id)
			delete(s.requests, o.RequestID)
		}
	}
	// Historical summaries and receipts remain queryable in SQLite, not in the
	// live execution cache. Fault-window collection and pending dispatch stay live.
	for _, t := range append([]*Task(nil), s.tasks...) {
		if !t.Terminal() || t.Cleanup != "complete" || !s.performancePending[t.ID].IsZero() {
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
	s.revisions = map[string]revisionState{}
	// Failed transfer creation can leave a directory before its index commit.
	entries, err := os.ReadDir(filepath.Join(s.root.Path, "transfers"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !wire.IDPattern.MatchString(entry.Name()) || s.uploading[entry.Name()] != nil {
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
	if err := s.db.SweepBodies(s.ctx); err != nil {
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
	for k := range s.revisions {
		if k == "lab/"+id || k == "nix_job/"+id {
			delete(s.revisions, k)
		}
	}
}
func (s *Server) removeTransfer(t *Transfer) error {
	if s.uploading[t.ID] != nil {
		return wire.Fail("conflict", "Upload is still settling")
	}
	// Removing the files first is recoverable: the next sweep retries the index.
	if err := s.root.RemoveAll(path.Join("transfers", t.ID)); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(s.ctx, "DELETE FROM transfers WHERE id=?", t.ID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(s.ctx, "DELETE FROM revisions WHERE key=?", "upload/"+t.ID); err != nil {
		return err
	}
	delete(s.transfers, t.ID)
	s.transferLocks.Delete(t.ID)
	return nil
}
func (s *Server) changeEvidence(name string, a EvidenceChange) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.task(a.TaskID.TaskID)
	if err != nil {
		return nil, err
	}
	for i, v := range t.Artifacts {
		if v.ID != a.ArtifactID {
			continue
		}
		if name == "evidence_delete" {
			if !t.Terminal() || t.Cleanup != "complete" || v.Pinned {
				return nil, wire.Fail("protected", "Evidence is active, required for cleanup or pinned")
			}
			if err = s.root.Remove(v.Path); err != nil && !os.IsNotExist(err) {
				return nil, err
			}
			if _, err = s.db.ExecContext(s.ctx, "DELETE FROM evidence WHERE id=?", v.ID); err != nil {
				return nil, err
			}
			t.Artifacts = append(t.Artifacts[:i], t.Artifacts[i+1:]...)
		} else {
			v.Pinned = name == "evidence_pin"
			s.save(t)
		}
		return map[string]any{"id": v.ID, "pinned": v.Pinned, "deleted": name == "evidence_delete"}, nil
	}
	return nil, wire.Fail("not_found", "Evidence not found")
}
func (s *Server) uploadObjects(a UploadObjectsRequest) (any, error) {
	t, err := s.transfer(a.TransferID)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for k := range t.Objects {
		names = append(names, k)
	}
	sort.Strings(names)
	start, end, err := page(a.Cursor, a.Limit, len(names))
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, k := range names[start:end] {
		o := t.Objects[k]
		items = append(items, map[string]any{"name": k, "size": o.Size, "sha256": o.SHA256})
	}
	return map[string]any{"items": items, "next_cursor": end}, nil
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
		if err := s.db.QueryRowContext(s.ctx, "SELECT sequence FROM executions WHERE id=?", a.Cursor).Scan(&after); err != nil {
			return nil, wire.Fail("invalid_argument", "Unknown execution cursor")
		}
	}
	filter := ""
	switch a.Phase {
	case "active":
		filter = " AND cleanup<>'complete'"
	case "all":
	case "ended":
		filter = " AND state NOT IN ('queued','preparing','running')"
	default:
		filter = " AND state='" + a.Phase + "'"
	}
	rows, err := s.db.QueryContext(s.ctx, "SELECT id FROM executions WHERE sequence>? AND (?='' OR kind=?)"+filter+" ORDER BY sequence LIMIT ?", after, kind, kind, a.Limit)
	if err != nil {
		return nil, err
	}
	ids := []string{}
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
		return nil, err
	}
	if e != nil {
		return nil, e
	}
	items := []any{}
	next := a.Cursor
	for _, id := range ids {
		t, err := s.task(id)
		if err != nil {
			return nil, err
		}
		v := t.summaryView()
		s.queueView(t, v)
		items = append(items, v)
		next = id
	}
	return *clone(&map[string]any{"items": items, "next_cursor": next}), nil
}
