package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/worker"
)

type fakeBackend struct {
	mu                                  sync.Mutex
	created, deleted                    []string
	failCreate, failDelete, failNetwork bool
	input                               []wire.Input
	commandState                        map[string]wire.CommandStatus
	busy                                string
	gui                                 string
}

func (f *fakeBackend) Available(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy, nil
}
func (f *fakeBackend) Prepare(_ context.Context, t *Task, n *Node) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !n.Intent {
		return fmt.Errorf("missing ownership intent")
	}
	f.created = append(f.created, n.UUID)
	if f.failCreate && len(f.created) > 1 {
		return fmt.Errorf("partial clone failure")
	}
	return nil
}
func (f *fakeBackend) Delete(_ context.Context, t *Task, n *Node) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDelete {
		return fmt.Errorf("cleanup failed")
	}
	f.deleted = append(f.deleted, n.UUID)
	return nil
}
func (f *fakeBackend) Network(context.Context, *Task, []string, bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNetwork {
		return "", fmt.Errorf("router down")
	}
	return "11111111-1111-4111-8111-111111111111", nil
}
func (f *fakeBackend) Endpoint(context.Context, *Task, *Endpoint, bool) error { return nil }
func (f *fakeBackend) Nix(context.Context, string, *Task) (json.RawMessage, error) {
	return wire.JSON(map[string]any{"state": "succeeded", "cleanup": "complete", "execution": wire.NixExecution{Resources: wire.NixResources{State: "released"}, Evidence: wire.NixEvidence{State: "transferring"}}}), nil
}
func (f *fakeBackend) Action(_ context.Context, t *Task, n *Node, a NodeAction) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commandState == nil {
		f.commandState = map[string]wire.CommandStatus{}
	}
	switch a.Action {
	case "node_status":
		return wire.JSON(NodeStatus{State: "running", Guest: map[string]any{"prepared": true, "privileged": true}}), nil
	case "command_start":
		f.commandState[a.Spec.ID] = wire.CommandStatus{State: "running", Started: new(time.Now().UTC())}
	case "command_poll":
		status, ok := f.commandState[a.CommandID]
		if !ok {
			return wire.JSON(wire.CommandSnapshot{Protocol: wire.CommandProtocol, CommandID: a.CommandID, Checked: time.Now().UTC(), Pending: true}), nil
		}
		return wire.JSON(snapshotFixture(a.CommandID, status, nil, nil, a.StdoutOffset, a.StderrOffset, wire.CommandReadLimit)), nil
	case "command_cancel":
		f.commandState[a.CommandID] = wire.CommandStatus{State: "cancelled", Final: true}
	case "command_input":
		f.input = append(f.input, *a.Input)
	case "input_click":
		return nil, fmt.Errorf("delivery uncertain")
	case "screenshot", "file_get", "capture_stop", "diagnostics":
		if e := os.WriteFile(a.Local, []byte("synthetic evidence"), 0600); e != nil {
			return nil, e
		}
	}
	return wire.JSON(map[string]any{"delivered": true}), nil
}
func testConfig(t *testing.T) config.Config {
	t.Helper()
	c := config.Defaults()
	c.Service.Token = "synthetic-test-token"
	c.Service.PublicURL = "http://localhost"
	c.Service.Hosts = []string{"localhost"}
	c.Service.Origins = []string{"http://localhost"}
	c.StateDir = t.TempDir()
	os.Chmod(c.StateDir, 0700)
	c.Tasks.DefaultTimeout = config.Duration(60 * time.Second)
	c.Tasks.MaxDuration = config.Duration(600 * time.Second)
	c.VirtualBox = config.VirtualBox{StartupParallelism: c.VirtualBox.StartupParallelism, StartupTimeout: c.VirtualBox.StartupTimeout, ShutdownTimeout: c.VirtualBox.ShutdownTimeout, UnavailableAfter: c.VirtualBox.UnavailableAfter, Network: c.VirtualBox.Network, Executable: "/fake/VBoxManage", LibraryDir: "/fake/lib", APIHelper: "/fake/input", ISOBuilder: "/fake/xorriso", Templates: map[string]config.Template{"windows": {Kind: "windows", VM: "11111111-1111-4111-8111-111111111111", Snapshot: "22222222-2222-4222-8222-222222222222", Username: "synthetic", Password: "synthetic", Supervisor: "C:\\guest.exe", WorkDir: "C:\\work", CPUs: 1, MemoryMiB: 512, MaxCPUs: 4, MaxMemoryMiB: 4096}}}
	c.VirtualBox.Router = config.Router{ISO: "/fake/router.iso"}
	return c
}
func testServer(t *testing.T) (*Server, *fakeBackend) {
	t.Helper()
	f := &fakeBackend{}
	s, e := New(testConfig(t), f)
	if e != nil {
		t.Fatal(e)
	}
	s.Start()
	t.Cleanup(s.Close)
	return s, f
}
func waitFor(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not complete")
}
func operation(t *testing.T, s *Server, tool string, args any) *Operation {
	t.Helper()
	value, e := s.accept(context.Background(), tool, wire.JSON(args))
	if e != nil {
		t.Fatal(e)
	}
	op := value.(*Operation)
	waitFor(t, func() bool {
		v, e := s.accept(context.Background(), "operation_get", wire.JSON(OperationRequest{OperationID: op.ID}))
		if e != nil {
			t.Fatal(e)
		}
		if err := json.Unmarshal(wire.JSON(v), op); err != nil {
			t.Fatal(err)
		}
		return op.State != "pending"
	})
	return op
}
func successful(t *testing.T, s *Server, tool string, args any) *Operation {
	t.Helper()
	o := operation(t, s, tool, args)
	if o.State != "succeeded" {
		t.Fatalf("%s: %#v", tool, o)
	}
	return o
}
func createLab(t *testing.T, s *Server, id string) *Task {
	t.Helper()
	op := successful(t, s, "lab_create", LabRequest{RequestID: id, Nodes: []NodeRequest{{Name: "one", Template: "windows"}, {Name: "two", Template: "windows"}}})
	taskID := op.Result.(map[string]any)["id"].(string)
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		v, _ := s.task(taskID)
		return v.State == "running" || v.Terminal()
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	v, _ := s.task(taskID)
	return clone(v)
}

