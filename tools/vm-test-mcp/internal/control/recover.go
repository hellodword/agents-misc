package control

import (
	"encoding/json"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (s *Server) recoverExecution(id, scope string) (any, error) {
	s.mu.Lock()
	t, err := s.task(id)
	if err == nil && !t.Terminal() {
		err = wire.Fail("invalid_state", "Recovery requires an ended execution")
	}
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if t.Kind == "nixos" {
		s.mu.Unlock()
		return s.recoverNix(id, scope)
	}
	if scope == "resources" {
		s.mu.Unlock()
		return s.cleanupTask(id)
	}
	defer s.mu.Unlock()
	if scope != "evidence" {
		return nil, wire.Fail("invalid_argument", "Recovery scope must be resources or evidence")
	}
	rows, err := s.db.QueryContext(s.ctx, "SELECT body FROM diagnostic_events WHERE task_id=? AND state='blocked'", id)
	if err != nil {
		return nil, err
	}
	var events []DiagnosticEvent
	for rows.Next() {
		var data []byte
		var e DiagnosticEvent
		if err = rows.Scan(&data); err != nil {
			break
		}
		if err = json.Unmarshal(data, &e); err != nil {
			break
		}
		events = append(events, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		e.State, e.Error = "captured", nil
		if err = s.storeDiagnostic(e); err != nil {
			return nil, err
		}
	}
	if err = s.refreshDiagnosticState(t); err != nil {
		return nil, err
	}
	s.diagnosticLifecycleLocked()
	return t.summaryView(), nil
}
