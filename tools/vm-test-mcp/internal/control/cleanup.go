package control

import (
	"context"
	"encoding/json"
	"path"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type cleanupKey struct{}
type cleanupProgress func(string, string, error)

func cleanupStep(ctx context.Context, name string, run func() error) error {
	progress, _ := ctx.Value(cleanupKey{}).(cleanupProgress)
	if progress != nil {
		progress(name, "running", nil)
	}
	err := ctx.Err()
	if err == nil {
		err = run()
	}
	state := "complete"
	if err != nil {
		state = "failed"
	}
	if progress != nil {
		progress(name, state, err)
	}
	return err
}

func (s *Server) cleanup(t *Task) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Tasks.CleanupTimeout.Value())
	defer cancel()
	s.mu.Lock()
	r := s.runtime[t.ID]
	if r == nil {
		c, stop := context.WithCancel(s.ctx)
		r = &Runtime{ctx: c, cancel: stop, relays: map[string]*Relay{}}
		s.runtime[t.ID] = r
	}
	s.mu.Unlock()
	if lockContext(ctx, &r.cleanup) != nil {
		return
	}
	defer r.cleanup.Unlock()
	s.mu.Lock()
	if t.Cleanup == "complete" {
		s.mu.Unlock()
		return
	}
	r.cancel()
	t.Cleanup = "running"
	if t.Ended == nil {
		now := time.Now().UTC()
		t.Ended = &now
	}
	for _, e := range t.Endpoints {
		e.State = "closed"
	}
	s.save(t)
	s.mu.Unlock()
	s.closeRelays(t, r)
	var errs []error
	if err := writeGate(ctx, &r.gate); err != nil {
		errs = append(errs, &wire.Error{Code: "cleanup_timeout", Message: "Operations did not release task resources within the cleanup budget"})
	} else {
		defer r.gate.Unlock()
		s.mu.Lock()
		snapshot := clone(t)
		s.mu.Unlock()
		if snapshot.Kind == "nixos" {
			errs = s.cleanupNix(ctx, t, snapshot)
		} else {
			// Evidence cannot consume the resource cleanup budget. All collectors
			// share one deadline, including cancellation and capture retrieval.
			budget := min(diagnosticBudget, time.Until(deadline(ctx))/3)
			evidence, stop := context.WithTimeout(ctx, budget)
			s.cleanupEvidence(evidence, t, r, snapshot)
			stop()
			errs = s.cleanupNodes(ctx, t, snapshot)
			if snapshot.GUI != nil && !snapshot.GUI.Restored {
				if err := s.setGUI(ctx, t, "", true); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if len(errs) == 0 {
			if err := s.root.RemoveAll(path.Join("tasks", t.ID, "runtime")); err != nil {
				errs = append(errs, wire.Fail("cleanup_required", "Task-owned temporary files could not be removed"))
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Cleanup, t.CleanupErrors = "complete", nil
	for _, err := range errs {
		t.CleanupErrors = append(t.CleanupErrors, wire.Safe(err))
	}
	if len(errs) != 0 {
		t.Cleanup = "required"
		s.event(t, "cleanup_blocked", "Resource cleanup is incomplete; scheduling remains occupied")
	} else {
		for _, c := range t.Commands {
			if oneOf(c.State, "running", "starting", "uncertain") {
				c.State, c.Cancellation = "cancelled", "confirmed"
			}
		}
		s.event(t, "cleanup_complete", "Task resources were released")
	}
	s.save(t)
}

func deadline(ctx context.Context) time.Time { d, _ := ctx.Deadline(); return d }

func (s *Server) cleanupEvidence(ctx context.Context, t *Task, r *Runtime, snapshot *Task) {
	for _, c := range snapshot.Captures {
		if c.State == "running" && ctx.Err() == nil {
			cctx, stop := context.WithTimeout(ctx, 3*time.Second)
			_ = s.stopCapture(cctx, t, r, c.ID, true)
			stop()
		}
	}
	for _, c := range snapshot.Commands {
		if !oneOf(c.State, "running", "starting", "uncertain") || ctx.Err() != nil {
			continue
		}
		n, _ := snapshot.Node(c.NodeID)
		cctx, stop := context.WithTimeout(ctx, 2*time.Second)
		_, _ = s.backend.Action(cctx, snapshot, n, NodeAction{Action: "command_cancel", CommandID: c.ID})
		stop()
		cctx, stop = context.WithTimeout(ctx, 2*time.Second)
		s.pollCommandContext(cctx, t, c.ID, false)
		stop()
	}
	_, _ = s.collectDiagnostics(ctx, t, snapshot, append(append([]*Node{}, snapshot.Nodes...), snapshot.Router))
}

func (s *Server) cleanupNodes(ctx context.Context, t, snapshot *Task) []error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	slots := make(chan struct{}, 4)
	for _, n := range append(append([]*Node{}, snapshot.Nodes...), snapshot.Router) {
		if n == nil || !n.Intent || n.Cleaned {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
			}
			progress := cleanupProgress(func(stage, state string, err error) {
				s.mu.Lock()
				defer s.mu.Unlock()
				real := t.Router
				if snapshot.Router == nil || n.ID != snapshot.Router.ID {
					real, _ = t.Node(n.ID)
				}
				if real.CleanupSteps == nil {
					real.CleanupSteps = map[string]*CleanupStep{}
				}
				step := real.CleanupSteps[stage]
				if step == nil {
					step = &CleanupStep{}
					real.CleanupSteps[stage] = step
				}
				if state == "running" {
					step.Attempts++
				}
				step.State, step.Updated, step.Error = state, time.Now().UTC(), nil
				if err != nil {
					step.Error = wire.Safe(err)
				}
				s.save(t)
			})
			var err error
			for attempt := 0; attempt < 3; attempt++ {
				attemptCtx, stop := context.WithTimeout(context.WithValue(ctx, cleanupKey{}, progress), 30*time.Second)
				err = cleanupStep(attemptCtx, "delete", func() error { return s.backend.Delete(attemptCtx, snapshot, n) })
				stop()
				if err == nil || ctx.Err() != nil || oneOf(wire.Safe(err).Code, "ownership_mismatch", "policy_denied") {
					break
				}
				if attempt < 2 {
					select {
					case <-ctx.Done():
					case <-time.After(time.Duration(attempt+1) * 250 * time.Millisecond):
					}
				}
			}
			s.mu.Lock()
			real := t.Router
			if snapshot.Router == nil || n.ID != snapshot.Router.ID {
				real, _ = t.Node(n.ID)
			}
			if err == nil {
				real.Cleaned, real.State = true, "deleted"
			}
			s.save(t)
			s.mu.Unlock()
			if err != nil {
				mu.Lock()
				errs = append(errs, &wire.Error{Code: "cleanup_required", Message: "Node resources could not be released", Details: map[string]any{"node_id": n.ID, "cause": wire.Safe(err)}})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errs
}

func (s *Server) cleanupNix(ctx context.Context, t, snapshot *Task) []error {
	raw, err := s.backend.Nix(ctx, "cleanup", snapshot)
	if err != nil {
		return []error{err}
	}
	var result struct {
		Cleanup       string      `json:"cleanup"`
		Artifacts     []*Artifact `json:"artifacts"`
		EvidenceError bool        `json:"evidence_error"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Cleanup != "complete" {
		return []error{wire.Fail("cleanup_required", "Nix daemon cleanup is not confirmed")}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result.EvidenceError {
		s.event(t, "evidence_error", "Some Nix evidence could not be collected; daemon cleanup was confirmed")
	}
	for _, a := range result.Artifacts {
		if wire.IDPattern.MatchString(a.ID) {
			t.Artifacts = append(t.Artifacts, a)
		}
	}
	s.save(t)
	return nil
}

func (s *Server) cleanupTask(id string) (any, error) {
	s.mu.Lock()
	t, err := s.task(id)
	if err == nil && (!t.Terminal() || t.Kind != "lab") {
		err = wire.Fail("invalid_state", "An ended VirtualBox task is required")
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	s.cleanup(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseScheduler(t)
	return t.Public(), nil
}

// Called under s.mu, including after a retry with a new request ID.
func (s *Server) releaseScheduler(t *Task) {
	if s.active != t.ID || t.Cleanup != "complete" {
		return
	}
	s.active = ""
	for _, other := range s.tasks {
		if other.Terminal() && other.Cleanup != "complete" {
			s.active = other.ID
			break
		}
	}
}
