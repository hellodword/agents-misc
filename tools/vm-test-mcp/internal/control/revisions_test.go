package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestEmptyHTTPAllowlistsAndBearer(t *testing.T) {
	c := testConfig(t)
	c.Service.Hosts = nil
	c.Service.Origins = []string{}
	s, err := New(c, &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, auth := range []bool{false, true} {
		r := httptest.NewRequest("POST", "http://arbitrary.example:8123/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"fixture","version":"1"}}}`))
		r.Header.Set("Origin", "https://another.example")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if auth {
			r.Header.Set("Authorization", "Bearer synthetic-test-token")
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		want := 401
		if auth {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
}

type offlineBackend struct {
	fakeBackend
	offline bool
}

func (f *offlineBackend) Available(ctx context.Context, kind string) (string, error) {
	f.mu.Lock()
	offline := f.offline
	f.mu.Unlock()
	if kind == "nixos" && offline {
		return "worker_unavailable", fmt.Errorf("offline")
	}
	return f.fakeBackend.Available(ctx, kind)
}
func TestOfflineWorkerDoesNotReserveRequestOrBlockLab(t *testing.T) {
	f := &offlineBackend{offline: true}
	s, err := New(testConfig(t), f)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Close()
	a := NixRequest{Change: Change{"retry-after-worker-start"}, CacheID: wire.ID(), Derivation: "/nix/store/" + strings.Repeat("0", 32) + "-test.drv", System: "x86_64-linux"}
	_, err = s.Call(context.Background(), "nixos_run", wire.JSON(a))
	if wire.Safe(err).Code != "worker_unavailable" {
		t.Fatal(err)
	}
	s.mu.Lock()
	reserved := len(s.requests)
	s.mu.Unlock()
	if reserved != 0 {
		t.Fatal("request was reserved")
	}
	lab := createLab(t, s, "lab-with-worker-offline")
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); v, _ := s.task(lab.ID); return v.State == "running" })
	f.mu.Lock()
	f.offline = false
	f.mu.Unlock()
	if _, err = s.Call(context.Background(), "nixos_run", wire.JSON(a)); err != nil {
		t.Fatal(err)
	}
}

func TestQueuedUnsubmittedNixFailsWithoutHoldingNextLab(t *testing.T) {
	f := &offlineBackend{offline: true}
	s, err := New(testConfig(t), f)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	job := newTask("nixos", 60)
	s.tasks = append(s.tasks, job)
	if err = s.db.Save(context.Background(), state.Document{Kind: "task", ID: job.ID, Value: job}); err != nil {
		t.Fatal(err)
	}
	s.Start()
	lab := createLab(t, s, "next-lab")
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); v, _ := s.task(lab.ID); return v.State == "running" })
	s.mu.Lock()
	defer s.mu.Unlock()
	if job.State != "failed" || job.Cleanup != "complete" || job.Started != nil {
		t.Fatal(job)
	}
}

type nixExecutionBackend struct {
	*fakeBackend
	cancelled bool
}

func (f *nixExecutionBackend) Nix(_ context.Context, action string, _ *Task) (json.RawMessage, error) {
	if action == "start" || f.cancelled && action == "status" {
		return wire.JSON(map[string]any{"state": "building", "cleanup": "pending"}), nil
	}
	execution := &wire.NixExecution{Stage: "build", Process: &wire.NixProcessResult{Stage: "build", Source: "nix_client_wait", Exited: true, ExitCode: new(23)}, Error: wire.Safe(wire.Fail("backend_error", "Synthetic Nix build failed"))}
	return wire.JSON(map[string]any{"state": "failed", "cleanup": "complete", "execution": execution}), nil
}

