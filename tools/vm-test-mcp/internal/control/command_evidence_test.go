package control

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type unavailableCommands struct{ fakeBackend }

func (b *unavailableCommands) Action(ctx context.Context, t *Task, n *Node, a NodeAction) (json.RawMessage, error) {
	if oneOf(a.Action, "command_poll", "command_cancel") {
		return nil, wire.Fail("backend_timeout", "Synthetic unavailable command channel")
	}
	return b.fakeBackend.Action(ctx, t, n, a)
}
func TestVMReleasePreservesExitEvidenceAndUncertainty(t *testing.T) {
	s, task, r := fixtureLab(t, &unavailableCommands{}, time.Second)
	code := 37
	known := &Command{ID: wire.ID(), NodeID: task.Nodes[0].ID, CommandStatus: wire.CommandStatus{State: "exited", Final: true, ExitCode: &code}}
	unknown := &Command{ID: wire.ID(), NodeID: task.Nodes[0].ID, Cancellation: "requested", CommandStatus: wire.CommandStatus{State: "running"}}
	failed := &Command{ID: wire.ID(), NodeID: task.Nodes[1].ID, CommandStatus: wire.CommandStatus{State: "uncertain", Final: true, ExitCode: &code, Termination: &wire.Termination{Source: "guest_supervisor", ProcessExited: true, Error: &wire.Error{Code: "supervision_failed", Message: "Group stop was refused"}}}}
	task.Commands = []*Command{known, unknown, failed}
	task.State = "cancelled"
	r.cancel()
	s.cleanup(task)
	if task.Cleanup != "complete" {
		t.Fatal(task.Public())
	}
	for _, c := range task.Commands {
		if c.Release == nil || c.Release.Source != "vm_deleted" {
			t.Fatal("missing release evidence", c)
		}
	}
	if known.State != "exited" || known.ExitCode == nil || *known.ExitCode != 37 {
		t.Fatal("known exit replaced", known)
	}
	if unknown.State != "uncertain" || !unknown.Final || unknown.ExitCode != nil || unknown.Cancellation != "requested" || unknown.Error == "" {
		t.Fatal("cleanup invented a command result", unknown)
	}
	if failed.Termination.Source != "guest_supervisor" || failed.Termination.Error == nil || failed.State != "uncertain" || *failed.ExitCode != 37 {
		t.Fatal("supervision failure overwritten", failed)
	}
}