func TestIdempotencyLifecycleAndCommandIsolation(t *testing.T) {
	s, f := testServer(t)
	lab := createLab(t, s, "same-baseline")
	if lab.lab().Nodes[0].UUID == lab.lab().Nodes[1].UUID || lab.lab().Nodes[0].MAC == lab.lab().Nodes[1].MAC {
		t.Fatal("clones share identity")
	}
	ids := make(chan string, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.accept(context.Background(), "lab_create", wire.JSON(LabRequest{RequestID: "same-baseline", Nodes: []NodeRequest{{Name: "one", Template: "windows"}, {Name: "two", Template: "windows"}}}))
			if e != nil {
				t.Error(e)
				return
			}
			ids <- v.(*Operation).ID
		}()
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first != "" && first != id {
			t.Fatal("duplicate operation")
		}
		first = id
	}
	if _, e := s.accept(context.Background(), "lab_create", []byte(`{"request_id":"same-baseline","nodes":[]}`)); wire.Safe(e).Code != "request_conflict" {
		t.Fatal(e)
	}
	a := StartRequest{NodeChange: NodeChange{Change: Change{"cmd-a"}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: lab.lab().Nodes[0].ID}}, Argv: []string{"cmd.exe", "/c", "echo", "safe & literal"}}
	one := successful(t, s, "command_start", a).Result.(map[string]any)["id"].(string)
	a.Context, a.Mode = "service", "finite"
	if repeated := successful(t, s, "command_start", a).Result.(map[string]any)["id"]; repeated != one {
		t.Fatal("explicit fixed defaults changed idempotency", repeated, one)
	}
	a.RequestID = "cmd-b"
	a.NodeID.NodeID = lab.lab().Nodes[1].ID
	two := successful(t, s, "command_start", a).Result.(map[string]any)["id"].(string)
	successful(t, s, "command_input", InputRequest{Change: Change{"stdin-0"}, CommandRequest: CommandRequest{TaskID: TaskID{lab.ID}, CommandID: one}, Sequence: 0, Data: "hello"})
	o := operation(t, s, "command_input", InputRequest{Change: Change{"bad-seq"}, CommandRequest: CommandRequest{TaskID: TaskID{lab.ID}, CommandID: one}, Sequence: 2})
	if o.Error.Code != "input_order" {
		t.Fatal(o)
	}
	successful(t, s, "command_cancel", CancelCommandRequest{Change: Change{"cancel-one"}, CommandRequest: CommandRequest{TaskID: TaskID{lab.ID}, CommandID: one}})
	f.mu.Lock()
	if f.commandState[two].State != "running" {
		t.Fatal("cancelled another command")
	}
	f.mu.Unlock()
	bad := operation(t, s, "node_input", InputNodeRequest{NodeChange: NodeChange{Change: Change{"uncertain-click"}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: lab.lab().Nodes[0].ID}}, Action: "click"})
	if bad.State != "uncertain" {
		t.Fatal(bad)
	}
	successful(t, s, "lab_finish", FinishRequest{TaskChange: TaskChange{Change: Change{"finish"}, TaskID: TaskID{lab.ID}}, Conclusion: "passed", Summary: "caller assertion"})
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		task, _ := s.task(lab.ID)
		return task.Cleanup == "complete"
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.created) != 3 || len(f.deleted) != 3 {
		t.Fatalf("resources: %d %d", len(f.created), len(f.deleted))
	}
}
func TestQueueCancelFailureAndRecovery(t *testing.T) {
	c := testConfig(t)
	f := &fakeBackend{failCreate: true, failDelete: true}
	s, e := New(c, f)
	if e != nil {
		t.Fatal(e)
	}
	s.Start()
	lab := createLab(t, s, "partial")
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); v, _ := s.task(lab.ID); return v.Cleanup == "required" })
	op := successful(t, s, "lab_create", LabRequest{RequestID: "queued", Nodes: []NodeRequest{{Name: "q", Template: "windows"}}})
	queued := op.Result.(map[string]any)["id"].(string)
	successful(t, s, "lab_cancel", TaskChange{Change: Change{"cancel-queued"}, TaskID: TaskID{queued}})
	s.Close()
	f.mu.Lock()
	f.failCreate = false
	f.failDelete = false
	f.mu.Unlock()
	s, e = New(c, f)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.Start()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); v, _ := s.task(lab.ID); return v.Cleanup == "complete" })
	s.mu.Lock()
	defer s.mu.Unlock()
	v, _ := s.task(lab.ID)
	if v.State != "failed" || v.Failure == nil {
		t.Fatal(v.State)
	}
	q, _ := s.task(queued)
	if q.State != "cancelled" {
		t.Fatal(q.State)
	}
}
func TestHTTPAuthenticationAndStrictTransport(t *testing.T) {
	s, _ := testServer(t)
	h := s.Handler()
	cases := []struct {
		host, origin, auth, body string
		status                   int
	}{{"evil", "", "Bearer synthetic-test-token", "{}", 403}, {"localhost", "https://evil", "Bearer synthetic-test-token", "{}", 403}, {"localhost", "", "", "{}", 401}, {"localhost", "", "Bearer synthetic-test-token", `{"jsonrpc":"2.0","jsonrpc":"2.0"}`, 400}}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "http://"+c.host+"/mcp", strings.NewReader(c.body))
		r.Header.Set("Origin", c.origin)
		r.Header.Set("Authorization", c.auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.status {
			t.Fatalf("got %d expected %d", w.Code, c.status)
		}
	}
	if _, e := s.accept(context.Background(), "lab_create", []byte(`{"request_id":"strict","nodes":[],"host_executable":"sh"}`)); e == nil {
		t.Fatal("host option accepted")
	}
	server := httptest.NewServer(h)
	defer server.Close()
	r, _ := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	r.Host = "localhost"
	r.Header.Set("Authorization", "Bearer synthetic-test-token")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	response, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
}
func TestUploadReuseEvidenceAndResourceIdentities(t *testing.T) {
	s, _ := testServer(t)
	lab := createLab(t, s, "artifacts")
	op := successful(t, s, "upload_create", TransferRequest{Change: Change{"upload"}, Kind: "file"})
	v := op.Result.(map[string]any)
	declareUpload(t, s, v["id"].(string), v["url"].(string), "", map[string]string{"blob": "payload"})
	u, _ := url.Parse(v["url"].(string) + "/blob")
	request := httptest.NewRequest("PUT", u.String(), strings.NewReader("payload"))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := v["id"].(string)
	put := PutRequest{NodeChange: NodeChange{Change: Change{"put"}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: lab.lab().Nodes[0].ID}}, TransferID: id, Path: "C:\\input"}
	successful(t, s, "guest_file_put", put)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", u.String(), strings.NewReader("changed")))
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	bad := put
	bad.RequestID = "forged"
	bad.NodeID.NodeID = "../../host"
	if _, err := s.accept(context.Background(), "guest_file_put", wire.JSON(bad)); err == nil || wire.Safe(err).Code != "invalid_argument" {
		t.Fatal("accepted forged node", err)
	}
	s.mu.Lock()
	forged := s.requests[bad.RequestID]
	s.mu.Unlock()
	if forged != nil {
		t.Fatal("forged path allocated a receipt", forged)
	}
	bad.RequestID, bad.NodeID.NodeID = "unowned-node", wire.ID()
	if _, err := s.accept(context.Background(), "guest_file_put", wire.JSON(bad)); wire.Safe(err).Code != "not_found" {
		t.Fatal("accepted unowned node identity")
	}
	o := successful(t, s, "node_screenshot", ScreenshotRequest{NodeChange: NodeChange{Change: Change{"screen"}, NodeID: put.NodeID}})
	a := o.Result.(map[string]any)
	if a["sha256"] == "" || a["size"].(float64) == 0 {
		t.Fatal("missing checksum")
	}
	artifact, e := s.accept(context.Background(), "evidence_get", wire.JSON(ArtifactGetRequest{TaskID: TaskID{lab.ID}, ArtifactID: a["id"].(string)}))
	if e != nil {
		t.Fatal(e)
	}
	download := artifact.(map[string]any)["url"].(string)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", download, nil))
	if w.Code != 200 || w.Body.String() != "synthetic evidence" {
		t.Fatal(w.Code, w.Body.String())
	}
	s.mu.Lock()
	s.transfers[id].State = "open"
	s.mu.Unlock()
	os.Symlink("/etc/passwd", filepath.Join(s.root.Path, "transfers", id, "escape"))
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", strings.TrimSuffix(u.String(), "blob")+"../escape", nil))
	if w.Code == 200 {
		t.Fatal("path escape")
	}
}

