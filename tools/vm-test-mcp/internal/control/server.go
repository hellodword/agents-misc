package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/telemetry"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Server struct {
	pending            []pendingDispatch
	mu                 sync.Mutex
	retentionMu        sync.Mutex
	evidenceMu         sync.Mutex
	evidenceBytes      int64
	lastRetention      time.Time
	storageBlocked     bool
	wg                 sync.WaitGroup
	cfg                config.Config
	root               *safefs.Root
	db                 *repository
	backend            Backend
	tasks              []*Task
	recovering         []*Task
	operations         map[string]*Operation
	requests           map[string]*Operation
	transfers          map[string]*Transfer
	runtime            map[string]*Runtime
	active             string
	ctx                context.Context
	cancel             context.CancelFunc
	closed             bool
	secret             []byte
	transferLocks      sync.Map
	uploading          map[string]*Upload
	performance        *telemetry.Monitor
	performancePending map[string]time.Time
}

func New(cfg config.Config, b Backend) (*Server, error) {
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	root, e := safefs.Private(cfg.ControllerDir(), true)
	if e != nil {
		return nil, e
	}
	db, e := openRepository(root.Path)
	if e != nil {
		root.Close()
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, root: root, db: db, backend: b, ctx: ctx, cancel: cancel, operations: map[string]*Operation{}, requests: map[string]*Operation{}, transfers: map[string]*Transfer{}, runtime: map[string]*Runtime{}, uploading: map[string]*Upload{}}
	s.performance = telemetry.New()
	s.performancePending = map[string]time.Time{}
	if backends, ok := b.(*Backends); ok {
		backends.Performance = s.performance
		if backends.NixBackend != nil {
			backends.NixBackend.PublishEvidence = s.db.PublishEvidence
		}
	}
	fail := func(e error) (*Server, error) { db.Close(); root.Close(); cancel(); return nil, e }
	if s.secret, e = root.Read("capability-key", 128); os.IsNotExist(e) {
		s.secret = []byte(wire.ID() + wire.ID())
		_, _, e = root.Write("capability-key", bytesReader(s.secret), 128)
	}
	if e != nil {
		return fail(e)
	}
	for _, kind := range []string{"task", "operation", "transfer"} {
		rows, e := db.Active(ctx, kind)
		if e != nil {
			return fail(e)
		}
		for _, row := range rows {
			switch kind {
			case "task":
				var t Task
				if e = json.Unmarshal(row, &t); e != nil {
					return fail(e)
				}
				t.repository = db
				s.tasks = append(s.tasks, &t)
			case "operation":
				var o Operation
				if e = json.Unmarshal(row, &o); e != nil {
					return fail(e)
				}
				if o.State == "pending" {
					intent, stage, err := db.DispatchIntent(ctx, o.ID)
					if err != nil {
						return fail(err)
					}
					if stage == "prepared" {
						s.pending = append(s.pending, pendingDispatch{ID: o.ID, Args: intent})
					} else {
						o.State = "uncertain"
						o.Error = &wire.Error{Code: "interrupted", Message: "Service stopped before confirming this operation"}
						if e = db.Save(ctx, &o); e != nil {
							return fail(e)
						}
					}
				}
				s.operations[o.ID] = &o
				s.requests[o.RequestID] = &o
			case "transfer":
				var t Transfer
				if e = json.Unmarshal(row, &t); e != nil {
					return fail(e)
				}
				if t.Objects, t.Bytes, e = db.TransferObjects(ctx, t.ID); e != nil {
					return fail(e)
				}
				if t.State == "deleting" {
					if e = root.RemoveAll("transfers/" + t.ID); e != nil {
						return fail(e)
					}
					if _, e = db.ExecContext(ctx, "DELETE FROM transfers WHERE id=?", t.ID); e != nil {
						return fail(e)
					}
					continue
				}
				if e = s.recoverTransfer(ctx, &t); e != nil {
					return fail(e)
				}
				if t.State == "open" && t.ManifestDigest != "" {
					if e = s.completeUpload(&t); e != nil {
						return fail(e)
					}
				}
				s.transfers[t.ID] = &t
			}
		}
	}
	s.recovering = append([]*Task(nil), s.tasks...)
	if used, err := s.evidenceUsage(); err == nil {
		s.evidenceBytes = used
		s.storageBlocked = used > s.cfg.Storage.EvidenceBytes
	} else {
		s.storageBlocked = true
	}
	return s, nil
}
func bytesReader(b []byte) io.Reader { return &byteReader{b: b} }

