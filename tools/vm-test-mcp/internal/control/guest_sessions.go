package control

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type GuestSessionState struct {
	State              string      `json:"state"`
	Checked            time.Time   `json:"checked"`
	Observed           *time.Time  `json:"observed,omitempty"`
	Before             *int        `json:"before"`
	After              *int        `json:"after"`
	Matched            int         `json:"matched"`
	CloseRequested     int         `json:"close_requested"`
	CloseErrors        int         `json:"close_errors"`
	CloseError         uint32      `json:"close_error"`
	LiveOwner          int         `json:"live_owner"`
	ForeignOwner       int         `json:"foreign_owner"`
	Unconfirmed        int         `json:"unconfirmed"`
	InFlight           int         `json:"in_flight"`
	AttributionPending bool        `json:"attribution_pending"`
	RuntimeVersion     uint32      `json:"runtime_version"`
	Error              *wire.Error `json:"error,omitempty"`
}
type sessionTracker struct {
	mu                 sync.Mutex
	pending            map[string]bool
	observation        GuestSessionState
	next               time.Time
	unmatched          int
	unmatchedKnown     bool
	inFlight           int
	attributionPending bool
	changed            chan struct{}
}

func (b *VBoxBackend) sessions(n *Node) *sessionTracker {
	v, _ := b.guestSessions.LoadOrStore(n.UUID, &sessionTracker{pending: map[string]bool{}, changed: make(chan struct{})})
	return v.(*sessionTracker)
}
func (b *VBoxBackend) retireGuestSession(n *Node, operation string, result hostexec.Result) {
	v := b.sessions(n)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.inFlight--
	close(v.changed)
	v.changed = make(chan struct{})
	if v.inFlight == 0 && v.attributionPending {
		v.next = time.Time{}
	}
	if result.ProcessID <= 1 || !result.Reaped || !oneOf(operation, "run", "copyto", "copyfrom") {
		return
	}
	// VBoxManage names sessions with its host PID, exact VM argument and operation.
	// Keep every completed invocation until the SDK verifies its absence. The
	// inspector also requires the same user and a presently dead host PID.
	name := fmt.Sprintf("[%d] VBoxManage Guest Control [%s] - %s", result.ProcessID, n.UUID, operation)
	v.pending[name] = true
}
func (b *VBoxBackend) admitGuestSession(ctx context.Context, t *Task, n *Node) error {
	v := b.sessions(n)
	blocked := func() bool {
		return v.attributionPending || len(v.pending) >= 4 || v.observation.After != nil && *v.observation.After >= 24 || v.observation.Error != nil && v.observation.Error.Code == "guest_session_attribution"
	}
	for {
		if err := lockContext(ctx, &v.mu); err != nil {
			return wire.Fail("resource_busy", "Guest session reconciliation occupied the admission budget")
		}
		if blocked() && !time.Now().Before(v.next) {
			b.reconcileSessions(ctx, t, n, v)
		}
		if v.attributionPending && v.inFlight > 0 {
			// Counts alone cannot identify a session from a still-running call.
			// Stop new admission and reconcile after those calls retire, rather
			// than growing the unattributed baseline or closing a live session.
			changed := v.changed
			v.mu.Unlock()
			select {
			case <-changed:
				continue
			case <-ctx.Done():
				return wire.Fail("resource_busy", "Guest session attribution awaited in-flight calls for the admission budget")
			}
		}
		if blocked() {
			err := &wire.Error{Code: "resource_busy", Message: "Guest sessions require confirmed release before another invocation", Details: map[string]any{"sessions": v.observation, "next_attempt": v.next}}
			v.mu.Unlock()
			return err
		}
		v.inFlight++
		v.mu.Unlock()
		return nil
	}
}
func (b *VBoxBackend) observeGuestSessions(ctx context.Context, t *Task, n *Node) *GuestSessionState {
	v := b.sessions(n)
	if err := lockContext(ctx, &v.mu); err != nil {
		return &GuestSessionState{State: "unavailable", Checked: time.Now().UTC(), Error: wire.Safe(err)}
	}
	defer v.mu.Unlock()
	if !time.Now().Before(v.next) {
		b.reconcileSessions(ctx, t, n, v)
	}
	copy := v.observation
	copy.Unconfirmed = len(v.pending)
	copy.InFlight = v.inFlight
	copy.AttributionPending = v.attributionPending
	return &copy
}

