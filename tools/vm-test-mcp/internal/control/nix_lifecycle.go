package control

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (s *Server) nixRuntime(t *Task) *Runtime {
	r := s.runtime[t.ID]
	if r == nil {
		ctx, cancel := context.WithCancel(s.ctx)
		r = &Runtime{ctx: ctx, cancel: cancel, relays: map[string]*Relay{}}
		s.runtime[t.ID] = r
	}
	return r
}

func (s *Server) releaseNix(t *Task, retry ...bool) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Tasks.CleanupTimeout.Value())
	defer cancel()
	s.mu.Lock()
	r := s.nixRuntime(t)
	s.mu.Unlock()
	if lockContext(ctx, &r.cleanup) != nil {
		return
	}
	defer r.cleanup.Unlock()
	s.mu.Lock()
	if t.Ended == nil {
		now := time.Now().UTC()
		t.Ended = &now
	}
	if t.Cleanup == "complete" {
		s.mu.Unlock()
		return
	}
	r.cancel()
	if !t.nix().Dispatched {
		t.Cleanup = "complete"
		t.nix().NixExecution = &wire.NixExecution{Resources: wire.NixResources{State: "unallocated"}, Evidence: wire.NixEvidence{State: "complete"}}
		t.nix().Evidence = wire.NixEvidence{State: "complete"}
		s.completeTransfers(t.ID)
		s.save(t)
		s.releaseScheduler(t)
		s.mu.Unlock()
		return
	}
	t.Cleanup = "running"
	s.save(t)
	snapshot := t.executionSnapshot()
	s.mu.Unlock()
	action := "resources"
	if len(retry) > 0 && retry[0] {
		action = "resources_retry"
	}
	raw, err := s.backend.Nix(ctx, action, snapshot)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.applyNixResources(t, raw, err)
}

func (s *Server) applyNixResources(t *Task, raw []byte, err error) {
	var v struct {
		Execution     *wire.NixExecution `json:"execution"`
		NeverAccepted bool               `json:"never_accepted"`
		Cleanup       string             `json:"cleanup"`
	}
	if err == nil {
		err = json.Unmarshal(raw, &v)
	}
	if err == nil && v.Execution == nil {
		err = wire.Fail("worker_protocol_error", "Worker omitted the resource release observation")
	}
	if err != nil {
		if t.nix().NixExecution == nil {
			t.nix().NixExecution = &wire.NixExecution{}
		}
		r := &t.nix().NixExecution.Resources
		r.State, r.Error = "blocked", nixLifecycleError(t, "resources", err)
	} else {
		t.nix().NixExecution = v.Execution
		if v.Execution.Resources.Released() && v.Cleanup != "complete" {
			t.nix().NixExecution.Resources.State = "releasing"
		}
		if v.Execution.Error != nil {
			t.Failure = v.Execution.Error
		}
		if v.NeverAccepted {
			t.nix().Evidence.State = "complete"
			t.nix().HandoffComplete = true
		}
	}
	r := t.nixResources()
	t.CleanupErrors = nil
	switch {
	case r.Released():
		t.Cleanup = "complete"
		s.completeTransfers(t.ID)
	case r.State == "blocked":
		t.Cleanup = "required"
		if r.Error == nil {
			r.Error = nixLifecycleError(t, "resources", wire.Fail("resource_release_unconfirmed", "Executor release observation is incomplete"))
			t.nix().NixExecution.Resources = r
		}
		t.CleanupErrors = []*wire.Error{r.Error}
	default:
		t.Cleanup = "running"
	}
	s.save(t)
	s.releaseScheduler(t)
}

// This observer is independent of the business execution's cancellation and
// keeps known resource ownership under observation after an ended result.
func (s *Server) nixLifecycleLocked() {
	if s.closed {
		return
	}
	for _, t := range s.tasks {
		if t.Kind != "nixos" || !t.Terminal() || t.settled() {
			continue
		}
		r := s.nixRuntime(t)
		if t.Cleanup != "complete" && !r.nixObserving && !time.Now().Before(r.nixNextObservation) {
			r.nixObserving = true
			r.nixNextObservation = time.Now().Add(time.Second)
			snapshot := t.executionSnapshot()
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
				raw, err := s.backend.Nix(ctx, "status", snapshot)
				cancel()
				s.mu.Lock()
				defer s.mu.Unlock()
				r.nixObserving = false
				if t.Cleanup != "complete" {
					s.applyNixResources(t, raw, err)
				}
			}()
		}
		e := &t.nix().Evidence
		if t.Cleanup == "complete" && !t.nix().Dispatched {
			e.State = "complete"
			s.save(t)
			continue
		}
		if t.Cleanup != "complete" || r.nixCollecting || s.nixEvidenceActive >= 2 || e.State == "blocked" || e.NextAttempt != nil && time.Now().Before(*e.NextAttempt) {
			continue
		}
		r.nixCollecting = true
		s.nixEvidenceActive++
		e.State, e.Attempts, e.Error, e.NextAttempt = "collecting", e.Attempts+1, nil, nil
		now := time.Now().UTC()
		e.Updated = &now
		s.save(t)
		s.wg.Add(1)
		go s.collectNixEvidence(t, r)
	}
}

