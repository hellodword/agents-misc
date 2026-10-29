package control

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/network"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type scheduledPreparationBackend struct {
	fakeBackend
	entered chan string
	release map[string]chan error
}

func (f *scheduledPreparationBackend) Prepare(ctx context.Context, task *Task, node *Node) error {
	if err := f.fakeBackend.Prepare(ctx, task, node); err != nil {
		return err
	}
	f.entered <- node.Name
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-f.release[node.Name]:
		return err
	}
}

func TestPreparationSlotsAndWaitingBudgets(t *testing.T) {
	for _, parallelism := range []int{1, 2} {
		t.Run(map[int]string{1: "serial", 2: "parallel"}[parallelism], func(t *testing.T) {
			f := &scheduledPreparationBackend{entered: make(chan string, 4), release: map[string]chan error{}}
			for _, name := range []string{"router", "one", "two", "three"} {
				f.release[name] = make(chan error, 1)
			}
			cfg := testConfig(t)
			cfg.VirtualBox.StartupParallelism = parallelism
			s, err := New(cfg, f)
			if err != nil {
				t.Fatal(err)
			}
			s.Start()
			defer s.Close()
			op := successful(t, s, "lab_create", LabRequest{RequestID: "ordered-preparation", Nodes: []NodeRequest{{Name: "one", Template: "windows"}, {Name: "two", Template: "windows"}, {Name: "three", Template: "windows"}}})
			id := op.Result.(map[string]any)["id"].(string)
			entered := func() string {
				t.Helper()
				select {
				case name := <-f.entered:
					return name
				case <-time.After(3 * time.Second):
					t.Fatal("preparation did not start")
					return ""
				}
			}
			if name := entered(); name != "router" {
				t.Fatal("first preparation", name)
			}
			f.release["router"] <- nil
			for range parallelism {
				entered()
			}
			s.mu.Lock()
			task, _ := s.task(id)
			before := clone(task)
			s.mu.Unlock()
			for i, n := range before.lab().Nodes {
				if i < parallelism {
					if !n.Intent || n.Preparation.Started == nil || n.Preparation.Deadline.Sub(*n.Preparation.Started) < cfg.VirtualBox.StartupTimeout.Value()-time.Second {
						t.Fatal("dispatched node lost its startup budget", n.Preparation)
					}
				} else if n.Intent || n.Preparation.Stage != "pending" || n.Preparation.Started != nil || n.Preparation.Deadline != nil {
					t.Fatal("waiting node acquired resources or consumed startup time", n.Preparation)
				}
			}
			released := time.Now().UTC()
			f.release["one"] <- nil
			expected := map[int]string{1: "two", 2: "three"}[parallelism]
			if name := entered(); name != expected {
				t.Fatal("dispatch order", name, expected)
			}
			s.mu.Lock()
			after := clone(task)
			s.mu.Unlock()
			if after.lab().Nodes[parallelism].Preparation.Started.Before(released) || !after.Deadline.Equal(*before.Deadline) {
				t.Fatal("waiting changed node or lab timing", after.Public())
			}
			f.release["two"] <- nil
			if parallelism == 1 && entered() != "three" {
				t.Fatal("last node did not follow the request order")
			}
			f.release["three"] <- nil
			waitFor(t, func() bool { return taskView(t, s, id)["phase"] == "running" })
			successful(t, s, "lab_cancel", TaskChange{Change: Change{"release-slots"}, TaskID: TaskID{id}})
			waitFor(t, func() bool { return taskView(t, s, id)["cleanup"].(map[string]any)["state"] == "complete" })
		})
	}
}

