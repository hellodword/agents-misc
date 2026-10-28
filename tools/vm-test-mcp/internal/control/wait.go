package control

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

const WaitWindow = 600 * time.Second
const TransportHeartbeat = 15 * time.Second

type WaitTarget struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Condition    string `json:"condition"`
	SeenRevision int64  `json:"seen_revision,omitempty"`
}
type WaitRequest struct {
	Targets []WaitTarget `json:"targets"`
}
type TargetState struct {
	WaitTarget
	Revision  int64 `json:"revision"`
	Satisfied bool  `json:"satisfied"`
	Attention bool  `json:"attention"`
	Summary   any   `json:"summary"`
}
type WaitResult struct {
	Reason   string        `json:"reason"`
	Targets  []TargetState `json:"targets"`
	Continue []WaitTarget  `json:"continue"`
}
type revisionState struct {
	value    string
	revision int64
}

// Only semantic changes advance ordinary revisions. Progress, byte growth and
// successful probes have an independent output revision or remain in memory.
func (s *Server) revision(key string, value any) int64 {
	v := string(wire.JSON(value))
	old := s.revisions[key]
	if old.value != v {
		old.value = v
		parts := strings.Split(key, "/")
		owner := parts[1]
		if parts[0] == "command" {
			_ = s.db.QueryRowContext(s.ctx, "SELECT task_id FROM commands WHERE id=?", parts[1]).Scan(&owner)
		}
		if parts[0] == "operation" {
			_ = s.db.QueryRowContext(s.ctx, "SELECT task_id FROM operations WHERE id=?", parts[1]).Scan(&owner)
		}
		if parts[0] == "upload" {
			_ = s.db.QueryRowContext(s.ctx, "SELECT task_id FROM transfers WHERE id=?", parts[1]).Scan(&owner)
		}
		rev, err := s.db.Revision(s.ctx, key, owner, []byte(v))
		if err != nil {
			s.cancel()
			s.closed = true
			return old.revision
		}
		old.revision = rev
		s.revisions[key] = old
	}
	return old.revision
}

func taskCondition(t *Task) any {
	nodes := []any{}
	for _, n := range append(append([]*Node{}, t.Nodes...), t.Router) {
		if n != nil {
			nodes = append(nodes, []any{n.ID, n.State, n.Preparation.Stage, n.Health.VirtualBox.State, n.Health.GuestControl.State, n.Health.Commands.State, n.Cleaned, n.CleanupSteps})
		}
	}
	return []any{t.State, t.Conclusion, t.Reason, t.Cleanup, t.CleanupErrors, t.Failure, t.Network, nodes}
}
func commandCondition(c *Command) any {
	return []any{c.State, c.Stage, c.Final, c.ExitCode, c.ExitSignal, c.Error, c.Observation.State, c.Cancellation, c.Termination, c.Release, c.Retained.Stdout.State, c.Retained.Stderr.State, c.Retained.Stdout.Error, c.Retained.Stderr.Error}
}

