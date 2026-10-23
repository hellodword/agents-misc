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
	return wire.JSON(map[string]any{"state": "succeeded", "cleanup": "complete"}), nil
}
func (f *fakeBackend) Action(_ context.Context, t *Task, n *Node, a NodeAction) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.commandState == nil {
		f.commandState = map[string]wire.CommandStatus{}
	}
	switch a.Action {
	case "command_start":
		f.commandState[a.Spec.ID] = wire.CommandStatus{State: "running"}
	case "command_poll":
		return wire.JSON(f.commandState[a.CommandID]), nil
	case "command_cancel":
		f.commandState[a.CommandID] = wire.CommandStatus{State: "cancelled"}
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
	c.Service.StateDir = t.TempDir()
	os.Chmod(c.Service.StateDir, 0700)
	c.Scheduler.DefaultSeconds = 60
	c.Scheduler.MaxSeconds = 600
	c.VirtualBox = config.VirtualBox{Executable: "/fake/VBoxManage", LibraryDir: "/fake/lib", InputHelper: "/fake/input", ISOBuilder: "/fake/xorriso", Templates: map[string]config.Template{"windows": {Kind: "windows", VM: "11111111-1111-4111-8111-111111111111", Snapshot: "22222222-2222-4222-8222-222222222222", Username: "synthetic", Password: "synthetic", Supervisor: "C:\\guest.exe", WorkDir: "C:\\work", CPUs: 1, MemoryMiB: 512, MaxCPUs: 4, MaxMemoryMiB: 4096}}}
	c.Router = config.Router{ISO: "/fake/router.iso"}
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
	value, e := s.Call(context.Background(), tool, wire.JSON(args))
	if e != nil {
		t.Fatal(e)
	}
	op := value.(*Operation)
	waitFor(t, func() bool {
		v, e := s.Call(context.Background(), "operation_get", wire.JSON(OperationRequest{OperationID: op.ID}))
		if e != nil {
			t.Fatal(e)
		}
		op = v.(*Operation)
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
	if lab.Nodes[0].UUID == lab.Nodes[1].UUID || lab.Nodes[0].MAC == lab.Nodes[1].MAC {
		t.Fatal("clones share identity")
	}
	ids := make(chan string, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.Call(context.Background(), "lab_create", wire.JSON(LabRequest{RequestID: "same-baseline", Nodes: []NodeRequest{{Name: "one", Template: "windows"}, {Name: "two", Template: "windows"}}}))
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
	if _, e := s.Call(context.Background(), "lab_create", []byte(`{"request_id":"same-baseline","nodes":[]}`)); wire.Safe(e).Code != "request_conflict" {
		t.Fatal(e)
	}
	a := StartRequest{NodeChange: NodeChange{Change: Change{"cmd-a"}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: lab.Nodes[0].ID}}, Argv: []string{"cmd.exe", "/c", "echo", "safe & literal"}}
	one := successful(t, s, "command_start", a).Result.(map[string]any)["id"].(string)
	a.RequestID = "cmd-b"
	a.NodeID.NodeID = lab.Nodes[1].ID
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
	bad := operation(t, s, "node_input", InputNodeRequest{NodeChange: NodeChange{Change: Change{"uncertain-click"}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: lab.Nodes[0].ID}}, Action: "click"})
	if bad.State != "uncertain" {
		t.Fatal(bad)
	}
	successful(t, s, "task_finish", FinishRequest{TaskChange: TaskChange{Change: Change{"finish"}, TaskID: TaskID{lab.ID}}, Conclusion: "passed", Summary: "caller assertion"})
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
	successful(t, s, "task_cancel", TaskChange{Change: Change{"cancel-queued"}, TaskID: TaskID{queued}})
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
	if v.State != "interrupted" {
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
	if _, e := s.Call(context.Background(), "lab_create", []byte(`{"request_id":"strict","nodes":[],"host_executable":"sh"}`)); e == nil {
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
func TestTransferSealArtifactsAndCrossTaskIDs(t *testing.T) {
	s, _ := testServer(t)
	lab := createLab(t, s, "artifacts")
	op := successful(t, s, "transfer_create", TransferRequest{Change: Change{"upload"}, TaskID: lab.ID, Kind: "file"})
	v := op.Result.(map[string]any)
	u, _ := url.Parse(v["url"].(string))
	request := httptest.NewRequest("PUT", u.String(), strings.NewReader("payload"))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	id := v["id"].(string)
	put := PutRequest{NodeChange: NodeChange{Change: Change{"put"}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: lab.Nodes[0].ID}}, TransferID: id, Path: "C:\\input"}
	successful(t, s, "guest_file_put", put)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", u.String(), strings.NewReader("changed")))
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	bad := put
	bad.RequestID = "forged"
	bad.NodeID.NodeID = "../../host"
	if operation(t, s, "guest_file_put", bad).Error.Code != "not_found" {
		t.Fatal("accepted forged node")
	}
	o := successful(t, s, "node_screenshot", ScreenshotRequest{NodeChange: NodeChange{Change: Change{"screen"}, NodeID: put.NodeID}})
	a := o.Result.(map[string]any)
	if a["sha256"] == "" || a["size"].(float64) == 0 {
		t.Fatal("missing checksum")
	}
	artifact, e := s.Call(context.Background(), "artifact_get", wire.JSON(ArtifactGetRequest{TaskID: TaskID{lab.ID}, ArtifactID: a["id"].(string)}))
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