func TestPreparationStopsDispatchAndCleansAllocatedNodes(t *testing.T) {
	for _, ending := range []string{"failure", "cancel", "deadline"} {
		t.Run(ending, func(t *testing.T) {
			f := &scheduledPreparationBackend{entered: make(chan string, 4), release: map[string]chan error{"router": make(chan error, 1), "one": make(chan error, 1)}}
			f.release["router"] <- nil
			cfg := testConfig(t)
			if ending == "deadline" {
				cfg.Tasks.DefaultTimeout = config.Duration(time.Second)
			}
			s, err := New(cfg, f)
			if err != nil {
				t.Fatal(err)
			}
			s.Start()
			defer s.Close()
			op := successful(t, s, "lab_create", LabRequest{RequestID: "stop-dispatch", Nodes: []NodeRequest{{Name: "one", Template: "windows"}, {Name: "two", Template: "windows"}}})
			id := op.Result.(map[string]any)["id"].(string)
			for range 2 {
				select {
				case <-f.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("first node did not start")
				}
			}
			switch ending {
			case "failure":
				f.release["one"] <- wire.Fail("initialization_failed", "Synthetic preparation failure")
			case "cancel":
				successful(t, s, "lab_cancel", TaskChange{Change: Change{"cancel-preparation"}, TaskID: TaskID{id}})
			}
			waitFor(t, func() bool { return taskView(t, s, id)["cleanup"].(map[string]any)["state"] == "complete" })
			s.mu.Lock()
			task, _ := s.task(id)
			snapshot := clone(task)
			s.mu.Unlock()
			pending := snapshot.lab().Nodes[1]
			if pending.Intent || pending.Preparation.Stage != "cancelled" || pending.Preparation.Started != nil || pending.Preparation.Deadline != nil {
				t.Fatal("unstarted node was allocated", pending)
			}
			if !snapshot.lab().Router.Cleaned || !snapshot.lab().Nodes[0].Cleaned {
				t.Fatal("allocated resources survived cleanup")
			}
			if ending == "failure" && snapshot.Failure.Code != "initialization_failed" {
				t.Fatal("initial failure was lost", snapshot.Failure)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.created) != 2 || len(f.deleted) != 2 {
				t.Fatal("dispatch continued after termination", f.created, f.deleted)
			}
		})
	}
}

type preparationNetworkBackend struct {
	fakeBackend
	routerEntered, peerEntered chan struct{}
	routerReady, peerReady     chan struct{}
	networkCalls               atomic.Int32
	policy                     network.Policy
}

type pollingPreparationBackend struct {
	fakeBackend
	entered, ready chan struct{}
}

func (f *pollingPreparationBackend) Prepare(ctx context.Context, _ *Task, _ *Node) error {
	var schedule PollSchedule
	schedule.set(time.Now().UTC(), 30*time.Second, "preparation_backoff")
	reportProgress(ctx, "guest_control", &NodeStatus{Polling: &schedule}, wire.Fail("guest_not_ready", "Synthetic delayed boot"))
	close(f.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.ready:
		return nil
	}
}

func TestPreparationPublishesRetryScheduleAndClearsItAtReadiness(t *testing.T) {
	f := &pollingPreparationBackend{entered: make(chan struct{}), ready: make(chan struct{})}
	s, err := New(testConfig(t), f)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Close()
	lab := successful(t, s, "lab_create", LabRequest{RequestID: "startup-schedule", Nodes: []NodeRequest{}})
	id := lab.Result.(map[string]any)["id"].(string)
	select {
	case <-f.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("router did not enter preparation")
	}
	view := taskView(t, s, id)
	router := view["router"].(map[string]any)
	preparation := router["preparation"].(map[string]any)
	polling := router["polling"].(map[string]any)
	if polling["next"] == nil || polling["interval_ms"] != float64(30000) || preparation["deadline"] == nil || preparation["error"] == nil {
		t.Fatal("preparation lost its retry schedule, deadline or cause", router)
	}
	close(f.ready)
	waitFor(t, func() bool { return taskView(t, s, id)["phase"] == "running" })
	router = taskView(t, s, id)["router"].(map[string]any)
	polling = router["polling"].(map[string]any)
	if polling["next"] != nil || polling["interval_ms"] != float64(0) || router["preparation"].(map[string]any)["deadline"] != preparation["deadline"] {
		t.Fatal("readiness retained an obsolete retry or changed the startup deadline", router)
	}
	successful(t, s, "lab_cancel", TaskChange{Change: Change{"release-startup-schedule"}, TaskID: TaskID{id}})
	waitFor(t, func() bool { return taskView(t, s, id)["cleanup"].(map[string]any)["state"] == "complete" })
}

