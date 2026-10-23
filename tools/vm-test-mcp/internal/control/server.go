package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/network"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Server struct {
	mu            sync.Mutex
	wg            sync.WaitGroup
	cfg           config.Config
	root          *safefs.Root
	db            *state.DB
	backend       Backend
	tasks         []*Task
	operations    map[string]*Operation
	requests      map[string]*Operation
	transfers     map[string]*Transfer
	runtime       map[string]*Runtime
	active        string
	ctx           context.Context
	cancel        context.CancelFunc
	closed        bool
	secret        []byte
	transferLocks sync.Map
	uploading     map[string]bool
}

func New(cfg config.Config, b Backend) (*Server, error) {
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	root, e := safefs.Private(cfg.Service.StateDir, true)
	if e != nil {
		return nil, e
	}
	db, e := state.Open(root.Path)
	if e != nil {
		root.Close()
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, root: root, db: db, backend: b, ctx: ctx, cancel: cancel, operations: map[string]*Operation{}, requests: map[string]*Operation{}, transfers: map[string]*Transfer{}, runtime: map[string]*Runtime{}, uploading: map[string]bool{}}
	fail := func(e error) (*Server, error) { db.Close(); root.Close(); cancel(); return nil, e }
	if s.secret, e = root.Read("capability-key", 128); os.IsNotExist(e) {
		s.secret = []byte(wire.ID() + wire.ID())
		_, _, e = root.Write("capability-key", bytesReader(s.secret), 128)
	}
	if e != nil {
		return fail(e)
	}
	for _, kind := range []string{"task", "operation", "transfer"} {
		rows, e := db.List(ctx, kind)
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
				s.tasks = append(s.tasks, &t)
			case "operation":
				var o Operation
				if e = json.Unmarshal(row, &o); e != nil {
					return fail(e)
				}
				if o.State == "pending" {
					o.State = "uncertain"
					o.Error = &wire.Error{Code: "interrupted", Message: "Service stopped before confirming this operation"}
					if e = db.Save(ctx, state.Document{Kind: kind, ID: o.ID, Value: o}); e != nil {
						return fail(e)
					}
				}
				s.operations[o.ID] = &o
				s.requests[o.RequestID] = &o
			case "transfer":
				var t Transfer
				if e = json.Unmarshal(row, &t); e != nil {
					return fail(e)
				}
				s.transfers[t.ID] = &t
			}
		}
	}
	sort.SliceStable(s.tasks, func(i, j int) bool { return s.tasks[i].Created.Before(s.tasks[j].Created) })
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
func (s *Server) persist(docs ...state.Document) {
	if e := s.db.Save(context.Background(), docs...); e != nil {
		slog.Error("state write failed", "error", e)
		s.cancel()
		s.closed = true
	}
}
func (s *Server) save(t *Task) { s.persist(state.Document{Kind: "task", ID: t.ID, Value: t}) }
func (s *Server) event(t *Task, code, msg string) {
	e := s.db.Event(context.Background(), t.ID, map[string]any{"time": time.Now().UTC(), "code": code, "message": msg})
	if e != nil {
		slog.Error("event write failed", "error", e)
		s.cancel()
	}
}
func (s *Server) task(id string) (*Task, error) {
	for _, t := range s.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, wire.Fail("not_found", "Task not found")
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
func (s *Server) Start() { s.wg.Add(1); go s.schedule() }
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	for _, r := range s.runtime {
		r.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	s.db.Close()
	s.root.Close()
}
func (s *Server) schedule() {
	defer s.wg.Done()
	// Recovery completes before admitting the first queued task.
	s.mu.Lock()
	recovery := []*Task{}
	for _, t := range s.tasks {
		if t.State != "queued" && t.Cleanup != "complete" {
			recovery = append(recovery, t)
		}
	}
	s.mu.Unlock()
	blocked := ""
	for _, t := range recovery {
		s.mu.Lock()
		t.State = "interrupted"
		t.Reason = "controller_restart"
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
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
			s.mu.Lock()
			if s.active != "" || s.closed {
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
			earlier := false
			for _, op := range s.operations {
				if op.State == "pending" && (op.Tool == "lab_create" || op.Tool == "nixos_run") && op.Created.Before(next.Created) {
					earlier = true
					break
				}
			}
			if earlier {
				s.mu.Unlock()
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
	if t.Kind == "lab" {
		err = s.prepare(t, r)
	} else {
		s.mu.Lock()
		snapshot := clone(t)
		s.mu.Unlock()
		_, err = s.backend.Nix(r.ctx, "start", snapshot)
	}
	s.mu.Lock()
	if err != nil && !t.Terminal() {
		t.State = "failed"
		t.Reason = wire.Safe(err).Code
		s.event(t, "prepare_failed", "Backend preparation failed")
	} else if t.State == "preparing" {
		t.State = "running"
	}
	s.save(t)
	s.mu.Unlock()
	if err == nil {
		s.observe(t, r)
	}
	r.cancel()
	s.cleanup(t)
	s.mu.Lock()
	if t.Cleanup == "complete" {
		s.active = ""
	}
	s.mu.Unlock()
}
func (s *Server) prepare(t *Task, r *Runtime) error {
	if len(t.Nodes) != 0 {
		if e := s.setGUI(r.ctx, t, "any", false); e != nil {
			return e
		}
	}
	prepare := func(n *Node) error {
		s.mu.Lock()
		n.Intent = true
		err := s.db.Save(context.Background(), state.Document{Kind: "task", ID: t.ID, Value: t})
		copyTask := clone(t)
		copyNode := clone(n)
		s.mu.Unlock()
		if err != nil {
			return err
		}
		e := s.backend.Prepare(r.ctx, copyTask, copyNode)
		s.mu.Lock()
		defer s.mu.Unlock()
		if e == nil {
			n.State = "running"
		} else {
			n.State = "error"
		}
		s.save(t)
		return e
	}
	if e := prepare(t.Router); e != nil {
		return e
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(t.Nodes))
	for _, n := range t.Nodes {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- prepare(n) }()
	}
	wg.Wait()
	close(errs)
	var es []error
	for e := range errs {
		if e != nil {
			es = append(es, e)
		}
	}
	if len(es) > 0 {
		return errors.Join(es...)
	}
	return s.refreshNetwork(t, r, false)
}
func (s *Server) observe(t *Task, r *Runtime) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	nextNetwork := time.Now()
	nextNodes := time.Now()
	var polls sync.Map
	var wg sync.WaitGroup
	defer wg.Wait()
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
			healthy := t.Network == "ready"
			snapshot := clone(t)
			s.mu.Unlock()
			if kind == "nixos" {
				raw, e := s.backend.Nix(r.ctx, "status", snapshot)
				if e != nil {
					continue
				}
				var v struct {
					State     string     `json:"state"`
					Cleanup   string     `json:"cleanup"`
					Artifacts []Artifact `json:"artifacts"`
				}
				if json.Unmarshal(raw, &v) != nil {
					continue
				}
				if v.State == "succeeded" || v.State == "failed" || v.State == "cleanup_required" {
					s.mu.Lock()
					t.State = "finished"
					t.Conclusion = "passed"
					if v.State != "succeeded" {
						t.State = "failed"
						t.Conclusion = "failed"
					}
					s.save(t)
					s.mu.Unlock()
					return
				}
				continue
			}
			if healthy && !time.Now().Before(nextNetwork) {
				lock := s.nodeLock(r, "network")
				lock.Lock()
				s.mu.Lock()
				healthy = t.Network == "ready" && !t.Terminal()
				s.mu.Unlock()
				if healthy {
					if e := s.refreshNetwork(t, r, false); e != nil {
						s.degrade(t, r)
					}
				}
				lock.Unlock()
				nextNetwork = time.Now().Add(time.Duration(s.cfg.Network.LeaseSeconds/3) * time.Second)
			}
			if !time.Now().Before(nextNodes) {
				for _, n := range snapshot.Nodes {
					poll("node/"+n.ID, func() { s.pollNode(t, r, n.ID) })
				}
				nextNodes = time.Now().Add(3 * time.Second)
			}
			for _, c := range snapshot.Commands {
				if c.State == "running" || c.State == "starting" || c.State == "uncertain" {
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
	r.gate.RLock()
	defer r.gate.RUnlock()
	lock := s.nodeLock(r, id)
	lock.Lock()
	defer lock.Unlock()
	s.mu.Lock()
	n, e := t.Node(id)
	if e != nil || t.Terminal() {
		s.mu.Unlock()
		return
	}
	tc, nc := clone(t), clone(n)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	raw, e := s.backend.Action(ctx, tc, nc, NodeAction{Action: "node_status"})
	if e != nil {
		return
	}
	var v struct {
		State string         `json:"state"`
		Guest map[string]any `json:"guest"`
	}
	if wire.Decode(raw, &v) != nil || v.State == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.State != v.State || !reflect.DeepEqual(n.Guest, v.Guest) {
		n.State = v.State
		n.Guest = v.Guest
		s.save(t)
	}
}
func (s *Server) pollCommand(t *Task, r *Runtime, id string) {
	r.gate.RLock()
	defer r.gate.RUnlock()
	ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
	defer cancel()
	s.pollCommandContext(ctx, t, id, true)
}
func (s *Server) pollCommandContext(ctx context.Context, t *Task, id string, sendInput bool) {
	s.mu.Lock()
	c, e := t.Command(id)
	if e != nil {
		s.mu.Unlock()
		return
	}
	n, e := t.Node(c.NodeID)
	if e != nil {
		s.mu.Unlock()
		return
	}
	tc, nc := clone(t), clone(n)
	s.mu.Unlock()
	local := path.Join(s.root.Path, "tasks", t.ID, "commands", id)
	_ = s.root.MkdirAll(path.Join("tasks", t.ID, "commands", id), 0700)
	raw, e := s.backend.Action(ctx, tc, nc, NodeAction{Action: "command_poll", CommandID: id, Local: local})
	if e != nil {
		return
	}
	var status wire.CommandStatus
	if wire.Decode(raw, &status) != nil {
		return
	}
	// Persisted stdin is an outbox. Re-send unacknowledged packets; the guest
	// publishes each sequence exclusively, making loss of a delivery reply safe.
	cc, _ := tc.Command(id)
	if sendInput && (status.State == "running" || status.State == "starting") && status.NextInput >= 0 && status.NextInput < len(cc.Input) {
		for _, packet := range cc.Input[status.NextInput:] {
			if _, err := s.backend.Action(ctx, tc, nc, NodeAction{Action: "command_input", CommandID: id, Input: &packet}); err != nil {
				break
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.CommandStatus = status
	s.save(t)
}
func (s *Server) cleanup(t *Task) {
	s.mu.Lock()
	r := s.runtime[t.ID]
	if r == nil {
		ctx, cancel := context.WithCancel(context.Background())
		r = &Runtime{ctx: ctx, cancel: cancel, relays: map[string]*Relay{}}
		s.runtime[t.ID] = r
	}
	r.cancel()
	t.Cleanup = "running"
	s.save(t)
	s.mu.Unlock()
	r.gate.Lock()
	defer r.gate.Unlock()
	s.closeRelays(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var errs []error
	s.mu.Lock()
	snapshot := clone(t)
	s.mu.Unlock()
	if t.Kind == "nixos" {
		raw, e := s.backend.Nix(ctx, "cleanup", snapshot)
		if e != nil {
			errs = append(errs, e)
		} else {
			var result struct {
				Cleanup       string      `json:"cleanup"`
				Artifacts     []*Artifact `json:"artifacts"`
				EvidenceError bool        `json:"evidence_error"`
			}
			if json.Unmarshal(raw, &result) != nil || result.Cleanup != "complete" {
				errs = append(errs, fmt.Errorf("daemon cleanup is not confirmed"))
			}
			s.mu.Lock()
			if result.EvidenceError {
				s.event(t, "evidence_error", "Some Nix evidence could not be collected; daemon cleanup was confirmed")
			}
			for _, a := range result.Artifacts {
				if wire.IDPattern.MatchString(a.ID) {
					t.Artifacts = append(t.Artifacts, a)
				}
			}
			s.save(t)
			s.mu.Unlock()
		}
	} else {
		for _, c := range snapshot.Captures {
			if c.State == "running" {
				if e := s.stopCapture(ctx, t, r, c.ID, true); e != nil {
					s.mu.Lock()
					s.event(t, "evidence_error", "Capture could not be collected")
					s.mu.Unlock()
				}
			}
		}
		for _, c := range snapshot.Commands {
			if c.State == "running" || c.State == "starting" || c.State == "uncertain" {
				n, _ := snapshot.Node(c.NodeID)
				_, _ = s.backend.Action(ctx, snapshot, n, NodeAction{Action: "command_cancel", CommandID: c.ID})
			}
			evidenceCtx, stop := context.WithTimeout(ctx, 10*time.Second)
			s.pollCommandContext(evidenceCtx, t, c.ID, false)
			stop()
		}
		for _, n := range append(snapshot.Nodes, snapshot.Router) {
			if n == nil || !n.Intent || n.Cleaned {
				continue
			}
			if e := s.diagnostics(ctx, t, snapshot, n); e != nil {
				s.mu.Lock()
				s.event(t, "evidence_error", "VirtualBox log was unavailable")
				s.mu.Unlock()
			}
			e := s.backend.Delete(ctx, snapshot, n)
			s.mu.Lock()
			real := t.Router
			if n != snapshot.Router {
				real, _ = t.Node(n.ID)
			}
			if e == nil {
				real.Cleaned = true
				real.State = "deleted"
			} else {
				errs = append(errs, e)
			}
			s.save(t)
			s.mu.Unlock()
		}
		if snapshot.GUI != nil && !snapshot.GUI.Restored {
			if e := s.setGUI(ctx, t, "", true); e != nil {
				errs = append(errs, e)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	t.Ended = &now
	t.Cleanup = "complete"
	t.CleanupErrors = nil
	if len(errs) > 0 {
		t.Cleanup = "required"
		for _, err := range errs {
			t.CleanupErrors = append(t.CleanupErrors, wire.Safe(err))
		}
		s.event(t, "cleanup_required", "Administrator verification is required before scheduling can resume")
	} else {
		if err := s.root.RemoveAll(path.Join("tasks", t.ID, "runtime")); err != nil {
			t.Cleanup = "required"
			s.event(t, "cleanup_required", "Task-owned temporary files could not be removed")
		}
		for _, c := range t.Commands {
			if c.State == "running" || c.State == "starting" || c.State == "uncertain" {
				c.State = "cancelled"
			}
		}
		for _, e := range t.Endpoints {
			e.State = "closed"
		}
	}
	s.save(t)
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
func (s *Server) degrade(t *Task, r *Runtime) {
	s.closeRelays(t, r)
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Network = "degraded"
	for _, e := range t.Endpoints {
		if e.State == "open" {
			e.State = "suspended"
		}
	}
	s.event(t, "network_degraded", "Network requires explicit recovery")
	s.save(t)
}
func (s *Server) protections() ([]string, error) {
	extra := append([]string{}, s.cfg.Network.Protected...)
	for _, a := range s.cfg.Network.PublicHostAddresses {
		extra = append(extra, a+"/32")
	}
	return network.HostProtection(extra)
}
func (s *Server) refreshNetwork(t *Task, r *Runtime, recover bool) error {
	protected, e := s.protections()
	if e != nil {
		return e
	}
	s.mu.Lock()
	copyTask := clone(t)
	s.mu.Unlock()
	timeout := 5 * time.Second
	if recover {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(r.ctx, timeout)
	defer cancel()
	if !recover && copyTask.Network == "ready" && time.Since(copyTask.NetworkUpdated) > time.Duration(s.cfg.Network.LeaseSeconds)*time.Second {
		return wire.Fail("network_degraded", "Network lease expired; explicit recovery is required")
	}
	bootID, err := s.backend.Network(ctx, copyTask, protected, recover)
	if err != nil {
		return err
	}
	if recover {
		for _, entry := range copyTask.Endpoints {
			if entry.State == "closed" {
				continue
			}
			if e = s.openRelay(ctx, t, r, entry); e != nil {
				s.closeRelays(t, r)
				return e
			}
		}
	}
	s.mu.Lock()
	t.Network = "ready"
	t.RouterBootID = bootID
	t.NetworkUpdated = time.Now().UTC()
	if recover {
		for _, e := range t.Endpoints {
			if e.State != "closed" {
				e.State = "open"
			}
		}
	}
	s.save(t)
	s.mu.Unlock()
	return nil
}
func (s *Server) openRelay(ctx context.Context, t *Task, r *Runtime, e *Endpoint) error {
	s.mu.Lock()
	tc, ec := clone(t), clone(e)
	s.mu.Unlock()
	if err := s.backend.Endpoint(ctx, tc, ec, true); err != nil {
		return err
	}
	sources := []netip.Prefix{}
	for _, cidr := range s.cfg.Network.Sources {
		sources = append(sources, netip.MustParsePrefix(cidr))
	}
	relay, err := OpenRelay(r.ctx, ec.Protocol, s.cfg.Network.Listen, ec.HostPort, fmt.Sprintf("127.0.0.1:%d", ec.LoopbackPort), sources)
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