func TestNixTaskRetainsExecutionThroughCompletionAndCancellationCleanup(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			f := &nixExecutionBackend{fakeBackend: &fakeBackend{}, cancelled: cancelled}
			s, err := New(testConfig(t), f)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			job := newTask("nixos", 60)
			s.tasks = append(s.tasks, job)
			if err = s.db.Save(context.Background(), state.Document{Kind: "task", ID: job.ID, Value: job}); err != nil {
				t.Fatal(err)
			}
			s.Start()
			if cancelled {
				waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return job.State == "running" })
				successful(t, s, "task_cancel", TaskChange{Change: Change{"cancel-nix-result"}, TaskID: TaskID{job.ID}})
			}
			waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return job.Cleanup == "complete" })
			s.mu.Lock()
			got := clone(job)
			s.mu.Unlock()
			want := "failed"
			if cancelled {
				want = "cancelled"
			}
			if got.State != want || got.NixExecution == nil || got.NixExecution.Process.ExitCode == nil || *got.NixExecution.Process.ExitCode != 23 || got.Failure == nil || got.Failure.Code != "backend_error" {
				t.Fatal(got.Public())
			}
			var stored Task
			if err = s.db.Load(context.Background(), "task", job.ID, &stored); err != nil || stored.NixExecution == nil || *stored.NixExecution.Process.ExitCode != 23 {
				t.Fatal(stored.Public(), err)
			}
		})
	}
}

func TestGUIOwnershipRestoreAndExternalConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			f := &fakeBackend{gui: "800,600"}
			s, err := New(testConfig(t), f)
			if err != nil {
				t.Fatal(err)
			}
			s.Start()
			defer s.Close()
			lab := createLab(t, s, "gui")
			waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); v, _ := s.task(lab.ID); return v.State == "running" })
			successful(t, s, "lab_gui_set", GUIRequest{TaskChange: TaskChange{Change: Change{"auto"}, TaskID: TaskID{lab.ID}}, MaxGuestResolution: "auto"})
			if conflict {
				f.mu.Lock()
				f.gui = "1024,768"
				f.mu.Unlock()
			}
			successful(t, s, "task_finish", FinishRequest{TaskChange: TaskChange{Change: Change{"finish"}, TaskID: TaskID{lab.ID}}, Conclusion: "passed", Summary: "fixture"})
			want := "complete"
			if conflict {
				want = "required"
			}
			waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); v, _ := s.task(lab.ID); return v.Cleanup == want })
			f.mu.Lock()
			defer f.mu.Unlock()
			expected := "800,600"
			if conflict {
				expected = "1024,768"
			}
			if f.gui != expected {
				t.Fatal(f.gui)
			}
		})
	}
}

func TestNetworkChangesValidateAtomicallyAndRequireLeaseRecovery(t *testing.T) {
	s, _ := testServer(t)
	lab := createLab(t, s, "network-updates")
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); v, _ := s.task(lab.ID); return v.State == "running" })
	a := NetworkRequest{TaskChange: TaskChange{Change: Change{"update"}, TaskID: TaskID{lab.ID}}, DHCP: new(false), DNS: []string{"8.8.8.8"}, Nodes: []AddressRequest{{NodeID: lab.Nodes[0].ID, IPv4: "10.77.0.80"}}}
	successful(t, s, "network_set", a)
	a.RequestID = "conflict"
	a.Nodes[0].IPv4 = "10.77.0.1"
	if o := operation(t, s, "network_set", a); o.State != "failed" {
		t.Fatal(o)
	}
	s.mu.Lock()
	v, _ := s.task(lab.ID)
	if v.DHCP || v.DNS[0] != "8.8.8.8" || v.Nodes[0].IPv4 != "10.77.0.80" {
		t.Fatal(v)
	}
	v.NetworkUpdated = time.Now().Add(-time.Minute)
	v.NetworkLeaseExpires = v.NetworkUpdated.Add(time.Duration(s.cfg.VirtualBox.Network.LeaseSeconds) * time.Second)
	s.mu.Unlock()
	a.RequestID = "expired"
	a.Nodes = nil
	if o := operation(t, s, "network_set", a); o.Error == nil || o.Error.Code != "network_lease_expired" {
		t.Fatal(o)
	}
	successful(t, s, "network_recover", TaskChange{Change: Change{"recover-expired"}, TaskID: TaskID{lab.ID}})
}

