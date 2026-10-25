package control

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type sessionRunner struct {
	mu                       sync.Mutex
	api, configPath          string
	guestCalls, sweeps, peak int
	leak, refuseClose        bool
	unattributed             bool
	pending                  map[string]bool
	holdCall                 int
	started, release         chan struct{}
}

func (r *sessionRunner) Run(ctx context.Context, exe string, args []string, in io.Reader) ([]byte, error) {
	v, err := r.RunResult(ctx, exe, args, in)
	return v.Stdout, err
}
func (r *sessionRunner) RunResult(ctx context.Context, exe string, args []string, _ io.Reader) (hostexec.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if exe == r.api {
		if len(args) < 4 || args[2] != "guest-sessions" {
			return hostexec.Result{}, fmt.Errorf("unexpected native call")
		}
		r.sweeps++
		before, matched := len(r.pending)+1, 0 // One foreign session must survive.
		remaining := make([]bool, len(args)-4)
		for i, name := range args[4:] {
			if r.pending[name] {
				matched++
				if !r.refuseClose {
					delete(r.pending, name)
				} else {
					remaining[i] = true
				}
			}
		}
		return hostexec.Result{Stdout: wire.JSON(map[string]any{"before": before, "after": len(r.pending) + 1, "matched": matched, "close_requested": matched, "close_errors": 0, "live_owner": 0, "foreign_owner": 0, "remaining": remaining, "runtime_version": 7002020})}, nil
	}
	if args[0] == "showvminfo" {
		return hostexec.Result{Stdout: []byte(fmt.Sprintf("UUID=%q\nCfgFile=%q\nVMState=\"running\"\n", args[1], r.configPath))}, nil
	}
	if args[0] != "guestcontrol" {
		return hostexec.Result{}, fmt.Errorf("unexpected backend call")
	}
	r.guestCalls++
	pid := 1000000 + r.guestCalls
	name := fmt.Sprintf("[%d] VBoxManage Guest Control [%s] - %s", pid, args[1], args[2])
	result := hostexec.Result{ProcessID: pid, Reaped: true}
	if r.leak {
		if r.unattributed {
			name = "unattributed-" + name
		}
		r.pending[name] = true
		r.peak = max(r.peak, len(r.pending))
		if r.guestCalls == r.holdCall {
			close(r.started)
			r.mu.Unlock()
			select {
			case <-r.release:
			case <-ctx.Done():
			}
			r.mu.Lock()
		}
		result.TimedOut, result.ExitCode = true, -1
		return result, &hostexec.Failure{Result: result, Cause: context.DeadlineExceeded}
	}
	return result, nil
}

func TestActiveGuestSessionReconcilesAfterInvocationExits(t *testing.T) {
	for _, unattributed := range []bool{false, true} {
		t.Run(fmt.Sprintf("unattributed=%t", unattributed), func(t *testing.T) {
			testActiveGuestSession(t, unattributed)
		})
	}
}

func testActiveGuestSession(t *testing.T, unattributed bool) {
	b, r, task, n := sessionFixture(t, true, false)
	r.holdCall, r.started, r.release = 2, make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if state := b.observeGuestSessions(ctx, task, n); state.Error != nil {
		t.Fatal(state)
	}
	_, _ = b.guestCaptured(ctx, task, n, "run")
	r.unattributed = unattributed
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = b.guestCaptured(ctx, task, n, "run")
	}()
	defer func() { cancel(); <-finished }()
	select {
	case <-r.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	state := b.observeGuestSessions(ctx, task, n)
	if state.Error != nil || state.InFlight != 1 || !state.AttributionPending {
		t.Fatalf("active invocation was mistaken for lost attribution: %#v", state)
	}
	r.mu.Lock()
	remaining := len(r.pending)
	r.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("active session was closed: %d remaining", remaining)
	}
	wait, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if _, err := b.guestCaptured(wait, task, n, "run"); err == nil || wire.Safe(err).Code != "resource_busy" {
		t.Fatalf("admission did not wait within its budget: %v", err)
	}
	r.mu.Lock()
	calls := r.guestCalls
	r.mu.Unlock()
	if calls != 2 {
		t.Fatalf("new invocation admitted before attribution settled: %d", calls)
	}
	close(r.release)
	<-finished
	state = b.observeGuestSessions(ctx, task, n)
	if unattributed {
		if state.Error == nil || state.Error.Code != "guest_session_attribution" || state.InFlight != 0 || state.Unconfirmed != 1 {
			t.Fatalf("unattributed growth was not retained and blocked: %#v", state)
		}
		if _, err := b.guestCaptured(ctx, task, n, "run"); err == nil || wire.Safe(err).Code != "resource_busy" {
			t.Fatalf("unattributed growth allowed another invocation: %v", err)
		}
		return
	}
	if state.State != "observed" || state.Error != nil || state.After == nil || *state.After != 1 || state.Unconfirmed != 0 || state.InFlight != 0 || state.AttributionPending {
		t.Fatalf("quiet reconciliation failed or foreign session was closed: %#v", state)
	}
}

