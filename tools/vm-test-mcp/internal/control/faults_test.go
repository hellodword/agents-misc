package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestHealthTracksFailureDurationRecoveryAndExpectedPower(t *testing.T) {
	s := &Server{cfg: config.Defaults()}
	n := &Node{Intent: true, State: "running", Preparation: Preparation{Stage: "ready"}}
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	good := &NodeStatus{State: "running", Guest: map[string]any{"prepared": true, "privileged": true}}
	s.observeNode(n, good, nil, now)
	bad := *good
	bad.ServiceError = &wire.Error{Code: "backend_timeout", Message: "systemd probe expired"}
	for _, seconds := range []int{1, 16} {
		if s.observeNode(n, &bad, nil, now.Add(time.Duration(seconds)*time.Second)) {
			t.Fatal("isolated failure became unavailable")
		}
	}
	if !s.observeNode(n, &bad, nil, now.Add(31*time.Second)) || n.Health.Commands.State != "unavailable" || n.Health.GuestControl.State != "available" {
		t.Fatal(n.Health)
	}
	if n.capabilities()["commands"] || !n.capabilities()["exec"] {
		t.Fatal(n.capabilities())
	}
	s.observeNode(n, nil, wire.Fail("backend_error", "VM metadata is temporarily unavailable"), now.Add(32*time.Second))
	if n.Health.Commands.State != "unavailable" {
		t.Fatal("failed metadata lookup recovered an unavailable command service")
	}
	s.observeNode(n, good, nil, now.Add(36*time.Second))
	if n.Health.Commands.State != "unavailable" {
		t.Fatal("one observation prematurely recovered capability")
	}
	s.observeNode(n, good, nil, now.Add(41*time.Second))
	if !n.capabilities()["commands"] {
		t.Fatal(n.Health)
	}
	until := now.Add(time.Minute)
	n.Expected = &ExpectedState{Action: "pause", Power: "paused", Until: &until}
	s.observeNode(n, good, nil, now.Add(42*time.Second))
	if n.Expected == nil {
		t.Fatal("expected pause was lost before VM changed state")
	}
	if s.observeNode(n, &NodeStatus{State: "paused"}, nil, now.Add(2*time.Minute)) || n.Health.GuestControl.State != "expected" {
		t.Fatal(n.Health)
	}
	if !s.observeNode(n, &NodeStatus{State: "aborted"}, nil, now.Add(43*time.Second)) {
		t.Fatal("aborted VM was hidden by expected transition")
	}
	before := n.Health.VirtualBox.Failures
	s.observeNode(n, nil, wire.Fail("resource_busy", "local slots occupied"), now.Add(44*time.Second))
	if n.Health.VirtualBox.Failures != before {
		t.Fatal("local congestion counted as a failed backend observation")
	}
}