func (s *Server) targetLocked(target WaitTarget, business bool) (TargetState, error) {
	out := TargetState{WaitTarget: target}
	key := target.Kind + "/" + target.ID
	switch target.Kind {
	case "lab", "nix_job":
		t, err := s.task(target.ID)
		if err != nil {
			return out, err
		}
		if (target.Kind == "lab") != (t.Kind == "lab") {
			return out, wire.Fail("invalid_argument", "Wait target kind does not match execution")
		}
		out.Summary = t.summaryView()
		s.queueView(t, out.Summary.(map[string]any))
		out.Revision = s.revision(key, taskCondition(t))
		out.Attention = t.cleanupState() == "blocked" || t.Failure != nil || t.Terminal() && t.Conclusion != "passed" || t.Network == "degraded"
		// Guest services are expected to be unavailable while nodes boot. The
		// preparation deadline/failure path decides when that requires intervention.
		if t.State == "running" {
			for _, n := range t.Nodes {
				out.Attention = out.Attention || n.Health.unavailable()
			}
		}
		switch target.Condition {
		case "ready":
			out.Satisfied = t.State == "running"
		case "complete", "cleaned":
			if target.Condition == "cleaned" {
				out.Attention = t.cleanupState() == "blocked"
			}
			out.Satisfied = t.Terminal() && t.Cleanup == "complete"
		case "changed":
			out.Satisfied = out.Revision > target.SeenRevision
		case "output":
			var size int64
			if f, err := s.root.Regular("tasks/" + t.ID + "/task.log"); err == nil {
				if st, err := f.Stat(); err == nil {
					size = st.Size()
				}
				f.Close()
			}
			out.Revision = s.revision(key+"/output", size)
			out.Satisfied = out.Revision > target.SeenRevision
		default:
			return out, wire.Fail("invalid_argument", "Execution condition must be ready, complete, cleaned, changed or output")
		}
	case "command":
		var command *Command
		for _, t := range s.tasks {
			if c, err := t.Command(target.ID); err == nil {
				command = c
				break
			}
		}
		if command == nil {
			var id string
			if err := s.db.QueryRowContext(s.ctx, "SELECT task_id FROM commands WHERE id=?", target.ID).Scan(&id); err == nil {
				if t, err := s.task(id); err == nil {
					command, _ = t.Command(target.ID)
				}
			}
		}
		if command == nil {
			return out, wire.Fail("not_found", "Command not found")
		}
		c := command
		out.Summary = c.publicStatus()
		out.Revision = s.revision(key, commandCondition(c))
		out.Attention = c.State == "uncertain" || c.State == "failed" || c.State == "cancelled" || c.State == "timed_out" || c.Observation.State == "unavailable" || c.Retained.Stdout.State == "incomplete" || c.Retained.Stderr.State == "incomplete" || c.ExitCode != nil && *c.ExitCode != 0
		if business && c.Final && !oneOf(c.State, "failed", "uncertain") {
			out.Attention = c.Observation.State == "unavailable" || c.Retained.Stdout.State == "incomplete" || c.Retained.Stderr.State == "incomplete"
		}
		switch target.Condition {
		case "started":
			out.Satisfied = c.Started != nil || c.State == "running" || c.Final
		case "complete":
			out.Satisfied = c.Final && c.Retained.done() && (c.Release != nil || c.Termination != nil && c.Termination.GroupStopped)
		case "changed":
			out.Satisfied = out.Revision > target.SeenRevision
		case "output":
			out.Revision = s.revision(key+"/output", []any{c.Retained.Stdout, c.Retained.Stderr})
			out.Satisfied = out.Revision > target.SeenRevision
		default:
			return out, wire.Fail("invalid_argument", "Command condition must be started, complete, changed or output")
		}
	case "operation":
		o, err := s.operation(target.ID)
		if err != nil {
			return out, err
		}
		out.Summary = clone(o)
		out.Revision = s.revision(key, []any{o.State, o.Error})
		out.Attention = oneOf(o.State, "failed", "uncertain")
		if target.Condition == "complete" {
			out.Satisfied = o.State != "pending"
		} else if target.Condition == "changed" {
			out.Satisfied = out.Revision > target.SeenRevision
		} else {
			return out, wire.Fail("invalid_argument", "Operation condition must be complete or changed")
		}
	case "upload":
		t, err := s.transfer(target.ID)
		if err != nil {
			return out, err
		}
		out.Summary = map[string]any{"id": t.ID, "state": t.State, "bytes": t.Bytes, "uploading": s.uploading[t.ID] != nil}
		out.Revision = s.revision(key, []any{t.State, t.Bytes, s.uploading[t.ID] != nil})
		out.Attention = t.State == "purged"
		if target.Condition == "complete" {
			out.Satisfied = t.Bytes > 0 && s.uploading[t.ID] == nil
		} else if target.Condition == "changed" {
			out.Satisfied = out.Revision > target.SeenRevision
		} else {
			return out, wire.Fail("invalid_argument", "Upload condition must be complete or changed")
		}
	default:
		return out, wire.Fail("invalid_argument", "Unknown wait target kind")
	}
	// All snapshots must be detached before releasing the server mutex.
	var copy any
	if err := json.Unmarshal(wire.JSON(out.Summary), &copy); err != nil {
		return out, err
	}
	out.Summary = copy
	return out, nil
}