func TestRouterBootChangeRequiresExplicitRecovery(t *testing.T) {
	b := &VBoxBackend{Config: testConfig(t)}
	task := newTask("lab", 60)
	task.LAN = b.Config.VirtualBox.Network.LAN
	task.DNS = b.Config.VirtualBox.Network.DNS
	task.Router = &Node{ID: wire.ID(), UUID: wire.UUID()}
	task.RouterBootID = wire.UUID()
	if err := os.MkdirAll(b.runtime(task), 0700); err != nil {
		t.Fatal(err)
	}
	r := &routerRecoveryRunner{uuid: task.Router.UUID, configPath: b.vmDir(task, task.Router) + "/router.vbox", state: "running"}
	b.Runner = r
	if _, err := b.Network(context.Background(), task, nil, false); wire.Safe(err).Code != "network_degraded" {
		t.Fatal(err)
	}
	for _, call := range r.calls {
		if strings.Contains(call, "-- apply") {
			t.Fatal("unexpected automatic recovery")
		}
	}
}

func TestPublicTaskHidesRouterBootAndGUIPolicyOwnership(t *testing.T) {
	task := newTask("lab", 60)
	task.RouterISO = "/fixture/private.iso"
	task.RouterBootID = wire.UUID()
	task.GUI = &GUIOwnership{Original: "fixture"}
	data, _ := json.Marshal(task.Public())
	for _, key := range []string{"router_iso", "router_boot_id", "gui_ownership"} {
		if strings.Contains(string(data), key) {
			t.Fatal(string(data))
		}
	}
}

type guestStatusBackend struct {
	fakeBackend
	guest map[string]any
}

type slowStatusBackend struct {
	fakeBackend
	entered chan struct{}
	updates chan struct{}
}