func (f *preparationNetworkBackend) Prepare(ctx context.Context, task *Task, node *Node) error {
	if err := f.fakeBackend.Prepare(ctx, task, node); err != nil {
		return err
	}
	var ready chan struct{}
	if node.Name == "router" {
		close(f.routerEntered)
		ready = f.routerReady
	} else {
		if f.networkCalls.Load() == 0 {
			return wire.Fail("policy_missing", "Client booted before DHCP and host protection")
		}
		if node.Name == "two" {
			close(f.peerEntered)
			ready = f.peerReady
		} else {
			return nil
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ready:
		return nil
	}
}

func (f *preparationNetworkBackend) Network(ctx context.Context, task *Task, protected []string, recover bool) (string, error) {
	f.mu.Lock()
	f.policy = taskPolicy(task, "10.78.0.0/24", 15, protected)
	f.mu.Unlock()
	f.networkCalls.Add(1)
	return f.fakeBackend.Network(ctx, task, protected, recover)
}

func TestNetworkPolicyPrecedesClientsAndDoesNotWaitForSlowPeer(t *testing.T) {
	for _, dhcp := range []bool{true, false} {
		t.Run(map[bool]string{true: "dhcp", false: "manual_addresses"}[dhcp], func(t *testing.T) {
			f := &preparationNetworkBackend{routerEntered: make(chan struct{}), peerEntered: make(chan struct{}), routerReady: make(chan struct{}), peerReady: make(chan struct{})}
			s, err := New(testConfig(t), f)
			if err != nil {
				t.Fatal(err)
			}
			s.Start()
			defer s.Close()
			op := successful(t, s, "lab_create", LabRequest{RequestID: "prepare-network", DHCP: &dhcp, Nodes: []NodeRequest{{Name: "one", Template: "windows", IPv4: "10.77.0.5"}, {Name: "two", Template: "windows", IPv4: "10.77.0.6"}}})
			id := op.Result.(map[string]any)["id"].(string)
			select {
			case <-f.routerEntered:
			case <-time.After(3 * time.Second):
				t.Fatal("router did not start")
			}
			view := taskView(t, s, id)
			prep := view["router"].(map[string]any)["preparation"].(map[string]any)
			if view["phase"] != "preparing" || prep["stage"] != "creating" || prep["started"] == nil || prep["deadline"] == nil || f.networkCalls.Load() != 0 {
				t.Fatal(view)
			}
			close(f.routerReady)
			select {
			case <-f.peerEntered:
			case <-time.After(3 * time.Second):
				t.Fatal("client boot did not reach initialized network", taskView(t, s, id))
			}
			f.mu.Lock()
			policy := f.policy
			f.mu.Unlock()
			if policy.DHCP != dhcp || len(policy.Nodes) != 2 || policy.Nodes[0].IPv4 != "10.77.0.5" || policy.Nodes[1].IPv4 != "10.77.0.6" || len(policy.Protected) == 0 {
				t.Fatal("initial policy omitted reservations or host protection", policy)
			}
			view = taskView(t, s, id)
			node := view["nodes"].([]any)[0].(map[string]any)
			if view["phase"] != "preparing" || view["network"].(map[string]any)["state"] != "ready" || node["planned_ipv4"] != "10.77.0.5" || node["network_observation"].(map[string]any)["state"] != "unknown" {
				t.Fatal("policy/planned/actual readiness conflated", view)
			}
			close(f.peerReady)
			waitFor(t, func() bool { return taskView(t, s, id)["phase"] == "running" })
			successful(t, s, "lab_cancel", TaskChange{Change: Change{"release-network"}, TaskID: TaskID{id}})
			waitFor(t, func() bool { return taskView(t, s, id)["cleanup"].(map[string]any)["state"] == "complete" })
		})
	}
}

type failingPreparationBackend struct {
	fakeBackend
	peerEntered chan struct{}
}

func (f *failingPreparationBackend) Prepare(ctx context.Context, task *Task, node *Node) error {
	if err := f.fakeBackend.Prepare(ctx, task, node); err != nil {
		return err
	}
	switch node.Name {
	case "one":
		reportProgress(ctx, "initializing", nil, nil)
		select {
		case <-f.peerEntered:
			return wire.Fail("initialization_failed", "Synthetic firstboot failure")
		case <-ctx.Done():
			return ctx.Err()
		}
	case "two":
		close(f.peerEntered)
		<-ctx.Done()
		// Model an adapter that returns its stale readiness failure on cancel.
		return wire.Fail("guest_not_ready", "Last probe had no guest response")
	}
	return nil
}

func TestPreparationPreservesPrimaryFailureAndPeerCancellation(t *testing.T) {
	f := &failingPreparationBackend{peerEntered: make(chan struct{})}
	cfg := testConfig(t)
	cfg.VirtualBox.StartupParallelism = 2
	s, err := New(cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Close()
	lab := createLab(t, s, "parallel-prepare-failure")
	waitFor(t, func() bool { return taskView(t, s, lab.ID)["cleanup"].(map[string]any)["state"] == "complete" })
	s.mu.Lock()
	task, _ := s.task(lab.ID)
	snapshot := clone(task)
	s.mu.Unlock()
	if snapshot.Failure.Code != "initialization_failed" || snapshot.Failure.Details["node_id"] != snapshot.lab().Nodes[0].ID || snapshot.Failure.Details["stage"] != "initializing" {
		t.Fatal("primary failure lost its stage or identity", snapshot.Failure)
	}
	peer := snapshot.lab().Nodes[1].Preparation
	if peer.Stage != "cancelled" || peer.Error.Code != "preparation_cancelled" || peer.Error.Details["cause"].(map[string]any)["code"] != "initialization_failed" {
		t.Fatal("cancelled peer was misclassified as another startup timeout", peer)
	}
	if peer.Started == nil || peer.Deadline == nil || !peer.Deadline.After(*peer.Started) || !snapshot.lab().Router.Cleaned || !snapshot.lab().Nodes[0].Cleaned || !snapshot.lab().Nodes[1].Cleaned {
		t.Fatal("preparation deadline or bounded cleanup missing", snapshot.Public())
	}
}

func TestGuestNetworkObservationDoesNotClaimApplicationReadiness(t *testing.T) {
	n := &Node{NodeRequest: NodeRequest{IPv4: "10.77.0.5"}, Guest: map[string]any{"network": wire.GuestNetwork{State: "observed", Checked: time.Now().UTC(), Interfaces: []wire.GuestInterface{{Name: "lan", MAC: "08:00:27:01:02:03", Up: true, Addresses: []string{"10.77.0.6/24"}}}}}}
	v := n.public().(map[string]any)
	data := v["network_observation"].(wire.GuestNetwork)
	if v["planned_ipv4"] != "10.77.0.5" || data.Interfaces[0].Addresses[0] != "10.77.0.6/24" || data.State != "observed" {
		t.Fatal(v)
	}
	n.Guest["network"] = wire.GuestNetwork{State: "unavailable", Error: "Synthetic probe error"}
	if n.networkObservation().(wire.GuestNetwork).State != "unavailable" {
		t.Fatal("missing data became an empty successful observation")
	}
}
