package control

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type powerBackend struct {
	fakeBackend
	statusMu sync.Mutex
	status   NodeStatus
}

func (b *powerBackend) Action(ctx context.Context, t *Task, n *Node, a NodeAction) (json.RawMessage, error) {
	if a.Action == "node_status" {
		b.statusMu.Lock()
		defer b.statusMu.Unlock()
		return wire.JSON(b.status), nil
	}
	return b.fakeBackend.Action(ctx, t, n, a)
}
func (b *powerBackend) observe(power, boot string, ready bool) {
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	b.status = NodeStatus{State: power, Guest: map[string]any{"boot_id": boot, "prepared": ready, "privileged": ready}}
}
func TestPowerWaitsForFreshBootAndCanBeSuperseded(t *testing.T) {
	b := &powerBackend{}
	s, lab, r := fixtureLab(t, b, time.Second)
	node := lab.lab().Nodes[0]
	node.Guest = map[string]any{"boot_id": "first", "prepared": true, "privileged": true}
	begin := func(request, action, condition string) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := s.power(PowerRequest{NodeChange: NodeChange{Change: Change{request}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: node.ID}}, Action: action, WaitUntil: condition})
			done <- err
		}()
		waitFor(t, func() bool {
			s.mu.Lock()
			defer s.mu.Unlock()
			return node.Expected != nil && node.Expected.RequestID == request && node.Expected.Dispatched != nil
		})
		return done
	}
	finished := func(done chan error, want string) {
		t.Helper()
		select {
		case err := <-done:
			if want == "" && err != nil || want != "" && (err == nil || wire.Safe(err).Code != want) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("power operation did not converge")
		}
	}
	reboot := begin("reboot", "reboot", "ready")
	for _, sample := range []struct {
		boot  string
		ready bool
	}{{"first", true}, {"second", false}} {
		b.observe("running", sample.boot, sample.ready)
		s.pollNode(lab, r, node.ID)
		s.mu.Lock()
		ready := node.Expected.Ready
		s.mu.Unlock()
		if ready {
			t.Fatal("stale boot or unavailable command service satisfied readiness")
		}
	}
	b.observe("running", "second", true)
	s.pollNode(lab, r, node.ID)
	finished(reboot, "")

	reset := begin("reset", "reset", "ready")
	off := begin("off", "poweroff", "ready")
	b.observe("poweroff", "", false)
	s.pollNode(lab, r, node.ID)
	finished(reset, "superseded")
	finished(off, "")

	start := begin("start", "start", "power_state")
	b.observe("running", "third", false)
	s.pollNode(lab, r, node.ID)
	finished(start, "")
	s.mu.Lock()
	ready := node.Expected.Ready
	s.mu.Unlock()
	if ready {
		t.Fatal("power-state observation invented guest readiness")
	}
}