type byteReader struct{ b []byte }

func (b *byteReader) Read(p []byte) (int, error) {
	if len(b.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.b)
	b.b = b.b[n:]
	return n, nil
}
func (s *Server) persist(docs ...record) {
	if e := s.db.Save(context.Background(), docs...); e != nil {
		slog.Error("state write failed", "error", e)
		s.cancel()
		s.closed = true
	}
}
func (s *Server) save(t *Task) {
	if t.Ended != nil && t.DetailExpires == nil {
		days := s.cfg.Storage.FailureDays
		if t.State == "finished" && t.Conclusion == "passed" {
			days = s.cfg.Storage.SuccessDays
		}
		expires := t.Ended.Add(time.Duration(days) * 24 * time.Hour)
		t.DetailExpires = &expires
	}
	s.persist(t)
	if s.closed {
		return
	}
	if t.Kind != "lab" {
		return
	}
	t.lab().Commands = slices.DeleteFunc(t.lab().Commands, func(c *Command) bool { return !c.Polling() })
	t.lab().Endpoints = slices.DeleteFunc(t.lab().Endpoints, func(e *Endpoint) bool { return e.State == "closed" })
	t.lab().Captures = slices.DeleteFunc(t.lab().Captures, func(c *Capture) bool { return oneOf(c.State, "stopped", "released") })
}
func (s *Server) event(t *Task, code, msg string) {
	s.eventDetails(t, code, msg, nil)
}
func (s *Server) eventDetails(t *Task, code, msg string, details any) {
	if oneOf(code, "prepare_failed", "network_degraded", "network_observation_late", "node_unavailable", "cleanup_blocked", "command_unavailable") {
		s.retainPerformance(t, code)
	}
	t.pendingEvents = append(t.pendingEvents, map[string]any{"time": time.Now().UTC(), "code": code, "message": msg, "phase": t.phase(), "result": t.result(), "cleanup": t.cleanupState(), "details": details})

}
func (s *Server) task(id string) (*Task, error) {
	for _, t := range s.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	var t Task
	if err := s.db.Load(s.ctx, "task", id, &t); err != nil {
		return nil, wire.Fail("not_found", "Execution not found")
	}
	return &t, nil
}
func (s *Server) running(id string) (*Task, *Runtime, error) {
	t, e := s.task(id)
	if e != nil {
		return nil, nil, e
	}
	if t.State != "running" || t.Kind != "lab" {
		return nil, nil, wire.Fail("invalid_state", "A running lab is required")
	}
	return t, s.runtime[id], nil
}
func (s *Server) Start() {
	s.wg.Add(2)
	go s.schedule()
	go func() { defer s.wg.Done(); s.performance.Run(s.ctx) }()
}
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	for _, r := range s.runtime {
		r.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	if closer, ok := s.backend.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			slog.Error("Backend collectors could not be closed", "error", wire.Safe(err))
		}
	}
	s.db.Close()
	s.root.Close()
}
func (s *Server) schedule() {
	defer s.wg.Done()
	// Recovery completes before admitting the first queued task.
	s.mu.Lock()
	recovery := s.recovering
	s.recovering = nil
	s.mu.Unlock()
	blocked := ""
	for _, t := range recovery {
		s.mu.Lock()
		if !t.Terminal() {
			t.State = "interrupted"
			t.Reason = "controller_restart"
		}
		s.save(t)
		s.active = t.ID
		s.mu.Unlock()
		s.cleanup(t)
		s.mu.Lock()
		if t.Cleanup == "complete" {
			s.active = ""
		} else if blocked == "" {
			blocked = t.ID
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.active = blocked
	s.mu.Unlock()
	s.resumeDispatches()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
			s.expireRetention()
			s.mu.Lock()
			if s.active != "" || s.closed || s.storageBlocked {
				s.mu.Unlock()
				continue
			}
			var next *Task
			for _, t := range s.tasks {
				if t.State == "queued" {
					next = t
					break
				}
			}
			if next == nil {
				s.mu.Unlock()
				s.expireRetention()
				continue
			}
			s.active = next.ID
			s.mu.Unlock()
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			reason, e := s.backend.Available(ctx, next.Kind)
			cancel()
			s.mu.Lock()
			if e != nil || reason != "" {
				next.Reason = "backend_unavailable"
				if reason != "" {
					next.Reason = reason
				}
				if next.Kind == "nixos" && oneOf(reason, "worker_unavailable", "nix_disabled") && next.State == "queued" {
					// No build was submitted. This does not require daemon cleanup.
					now := time.Now().UTC()
					next.State, next.Cleanup, next.Ended = "failed", "complete", &now
				}
				s.save(next)
				s.active = ""
				s.mu.Unlock()
				continue
			}
			if next.State != "queued" {
				s.active = ""
				s.mu.Unlock()
				continue
			}
			now := time.Now().UTC()
			deadline := now.Add(time.Duration(next.TimeoutSeconds) * time.Second)
			next.Started = &now
			next.Deadline = &deadline
			next.State = "preparing"
			next.Reason = ""
			s.save(next)
			ctx, cancel = s.contextForTask()
			s.runtime[next.ID] = &Runtime{ctx: ctx, cancel: cancel, relays: map[string]*Relay{}}
			s.wg.Add(1)
			s.mu.Unlock()
			go s.execute(next)
		}
	}
}
func (s *Server) contextForTask() (context.Context, context.CancelFunc) {
	return context.WithCancel(s.ctx)
}
func (s *Server) execute(t *Task) {
	defer s.wg.Done()
	s.mu.Lock()
	r := s.runtime[t.ID]
	s.mu.Unlock()
	deadlineDone := make(chan struct{})
	go func() {
		defer close(deadlineDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-tick.C:
				s.mu.Lock()
				expired := t.Deadline != nil && !time.Now().Before(*t.Deadline) && !t.Terminal()
				if expired {
					t.State = "timed_out"
					s.save(t)
				}
				s.mu.Unlock()
				if expired {
					r.cancel()
					return
				}
			}
		}
	}()
	defer func() { r.cancel(); <-deadlineDone }()
	var err error
	var observed chan struct{}
	if t.Kind == "lab" {
		observed = make(chan struct{})
		go func() { defer close(observed); s.observe(t, r) }()
		err = s.prepare(t, r)
	} else {
		s.mu.Lock()
		snapshot := t.executionSnapshot()
		s.mu.Unlock()
		_, err = s.backend.Nix(r.ctx, "start", snapshot)
	}
	s.mu.Lock()
	if err != nil && !t.Terminal() {
		t.State = "failed"
		t.Reason = wire.Safe(err).Code
		t.Failure = wire.Safe(err)
		if s.ctx.Err() != nil {
			t.State = "interrupted"
			t.Reason = "service_stopped"
		}
		s.event(t, "prepare_failed", "Backend preparation failed")
	} else if t.State == "preparing" {
		t.State = "running"
	}
	s.save(t)
	s.mu.Unlock()
	if observed != nil {
		if err != nil {
			r.cancel()
		}
		<-observed
	} else if err == nil {
		s.observe(t, r)
	}
	r.cancel()
	s.cleanup(t)
	s.mu.Lock()
	s.releaseScheduler(t)
	s.mu.Unlock()
}
func (s *Server) prepare(t *Task, r *Runtime) error {
	ctx, cancel := context.WithCancelCause(r.ctx)
	defer cancel(nil)
	if len(t.lab().Nodes) != 0 {
		if err := s.setGUI(ctx, t, "any", false); err != nil {
			return err
		}
	}
	prepare := func(n *Node) error {
		prepareCtx, stop := context.WithTimeout(ctx, s.cfg.VirtualBox.StartupTimeout.Value())
		defer stop()
		started, until := time.Now().UTC(), deadline(prepareCtx).UTC()
		s.mu.Lock()
		n.Intent = true
		n.Preparation = Preparation{Stage: "creating", Started: &started, StageStarted: &started, Deadline: &until, Updated: started}
		err := s.db.Save(context.Background(), t)
		tc, nc := t.executionSnapshot(), clone(n)
		s.mu.Unlock()
		if err != nil {
			cancel(err)
			return err
		}
		progress := progressFunc(func(stage string, status *NodeStatus, err error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			now := time.Now().UTC()
			if n.Preparation.Stage != stage {
				n.Preparation.StageStarted = &now
			}
			n.Preparation.Stage, n.Preparation.Updated, n.Preparation.Error = stage, now, nil
			if err != nil {
				n.Preparation.Error = wire.Safe(err)
			}
			if status != nil && status.Identity != nil {
				n.Identity = status.Identity
			}
			if status != nil && status.State != "" {
				s.observeNode(n, status, nil, time.Now().UTC())
			}
			if status != nil && status.Polling != nil {
				n.Polling = *status.Polling
			}
			s.save(t)
		})
		err = s.backend.Prepare(context.WithValue(prepareCtx, progressKey{}, progress), tc, nc)
		if err == nil {
			err = prepareCtx.Err()
		}
		if err != nil {
			// Only the initiating failure cancels peers. A peer's adapter may
			// report its last probe error after cancellation; preserve the cause.
			if cause := context.Cause(ctx); cause != nil {
				err = &wire.Error{Code: "preparation_cancelled", Message: "Node preparation was cancelled", Details: map[string]any{"node_id": n.ID, "cause": wire.Safe(cause), "last_error": wire.Safe(err)}}
			} else {
				s.mu.Lock()
				stage := n.Preparation.Stage
				s.mu.Unlock()
				cause := wire.Safe(err)
				err = &wire.Error{Code: cause.Code, Message: cause.Message, Details: map[string]any{"node_id": n.ID, "stage": stage, "started": started, "deadline": until, "elapsed_ms": time.Since(started).Milliseconds(), "cause": cause}}
				cancel(err)
			}
		}
		s.mu.Lock()
		n.Polling = PollSchedule{}
		n.Preparation.Updated = time.Now().UTC()
		if err == nil {
			n.State = "running"
			n.Preparation.Stage, n.Preparation.Error = "ready", nil
			now := time.Now().UTC()
			n.Health = Health{VirtualBox: Observation{State: "available", LastSuccess: &now}, GuestControl: Observation{State: "available", LastSuccess: &now}, Commands: Observation{State: "available", LastSuccess: &now}}
		} else {
			n.Preparation.Stage, n.Preparation.Error = "failed", wire.Safe(err)
			if wire.Safe(err).Code == "preparation_cancelled" {
				n.Preparation.Stage = "cancelled"
			}
		}
		n.Preparation.StageStarted = &n.Preparation.Updated
		s.save(t)
		s.mu.Unlock()
		return err
	}
	if err := prepare(t.lab().Router); err != nil {
		return err
	}
	// All clone MACs and reservations are fixed at admission. Establish DHCP,
	// DNS and host protection before any client starts its network manager.
	if err := s.refreshNetworkContext(ctx, t, r, false); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, n := range t.lab().Nodes {
		wg.Add(1)
		go func() { defer wg.Done(); _ = prepare(n) }()
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return nil
}
func (s *Server) observe(t *Task, r *Runtime) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	nextNetwork := time.Now()
	var polls sync.Map
	var wg sync.WaitGroup
	defer func() { r.cancel(); wg.Wait() }()
	poll := func(key string, run func()) {
		if _, active := polls.LoadOrStore(key, true); active {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer polls.Delete(key)
			run()
		}()
	}
	for {
		select {
		case <-r.ctx.Done():
			s.mu.Lock()
			if !t.Terminal() {
				t.State = "interrupted"
				t.Reason = "service_stopped"
				s.save(t)
			}
			s.mu.Unlock()
			return
		case <-tick.C:
			s.mu.Lock()
			if t.Terminal() {
				s.mu.Unlock()
				return
			}
			if t.Deadline != nil && !time.Now().Before(*t.Deadline) {
				t.State = "timed_out"
				s.save(t)
				s.mu.Unlock()
				return
			}
			kind := t.Kind
			healthy := t.Kind == "lab" && oneOf(t.lab().Network, "ready", "suspect")
			expired := healthy && !t.lab().NetworkLeaseExpires.IsZero() && !time.Now().Before(t.lab().NetworkLeaseExpires)
			snapshot := t.executionSnapshot()
			s.mu.Unlock()
			if expired {
				s.degrade(t, r, wire.Fail("network_lease_expired", "Confirmed network lease expired; explicit recovery is required"))
				healthy = false
			}
			if kind == "nixos" {
				raw, e := s.backend.Nix(r.ctx, "status", snapshot)
				if e != nil {
					continue
				}
				var v struct {
					State     string             `json:"state"`
					Cleanup   string             `json:"cleanup"`
					Artifacts []Artifact         `json:"artifacts"`
					Execution *wire.NixExecution `json:"execution"`
				}
				if json.Unmarshal(raw, &v) != nil {
					continue
				}
				s.mu.Lock()
				if v.Execution != nil && string(wire.JSON(t.nix().NixExecution)) != string(wire.JSON(v.Execution)) {
					t.nix().NixExecution, t.Failure = v.Execution, v.Execution.Error
					if t.Failure == nil {
						t.Failure = v.Execution.PersistenceError
					}
					s.save(t)
				}
				if v.Execution != nil && v.Execution.Imported {
					if input := s.transfers[t.nix().CacheID]; input != nil {
						if err := s.removeTransfer(input); err != nil {
							s.event(t, "input_release_failed", "Imported upload removal will be retried")
							s.save(t)
						}
					}
				}
				s.mu.Unlock()
				if v.State == "succeeded" || v.State == "failed" || v.State == "cleanup_required" || v.State == "interrupted" {
					s.mu.Lock()
					// A reply already in flight can arrive after task cancellation or
					// timeout. Its execution evidence remains valid independently.
					if t.Terminal() {
						s.mu.Unlock()
						return
					}
					t.State = "finished"
					t.Conclusion = "passed"
					if v.State != "succeeded" {
						t.State = "failed"
						t.Conclusion = "failed"
					}
					if v.State == "interrupted" {
						t.State, t.Conclusion = "interrupted", "inconclusive"
					}
					s.save(t)
					s.mu.Unlock()
					return
				}
				continue
			}
			if healthy && !time.Now().Before(nextNetwork) {
				poll("network", func() {
					ctx, stop := context.WithTimeout(r.ctx, networkBudget)
					defer stop()
					s.heartbeatNetwork(ctx, t, r)
				})
				nextNetwork = time.Now().Add(time.Duration(s.cfg.VirtualBox.Network.LeaseSeconds/3) * time.Second)
			}

			for _, n := range snapshot.lab().Nodes {
				if n.Preparation.Stage == "ready" && n.Polling.due(time.Now()) {
					poll("node/"+n.ID, func() { s.pollNode(t, r, n.ID) })
				}
			}
			for _, c := range snapshot.lab().Commands {
				if c.Polling() && c.Schedule.due(time.Now()) {
					poll("command/"+c.ID, func() { s.pollCommand(t, r, c.ID) })
				}
			}
			// Slow guest operations must not delay lease renewal or deadlines.
			// Polls never overlap for one resource and finish before cleanup.
		}
	}
}
func (s *Server) nodeLock(r *Runtime, id string) *sync.Mutex {
	v, _ := r.nodes.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func (s *Server) pollNode(t *Task, r *Runtime, id string) {
	ctx, cancel := context.WithTimeout(r.ctx, probeTimeout)
	defer cancel()
	if readGate(ctx, &r.gate) != nil {
		return
	}
	// Read-only observation does not queue behind a node mutation.
	s.mu.Lock()
	n, err := t.Node(id)
	if err != nil || t.Terminal() {
		s.mu.Unlock()
		r.gate.RUnlock()
		return
	}
	tc, nc := t.executionSnapshot(), clone(n)
	s.mu.Unlock()
	raw, err := s.backend.Action(ctx, tc, nc, NodeAction{Action: "node_status"})
	r.gate.RUnlock()
	var status NodeStatus
	if err == nil {
		err = wire.Decode(raw, &status)
		if err == nil && status.State == "" {
			err = wire.Fail("backend_protocol_error", "Node observation has no power state")
		}
	}
	s.mu.Lock()
	if t.Terminal() {
		s.mu.Unlock()
		return
	}
	if nc.Expected != nil && (nc.Expected.Dispatched == nil || n.Expected == nil || nc.Expected.RequestID != n.Expected.RequestID) || nc.Expected == nil && n.Expected != nil {
		s.mu.Unlock()
		return
	}
	before := n.Health.unavailable()
	incident := s.observeNode(n, &status, err, time.Now().UTC())
	n.nextProbe(time.Now().UTC())
	if incident {
		s.event(t, "node_unavailable", "Node "+n.Name+" has an unavailable capability")
	}
	if before && n.Health.VirtualBox.State == "available" && n.Health.GuestControl.State == "available" && n.Health.Commands.State == "available" {
		s.event(t, "node_recovered", "Node "+n.Name+" capabilities recovered")
	}
	s.save(t)
	if incident && !s.closed {
		// Collect the lab's evidence on an independent job. Slow collection
		// must not postpone the next health observation or recovery signal.
		s.wg.Add(1)
		go func() { defer s.wg.Done(); _, _ = s.diagnose(r.ctx, t.ID, nil, true) }()
	}
	s.mu.Unlock()
}
func (s *Server) pollCommand(t *Task, r *Runtime, id string) {
	s.mu.Lock()
	c, err := t.Command(id)
	if err != nil {
		s.mu.Unlock()
		return
	}
	n, err := t.Node(c.NodeID)
	if err != nil {
		s.mu.Unlock()
		return
	}
	if c.Retained.Deadline != nil && !time.Now().Before(*c.Retained.Deadline) && !c.Retained.done() {
		c.Retained.stop(wire.Fail("output_timeout", "Final output collection exceeded its time budget"))
		s.save(t)
	}
	if !c.Polling() {
		s.mu.Unlock()
		return
	}
	if n.blocksCommands() {
		// One low-frequency node probe owns recovery of the shared channel.
		// Per-command polls do not each create another session during an outage.
		c.Schedule = n.Polling
		c.Schedule.Reason = "node_unavailable"
		if c.Schedule.Next == nil || c.Schedule.due(time.Now()) {
			c.Schedule.set(time.Now().UTC(), probeInterval, "node_unavailable")
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	if readGate(ctx, &r.gate) != nil {
		return
	}
	defer r.gate.RUnlock()
	s.pollCommandContext(ctx, t, id, true)
}
func (s *Server) closeRelays(t *Task, r *Runtime) {
	s.mu.Lock()
	relays := r.relays
	r.relays = map[string]*Relay{}
	s.mu.Unlock()
	for _, relay := range relays {
		relay.Close()
	}
}
func (s *Server) degrade(t *Task, r *Runtime, err error) {
	s.mu.Lock()
	if t.Terminal() || t.lab().Network == "degraded" {
		s.mu.Unlock()
		return
	}
	// A renewal may have completed between the observer's expiry snapshot and
	// this transition. Never suspend a newly confirmed lease using stale data.
	if wire.Safe(err).Code == "network_lease_expired" && time.Now().Before(t.lab().NetworkLeaseExpires) {
		s.mu.Unlock()
		return
	}
	t.lab().Network = "degraded"
	relays := r.relays
	r.relays = map[string]*Relay{}
	for _, e := range t.lab().Endpoints {
		if e.State == "open" {
			e.State = "suspended"
		}
	}
	s.eventDetails(t, "network_degraded", "Network requires explicit recovery", map[string]any{"cause": wire.Safe(err), "observation": t.lab().NetworkObservation, "policy_updated": t.lab().NetworkUpdated, "lease_expires": t.lab().NetworkLeaseExpires, "router_identity": routerIdentity(t)})
	s.save(t)
	s.mu.Unlock()
	for _, relay := range relays {
		relay.Close()
	}
}
func (s *Server) openRelay(ctx context.Context, t *Task, r *Runtime, e *Endpoint) error {
	s.mu.Lock()
	tc, ec := clone(t), clone(e)
	s.mu.Unlock()
	if err := s.backend.Endpoint(ctx, tc, ec, true); err != nil {
		return err
	}
	sources := []netip.Prefix{}
	for _, cidr := range s.cfg.VirtualBox.Network.Sources {
		sources = append(sources, netip.MustParsePrefix(cidr))
	}
	relay, err := OpenRelay(r.ctx, ec.Protocol, s.cfg.VirtualBox.Network.Listen, ec.HostPort, fmt.Sprintf("127.0.0.1:%d", ec.LoopbackPort), sources)
	if err != nil {
		return err
	}
	s.mu.Lock()
	r.relays[e.ID] = relay
	s.mu.Unlock()
	return nil
}
func (s *Server) readLog(name string, offset int64, limit int) (any, error) {
	limit, e := window(offset, limit)
	if e != nil {
		return nil, e
	}
	f, e := s.root.Regular(name)
	if os.IsNotExist(e) {
		return map[string]any{"data": "", "next_offset": offset, "eof": true}, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b := make([]byte, limit)
	n, e := f.ReadAt(b, offset)
	if e != nil && e != io.EOF {
		return nil, e
	}
	return map[string]any{"data": base64.StdEncoding.EncodeToString(b[:n]), "next_offset": offset + int64(n), "eof": e == io.EOF}, nil
}
