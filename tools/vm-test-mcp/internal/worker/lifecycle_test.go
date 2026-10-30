package worker

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/nixexecutor"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type executorFixture struct {
	mu       sync.Mutex
	instance string
	sessions map[string]*nixexecutor.Session
	hold     bool
	creates  int
}

func newExecutorFixture() *executorFixture {
	return &executorFixture{instance: wire.ID(), sessions: map[string]*nixexecutor.Session{}}
}
func (f *executorFixture) Call(_ context.Context, method string, a nixexecutor.Request, out any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var v any
	if method == "describe" {
		v = nixexecutor.Description{Build: wire.Identity(), InstanceID: f.instance, Ready: true}
	} else {
		s := f.sessions[a.TaskID]
		if s == nil {
			id, now := wire.ID(), time.Now().UTC()
			s = &nixexecutor.Session{ID: id, ExecutorInstance: f.instance, WorkerInstance: a.WorkerInstance, TaskID: a.TaskID, Created: now, Deadline: now.Add(time.Hour), Store: "unix:///synthetic/" + id + ".sock", Resources: wire.NixResources{State: "owned", SessionID: id, ExecutorInstance: f.instance}}
			f.sessions[a.TaskID] = s
			if method == "create" {
				f.creates++
			}
		}
		if method == "extend" {
			s.Deadline = *a.Deadline
		}
		if method == "stop" {
			s.Resources.State = "released"
			s.Resources.Confirmation = "sealed_cgroup_empty"
			if f.hold {
				s.Resources.State = "blocked"
				s.Resources.Confirmation = ""
				s.Resources.Error = wire.Safe(wire.Fail("resource_release_unconfirmed", "Synthetic descendant remains alive"))
			}
		}
		v = s
	}
	return json.Unmarshal(wire.JSON(v), out)
}

func waitLifecycle(t *testing.T, w *Worker, id string) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		w.mu.Lock()
		j := w.jobs[id]
		ready := j != nil && j.Cleanup == "complete" && j.Execution.Evidence.State == "transferring"
		w.mu.Unlock()
		if ready {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, _ := w.Call("status", Request{InstanceID: w.db.InstanceID, TaskID: id})
	t.Fatal("lifecycle did not settle", v)
}

func TestRestartReconcilesOwnedSessionWithoutRebuilding(t *testing.T) {
	w, cfg := fixture(t, 0)
	f := w.executor.(*executorFixture)
	f.hold = true
	id, now := wire.ID(), time.Now().UTC()
	j := &Job{Request: Request{TaskID: id}, State: "building", Cleanup: "pending", Started: now, Deadline: now.Add(time.Minute), Execution: &wire.NixExecution{Stage: "build", Resources: wire.NixResources{State: "owned"}, Evidence: wire.NixEvidence{State: "pending"}}}
	if err := w.admit(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	w.jobs[id], w.active = j, id
	w.Close()
	w, err := newWorker(cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.reconcileResources(w.jobs[id])
	w.mu.Lock()
	if w.active != id || w.jobs[id].Execution.Resources.State != "blocked" {
		t.Fatal("unconfirmed descendants released the worker slot")
	}
	w.mu.Unlock()
	f.mu.Lock()
	f.hold = false
	f.mu.Unlock()
	w.reconcileResources(w.jobs[id])
	w.mu.Lock()
	if w.active != "" || !w.jobs[id].Execution.Resources.Released() || w.jobs[id].State != "interrupted" {
		t.Fatal(w.jobs[id])
	}
	w.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.creates != 1 {
		t.Fatal("restart admitted another build", f.creates)
	}
}
