package control

import (
	"context"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (b *VBoxBackend) native(ctx context.Context, t *Task, n *Node, action string, args ...string) ([]byte, error) {
	library, err := config.LibraryDir(b.Config.VirtualBox.LibraryDir, b.Config.VirtualBox.Executable)
	if err != nil {
		return nil, wire.Fail("backend_unavailable", "VirtualBox runtime library could not be located")
	}
	helper, err := config.Program(b.Config.VirtualBox.APIHelper, "vbox-api-helper")
	if err != nil {
		return nil, wire.Fail("backend_unavailable", "Native VirtualBox API helper could not be located")
	}
	started := time.Now()
	result, err := hostexec.Capture(ctx, b.Runner, helper, append([]string{library, n.UUID, action}, args...), nil)
	b.recordCall(started, 0, "virtualbox_api", action, t.ID, n.ID, result, err)
	return result.Stdout, b.executionError("api_helper/"+action, err)
}

// Power-off precedes release of the VM's direct session. Observe that separate
// boundary without acquiring a new session or terminating its GUI owner.
func (b *VBoxBackend) waitUnlocked(ctx context.Context, t *Task, n *Node) error {
	started := time.Now()
	var last struct {
		State          string `json:"state"`
		RuntimeVersion uint32 `json:"runtime_version"`
	}
	for {
		raw, err := b.native(ctx, t, n, "session")
		if err == nil {
			err = wire.Decode(raw, &last)
		}
		if err != nil {
			return &wire.Error{Code: "session_observation_failed", Message: "VM session unlock could not be observed", Details: map[string]any{"cause": wire.Safe(err), "last_state": last.State, "elapsed_ms": time.Since(started).Milliseconds(), "deadline": deadline(ctx)}}
		}
		if last.State == "unlocked" {
			return nil
		}
		if !oneOf(last.State, "locked", "unlocking", "spawning") {
			return wire.Fail("session_state_unknown", "VirtualBox returned an unrecognized session state")
		}
		select {
		case <-ctx.Done():
			return &wire.Error{Code: "session_unlock_timeout", Message: "VM session remained locked within the cleanup budget", Details: map[string]any{"last_state": last.State, "elapsed_ms": time.Since(started).Milliseconds(), "deadline": deadline(ctx), "cause": wire.Safe(ctx.Err())}}
		case <-time.After(250 * time.Millisecond):
		}
	}
}
