package guest

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type serviceHeartbeat struct {
	Time       time.Time            `json:"time"`
	Privileged bool                 `json:"privileged"`
	Protocol   int                  `json:"command_protocol"`
	Build      wire.BuildIdentity   `json:"build"`
	Process    wire.ProcessIdentity `json:"process"`
}

type serviceObservation struct {
	State     string            `json:"state"`
	Heartbeat *serviceHeartbeat `json:"heartbeat,omitempty"`
	Error     *wire.Error       `json:"error,omitempty"`
}

func observeService(root string, now time.Time, build wire.BuildIdentity, lookup func(int) (wire.ProcessIdentity, bool, error)) serviceObservation {
	v := serviceObservation{State: "unavailable"}
	var h serviceHeartbeat
	if err := readJSON(filepath.Join(root, "service.json"), &h); err != nil {
		v.Error = wire.Safe(wire.Fail("guest_service_unavailable", "Command service heartbeat is absent or invalid"))
		return v
	}
	v.Heartbeat = &h
	switch {
	case now.Sub(h.Time) < -time.Second || now.Sub(h.Time) >= 10*time.Second || !h.Privileged:
		v.Error = wire.Safe(wire.Fail("guest_service_unavailable", "Command service heartbeat is stale, future-dated or unprivileged"))
	case h.Protocol != wire.CommandProtocol || h.Build.BinaryState != "observed" || build.BinaryState != "observed" || h.Build.SHA256 == "" || h.Build.SHA256 != build.SHA256:
		v.Error = wire.Safe(wire.Fail("guest_service_mismatch", "Running command service and guest executable must have the same build and command protocol"))
	default:
		identity, alive, err := lookup(h.Process.PID)
		state := "matching"
		details := map[string]any{"stage": "process_identity"}
		switch {
		case os.IsNotExist(err):
			state = "not_found"
		case err != nil:
			state = "unobserved"
			details["permission_denied"] = os.IsPermission(err)
			var errno syscall.Errno
			if errors.As(err, &errno) {
				details["os_error_code"] = uint64(errno)
			}
		case !alive:
			state = "exited"
		case h.Process.Start == "" || identity != h.Process:
			state = "identity_mismatch"
		}
		if state == "matching" {
			v.State = "ready"
		} else {
			details["process_state"] = state
			v.Error = wire.Safe(&wire.Error{Code: "guest_service_unavailable", Message: "Command service process identity could not be confirmed", Details: details})
		}
	}
	return v
}
