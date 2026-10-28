package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

const commandStatusGrace = 30 * time.Second
const commandOutputBudget = 60 * time.Second

// Output is append-only. Replaying a chunk after a state write was interrupted
// verifies the already retained bytes instead of replacing or duplicating them.
func (s *Server) appendOutput(name string, offset int64, data []byte) error {
	if err := s.root.MkdirAll(path.Dir(name), 0700); err != nil {
		return err
	}
	f, err := s.root.OpenFile(name, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Size() < offset {
		return wire.Fail("output_gap", "Retained output is missing confirmed bytes")
	}
	existing := min(int64(len(data)), st.Size()-offset)
	if existing > 0 {
		previous := make([]byte, existing)
		if _, err = f.ReadAt(previous, offset); err != nil {
			return err
		}
		if !bytes.Equal(previous, data[:existing]) {
			return wire.Fail("output_changed", "Replayed output differs from the retained prefix")
		}
	}
	if existing < int64(len(data)) {
		if _, err = f.WriteAt(data[existing:], offset+existing); err != nil {
			return err
		}
	}
	return f.Sync()
}

func (s *Server) retainStream(name string, retained *RetainedStream, chunk wire.StreamChunk, status wire.CommandStatus, size int64, expectedHash string) bool {
	if retained.done() {
		return false
	}
	fail := func(err error) bool { retained.State, retained.Error = "incomplete", wire.Safe(err); return false }
	if chunk.Offset != retained.Bytes || chunk.Next < chunk.Offset || chunk.Size < chunk.Next || chunk.Size > wire.CommandOutputLimit {
		return fail(wire.Fail("output_offset", "Guest output offsets are inconsistent with the retained prefix"))
	}
	if chunk.State == "unavailable" {
		if chunk.Error == nil {
			chunk.Error = &wire.Error{Code: "output_unavailable", Message: "Guest output is unavailable"}
		}
		return fail(chunk.Error)
	}
	if chunk.State == "pending" && !status.Final {
		retained.State = "pending"
		return false
	}
	if chunk.State != "available" || chunk.Next-chunk.Offset > wire.CommandReadLimit {
		return fail(wire.Fail("guest_protocol_error", "Invalid guest output chunk"))
	}
	data, err := base64.StdEncoding.Strict().DecodeString(chunk.Data)
	if err != nil || int64(len(data)) != chunk.Next-chunk.Offset {
		return fail(wire.Fail("output_encoding", "Guest output chunk has invalid base64 or length"))
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != chunk.SHA256 {
		return fail(wire.Fail("output_checksum", "Guest output chunk checksum does not match"))
	}
	if err = s.appendOutput(name, retained.Bytes, data); err != nil {
		return fail(err)
	}
	retained.Bytes, retained.State, retained.Error = chunk.Next, "collecting", nil
	if status.Final && chunk.Next == chunk.Size {
		if status.Output == nil || status.Output.State != "complete" {
			return fail(wire.Fail("output_incomplete", "Supervisor did not confirm complete output drainage"))
		}
		if size != chunk.Size || expectedHash == "" {
			return fail(wire.Fail("output_size", "Final output size or checksum is missing or inconsistent"))
		}
		f, err := s.root.Regular(name)
		if err != nil {
			return fail(err)
		}
		hash := sha256.New()
		n, err := io.Copy(hash, f)
		_ = f.Close()
		if err != nil {
			return fail(err)
		}
		retained.SHA256 = hex.EncodeToString(hash.Sum(nil))
		if n != size || retained.SHA256 != expectedHash {
			return fail(wire.Fail("output_checksum", "Retained output does not match the supervisor's final checksum"))
		}
		retained.State = "complete"
	}
	return chunk.Next > chunk.Offset && chunk.Next < chunk.Size
}

func validateSnapshot(id string, v wire.CommandSnapshot) error {
	if v.Protocol != wire.CommandProtocol || v.CommandID != id || v.Checked.IsZero() {
		return wire.Fail("guest_protocol_error", "Guest command protocol, identity or observation time is invalid")
	}
	if v.Pending && v.Status == nil {
		return nil
	}
	if v.Pending || v.Status == nil || !oneOf(v.Status.State, "starting", "running", "exited", "cancelled", "timed_out", "uncertain", "failed") {
		return wire.Fail("guest_protocol_error", "Guest command has no recognized execution state")
	}
	if v.Status.Final && oneOf(v.Status.State, "starting", "running") || !v.Status.Final && oneOf(v.Status.State, "exited", "cancelled", "timed_out", "failed") {
		return wire.Fail("guest_protocol_error", "Guest command finality conflicts with its execution state")
	}
	if v.Status.WrittenInput < 0 {
		return wire.Fail("guest_protocol_error", "Guest input acknowledgement has a negative sequence")
	}
	return nil
}

func (s *Server) pollCommandContext(ctx context.Context, t *Task, id string, sendInput bool) {
	// Catch up a large stream within one bounded operation, without monopolizing
	// a Guest Control slot indefinitely. Each call releases its slot and session.
	for i := 0; i < 16 && ctx.Err() == nil; i++ {
		if !s.readCommand(ctx, t, id, sendInput && i == 0) {
			return
		}
	}
}

func (s *Server) readCommand(ctx context.Context, t *Task, id string, sendInput bool) bool {
	s.mu.Lock()
	c, err := t.Command(id)
	if err != nil || !c.Polling() {
		s.mu.Unlock()
		return false
	}
	n, err := t.Node(c.NodeID)
	if err != nil {
		s.mu.Unlock()
		return false
	}
	if c.Retained.Deadline != nil && !time.Now().Before(*c.Retained.Deadline) {
		c.Retained.stop(wire.Fail("output_timeout", "Final output collection exceeded its time budget"))
		s.save(t)
		s.mu.Unlock()
		return false
	}
	tc, nc, retained := t.executionSnapshot(), clone(n), c.Retained
	previous := c.CommandStatus
	s.mu.Unlock()
	raw, err := s.backend.Action(ctx, tc, nc, NodeAction{Action: "command_poll", CommandID: id, StdoutOffset: retained.Stdout.Bytes, StderrOffset: retained.Stderr.Bytes})
	var v wire.CommandSnapshot
	if err == nil {
		err = wire.Decode(raw, &v)
	}
	if err == nil {
		err = validateSnapshot(id, v)
	}
	if err == nil && previous.Final && (v.Status == nil || !bytes.Equal(wire.JSON(previous), wire.JSON(v.Status))) {
		err = wire.Fail("command_status_changed", "Guest changed a previously observed final execution record")
	}
	if err == nil && previous.Supervisor != nil && v.Status != nil && (v.Status.Supervisor == nil || *previous.Supervisor != *v.Status.Supervisor) {
		err = wire.Fail("command_identity_changed", "Guest command supervisor identity changed")
	}
	now := time.Now().UTC()
	if err != nil {
		s.mu.Lock()
		unavailable := c.Observation.State == "unavailable"
		c.Observation.update(now, wire.Safe(err), s.cfg.VirtualBox.UnavailableAfter.Value(), false)
		c.failedPoll(now)
		if !unavailable && c.Observation.State == "unavailable" {
			s.event(t, "command_unavailable", "Command observation requires channel recovery")
		}
		s.save(t)
		s.mu.Unlock()
		return false
	}
	if v.Pending {
		s.mu.Lock()
		if c.StatusDeadline == nil {
			end := now.Add(commandStatusGrace)
			c.StatusDeadline = &end
		}
		if c.Observation.LastSuccess != nil {
			c.Observation.update(now, &wire.Error{Code: "command_status_lost", Message: "A previously observed supervisor status disappeared"}, 0, true)
		} else if now.Before(*c.StatusDeadline) {
			c.Observation = Observation{State: "pending", Checked: &now}
		} else {
			c.State = "uncertain"
			c.Observation.update(now, &wire.Error{Code: "command_status_missing", Message: "Supervisor has not published a status within the observation budget", Details: map[string]any{"deadline": *c.StatusDeadline}}, 0, true)
		}
		if c.Observation.State == "pending" {
			c.Schedule.set(now, time.Second, "awaiting_status")
		} else {
			c.failedPoll(now)
		}
		s.save(t)
		s.mu.Unlock()
		return false
	}
	status := *v.Status
	if status.Final && retained.Deadline == nil {
		end := now.Add(commandOutputBudget)
		retained.Deadline = &end
	}
	var out wire.CommandOutput
	if status.Output != nil {
		out = *status.Output
	}
	base := path.Join("tasks", t.ID, "commands", id)
	moreOut := s.retainStream(path.Join(base, "stdout"), &retained.Stdout, v.Stdout, status, out.StdoutBytes, out.StdoutSHA256)
	moreErr := s.retainStream(path.Join(base, "stderr"), &retained.Stderr, v.Stderr, status, out.StderrBytes, out.StderrSHA256)
	s.mu.Lock()
	changed := previous.State != status.State || previous.Stage != status.Stage || previous.Final != status.Final || previous.WrittenInput != status.WrittenInput || previous.InputClosed != status.InputClosed || c.Retained.Stdout.Bytes != retained.Stdout.Bytes || c.Retained.Stderr.Bytes != retained.Stderr.Bytes
	c.CommandStatus, c.Retained = status, retained
	c.Stdin.Written = status.WrittenInput
	c.Stdin.Delivered = max(c.Stdin.Delivered, status.WrittenInput)
	c.successfulPoll(now, changed)
	// A valid record is direct evidence, including a terminal record whose next
	// poll is unnecessary. The node's recovery hysteresis is independent.
	c.Observation = Observation{State: "available", Checked: &now, LastSuccess: &now, Successes: 1, Error: v.Error}
	if v.Error != nil {
		c.Observation.State = "suspect"
	}
	if status.State == "cancelled" {
		c.Cancellation = "confirmed"
	}
	var packets []wire.Input
	cancel := sendInput && c.Observing() && c.Cancellation == "requested"
	if sendInput && c.Observing() && status.WrittenInput >= 0 && status.WrittenInput < c.Stdin.NextSequence {
		var err error
		packets, err = s.db.Inputs(ctx, t.ID, id, status.WrittenInput, 16)
		if err != nil {
			c.Observation.State, c.Observation.Error = "suspect", &wire.Error{Code: "input_read", Message: "Cannot read persisted stdin packets", Details: map[string]any{"cause": wire.Safe(err)}}
		}
	}
	s.save(t)
	s.mu.Unlock()
	if cancel {
		_, _ = s.backend.Action(ctx, tc, nc, NodeAction{Action: "command_cancel", CommandID: id})
	}
	for _, packet := range packets {
		if _, err := s.backend.Action(ctx, tc, nc, NodeAction{Action: "command_input", CommandID: id, Input: &packet}); err != nil {
			break
		}
		s.mu.Lock()
		if next, err := s.db.DeliveredInput(ctx, t, c, packet.Sequence); err == nil {
			c.Stdin.Delivered = next
			s.save(t)
		}
		s.mu.Unlock()
	}
	return moreOut || moreErr
}
