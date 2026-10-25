package control

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type unlockRunner struct {
	configPath, uuid, api                    string
	states                                   []string
	stateIndex, unregister, detach, poweroff int
	missing, foreignAfterUnlock              bool
}

func (r *unlockRunner) Run(ctx context.Context, exe string, args []string, _ io.Reader) ([]byte, error) {
	if exe == r.api {
		if len(args) != 3 || args[2] != "session" {
			return nil, fmt.Errorf("unexpected native action")
		}
		state := r.states[min(r.stateIndex, len(r.states)-1)]
		r.stateIndex++
		return wire.JSON(map[string]any{"state": state, "runtime_version": 7002020}), nil
	}
	switch args[0] {
	case "list":
		if r.missing {
			return nil, nil
		}
		return []byte(fmt.Sprintf("\"fixture\" {%s}\n", r.uuid)), nil
	case "showvminfo":
		config := r.configPath
		if r.foreignAfterUnlock && r.stateIndex > 0 {
			config = "/outside/foreign.vbox"
		}
		return []byte(fmt.Sprintf("UUID=%q\nCfgFile=%q\nVMState=\"poweroff\"\n\"SATA-1-0\"=\"/borrowed/install.iso\"\n", r.uuid, config)), nil
	case "controlvm":
		r.poweroff++
	case "storageattach":
		r.detach++
	case "unregistervm":
		r.unregister++
		r.missing = true
	default:
		return nil, fmt.Errorf("unexpected deletion command")
	}
	return nil, ctx.Err()
}

func TestDeleteWaitsForSessionUnlockBeforeDetachingAndVerifiesRelease(t *testing.T) {
	for _, gui := range []bool{false, true} {
		t.Run(fmt.Sprint(gui), func(t *testing.T) {
			cfg := testConfig(t)
			b := &VBoxBackend{Config: cfg}
			task := newTask("lab", 60)
			n := &Node{ID: wire.ID(), UUID: wire.UUID(), NodeRequest: NodeRequest{Template: "windows", GUI: gui}, Intent: true}
			r := &unlockRunner{uuid: n.UUID, api: cfg.VirtualBox.APIHelper, configPath: filepath.Join(b.vmDir(task, n), "clone.vbox"), states: []string{"locked", "unlocking", "unlocked"}}
			b.Runner = r
			b.guestSlots.Store(n.UUID, make(chan struct{}, 4))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := b.Delete(ctx, task, n); err != nil {
				t.Fatal(err)
			}
			if r.stateIndex != 3 || r.unregister != 1 || r.detach != 1 || r.poweroff != 0 {
				t.Fatal(r)
			}
			if _, exists := b.guestSlots.Load(n.UUID); exists {
				t.Fatal("released VM retained a capacity pool")
			}
			if err := b.Delete(ctx, task, n); err != nil || r.unregister != 1 || r.stateIndex != 3 {
				t.Fatal(r, err)
			}
		})
	}
}

func TestDeleteKeepsResourcesWhenSessionCannotReleaseOrOwnershipChanges(t *testing.T) {
	for _, mode := range []string{"locked", "unknown", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			cfg := testConfig(t)
			b := &VBoxBackend{Config: cfg}
			task := newTask("lab", 60)
			n := &Node{ID: wire.ID(), UUID: wire.UUID(), NodeRequest: NodeRequest{Template: "windows"}, Intent: true}
			r := &unlockRunner{uuid: n.UUID, api: cfg.VirtualBox.APIHelper, configPath: filepath.Join(b.vmDir(task, n), "clone.vbox"), states: []string{mode}, foreignAfterUnlock: mode == "foreign"}
			if mode == "foreign" {
				r.states = []string{"unlocked"}
			}
			b.Runner = r
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			err := b.Delete(ctx, task, n)
			if err == nil || r.detach != 0 || r.unregister != 0 {
				t.Fatal(r, err)
			}
			want := map[string]string{"locked": "session_unlock_timeout", "unknown": "session_state_unknown", "foreign": "ownership_mismatch"}[mode]
			if wire.Safe(err).Code != want {
				t.Fatal(err)
			}
		})
	}
}