func (f *fakeBackend) GUI(_ context.Context, value *string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value != nil {
		f.gui = *value
	}
	return f.gui, nil
}

func evidenceForTest(t *testing.T, s *Server, id string) []*Artifact {
	t.Helper()
	var values []*Artifact
	if err := loadChildren(context.Background(), s.db, "evidence", id, &values); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestControllerAndWorkerShareStateRoot(t *testing.T) {
	cfg := testConfig(t)
	cfg.Nix.StateDir = cfg.StateDir
	controller, err := New(cfg, &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	w, err := worker.New(config.Worker{StateDir: cfg.StateDir, Socket: filepath.Join(cfg.WorkerDir(), "worker.sock"), ControllerUID: os.Getuid()})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	described, err := w.Call("describe", worker.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if described.(map[string]any)["instance_id"] == controller.db.InstanceID {
		t.Fatal("roles share a database identity")
	}
	for _, dir := range []string{cfg.ControllerDir(), cfg.WorkerDir()} {
		if _, err = os.Stat(filepath.Join(dir, "state.sqlite")); err != nil {
			t.Fatal(err)
		}
	}
	_, err = controller.accept(context.Background(), "lab_create", wire.JSON(LabRequest{RequestID: "shared-root", Nodes: []NodeRequest{}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Call("describe", worker.Request{}); err != nil {
		t.Fatal("controller write affected worker", err)
	}
}
