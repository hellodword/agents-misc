package control

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// Publication can be interrupted after fsync and before its index transaction.
// Active executions protect their whole workspace, including unfinished exports.
func (s *Server) sweepExecutionFiles(now time.Time) error {
	entries, err := os.ReadDir(filepath.Join(s.root.Path, "tasks"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !wire.IDPattern.MatchString(entry.Name()) {
			continue
		}
		id := entry.Name()
		var active int
		if err = s.db.QueryRowContext(s.ctx, `SELECT
   (SELECT count(*) FROM executions WHERE id=? AND cleanup<>'complete')+
   (SELECT count(*) FROM operations WHERE task_id=? AND state='pending')`, id, id).Scan(&active); err != nil {
			return err
		}
		s.mu.Lock()
		pending := !s.performancePending[id].IsZero()
		s.mu.Unlock()
		if active > 0 || pending {
			continue
		}
		s.evidenceMu.Lock()
		err = s.sweepExecutionDirectory(id, now)
		s.evidenceMu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) sweepExecutionDirectory(id string, now time.Time) error {
	var refs int
	if err := s.db.QueryRowContext(s.ctx, `SELECT (SELECT count(*) FROM executions WHERE id=?)+(SELECT count(*) FROM evidence WHERE task_id=?)`, id, id).Scan(&refs); err != nil {
		return err
	}
	dir := path.Join("tasks", id)
	if refs == 0 {
		info, err := s.root.Lstat(dir)
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) >= time.Hour {
			return s.root.RemoveAll(dir)
		}
		return nil
	}
	artifacts := path.Join(dir, "artifacts")
	entries, err := os.ReadDir(filepath.Join(s.root.Path, artifacts))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		base := strings.Split(name, ".")[0]
		if !wire.IDPattern.MatchString(base) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) < time.Hour {
			continue
		}
		relative := path.Join(artifacts, name)
		if err = s.db.QueryRowContext(s.ctx, "SELECT count(*) FROM evidence WHERE json_extract(body,'$.path')=?", relative).Scan(&refs); err != nil {
			return err
		}
		if refs == 0 {
			if err = s.root.Remove(relative); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