func (s *Server) collectNixEvidence(t *Task, r *Runtime) {
	defer s.wg.Done()
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.Tasks.EvidenceTimeout.Value())
	defer cancel()
	err := s.transferNixEvidence(ctx, t)
	s.mu.Lock()
	defer s.mu.Unlock()
	r.nixCollecting = false
	s.nixEvidenceActive--
	e := &t.nix().Evidence
	now := time.Now().UTC()
	e.Updated = &now
	if err == nil {
		e.State, e.Error, e.NextAttempt = "complete", nil, nil
	} else {
		e.State, e.Error = "blocked", nixLifecycleError(t, "evidence", err)
		if s.ctx.Err() != nil {
			e.State = "pending"
			e.Attempts--
		} else if wire.TransientEvidence(err) && e.Attempts < 3 {
			delay := time.Second
			if e.Attempts == 2 {
				delay = 5 * time.Second
			}
			next := now.Add(delay)
			e.State, e.NextAttempt = "pending", &next
		}
	}
	s.save(t)
}

func (s *Server) transferNixEvidence(ctx context.Context, t *Task) error {
	s.mu.Lock()
	snapshot := t.executionSnapshot()
	s.mu.Unlock()
	if !snapshot.nix().HandoffComplete {
		raw, err := s.backend.Nix(ctx, "evidence", snapshot)
		if err != nil {
			return err
		}
		var v struct {
			ManifestDigest string             `json:"manifest_digest"`
			Execution      *wire.NixExecution `json:"execution"`
		}
		if err = json.Unmarshal(raw, &v); err != nil {
			return err
		}
		s.mu.Lock()
		if v.ManifestDigest != "" {
			t.nix().ManifestDigest = v.ManifestDigest
		}
		t.nix().Evidence.State = "transferring"
		err = s.db.Save(ctx, t)
		snapshot = t.executionSnapshot()
		s.mu.Unlock()
		if err != nil {
			return err
		}
		if _, err = s.backend.Nix(ctx, "handoff", snapshot); err != nil {
			return err
		}
		s.mu.Lock()
		t.nix().HandoffComplete = true
		err = s.db.Save(ctx, t)
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input, err := s.transfer(t.nix().CacheID); err == nil {
		return s.removeTransfer(input)
	}
	return nil
}

func nixLifecycleError(t *Task, stage string, err error) *wire.Error {
	return &wire.Error{Code: stage + "_blocked", Category: "execution", Message: "Nix " + stage + " requires recovery", Retry: "reconcile", NextAction: "Resolve the recorded cause, then call nix_job_recover for this scope", Details: map[string]any{"stage": stage, "job_id": t.ID, "worker_instance": t.nix().WorkerInstance, "session_id": t.nixResources().SessionID, "cause": wire.LifecycleCause(err), "required_actor": "administrator", "observed": time.Now().UTC()}}
}

func (s *Server) recoverNix(id, scope string) (any, error) {
	s.mu.Lock()
	t, err := s.task(id)
	if err == nil && (t.Kind != "nixos" || !t.Terminal()) {
		err = wire.Fail("invalid_state", "Recovery requires an ended Nix job")
	}
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if scope == "evidence" {
		if t.Cleanup != "complete" {
			s.mu.Unlock()
			return nil, wire.Fail("invalid_state", "Resource release must be confirmed before evidence recovery")
		}
		if t.nix().Evidence.State == "blocked" {
			snapshot := t.executionSnapshot()
			s.mu.Unlock()
			_, retryErr := s.backend.Nix(s.ctx, "evidence_retry", snapshot)
			s.mu.Lock()
			if retryErr != nil {
				s.mu.Unlock()
				return nil, retryErr
			}
			t.nix().Evidence = wire.NixEvidence{State: "pending"}
			s.save(t)
		}
		s.nixLifecycleLocked()
		s.mu.Unlock()
	} else if scope == "resources" {
		s.mu.Unlock()
		s.releaseNix(t, true)
	} else {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_argument", "Recovery scope must be resources or evidence")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return t.Public(), nil
}
