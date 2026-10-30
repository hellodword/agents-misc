package control

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type lifecycleBackend struct {
	*fakeBackend
	blocked                     atomic.Bool
	transfers, starts, handoffs atomic.Int32
	evidence                    chan struct{}
}

func (f *lifecycleBackend) Nix(ctx context.Context, action string, t *Task) (json.RawMessage, error) {
	if action == "start" {
		f.starts.Add(1)
	}
	if action == "evidence" {
		n := f.transfers.Add(1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.evidence:
		}
		if n == 2 {
			return nil, io.ErrUnexpectedEOF
		}
	}
	if action == "handoff" {
		f.handoffs.Add(1)
	}
	r := wire.NixResources{State: "released", SessionID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Confirmation: "sealed_cgroup_empty"}
	cleanup := "complete"
	if f.blocked.Load() {
		cleanup = "required"
		r.State = "blocked"
		r.Error = &wire.Error{Code: "resource_release_unconfirmed", Message: "Synthetic descendant remains", Details: map[string]any{"required_actor": "administrator"}}
	}
	return wire.JSON(map[string]any{"state": "succeeded", "cleanup": cleanup, "manifest_digest": wire.ManifestDigest(nil), "execution": wire.NixExecution{Resources: r, Evidence: wire.NixEvidence{State: "transferring"}}}), nil
}

func TestNixRecoverySeparatesResourcesEvidenceAndQueue(t *testing.T) {
	cfg := testConfig(t)
	f := &lifecycleBackend{fakeBackend: &fakeBackend{}, evidence: make(chan struct{})}
	f.blocked.Store(true)
	s, err := New(cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	job := newTask("nixos", 60)
	now := time.Now().UTC()
	job.State, job.Conclusion, job.Ended = "finished", "passed", &now
	job.nix().Dispatched = true
	s.tasks = append(s.tasks, job)
	s.active = job.ID
	s.recovering = []*Task{job}
	s.save(job)
	s.Start()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return job.Cleanup == "required" })
	wait, err := s.wait(context.Background(), WaitRequest{Targets: []WaitTarget{{Kind: "nix_job", ID: job.ID, Condition: "complete"}}}, time.Second)
	if err != nil || wait.Reason != "attention" || len(wait.Continue) != 0 || !wait.Targets[0].Manual {
		t.Fatal(wait, err)
	}
	op := successful(t, s, "lab_create", LabRequest{RequestID: "after-blocked-nix", Nodes: []NodeRequest{}})
	queued := op.Result.(map[string]any)["id"].(string)
	s.mu.Lock()
	next, _ := s.task(queued)
	held := next.State == "queued" && s.active == job.ID
	s.mu.Unlock()
	if !held {
		t.Fatal("next execution entered an unresolved resource boundary")
	}
	f.blocked.Store(false)
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return next.State == "running" && job.Cleanup == "complete" && f.transfers.Load() == 1
	})
	s.mu.Lock()
	err = s.purge(job)
	s.mu.Unlock()
	if err == nil {
		t.Fatal("pending handoff was reclaimable")
	}
	s.Close()
	close(f.evidence)
	s, err = New(cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); j, e := s.task(job.ID); return e == nil && j.settled() })
	result, err := s.Call(context.Background(), "nix_job_get", wire.JSON(JobID{JobID: job.ID}))
	if err != nil {
		t.Fatal(err)
	}
	view := result.(map[string]any)
	if f.starts.Load() != 0 || f.transfers.Load() != 3 || f.handoffs.Load() != 1 || view["resources"].(map[string]any)["state"] != "released" || view["evidence"].(map[string]any)["state"] != "complete" {
		t.Fatal(view, f.starts.Load(), f.transfers.Load(), f.handoffs.Load())
	}
	observation := view["execution"].(map[string]any)
	for _, scope := range []string{"resources", "evidence"} {
		if !reflect.DeepEqual(observation[scope], view[scope]) {
			t.Fatalf("public %s differs after recovery: execution=%v current=%v", scope, observation[scope], view[scope])
		}
	}
}
