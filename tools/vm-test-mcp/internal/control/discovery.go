package control

import "github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"

// queueView is computed under s.mu so readers immediately see admission and
// cleanup changes without a polling loop rewriting every queued document.
func (s *Server) queueView(t *Task, v map[string]any) {
	if t.State != "queued" {
		return
	}
	position := 0
	for _, other := range s.tasks {
		if other.State == "queued" {
			position++
		}
		if other.ID == t.ID {
			break
		}
	}
	reason, blocker := t.Reason, ""
	if s.active != "" && s.active != t.ID {
		blocker, reason = s.active, "executor_occupied"
		if active, err := s.task(s.active); err == nil && active.Terminal() {
			reason = "cleanup_pending"
			if active.cleanupState() == "blocked" {
				reason = "cleanup_blocked"
			}
		}
	} else if position > 1 {
		reason = "earlier_submission"
	} else if reason == "" {
		reason = "admission_pending"
	}
	v["waiting_reason"] = reason
	v["queue"] = map[string]any{"position": position, "blocking_task_id": blocker, "reason": reason}
}

func (s *Server) listTasks(a TaskListRequest) (any, error) {
	if a.Phase == "" {
		a.Phase = "active"
	}
	if !oneOf(a.Phase, "active", "all", "queued", "preparing", "running", "ended") || a.Limit < 0 || a.Limit > 200 {
		return nil, wire.Fail("invalid_argument", "phase must be active/all/queued/preparing/running/ended; limit must be 0..200")
	}
	if a.Limit == 0 {
		a.Limit = 50
	}
	start := 0
	if a.Cursor != "" {
		found := false
		for i, t := range s.tasks {
			if t.ID == a.Cursor {
				start, found = i+1, true
				break
			}
		}
		if !found {
			return nil, wire.Fail("invalid_argument", "cursor must be a task ID returned by this service")
		}
	}
	items := []any{}
	next := a.Cursor
	for _, t := range s.tasks[start:] {
		next = t.ID
		matches := a.Phase == "all" || a.Phase == t.phase() || a.Phase == "active" && (!t.Terminal() || t.Cleanup != "complete")
		if matches {
			v := t.summaryView()
			s.queueView(t, v)
			items = append(items, v)
			if len(items) == a.Limit {
				break
			}
		}
	}
	// Release all pointers before query releases s.mu and HTTP encodes the view.
	v := map[string]any{"items": items, "next_cursor": next}
	return *clone(&v), nil
}