func TestUnattributedSessionGrowthStopsAdmissionWithoutClosingForeignSessions(t *testing.T) {
	b, r, task, n := sessionFixture(t, true, false)
	r.unattributed = true
	for range 40 {
		_, _ = b.guestCaptured(context.Background(), task, n, "run")
	}
	if r.guestCalls != 8 || len(r.pending) != 8 {
		t.Fatal(r)
	}
	state := b.observeGuestSessions(context.Background(), task, n)
	if state.Error == nil || state.Error.Code != "guest_session_attribution" || state.Unconfirmed != 4 {
		t.Fatal(state)
	}
}
func sessionFixture(t *testing.T, leak, refuse bool) (*VBoxBackend, *sessionRunner, *Task, *Node) {
	c := testConfig(t)
	b := &VBoxBackend{Config: c}
	task := newTask("lab", 60)
	n := &Node{ID: wire.ID(), UUID: wire.UUID(), Intent: true, NodeRequest: NodeRequest{Template: "windows"}}
	r := &sessionRunner{api: c.VirtualBox.APIHelper, configPath: filepath.Join(b.vmDir(task, n), "clone.vbox"), leak: leak, refuseClose: refuse, pending: map[string]bool{}}
	b.Runner = r
	if err := os.MkdirAll(b.runtime(task), 0700); err != nil {
		t.Fatal(err)
	}
	return b, r, task, n
}

func TestGuestSessionFailureCannotGrowWithoutReleaseConfirmation(t *testing.T) {
	b, r, task, n := sessionFixture(t, true, true)
	for range 40 {
		_, _ = b.guestCaptured(context.Background(), task, n, "run")
	}
	if r.guestCalls != 4 || len(r.pending) != 4 || r.sweeps != 1 {
		t.Fatal(r)
	}
	state := b.observeGuestSessions(context.Background(), task, n)
	if state.State != "release_pending" || state.Unconfirmed != 4 || state.After == nil || *state.After != 5 {
		t.Fatal(state)
	}
	r.refuseClose = false
	v := b.sessions(n)
	v.mu.Lock()
	v.next = time.Time{}
	v.mu.Unlock()
	_, _ = b.guestCaptured(context.Background(), task, n, "run")
	if r.guestCalls != 5 || len(r.pending) != 1 {
		t.Fatal("recovery did not release exact pending sessions", r)
	}
	state = b.observeGuestSessions(context.Background(), task, n)
	if state.After == nil || *state.After != 1 || state.Unconfirmed != 0 {
		t.Fatal("foreign session was removed or owned session retained", state)
	}
}

func TestSuccessfulGuestCallsAreBatchedWithoutThrottleOrLedgerGrowth(t *testing.T) {
	b, r, task, n := sessionFixture(t, false, false)
	for range 40 {
		if _, err := b.guestCaptured(context.Background(), task, n, "run"); err != nil {
			t.Fatal(err)
		}
	}
	if r.guestCalls != 40 || r.sweeps != 9 || len(b.sessions(n).pending) != 4 {
		t.Fatal(r)
	}
}

func TestConcurrentLostGuestCallsStayWithinSessionBudget(t *testing.T) {
	b, r, task, n := sessionFixture(t, true, true)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_, _ = b.guestCaptured(context.Background(), task, n, "run")
			}
		}()
	}
	wg.Wait()
	if r.peak > 7 || r.guestCalls > 7 || r.peak < 4 {
		t.Fatal(r)
	}
}

func TestSessionReaperRefusesChangedVMOwnership(t *testing.T) {
	b, r, task, n := sessionFixture(t, true, true)
	for range 4 {
		_, _ = b.guestCaptured(context.Background(), task, n, "run")
	}
	r.configPath = "/outside/foreign.vbox"
	_, err := b.guestCaptured(context.Background(), task, n, "run")
	if err == nil || wire.Safe(err).Code != "resource_busy" || r.sweeps != 0 || r.guestCalls != 4 {
		t.Fatal(r, err)
	}
	state := b.observeGuestSessions(context.Background(), task, n)
	if state.State != "unavailable" || state.Error.Code != "ownership_mismatch" || state.Before != nil || state.After != nil {
		t.Fatal(state)
	}
}
