package control

import (
	"context"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

const probeInterval = 5 * time.Second
const probeTimeout = 10 * time.Second
const diagnosticBudget = 30 * time.Second

type Observation struct {
	State        string      `json:"state"`
	Checked      *time.Time  `json:"checked,omitempty"`
	LastSuccess  *time.Time  `json:"last_success,omitempty"`
	FailureSince *time.Time  `json:"failure_since,omitempty"`
	Failures     int         `json:"failures"`
	Successes    int         `json:"successes"`
	Error        *wire.Error `json:"error,omitempty"`
}

func (o *Observation) update(now time.Time, err *wire.Error, after time.Duration, immediate bool) {
	o.Checked = &now
	if err == nil {
		o.LastSuccess, o.FailureSince, o.Error = &now, nil, nil
		o.Failures = 0
		o.Successes++
		if o.State != "unavailable" || o.Successes >= 2 {
			o.State = "available"
		}
		return
	}
	o.Successes = 0
	o.Error = err
	if err.Code == "resource_busy" {
		if o.State != "unavailable" {
			o.State = "unknown"
		}
		return
	}
	if o.FailureSince == nil {
		o.FailureSince = &now
	}
	o.Failures++
	if immediate || (o.Failures >= 3 && now.Sub(*o.FailureSince) >= after) {
		o.State = "unavailable"
	} else if o.State != "unavailable" {
		o.State = "suspect"
	}
}

type Health struct {
	VirtualBox   Observation `json:"virtualbox"`
	GuestControl Observation `json:"guest_control"`
	Commands     Observation `json:"commands"`
}

func (h Health) unavailable() bool {
	return h.VirtualBox.State == "unavailable" || h.GuestControl.State == "unavailable" || h.Commands.State == "unavailable"
}
func (h *Health) normalize() {
	for _, o := range []*Observation{&h.VirtualBox, &h.GuestControl, &h.Commands} {
		if o.State == "" {
			o.State = "unknown"
		}
	}
}

// Startup permits a responsive guest to keep initializing until its startup
// deadline. A control path that stops responding has its own shorter budget.
func (h *Health) startupFailure(now time.Time, status *NodeStatus, err error, after time.Duration) error {
	if err != nil {
		h.VirtualBox.update(now, wire.Safe(err), after, false)
	} else {
		h.VirtualBox.update(now, nil, after, false)
		if h.GuestControl.LastSuccess == nil && status.GuestError == nil {
			// Initial boot establishes this capability for the first time. The
			// runtime recovery threshold applies only after that first contact.
			h.GuestControl.State = "unknown"
		}
		h.GuestControl.update(now, status.GuestError, after, false)
		serviceErr := status.ServiceError
		if serviceErr != nil && serviceErr.Code == "guest_not_ready" {
			serviceErr = nil
		}
		if status.GuestError == nil {
			h.Commands.update(now, serviceErr, after, status.PreparationFailed)
		}
	}
	if h.VirtualBox.State == "unavailable" || (h.GuestControl.State == "unavailable" && h.GuestControl.LastSuccess != nil) || h.Commands.State == "unavailable" {
		return &wire.Error{Code: "node_unavailable", Message: "Node control capability remained unresponsive during preparation", Details: map[string]any{"observations": h}}
	}
	return nil
}

type Preparation struct {
	Stage        string      `json:"stage"`
	Started      *time.Time  `json:"started,omitempty"`
	StageStarted *time.Time  `json:"stage_started,omitempty"`
	Deadline     *time.Time  `json:"deadline,omitempty"`
	Updated      time.Time   `json:"updated"`
	Error        *wire.Error `json:"error,omitempty"`
}
type ExpectedState struct {
	Action    string     `json:"action"`
	Power     string     `json:"power"`
	Requested time.Time  `json:"requested"`
	Until     *time.Time `json:"until,omitempty"`
}
type NodeStatus struct {
	Identity          *NodeIdentity      `json:"identity,omitempty"`
	Polling           *PollSchedule      `json:"polling,omitempty"`
	State             string             `json:"state"`
	GuestSessions     *GuestSessionState `json:"guest_sessions,omitempty"`
	Guest             map[string]any     `json:"guest,omitempty"`
	GuestError        *wire.Error        `json:"guest_error,omitempty"`
	ServiceError      *wire.Error        `json:"service_error,omitempty"`
	PreparationFailed bool               `json:"preparation_failed,omitempty"`
}
type CleanupStep struct {
	State    string      `json:"state"`
	Attempts int         `json:"attempts"`
	Started  *time.Time  `json:"started,omitempty"`
	Deadline *time.Time  `json:"deadline,omitempty"`
	Updated  time.Time   `json:"updated"`
	Error    *wire.Error `json:"error,omitempty"`
}

type progressKey struct{}
type progressFunc func(string, *NodeStatus, error)

func reportProgress(ctx context.Context, stage string, status *NodeStatus, err error) {
	if f, ok := ctx.Value(progressKey{}).(progressFunc); ok {
		f(stage, status, err)
	}
}

func (s *Server) observeNode(n *Node, status *NodeStatus, err error, now time.Time) bool {
	wasUnavailable := n.Health.unavailable()
	after := s.cfg.VirtualBox.UnavailableAfter.Value()
	if err != nil {
		n.Health.VirtualBox.update(now, wire.Safe(err), after, false)
		for _, o := range []*Observation{&n.Health.GuestControl, &n.Health.Commands} {
			if o.State != "unavailable" {
				o.State = "unknown"
			}
		}
	} else {
		n.Health.VirtualBox.update(now, nil, after, false)
		n.State, n.Guest, n.GuestSessions = status.State, status.Guest, status.GuestSessions
		expected := n.Expected != nil && ((n.Expected.Until != nil && now.Before(*n.Expected.Until)) || (status.State != "running" && n.Expected.Power == status.State))
		if oneOf(status.State, "aborted", "stuck", "gurumeditation") || status.PreparationFailed {
			expected = false
		}
		if status.State != "running" {
			if expected {
				n.Health.GuestControl = Observation{State: "expected", Checked: &now}
				n.Health.Commands = Observation{State: "expected", Checked: &now}
			} else {
				e := &wire.Error{Code: "node_stopped", Message: "Node is not running", Details: map[string]any{"power": status.State}}
				n.Health.GuestControl.update(now, e, after, true)
				n.Health.Commands.update(now, e, after, true)
			}
		} else if expected && (status.GuestError != nil || status.ServiceError != nil) {
			n.Health.GuestControl = Observation{State: "expected", Checked: &now, Error: status.GuestError}
			n.Health.Commands = Observation{State: "expected", Checked: &now, Error: status.ServiceError}
		} else {
			n.Health.GuestControl.update(now, status.GuestError, after, false)
			serviceErr := status.ServiceError
			if status.GuestError != nil {
				serviceErr = status.GuestError
			}
			if serviceErr == nil && (status.Guest["prepared"] != true || status.Guest["privileged"] != true) {
				serviceErr = &wire.Error{Code: "guest_not_ready", Message: "Guest command service is not ready"}
			}
			if n.Preparation.Stage != "ready" && serviceErr != nil && serviceErr.Code == "guest_not_ready" {
				n.Health.Commands = Observation{State: "expected", Checked: &now, Error: serviceErr}
			} else {
				n.Health.Commands.update(now, serviceErr, after, status.PreparationFailed)
			}
			transitionObserved := n.Expected == nil || !oneOf(n.Expected.Action, "reboot", "reset") || !now.Before(n.Expected.Requested.Add(probeInterval))
			if status.GuestError == nil && serviceErr == nil && transitionObserved && (n.Expected == nil || n.Expected.Power == "running") {
				n.Expected = nil
			}
		}
	}
	return !wasUnavailable && n.Health.unavailable()
}

func (n *Node) capabilities() map[string]bool {
	vm := n.Intent && !n.Cleaned && n.Health.VirtualBox.State != "unavailable"
	running := n.State == "running"
	guest := vm && running && n.Health.GuestControl.State != "unavailable"
	return map[string]bool{
		"control": vm, "screenshot": vm && (running || n.State == "paused"),
		"exec": guest, "commands": guest && n.Preparation.Stage == "ready" && !oneOf(n.Health.Commands.State, "unavailable", "expected"),
	}
}

func (n *Node) public() any {
	copyNode := *n
	copyNode.Health.normalize()
	if copyNode.Preparation.Stage == "" {
		copyNode.Preparation.Stage = "pending"
	}
	v := map[string]any{}
	decodeMap(&copyNode, v)
	for _, key := range []string{"uuid", "intent", "cleaned"} {
		delete(v, key)
	}
	delete(v, "ipv4")
	v["planned_ipv4"] = n.IPv4
	v["network_observation"] = n.networkObservation()
	v["capabilities"] = n.capabilities()
	return v
}

func (n *Node) networkObservation() any {
	var observed wire.GuestNetwork
	if n.Guest == nil || n.Guest["network"] == nil || wire.Decode(wire.JSON(n.Guest["network"]), &observed) != nil {
		return map[string]any{"state": "unknown", "reason": "Guest has not provided network observations"}
	}
	return observed
}

func lockContext(ctx context.Context, lock *sync.Mutex) error {
	return waitLock(ctx, lock.TryLock)
}
func readGate(ctx context.Context, gate *sync.RWMutex) error  { return waitLock(ctx, gate.TryRLock) }
func writeGate(ctx context.Context, gate *sync.RWMutex) error { return waitLock(ctx, gate.TryLock) }
func waitLock(ctx context.Context, acquire func() bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if acquire() {
		return nil
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if acquire() {
				return nil
			}
		}
	}
}
