package control

import (
	"context"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (s *Server) extendTask(a ExtendRequest) (any, error) {
	s.mu.Lock()
	t, err := s.task(a.TaskID.TaskID)
	r := s.runtime[a.TaskID.TaskID]
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, wire.Fail("invalid_state", "Task has not started")
	}
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	lock := s.nodeLock(r, "deadline")
	if err = lockContext(ctx, lock); err != nil {
		return nil, err
	}
	defer lock.Unlock()
	s.mu.Lock()
	if a.Seconds < 1 || a.Seconds > s.cfg.Tasks.MaxDuration.Seconds() || t.Started == nil || t.Terminal() || t.Deadline == nil {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "Task cannot be extended")
	}
	until := t.Deadline.Add(time.Duration(a.Seconds) * time.Second)
	if until.After(t.Started.Add(s.cfg.Tasks.MaxDuration.Value())) {
		s.mu.Unlock()
		return nil, wire.Fail("limit_exceeded", "Maximum total task duration exceeded")
	}
	snapshot := clone(t)
	snapshot.Deadline = &until
	s.mu.Unlock()
	if snapshot.Kind == "nixos" {
		if _, err = s.backend.Nix(ctx, "extend", snapshot); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Terminal() {
		return nil, wire.Fail("invalid_state", "Task ended before the extension was confirmed")
	}
	t.Deadline = &until
	s.save(t)
	return t.Public(), nil
}