func (s *Server) wait(ctx context.Context, req WaitRequest, budget time.Duration, business ...bool) (WaitResult, error) {
	if len(req.Targets) < 1 || len(req.Targets) > 64 {
		return WaitResult{}, wire.Fail("invalid_argument", "wait requires 1..64 targets")
	}
	for _, t := range req.Targets {
		if !wire.IDPattern.MatchString(t.ID) || t.SeenRevision < 0 {
			return WaitResult{}, wire.Fail("invalid_argument", "Invalid wait reference")
		}
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	expired := false
	for {
		result := WaitResult{Reason: "timeout", Targets: []TargetState{}, Continue: []WaitTarget{}}
		s.mu.Lock()
		for _, target := range req.Targets {
			view, err := s.targetLocked(target, len(business) > 0 && business[0])
			if err != nil {
				s.mu.Unlock()
				return result, err
			}
			result.Targets = append(result.Targets, view)
			next := target
			next.SeenRevision = view.Revision
			result.Continue = append(result.Continue, next)
			if view.Satisfied {
				result.Reason = "satisfied"
			} else if view.Attention && view.Revision > target.SeenRevision {
				result.Reason = "attention"
			}
		}
		s.mu.Unlock()
		if expired || result.Reason != "timeout" {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-s.ctx.Done():
			return result, wire.Fail("unavailable", "Observer stopped; accepted execution remains durable")
		case <-timer.C:
			expired = true
		case <-tick.C:
		}
	}
}

// Call observes the business condition. accept owns durable admission and never
// inherits the observing HTTP request's cancellation after acceptance.
func (s *Server) Call(ctx context.Context, name string, raw []byte) (any, error) {
	if name == "wait" {
		var req WaitRequest
		if err := wire.Decode(raw, &req); err != nil {
			return nil, err
		}
		return s.wait(ctx, req, WaitWindow)
	}
	started := time.Now()
	value, err := s.accept(ctx, name, raw)
	if err != nil {
		return nil, err
	}
	op, ok := value.(*Operation)
	if !ok {
		return value, nil
	}
	reply, err := s.wait(ctx, WaitRequest{[]WaitTarget{{Kind: "operation", ID: op.ID, Condition: "complete"}}}, max(0, WaitWindow-time.Since(started)))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	current, e := s.operation(op.ID)
	if e == nil {
		op = clone(current)
	}
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	target := WaitTarget{Kind: "operation", ID: op.ID, Condition: "complete"}
	if op.State == "succeeded" {
		var args struct {
			Mode      string `json:"mode"`
			CommandID string `json:"command_id"`
		}
		_ = json.Unmarshal(raw, &args)
		switch name {
		case "lab_create":
			target = WaitTarget{Kind: "lab", ID: op.TaskID, Condition: "ready"}
		case "nix_job_create":
			target = WaitTarget{Kind: "nix_job", ID: op.TaskID, Condition: "cleaned"}
		case "lab_finish", "lab_cancel", "lab_cleanup", "nix_job_cancel", "nix_job_cleanup":
			target = WaitTarget{Kind: "lab", ID: op.TaskID, Condition: "cleaned"}
			if name == "nix_job_cancel" || name == "nix_job_cleanup" {
				target.Kind = "nix_job"
			}
		case "command_start", "command_cancel":
			id := args.CommandID
			if name == "command_start" {
				var c struct{ ID string }
				_ = json.Unmarshal(wire.JSON(op.Result), &c)
				id = c.ID
			}
			target = WaitTarget{Kind: "command", ID: id, Condition: "complete"}
			if name == "command_start" && oneOf(args.Mode, "service", "interactive") {
				target.Condition = "started"
			}
		}
		if target.Kind != "operation" {
			reply, err = s.wait(ctx, WaitRequest{[]WaitTarget{target}}, max(0, WaitWindow-time.Since(started)), true)
			if err != nil {
				return nil, err
			}
		}
	}
	return map[string]any{"operation": op, "wait": reply}, nil
}

func (s *Server) operation(id string) (*Operation, error) {
	if op := s.operations[id]; op != nil {
		return op, nil
	}
	var op Operation
	if err := s.db.Load(s.ctx, "operation", id, &op); err != nil {
		return nil, wire.Fail("not_found", fmt.Sprintf("Operation %s not found", id))
	}
	return &op, nil
}
func (s *Server) transfer(id string) (*Transfer, error) {
	if t := s.transfers[id]; t != nil {
		return t, nil
	}
	var t Transfer
	if err := s.db.Load(s.ctx, "transfer", id, &t); err != nil {
		return nil, wire.Fail("not_found", "Upload not found")
	}
	var err error
	t.Objects, t.Bytes, err = s.db.TransferObjects(s.ctx, id)
	return &t, err
}
