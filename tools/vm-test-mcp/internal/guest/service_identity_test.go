package guest

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestServiceRequiresCurrentBuildAndLiveIdentity(t *testing.T) {
	now := time.Now().UTC()
	build := wire.BuildIdentity{Version: wire.Version, BinaryState: "observed", SHA256: "synthetic-current-image"}
	identity := wire.ProcessIdentity{PID: 123, Start: "synthetic-creation-time"}
	for _, tc := range []struct {
		name, want string
		change     func(*serviceHeartbeat)
		alive      bool
	}{
		{"ready", "", func(*serviceHeartbeat) {}, true},
		{"old binary", "guest_service_mismatch", func(h *serviceHeartbeat) { h.Build.SHA256 = "synthetic-old-image" }, true},
		{"old protocol", "guest_service_mismatch", func(h *serviceHeartbeat) { h.Protocol = 0 }, true},
		{"unknown binary", "guest_service_mismatch", func(h *serviceHeartbeat) { h.Build.BinaryState = "unavailable" }, true},
		{"stale", "guest_service_unavailable", func(h *serviceHeartbeat) { h.Time = now.Add(-10 * time.Second) }, true},
		{"future", "guest_service_unavailable", func(h *serviceHeartbeat) { h.Time = now.Add(time.Minute) }, true},
		{"filtered token", "guest_service_unavailable", func(h *serviceHeartbeat) { h.Privileged = false }, true},
		{"stopped", "guest_service_unavailable", func(*serviceHeartbeat) {}, false},
		{"PID reused", "guest_service_unavailable", func(h *serviceHeartbeat) { h.Process.Start = "old-creation-time" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			h := serviceHeartbeat{Time: now, Privileged: true, Protocol: wire.CommandProtocol, Build: build, Process: identity}
			tc.change(&h)
			if err := atomic(filepath.Join(root, "service.json"), h); err != nil {
				t.Fatal(err)
			}
			v := observeService(root, now, build, func(pid int) (wire.ProcessIdentity, bool, error) {
				if pid != identity.PID {
					t.Fatal(pid)
				}
				return identity, tc.alive, nil
			})
			if tc.want == "" {
				if v.State != "ready" || v.Error != nil {
					t.Fatal(v)
				}
			} else if v.State != "unavailable" || v.Error == nil || v.Error.Code != tc.want {
				t.Fatal(v)
			}
		})
	}
	for _, tc := range []struct {
		name, state string
		err         error
	}{
		{"query denied", "unobserved", os.ErrPermission},
		{"query failed", "unobserved", syscall.EIO},
		{"missing process", "not_found", os.ErrNotExist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			h := serviceHeartbeat{Time: now, Privileged: true, Protocol: wire.CommandProtocol, Build: build, Process: identity}
			if err := atomic(filepath.Join(root, "service.json"), h); err != nil {
				t.Fatal(err)
			}
			v := observeService(root, now, build, func(int) (wire.ProcessIdentity, bool, error) { return wire.ProcessIdentity{}, false, tc.err })
			if v.State != "unavailable" || v.Error == nil {
				t.Fatal(v)
			}
			details := v.Error.Details
			if details["process_state"] != tc.state {
				t.Fatal(v.Error)
			}
			if tc.err == os.ErrPermission && details["permission_denied"] != true {
				t.Fatal(details)
			}
			if tc.err == syscall.EIO && details["os_error_code"] != uint64(syscall.EIO) {
				t.Fatal(details)
			}
		})
	}
	t.Run("legacy heartbeat cannot prove new service", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "service.json"), wire.JSON(map[string]any{"time": now, "privileged": true}), 0600); err != nil {
			t.Fatal(err)
		}
		v := observeService(root, now, build, func(int) (wire.ProcessIdentity, bool, error) { panic("no identity to query") })
		if v.Error == nil || v.Error.Code != "guest_service_mismatch" {
			t.Fatal(v)
		}
	})
}
