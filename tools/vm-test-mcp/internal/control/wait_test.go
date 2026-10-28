package control

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestDefaultBusinessWaitAndDisconnect(t *testing.T) {
	s, _ := testServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw := wire.JSON(LabRequest{RequestID: "business", Nodes: []NodeRequest{}})
	result, err := s.Call(ctx, "lab_create", raw)
	if err != nil {
		t.Fatal(err)
	}
	reply := result.(map[string]any)
	var op Operation
	if err := json.Unmarshal(wire.JSON(reply["operation"]), &op); err != nil {
		t.Fatal(err)
	}
	if reply["wait"].(WaitResult).Targets[0].Summary.(map[string]any)["phase"] != "running" {
		t.Fatal(reply)
	}
	// A different queued creation survives observation cancellation.
	request := wire.JSON(LabRequest{RequestID: "disconnected", Nodes: []NodeRequest{}})
	observer, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = s.Call(observer, "lab_create", request)
	stop()
	if err == nil {
		t.Fatal("observer should time out")
	}
	s.mu.Lock()
	pending := clone(s.requestOperation("disconnected"))
	s.mu.Unlock()
	if pending == nil || pending.TaskID == "" {
		t.Fatal("accepted target was lost")
	}
	ended, err := s.Call(ctx, "lab_cancel", wire.JSON(TaskChange{Change: Change{"release"}, TaskID: TaskID{op.TaskID}}))
	if err != nil {
		t.Fatal(err)
	}
	if !ended.(map[string]any)["wait"].(WaitResult).Targets[0].Satisfied {
		t.Fatal(ended)
	}
	again, err := s.Call(ctx, "lab_create", request)
	if err != nil {
		t.Fatal(err)
	}
	if again.(map[string]any)["operation"].(map[string]any)["id"] != pending.ID {
		t.Fatal("observation retried the effect")
	}
	_, err = s.Call(ctx, "lab_cancel", wire.JSON(TaskChange{Change: Change{"release-disconnected"}, TaskID: TaskID{pending.TaskID}}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestWaitRevisionsMultiTargetAndOutput(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, second := newTask("lab", 60), newTask("lab", 60)
	s.tasks = []*Task{first, second}
	s.save(first)
	s.save(second)
	req := WaitRequest{Targets: []WaitTarget{{Kind: "lab", ID: first.ID, Condition: "complete"}, {Kind: "lab", ID: second.ID, Condition: "ready"}}}
	result, err := s.wait(context.Background(), req, 5*time.Millisecond)
	if err != nil || result.Reason != "timeout" || len(result.Continue) != 2 {
		t.Fatal(result, err)
	}
	revision := result.Continue[0].SeenRevision
	s.mu.Lock()
	first.lab().NetworkUpdated = time.Now()
	first.lab().NetworkLeaseExpires = time.Now().Add(time.Hour)
	s.save(first)
	s.mu.Unlock()
	result, err = s.wait(context.Background(), WaitRequest{Targets: result.Continue}, 5*time.Millisecond)
	if err != nil || result.Reason != "timeout" || result.Continue[0].SeenRevision != revision {
		t.Fatal("heartbeat advanced ordinary revision", result, err)
	}
	second.State = "running"
	s.save(second)
	result, err = s.wait(context.Background(), req, time.Second)
	if err != nil || result.Reason != "satisfied" || !result.Targets[1].Satisfied {
		t.Fatal(result, err)
	}
	out := WaitRequest{Targets: []WaitTarget{{Kind: "lab", ID: first.ID, Condition: "output"}}}
	result, err = s.wait(context.Background(), out, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.root.MkdirAll("tasks/"+first.ID, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.root.Path, "tasks", first.ID, "task.log"), []byte("output"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = s.wait(context.Background(), WaitRequest{Targets: result.Continue}, time.Second)
	if err != nil || result.Reason != "satisfied" {
		t.Fatal(result, err)
	}
	first.State = "failed"
	first.Cleanup = "required"
	s.save(first)
	result, err = s.wait(context.Background(), WaitRequest{Targets: []WaitTarget{{Kind: "lab", ID: first.ID, Condition: "complete", SeenRevision: revision}}}, time.Second)
	if err != nil || result.Reason != "attention" {
		t.Fatal(result, err)
	}
	result, err = s.wait(context.Background(), WaitRequest{Targets: result.Continue}, 5*time.Millisecond)
	if err != nil || result.Reason != "timeout" {
		t.Fatal("same fault woke observer again", result, err)
	}
}

func TestDeletedTargetPreservesReceiptAndOtherWaitTargets(t *testing.T) {
	s, _ := testServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created, err := s.Call(ctx, "lab_create", wire.JSON(LabRequest{RequestID: "deleted-target", Nodes: []NodeRequest{}}))
	if err != nil {
		t.Fatal(err)
	}
	id := created.(map[string]any)["wait"].(WaitResult).Targets[0].ID
	stop := TaskChange{Change: Change{"deleted-stop"}, TaskID: TaskID{id}}
	ended, err := s.Call(ctx, "lab_cancel", wire.JSON(stop))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Call(ctx, "execution_delete", wire.JSON(ExecutionChange{Change: Change{"delete-summary"}, ExecutionID: id})); err != nil {
		t.Fatal(err)
	}
	again, err := s.Call(ctx, "lab_cancel", wire.JSON(stop))
	if err != nil || string(wire.JSON(again.(map[string]any)["operation"])) != string(wire.JSON(ended.(map[string]any)["operation"])) {
		t.Fatal("deleted target lost its idempotency receipt", again, err)
	}
	s.mu.Lock()
	pending := newTask("lab", 60)
	s.tasks = append(s.tasks, pending)
	s.save(pending)
	s.mu.Unlock()
	result, err := s.wait(ctx, WaitRequest{Targets: []WaitTarget{{Kind: "lab", ID: id, Condition: "complete"}, {Kind: "lab", ID: pending.ID, Condition: "complete"}}}, time.Second)
	if err != nil || result.Reason != "unavailable" || result.Targets[0].Unavailable == nil || len(result.Continue) != 1 || result.Continue[0].ID != pending.ID {
		t.Fatal(result, err)
	}
}

func TestReadinessWaitAllowsGuestBootButReportsRuntimeFailure(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lab := newTask("lab", 3600)
	lab.State = "preparing"
	node := &Node{ID: wire.ID(), NodeRequest: NodeRequest{Name: "booting"}, State: "running"}
	node.Preparation.Stage = "guest_control"
	node.Health.Commands.State = "unavailable"
	node.Health.GuestControl.State = "unavailable"
	lab.lab().Nodes = []*Node{node}
	s.tasks = []*Task{lab}
	s.save(lab)
	result, err := s.wait(context.Background(), WaitRequest{Targets: []WaitTarget{{Kind: "lab", ID: lab.ID, Condition: "ready"}}}, 5*time.Millisecond, true)
	if err != nil || result.Reason != "timeout" || result.Targets[0].Attention {
		t.Fatal("normal boot interrupted readiness wait", result, err)
	}
	lab.State = "running"
	node.Preparation.Stage = "ready"
	s.save(lab)
	result, err = s.wait(context.Background(), WaitRequest{Targets: []WaitTarget{{Kind: "lab", ID: lab.ID, Condition: "complete"}}}, time.Second)
	if err != nil || result.Reason != "attention" || !result.Targets[0].Attention {
		t.Fatal("runtime failure did not interrupt wait", result, err)
	}
}

func TestStartupInterruptsOnlyRecoveredExecutions(t *testing.T) {
	cfg := testConfig(t)
	s, err := New(cfg, &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	old := newTask("lab", 60)
	s.save(old)
	s.Close()
	s, err = New(cfg, &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Start()
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		v, _ := s.task(old.ID)
		return v.State == "interrupted" && v.Cleanup == "complete"
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v, err := s.Call(ctx, "lab_create", wire.JSON(LabRequest{RequestID: "fresh-after-recovery", Nodes: []NodeRequest{}}))
	if err != nil || !v.(map[string]any)["wait"].(WaitResult).Targets[0].Satisfied {
		t.Fatal(v, err)
	}
}

// Used by scripts/check-codex.py with actual CLI binaries. All backend resources
// are synthetic; the same production Handler/Call/wait/database code is exercised.
type delayedFixture struct {
	fakeBackend
	delay time.Duration
	once  atomic.Bool
}

func (b *delayedFixture) Prepare(ctx context.Context, t *Task, n *Node) error {
	if b.once.CompareAndSwap(false, true) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(b.delay):
		}
	}
	return b.fakeBackend.Prepare(ctx, t, n)
}
func TestCodexTransportFixture(t *testing.T) {
	file := os.Getenv("VM_TEST_CODEX_FIXTURE")
	if file == "" {
		t.Skip("requires the Codex client acceptance runner")
	}
	delay := 625
	if value := os.Getenv("VM_TEST_CODEX_DELAY"); value != "" {
		var err error
		delay, err = strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	cfg := testConfig(t)
	cfg.Service.Hosts = nil
	cfg.Service.Origins = nil
	cfg.VirtualBox.StartupTimeout = config.Duration(time.Hour)
	cfg.Tasks.DefaultTimeout = config.Duration(time.Hour)
	cfg.Tasks.MaxDuration = config.Duration(24 * time.Hour)
	backend := &delayedFixture{delay: time.Duration(delay) * time.Second}
	s, err := New(cfg, backend)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.Start()
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	info := map[string]any{"url": srv.URL + "/mcp", "token": cfg.Service.Token, "state": cfg.ControllerDir(), "delay": delay}
	raw, _ := json.Marshal(info)
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(20 * time.Minute)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("Codex fixture deadline expired")
		case <-tick.C:
			if _, err = os.Stat(file + ".stop"); err == nil {
				return
			}
		}
	}
}

func TestFiniteCommandsWaitForGroupAndRetainedOutput(t *testing.T) {
	s, f := testServer(t)
	lab := createLab(t, s, "finite-lab")
	args := StartRequest{NodeChange: NodeChange{Change: Change{"finite-work"}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: lab.lab().Nodes[0].ID}}, Argv: []string{"example"}}
	result := make(chan any, 1)
	failure := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go func() {
		v, err := s.Call(ctx, "command_start", wire.JSON(args))
		if err != nil {
			failure <- err
		} else {
			result <- v
		}
	}()
	var id string
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, task := range s.tasks {
			if task.ID == lab.ID && len(task.lab().Commands) > 0 {
				id = task.lab().Commands[0].ID
				return true
			}
		}
		return false
	})
	select {
	case v := <-result:
		t.Fatal("finite command returned before exit", v)
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(150 * time.Millisecond):
	}
	zero := 0
	f.mu.Lock()
	f.commandState[id] = wire.CommandStatus{State: "exited", Final: true, ExitCode: &zero, Termination: &wire.Termination{GroupStopped: true, ProcessExited: true, Source: "guest_supervisor"}}
	f.mu.Unlock()
	select {
	case v := <-result:
		if !v.(map[string]any)["wait"].(WaitResult).Targets[0].Satisfied {
			t.Fatal(v)
		}
	case err := <-failure:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	args.RequestID = "explicit-service"
	args.Mode = "service"
	v, err := s.Call(ctx, "command_start", wire.JSON(args))
	if err != nil {
		t.Fatal(err)
	}
	state := v.(map[string]any)["wait"].(WaitResult).Targets[0]
	if !state.Satisfied || state.Condition != "started" {
		t.Fatal(state)
	}
}
