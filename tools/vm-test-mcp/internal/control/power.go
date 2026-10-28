package control

import (
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"time"
)

func bootOf(guest map[string]any) string { value, _ := guest["boot_id"].(string); return value }
func (s *Server) power(a PowerRequest) (any, error) {
	if _, err := s.action(a.NodeID, NodeAction{Action: a.Action, RequestID: a.RequestID}); err != nil {
		return nil, err
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		s.mu.Lock()
		t, err := s.task(a.TaskID.TaskID)
		var n *Node
		if err == nil {
			n, err = t.Node(a.NodeID.NodeID)
		}
		if err == nil && t.Terminal() {
			err = wire.Fail("superseded", "Execution ended while waiting for node power")
		}
		if err == nil && (n.Expected == nil || n.Expected.RequestID != a.RequestID) {
			err = wire.Fail("superseded", "A subsequent power operation replaced this operation")
		}
		if err == nil {
			expected := n.Expected
			ready := expected.PowerObserved
			if a.WaitUntil != "power_state" && oneOf(a.Action, "start", "reboot", "reset", "resume") {
				ready = ready && expected.Ready
			}
			if ready {
				view := map[string]any{"node_id": n.ID, "state": n.State, "ready": expected.Ready}
				s.mu.Unlock()
				return view, nil
			}
			if expected.Until != nil && time.Now().After(*expected.Until) {
				err = wire.Fail("readiness_timeout", "Node did not reach the requested power and readiness condition")
			}
		}
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		select {
		case <-s.ctx.Done():
			return nil, wire.Fail("interrupted", "Power observation interrupted by controller shutdown")
		case <-tick.C:
		}
	}
}