func (f *slowStatusBackend) Action(ctx context.Context, task *Task, node *Node, action NodeAction) (json.RawMessage, error) {
	if action.Action == "node_status" {
		select {
		case f.entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.fakeBackend.Action(ctx, task, node, action)
}

func (f *slowStatusBackend) Network(ctx context.Context, task *Task, protected []string, recover bool) (string, error) {
	select {
	case f.updates <- struct{}{}:
	default:
	}
	return f.fakeBackend.Network(ctx, task, protected, recover)
}

func TestSlowGuestPollDoesNotStarveNetworkLease(t *testing.T) {
	f := &slowStatusBackend{entered: make(chan struct{}, 1), updates: make(chan struct{}, 10)}
	c := testConfig(t)
	c.VirtualBox.Network.LeaseSeconds = 5
	s, err := New(c, f)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := newTask("lab", 60)
	task.State, task.Network = "running", "ready"
	task.NetworkUpdated = time.Now().UTC()
	task.Nodes = []*Node{{ID: wire.ID(), State: "running", Preparation: Preparation{Stage: "ready"}}}
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &Runtime{ctx: ctx, cancel: cancel}
	done := make(chan struct{})
	go func() { defer close(done); s.observe(task, runtime) }()
	defer func() { cancel(); <-done }()
	select {
	case <-f.entered:
	case <-time.After(4 * time.Second):
		t.Fatal("node status poll did not start")
	}
	// Discard renewals before the guest poll blocked. The lease must keep
	// renewing while that same poll remains outstanding, without duplicates.
	for len(f.updates) > 0 {
		<-f.updates
	}
	deadline := time.After(4 * time.Second)
	for range 2 {
		select {
		case <-f.updates:
		case <-deadline:
			t.Fatal("blocked guest status poll starved network heartbeats")
		}
	}
	select {
	case <-f.entered:
		t.Fatal("started overlapping polls for the same node")
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if task.Network != "ready" {
		t.Fatal("healthy router was degraded by a slow guest poll")
	}
}

func (f *guestStatusBackend) Action(ctx context.Context, task *Task, node *Node, action NodeAction) (json.RawMessage, error) {
	if action.Action == "node_status" {
		return wire.JSON(map[string]any{"state": "running", "guest": f.guest}), nil
	}
	return f.fakeBackend.Action(ctx, task, node, action)
}

func TestGuestStatusRefreshesWithoutPowerStateChange(t *testing.T) {
	f := &guestStatusBackend{}
	s, err := New(testConfig(t), f)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := newTask("lab", 60)
	task.State = "running"
	node := &Node{ID: wire.ID(), State: "running"}
	task.Nodes = []*Node{node}
	runtime := &Runtime{ctx: context.Background()}
	for _, ready := range []bool{false, true} {
		f.guest = map[string]any{"hostname": "independent-clone", "prepared": ready}
		s.pollNode(task, runtime, node.ID)
		if node.State != "running" || node.Guest["hostname"] != "independent-clone" || node.Guest["prepared"] != ready {
			t.Fatalf("guest status not refreshed: %+v", node)
		}
		var saved Task
		if err = s.db.Load(context.Background(), "task", task.ID, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Nodes[0].Guest["prepared"] != ready {
			t.Fatal("guest status was not persisted")
		}
	}
}

type delayedNixStatusBackend struct {
	*nixExecutionBackend
	entered, release chan struct{}
}

func (f *delayedNixStatusBackend) Nix(ctx context.Context, action string, task *Task) (json.RawMessage, error) {
	if action == "status" {
		close(f.entered)
		<-f.release
	}
	return f.nixExecutionBackend.Nix(ctx, action, task)
}

func TestNixTerminalReplyPreservesConcurrentTaskTermination(t *testing.T) {
	for _, termination := range []string{"cancelled", "timed_out"} {
		t.Run(termination, func(t *testing.T) {
			f := &delayedNixStatusBackend{nixExecutionBackend: &nixExecutionBackend{fakeBackend: &fakeBackend{}}, entered: make(chan struct{}), release: make(chan struct{})}
			s, err := New(testConfig(t), f)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			released := false
			defer func() {
				if !released {
					close(f.release)
				}
			}()
			job := newTask("nixos", 60)
			s.tasks = append(s.tasks, job)
			if err = s.db.Save(context.Background(), state.Document{Kind: "task", ID: job.ID, Value: job}); err != nil {
				t.Fatal(err)
			}
			s.Start()
			select {
			case <-f.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("status did not start")
			}
			if termination == "cancelled" {
				successful(t, s, "task_cancel", TaskChange{Change: Change{"cancel-inflight-nix-result"}, TaskID: TaskID{job.ID}})
			} else {
				s.mu.Lock()
				expired := time.Now().Add(-time.Second)
				job.Deadline = &expired
				s.mu.Unlock()
				waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return job.State == "timed_out" })
			}
			close(f.release)
			released = true
			waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return job.Cleanup == "complete" })
			s.mu.Lock()
			got := clone(job)
			s.mu.Unlock()
			if got.State != termination || got.NixExecution == nil || got.NixExecution.Process == nil || got.NixExecution.Process.ExitCode == nil || *got.NixExecution.Process.ExitCode != 23 || got.Failure == nil || got.Failure.Code != "backend_error" {
				t.Fatal(got.Public())
			}
			if termination == "cancelled" && got.Conclusion != "inconclusive" {
				t.Fatal(got.Public())
			}
			var persisted Task
			if err := s.db.Load(context.Background(), "task", job.ID, &persisted); err != nil || persisted.State != termination || persisted.NixExecution == nil || persisted.NixExecution.Process.ExitCode == nil || *persisted.NixExecution.Process.ExitCode != 23 {
				t.Fatal(persisted.Public(), err)
			}
		})
	}
}
