package control

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
	v["queue"] = map[string]any{"position": position, "blocking_execution_id": blocker, "reason": reason}
}
