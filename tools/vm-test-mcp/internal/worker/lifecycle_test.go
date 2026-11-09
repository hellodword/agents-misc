package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/nixexecutor"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type executorFixture struct {
	mu        sync.Mutex
	instance  string
	sessions  map[string]*nixexecutor.Session
	hold      bool
	creates   int
	artifacts map[string][]nixexecutor.Artifact
	bodies    map[string][]byte
}

func newExecutorFixture() *executorFixture {
	return &executorFixture{instance: wire.ID(), sessions: map[string]*nixexecutor.Session{}, artifacts: map[string][]nixexecutor.Artifact{}, bodies: map[string][]byte{}}
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
		if method == "evidence_list" {
			v = nixexecutor.ArtifactPage{Files: f.artifacts[a.TaskID]}
		}
		if method == "evidence_release" {
			s.ArtifactsReleased = true
		}
	}
	return json.Unmarshal(wire.JSON(v), out)
}

func (f *executorFixture) ReadArtifact(_ context.Context, a nixexecutor.Request) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return io.NopCloser(bytes.NewReader(f.bodies[a.ArtifactID])), nil
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
	runID, artifactID := wire.ID(), wire.ID()
	body := wire.JSON(wire.NixTestResult{Version: 1, RunID: runID, Started: new(true), Phase: "script", Outcome: "failed", Collection: wire.CollectionStatus{State: "collecting"}})
	f.artifacts[id] = []nixexecutor.Artifact{{ID: artifactID, Name: "driver-result.json", Size: int64(len(body))}}
	f.bodies[artifactID] = body
	j := &Job{Request: Request{TaskID: id, RunID: runID}, State: "building", Cleanup: "pending", Started: now, Deadline: now.Add(time.Minute), Execution: &wire.NixExecution{Stage: "build", Resources: wire.NixResources{State: "owned"}, Evidence: wire.NixEvidence{State: "pending"}}, Outputs: map[string]string{}}
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
	waitLifecycle(t, w, id)
	w.mu.Lock()
	j = w.jobs[id]
	if j.Execution.Test == nil || j.Execution.Test.Outcome != "failed" || j.Execution.Process != nil || j.Execution.Collection.State != "unavailable" || len(j.Files) != 1 {
		t.Fatal("restart lost the driver receipt or invented a client exit", public(j))
	}
	digest := j.ManifestDigest
	w.mu.Unlock()
	if data, err := os.ReadFile(filepath.Join(w.root.Path, id, "retained", artifactID)); err != nil || !bytes.Equal(data, body) {
		t.Fatal("retained driver evidence", err)
	}
	if _, err := w.Call("handoff", Request{InstanceID: w.db.InstanceID, TaskID: id, ManifestDigest: digest}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.root.Path, id)); !os.IsNotExist(err) {
		t.Fatal("acknowledged worker copies remain", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.creates != 1 || !f.sessions[id].ArtifactsReleased {
		t.Fatal("restart admitted another build", f.creates)
	}
}