// The four local slots and four-confirmation admission threshold bound unverified
// sessions to seven, even when concurrent calls finish together. Inspection and
// Close use the host SDK without creating another authenticated guest session.
func (b *VBoxBackend) reconcileSessions(parent context.Context, t *Task, n *Node, v *sessionTracker) {
	ctx, stop := context.WithTimeout(parent, 3*time.Second)
	defer stop()
	now := time.Now().UTC()
	v.next = now.Add(5 * time.Second)
	fail := func(err error) {
		v.observation.State, v.observation.Checked, v.observation.Unconfirmed, v.observation.Error = "unavailable", now, len(v.pending), wire.Safe(err)
	}
	names := make([]string, 0, len(v.pending))
	for name := range v.pending {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 7 {
		fail(wire.Fail("guest_session_limit", "Guest session reconciliation exceeded its bounded ledger"))
		return
	}
	if len(names) > 0 {
		info, err := b.info(ctx, n.UUID)
		if err == nil {
			err = b.owned(t, n, info)
		}
		if err != nil {
			fail(err)
			return
		}
	}
	c := b.guestConfig(t, n)
	raw, err := b.native(ctx, t, n, "guest-sessions", append([]string{c.Username}, names...)...)
	var response struct {
		Before         int    `json:"before"`
		After          int    `json:"after"`
		Matched        int    `json:"matched"`
		CloseRequested int    `json:"close_requested"`
		CloseErrors    int    `json:"close_errors"`
		CloseError     uint32 `json:"close_error"`
		LiveOwner      int    `json:"live_owner"`
		ForeignOwner   int    `json:"foreign_owner"`
		Remaining      []bool `json:"remaining"`
		RuntimeVersion uint32 `json:"runtime_version"`
	}
	if err == nil {
		err = wire.Decode(raw, &response)
	}
	if err == nil && (len(response.Remaining) != len(names) || response.RuntimeVersion == 0 || response.Before < 0 || response.After < 0) {
		err = wire.Fail("guest_session_protocol", "Session inspector returned invalid confirmation data")
	}
	if err != nil {
		fail(err)
		return
	}
	present := 0
	for _, value := range response.Remaining {
		if value {
			present++
		}
	}
	unmatched := response.After - present
	if unmatched < 0 {
		fail(wire.Fail("guest_session_protocol", "Session counts conflict with selected identities"))
		return
	}
	if v.unmatchedKnown && (len(names) > 0 || v.attributionPending) && unmatched > v.unmatched+v.inFlight {
		// A forking executable wrapper or changed naming convention cannot be
		// attributed using this PID ledger. Preserve those sessions and stop new
		// admissions instead of treating unmatched names as successful cleanup.
		fail(&wire.Error{Code: "guest_session_attribution", Message: "Unattributed guest sessions increased; no unmatched session was closed", Details: map[string]any{"previous": v.unmatched, "observed": unmatched, "runtime_version": response.RuntimeVersion}})
		return
	}
	v.attributionPending = v.inFlight > 0 && (!v.unmatchedKnown || unmatched > v.unmatched)
	if v.inFlight == 0 {
		v.unmatched, v.unmatchedKnown = unmatched, true
	}
	for i, present := range response.Remaining {
		if !present {
			delete(v.pending, names[i])
		}
	}
	v.observation = GuestSessionState{State: "observed", Checked: now, Observed: &now, Before: &response.Before, After: &response.After, Matched: response.Matched, CloseRequested: response.CloseRequested, CloseErrors: response.CloseErrors, LiveOwner: response.LiveOwner, ForeignOwner: response.ForeignOwner, Unconfirmed: len(v.pending), RuntimeVersion: response.RuntimeVersion}
	v.observation.InFlight, v.observation.AttributionPending = v.inFlight, v.attributionPending
	if len(v.pending) > 0 {
		v.observation.State = "release_pending"
		v.observation.CloseError = response.CloseError
		v.observation.Error = &wire.Error{Code: "guest_session_release_unconfirmed", Message: "Selected guest sessions have not been confirmed absent", Details: map[string]any{"close_hresult": response.CloseError, "live_owner": response.LiveOwner, "foreign_owner": response.ForeignOwner}}
	}
	if len(v.pending) == 0 && response.After < 24 && !v.attributionPending {
		v.next = time.Time{}
	}
}