func TestStartupDistinguishesInitializationFromUnresponsiveService(t *testing.T) {
	var h Health
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	status := &NodeStatus{State: "running", ServiceError: &wire.Error{Code: "guest_not_ready", Message: "still initializing"}}
	for _, second := range []int{0, 15, 30, 60, 120} {
		if err := h.startupFailure(now.Add(time.Duration(second)*time.Second), status, nil, 30*time.Second); err != nil {
			t.Fatal("responsive initialization was treated as a hang", err)
		}
	}
	status.ServiceError = &wire.Error{Code: "backend_timeout", Message: "service manager stopped responding"}
	for _, second := range []int{125, 140} {
		if err := h.startupFailure(now.Add(time.Duration(second)*time.Second), status, nil, 30*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.startupFailure(now.Add(155*time.Second), status, nil, 30*time.Second); wire.Safe(err).Code != "node_unavailable" {
		t.Fatal("unresponsive service consumed the full startup deadline", err)
	}
}

func TestStartupAllowsFirstGuestControlSuccessAfterSlowBoot(t *testing.T) {
	var h Health
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	status := &NodeStatus{State: "running", GuestError: &wire.Error{Code: "backend_error", Message: "Guest execution service is not ready yet"}}
	for _, second := range []int{0, 15, 30, 120} {
		if err := h.startupFailure(now.Add(time.Duration(second)*time.Second), status, nil, 30*time.Second); err != nil {
			t.Fatal("initial Guest Control startup used the runtime failure deadline", err)
		}
	}
	status.GuestError = nil
	status.ServiceError = &wire.Error{Code: "guest_not_ready", Message: "OOBE is completing"}
	if err := h.startupFailure(now.Add(125*time.Second), status, nil, 30*time.Second); err != nil {
		t.Fatal("first successful Guest Control probe ended preparation", err)
	}
	status.GuestError = &wire.Error{Code: "backend_timeout", Message: "Guest Control stopped responding"}
	for _, second := range []int{130, 145} {
		if err := h.startupFailure(now.Add(time.Duration(second)*time.Second), status, nil, 30*time.Second); err != nil {
			t.Fatal("control loss reused the initial boot's failure streak", err)
		}
	}
	if err := h.startupFailure(now.Add(160*time.Second), status, nil, 30*time.Second); wire.Safe(err).Code != "node_unavailable" {
		t.Fatal("established Guest Control loss did not end preparation", err)
	}
}

type faultBackend struct {
	fakeBackend
	faultMu          sync.Mutex
	failNode         string
	hangDiagnostics  bool
	deletionAttempts map[string]int
	uncertainExec    bool
	status           *NodeStatus
}

func (f *faultBackend) Delete(ctx context.Context, task *Task, node *Node) error {
	f.faultMu.Lock()
	if f.deletionAttempts == nil {
		f.deletionAttempts = map[string]int{}
	}
	f.deletionAttempts[node.ID]++
	fail := node.ID == f.failNode
	f.faultMu.Unlock()
	if fail {
		return wire.Fail("backend_timeout", "VM session lock did not release")
	}
	return f.fakeBackend.Delete(ctx, task, node)
}
func (f *faultBackend) Action(ctx context.Context, task *Task, node *Node, action NodeAction) (json.RawMessage, error) {
	f.faultMu.Lock()
	hang, uncertain, status := f.hangDiagnostics, f.uncertainExec, clone(f.status)
	f.faultMu.Unlock()
	if hang && oneOf(action.Action, "vm_info", "diagnostics", "screenshot", "node_exec") {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if action.Action == "node_status" && status.State != "" {
		return wire.JSON(status), nil
	}
	if action.Action == "node_exec" {
		var err error
		if uncertain {
			err = wire.Fail("backend_timeout", "Guest result is unconfirmed")
		}
		return wire.JSON(execView(hostexec.Result{Stdout: []byte("partial"), Stderr: []byte("reason"), ExitCode: -1}, err)), nil
	}
	return f.fakeBackend.Action(ctx, task, node, action)
}

func fixtureLab(t *testing.T, backend Backend, timeout time.Duration) (*Server, *Task, *Runtime) {
	t.Helper()
	c := testConfig(t)
	c.Tasks.CleanupTimeout = config.Duration(timeout)
	s, err := New(c, backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	task := newTask("lab", 60)
	task.State = "running"
	for _, name := range []string{"one", "two"} {
		task.Nodes = append(task.Nodes, &Node{ID: wire.ID(), UUID: wire.UUID(), Intent: true, State: "running", NodeRequest: NodeRequest{Name: name, Template: "windows"}, Preparation: Preparation{Stage: "ready"}})
	}
	task.Router = &Node{ID: wire.ID(), UUID: wire.UUID(), Intent: true, NodeRequest: NodeRequest{Name: "router"}}
	ctx, cancel := context.WithCancel(s.ctx)
	r := &Runtime{ctx: ctx, cancel: cancel, relays: map[string]*Relay{}}
	s.tasks = append(s.tasks, task)
	s.runtime[task.ID] = r
	s.active = task.ID
	s.save(task)
	return s, task, r
}

func TestCleanupBudgetPartialProgressAndExplicitRetry(t *testing.T) {
	f := &faultBackend{hangDiagnostics: true}
	s, task, _ := fixtureLab(t, f, 2*time.Second)
	f.failNode = task.Nodes[0].ID
	task.State = "failed"
	task.Failure = &wire.Error{Code: "initialization_failed", Message: "first boot failed"}
	started := time.Now()
	s.cleanup(task)
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatal("cleanup exceeded total budget", elapsed)
	}
	if task.Cleanup != "required" || !task.Nodes[1].Cleaned || !task.Router.Cleaned || task.Nodes[0].Cleaned || s.active != task.ID {
		t.Fatal(task.Public())
	}
	if len(task.Artifacts) == 0 || task.Nodes[0].CleanupSteps["delete"].Attempts != 3 {
		t.Fatal("missing partial evidence or retry journal", task.Public())
	}
	ended := *task.Ended
	f.faultMu.Lock()
	f.failNode = ""
	f.hangDiagnostics = false
	f.faultMu.Unlock()
	op := successful(t, s, "lab_cleanup", TaskChange{Change: Change{"retry-cleanup"}, TaskID: TaskID{task.ID}})
	if op.Result.(map[string]any)["cleanup"].(map[string]any)["state"] != "complete" {
		t.Fatal(op)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != "" || task.State != "failed" || !task.Ended.Equal(ended) || task.Nodes[0].CleanupSteps["delete"].Attempts != 4 {
		t.Fatal("retry changed result or did not release scheduler", task.Public())
	}
	if f.deletionAttempts[task.Nodes[1].ID] != 1 {
		t.Fatal("retried an already released resource")
	}
}

func TestDiagnosticsAndDirectExecDuringPreparation(t *testing.T) {
	f := &faultBackend{uncertainExec: true}
	s, task, r := fixtureLab(t, f, 2*time.Second)
	task.State = "preparing"
	task.Nodes[0].Preparation.Stage = "initializing"
	task.Nodes[0].Health.Commands.State = "unavailable"
	id := NodeID{TaskID: TaskID{task.ID}, NodeID: task.Nodes[0].ID}
	// An in-flight mutation cannot monopolize the diagnostic path.
	lock := s.nodeLock(r, id.NodeID)
	lock.Lock()
	op := operation(t, s, "node_exec", ExecRequest{NodeChange: NodeChange{Change: Change{"direct"}, NodeID: id}, Argv: []string{`C:\Windows\System32\cmd.exe`, "/d", "/c", "ver"}, TimeoutSeconds: 1})
	lock.Unlock()
	if op.State != "uncertain" {
		t.Fatal(op)
	}
	result := op.Result.(map[string]any)
	if result["termination_confirmed"] != false || result["stdout"] != nil || result["artifact_id"] == nil {
		t.Fatal(result)
	}
	var output map[string]any
	for _, a := range task.Artifacts {
		if a.ID == result["artifact_id"] {
			raw, err := s.root.Read(a.Path, wire.MaxJSON)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &output); err != nil {
				t.Fatal(err)
			}
		}
	}
	if output["stdout"] != base64.StdEncoding.EncodeToString([]byte("partial")) {
		t.Fatal(output)
	}
	if repeated := operation(t, s, "node_exec", ExecRequest{NodeChange: NodeChange{Change: Change{"direct"}, NodeID: id}, Argv: []string{`C:\Windows\System32\cmd.exe`, "/d", "/c", "ver"}, TimeoutSeconds: 1}); repeated.ID != op.ID {
		t.Fatal("direct command was replayed")
	}
	diag := successful(t, s, "lab_diagnose", DiagnoseRequest{TaskChange: TaskChange{Change: Change{"diagnose"}, TaskID: TaskID{task.ID}}})
	data := wire.JSON(diag.Result)
	if !strings.Contains(string(data), task.Router.ID) || !strings.Contains(string(data), "partial") {
		t.Fatal(string(data))
	}
	successful(t, s, "node_screenshot", ScreenshotRequest{NodeChange: NodeChange{Change: Change{"early-screen"}, NodeID: id}})
}

func TestRunningFaultPreservesLabAndHealthyPeer(t *testing.T) {
	f := &faultBackend{status: &NodeStatus{State: "aborted"}}
	s, task, r := fixtureLab(t, f, 2*time.Second)
	s.pollNode(task, r, task.Nodes[0].ID)
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(task.Artifacts) > 0 })
	s.mu.Lock()
	if task.State != "running" || task.Cleanup != "pending" || !task.Nodes[0].Health.unavailable() || len(task.Artifacts) == 0 {
		t.Fatal(task.Public())
	}
	s.mu.Unlock()
	if len(f.created) != 0 || len(f.deleted) != 0 {
		t.Fatal("a running fault recreated or deleted a node")
	}
	op := successful(t, s, "command_start", StartRequest{NodeChange: NodeChange{Change: Change{"peer-command"}, NodeID: NodeID{TaskID: TaskID{task.ID}, NodeID: task.Nodes[1].ID}}, Argv: []string{"cmd", "/c", "echo", "ok"}})
	if op.Result.(map[string]any)["state"] != "starting" {
		t.Fatal(op)
	}
	id := op.Result.(map[string]any)["id"].(string)
	s.pollCommand(task, r, id)
	c, err := task.Command(id)
	if err != nil || c.State != "running" || c.Observation.State != "available" {
		t.Fatal(c, err)
	}
}

type probeRunner struct {
	failed      bool
	mismatch    bool
	unsupported bool
	calls       [][]string
}

func (r *probeRunner) Run(_ context.Context, _ string, args []string, _ io.Reader) ([]byte, error) {
	r.calls = append(r.calls, append([]string{}, args...))
	if args[0] == "showvminfo" {
		return []byte(fmt.Sprintf("UUID=%q\nVMState=\"running\"\n", args[1])), nil
	}
	if args[0] != "guestcontrol" {
		return nil, fmt.Errorf("unexpected operation")
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--exe /bin/true") {
		return nil, nil
	}
	if strings.Contains(joined, "--exe /bin/systemctl") {
		if r.failed {
			return []byte("ActiveState=failed\nSubState=failed\nResult=timeout\n"), nil
		}
		return []byte("257\n"), nil
	}
	protocol := wire.CommandProtocol
	if r.mismatch {
		protocol++
	}
	guest := map[string]any{"version": "0.9.0", "sha256": "different-build", "command_protocol": protocol, "prepared": !r.failed, "privileged": true}
	if r.unsupported {
		guest["command_service"] = map[string]any{"state": "unavailable", "error": &wire.Error{Code: "guest_execution_unavailable", Message: "cgroup.kill unavailable", Details: map[string]any{"requirements": map[string]any{"cgroup_kill": false}}}}
	}
	return wire.JSON(guest), nil
}
func TestGuestReadinessUsesCapabilitiesAndExplicitInitializationFailure(t *testing.T) {
	c := testConfig(t)
	p := c.VirtualBox.Templates["windows"]
	p.Kind = "linux"
	p.Username = "root"
	p.Supervisor = "/opt/vm-test/vm-test-guest"
	p.WorkDir = "/var/lib/vm-test/work"
	c.VirtualBox.Templates["linux"] = p
	r := &probeRunner{}
	b := &VBoxBackend{Config: c, Runner: r}
	task := newTask("lab", 60)
	task.Router = &Node{ID: wire.ID()}
	n := &Node{ID: wire.ID(), UUID: wire.UUID(), NodeRequest: NodeRequest{Template: "linux"}}
	if err := os.MkdirAll(b.runtime(task), 0700); err != nil {
		t.Fatal(err)
	}
	status, err := b.nodeStatus(context.Background(), task, n)
	if err != nil || status.GuestError != nil || status.ServiceError != nil || status.Guest["version"] != "0.9.0" {
		t.Fatal(status, err)
	}
	r.mismatch = true
	status, err = b.nodeStatus(context.Background(), task, n)
	if err != nil || status.ServiceError == nil || status.ServiceError.Code != "guest_protocol_mismatch" || status.GuestError != nil {
		t.Fatal(status, err)
	}
	r.mismatch = false
	r.unsupported = true
	status, err = b.nodeStatus(context.Background(), task, n)
	if err != nil || status.GuestError != nil || status.ServiceError == nil || status.ServiceError.Code != "guest_execution_unavailable" || wire.Safe(status.ServiceError).Category != "precondition" || status.ServiceError.Details["requirements"].(map[string]any)["cgroup_kill"] != false {
		t.Fatal(status, err)
	}
	r.unsupported = false
	r.failed = true
	status, err = b.nodeStatus(context.Background(), task, n)
	if err != nil || !status.PreparationFailed || status.ServiceError.Code != "initialization_failed" || status.GuestError != nil {
		t.Fatal(status, err)
	}
}

func TestBackendErrorsRetainUsefulRedactedDetails(t *testing.T) {
	c := testConfig(t)
	b := &VBoxBackend{Config: c}
	failure := &hostexec.Failure{Result: hostexec.Result{ExitCode: 1, Stderr: []byte("VBOX_E_INVALID_VM_STATE " + c.Service.StateDir + " " + c.Service.Token + " " + c.VirtualBox.Templates["windows"].Password)}, Cause: fmt.Errorf("failed")}
	err := wire.Safe(b.executionError("unregistervm", failure))
	data := string(wire.JSON(err))
	if !strings.Contains(data, "VBOX_E_INVALID_VM_STATE") || !strings.Contains(data, "unregistervm") || strings.Contains(data, c.Service.Token) || strings.Contains(data, c.Service.StateDir) {
		t.Fatal(data)
	}
}

func TestDirectExecDistinguishesGuestExitFromTransportTimeout(t *testing.T) {
	for _, tc := range []struct {
		code    int
		state   string
		exit    any
		minimum any
	}{
		{39, "completed", 7, nil}, {126, "completed", nil, 94}, {18, "completed", nil, nil}, {20, "uncertain", nil, nil}, {1, "uncertain", nil, nil},
	} {
		value := execView(hostexec.Result{ExitCode: tc.code}, wire.Fail("backend_error", "run returned nonzero"))
		if value["state"] != tc.state || value["exit_code"] != tc.exit || value["exit_code_min"] != tc.minimum {
			t.Fatal(value)
		}
	}
}

type initializingBackend struct {
	fakeBackend
	entered chan struct{}
	fail    chan struct{}
}

func (f *initializingBackend) Prepare(ctx context.Context, task *Task, node *Node) error {
	if err := f.fakeBackend.Prepare(ctx, task, node); err != nil {
		return err
	}
	if node.Name == "router" {
		return nil
	}
	if node.Name == "one" {
		return nil
	}
	reportProgress(ctx, "initializing", &NodeStatus{State: "running", Guest: map[string]any{"prepared": false, "privileged": true}}, nil)
	close(f.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.fail:
		return wire.Fail("initialization_failed", "First-boot service reported a failure")
	}
}
func TestPreparingLabSupportsReadyPeerAndEndsAfterInitializationFailure(t *testing.T) {
	f := &initializingBackend{entered: make(chan struct{}), fail: make(chan struct{})}
	s, err := New(testConfig(t), f)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Close()
	op := successful(t, s, "lab_create", LabRequest{RequestID: "init-failure", Nodes: []NodeRequest{{Name: "one", Template: "windows"}, {Name: "two", Template: "windows"}}})
	id := op.Result.(map[string]any)["id"].(string)
	select {
	case <-f.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("preparation did not start")
	}
	var task *Task
	s.mu.Lock()
	task, _ = s.task(id)
	first := task.Nodes[0].ID
	s.mu.Unlock()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return task.Nodes[0].Preparation.Stage == "ready" })
	command := successful(t, s, "command_start", StartRequest{NodeChange: NodeChange{Change: Change{"during-init"}, NodeID: NodeID{TaskID: TaskID{id}, NodeID: first}}, Argv: []string{"cmd", "/c", "echo", "ready"}})
	cid := command.Result.(map[string]any)["id"].(string)
	for sequence, packet := range []wire.Input{{Data: "ready-peer-input"}, {EOF: true}} {
		input := successful(t, s, "command_input", InputRequest{Change: Change{fmt.Sprintf("during-init-input-%d", sequence)}, CommandRequest: CommandRequest{TaskID: TaskID{id}, CommandID: cid}, Sequence: sequence, Data: packet.Data, EOF: packet.EOF})
		if input.Result.(map[string]any)["delivery"] != "submitted" {
			t.Fatal("ready peer input was not delivered during preparation", input)
		}
	}
	s.mu.Lock()
	started, _ := task.Command(cid)
	packets, err := s.db.Inputs(context.Background(), task.ID, cid, 0, 16)
	if err != nil || started.Stdin.NextSequence != 2 || !started.Stdin.Closed || len(packets) != 2 || packets[0].Data != "ready-peer-input" || !packets[1].EOF {
		t.Fatal("ready peer input was not persisted in order", started)
	}
	s.mu.Unlock()
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		c, _ := task.Command(cid)
		return c.Observation.LastSuccess != nil
	})
	s.mu.Lock()
	if task.State != "preparing" {
		t.Fatal("preparation was not independently observed")
	}
	s.mu.Unlock()
	close(f.fail)
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return task.Cleanup == "complete" && s.active == "" })
	s.mu.Lock()
	if task.State != "failed" || task.Failure.Code != "initialization_failed" || len(task.Artifacts) == 0 {
		t.Fatal(task.Public())
	}
	s.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.created) != 3 || len(f.deleted) != 3 {
		t.Fatal("initialization failure did not release exactly its original resources", f.created, f.deleted)
	}
}

func TestCleanupLockWaitIsBoundedAndQueriesRemainResponsive(t *testing.T) {
	s, task, r := fixtureLab(t, &fakeBackend{}, time.Second)
	task.State = "cancelled"
	r.gate.RLock()
	done := make(chan struct{})
	go func() { defer close(done); s.cleanup(task) }()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return task.Cleanup == "running" })
	ctx, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	if _, err := s.accept(ctx, "lab_get", wire.JSON(TaskID{task.ID})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		r.gate.RUnlock()
		t.Fatal("cleanup waited indefinitely for operations")
	}
	r.gate.RUnlock()
	if task.Cleanup != "required" || task.CleanupErrors[0].Code != "cleanup_timeout" {
		t.Fatal(task.Public())
	}
}
