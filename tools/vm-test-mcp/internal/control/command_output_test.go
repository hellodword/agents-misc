package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func checksum(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func snapshotFixture(id string, status wire.CommandStatus, out, errout []byte, off, erroff int64, limit int) wire.CommandSnapshot {
	chunk := func(data []byte, offset int64) wire.StreamChunk {
		next := min(int64(len(data)), offset+int64(limit))
		part := data[offset:next]
		return wire.StreamChunk{State: "available", Offset: offset, Next: next, Size: int64(len(data)), Data: base64.StdEncoding.EncodeToString(part), SHA256: checksum(part)}
	}
	if status.Final && status.Output == nil {
		status.Output = &wire.CommandOutput{State: "complete", StdoutBytes: int64(len(out)), StderrBytes: int64(len(errout)), StdoutSHA256: checksum(out), StderrSHA256: checksum(errout)}
	}
	return wire.CommandSnapshot{Protocol: wire.CommandProtocol, CommandID: id, Checked: time.Now().UTC(), Status: &status, Stdout: chunk(out, off), Stderr: chunk(errout, erroff)}
}

type readBackend struct {
	fakeBackend
	read  func(NodeAction) (wire.CommandSnapshot, error)
	calls int
}

func (b *readBackend) Action(ctx context.Context, task *Task, n *Node, a NodeAction) (json.RawMessage, error) {
	if a.Action != "command_poll" {
		return b.fakeBackend.Action(ctx, task, n, a)
	}
	b.calls++
	v, err := b.read(a)
	return wire.JSON(v), err
}
func commandFixture(t *testing.T, b Backend) (*Server, *Task, *Command) {
	s, task, _ := fixtureLab(t, b, time.Second)
	end := time.Now().UTC().Add(time.Minute)
	c := &Command{ID: wire.ID(), NodeID: task.lab().Nodes[0].ID, CommandStatus: wire.CommandStatus{State: "starting"}, StatusDeadline: &end}
	task.lab().Commands = append(task.lab().Commands, c)
	return s, task, c
}

func TestCommandStatusPendingHasItsOwnDeadlineAndCanRecover(t *testing.T) {
	b := &readBackend{}
	s, task, c := commandFixture(t, b)
	b.read = func(a NodeAction) (wire.CommandSnapshot, error) {
		return wire.CommandSnapshot{Protocol: wire.CommandProtocol, CommandID: c.ID, Checked: time.Now().UTC(), Pending: true}, nil
	}
	s.pollCommandContext(context.Background(), task, c.ID, false)
	if c.Observation.State != "pending" || c.Observation.Failures != 0 || c.State != "starting" {
		t.Fatal(c)
	}
	end := time.Now().UTC().Add(-time.Second)
	c.StatusDeadline = &end
	s.pollCommandContext(context.Background(), task, c.ID, false)
	if c.State != "uncertain" || c.Final || c.Observation.Error.Code != "command_status_missing" || c.ExitCode != nil {
		t.Fatal(c)
	}
	code := 41
	b.read = func(a NodeAction) (wire.CommandSnapshot, error) {
		return snapshotFixture(c.ID, wire.CommandStatus{State: "exited", Final: true, ExitCode: &code}, nil, nil, a.StdoutOffset, a.StderrOffset, 7), nil
	}
	s.pollCommandContext(context.Background(), task, c.ID, false)
	if !c.Final || *c.ExitCode != 41 || c.Observation.State != "available" || c.Polling() || c.Retained.Stdout.State != "complete" {
		t.Fatal(c)
	}
}

func TestSignalExitSurvivesCollectionAndRepeatedReads(t *testing.T) {
	b := &readBackend{}
	s, task, c := commandFixture(t, b)
	if err := s.db.CreateCommand(context.Background(), task, c.ID, wire.CommandSpec{ID: c.ID, Argv: []string{"synthetic"}}); err != nil {
		t.Fatal(err)
	}
	b.read = func(a NodeAction) (wire.CommandSnapshot, error) {
		status := wire.CommandStatus{State: "timed_out", Final: true, ExitSignal: "SIGKILL", Termination: &wire.Termination{Reason: "timeout", ProcessExited: true, GroupStopped: true, Source: "guest_supervisor"}}
		return snapshotFixture(c.ID, status, []byte("before-timeout"), nil, a.StdoutOffset, a.StderrOffset, 4), nil
	}
	for c.Polling() && b.calls < 10 {
		s.pollCommandContext(context.Background(), task, c.ID, false)
	}
	if c.ExitCode != nil || c.ExitSignal != "SIGKILL" || c.State != "timed_out" || c.Polling() || c.Retained.Stdout.State != "complete" {
		t.Fatal(c)
	}
	for range 2 {
		value, err := s.accept(context.Background(), "command_get", wire.JSON(CommandRequest{TaskID: TaskID{task.ID}, CommandID: c.ID}))
		var observed Command
		if err != nil || json.Unmarshal(wire.JSON(value), &observed) != nil || observed.ExitCode != nil || observed.ExitSignal != "SIGKILL" || !observed.Final || !observed.Termination.GroupStopped {
			t.Fatal(value, err)
		}
	}
}

func TestFinalExecutionAndIncrementalBinaryOutputAreIndependent(t *testing.T) {
	b := &readBackend{}
	s, task, c := commandFixture(t, b)
	out := bytes.Repeat([]byte("中文\x00\xff\xfe\n"), 10000)
	errout := []byte("final stderr\n")
	code, transferred := 37, int64(0)
	fail := false
	b.read = func(a NodeAction) (wire.CommandSnapshot, error) {
		if fail {
			return wire.CommandSnapshot{}, context.DeadlineExceeded
		}
		v := snapshotFixture(c.ID, wire.CommandStatus{State: "exited", Final: true, ExitCode: &code}, out, errout, a.StdoutOffset, a.StderrOffset, 4096)
		transferred += v.Stdout.Next - v.Stdout.Offset + v.Stderr.Next - v.Stderr.Offset
		return v, nil
	}
	s.readCommand(context.Background(), task, c.ID, false)
	if !c.Final || *c.ExitCode != 37 || !c.Polling() || c.Retained.Stdout.State != "collecting" {
		t.Fatal(c)
	}
	fail = true
	s.readCommand(context.Background(), task, c.ID, false)
	if !c.Final || *c.ExitCode != 37 || c.Retained.Stdout.Bytes != 4096 || c.Observation.Error == nil {
		t.Fatal(c)
	}
	fail = false
	for c.Polling() && b.calls < 100 {
		s.pollCommandContext(context.Background(), task, c.ID, false)
	}
	if c.Polling() || c.Retained.Stdout.State != "complete" || c.Retained.Stderr.State != "complete" || transferred != int64(len(out)+len(errout)) {
		t.Fatal(c, transferred)
	}
	for name, want := range map[string][]byte{"stdout": out, "stderr": errout} {
		got, err := s.root.Read(filepath.Join("tasks", task.ID, "commands", c.ID, name), 1<<20)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s bytes differ: %v", name, err)
		}
	}
	result, err := s.accept(context.Background(), "command_read", wire.JSON(ReadRequest{CommandRequest: CommandRequest{TaskID: TaskID{task.ID}, CommandID: c.ID}, Stream: "stdout", Offset: int64(len(out)) - 1, Limit: 1}))
	if err != nil || result.(map[string]any)["eof"] != true || result.(map[string]any)["encoding"] != "base64" {
		t.Fatal(result, err)
	}
}

func TestOutputReplayAndIntegrityFailuresKeepExecutionEvidence(t *testing.T) {
	for _, mode := range []string{"replay", "changed_prefix", "chunk_checksum", "final_checksum", "missing_stderr"} {
		t.Run(mode, func(t *testing.T) {
			b := &readBackend{}
			s, task, c := commandFixture(t, b)
			out, code := []byte("retained bytes"), 29
			name := filepath.Join("tasks", task.ID, "commands", c.ID, "stdout")
			prior := out[:4]
			if mode == "changed_prefix" {
				prior = []byte("else")
			}
			if err := s.appendOutput(name, 0, prior); err != nil {
				t.Fatal(err)
			}
			b.read = func(a NodeAction) (wire.CommandSnapshot, error) {
				v := snapshotFixture(c.ID, wire.CommandStatus{State: "exited", Final: true, ExitCode: &code}, out, nil, 0, 0, 1024)
				switch mode {
				case "chunk_checksum":
					v.Stdout.SHA256 = checksum(nil)
				case "final_checksum":
					v.Status.Output.StdoutSHA256 = checksum(nil)
				case "missing_stderr":
					v.Stderr = wire.StreamChunk{State: "unavailable", Error: &wire.Error{Code: "output_missing", Message: "missing fixture"}}
				}
				return v, nil
			}
			s.pollCommandContext(context.Background(), task, c.ID, false)
			if !c.Final || *c.ExitCode != 29 {
				t.Fatal("lost exit evidence", c)
			}
			if mode == "replay" {
				data, err := s.root.Read(name, 100)
				if err != nil || !bytes.Equal(data, out) || c.Retained.Stdout.State != "complete" {
					t.Fatal(string(data), c, err)
				}
			} else if mode == "missing_stderr" {
				if c.Retained.Stdout.State != "complete" || c.Retained.Stderr.Error.Code != "output_missing" {
					t.Fatal(c)
				}
			} else if c.Retained.Stdout.State != "incomplete" || c.Retained.Stdout.Error == nil {
				t.Fatal(c)
			}
			if mode == "changed_prefix" {
				data, _ := s.root.Read(name, 100)
				if !bytes.Equal(data, prior) {
					t.Fatal("changed retained evidence", string(data))
				}
			}
		})
	}
}

func TestFinalOutputCollectionDeadlineDoesNotAlterExit(t *testing.T) {
	b := &readBackend{}
	s, task, c := commandFixture(t, b)
	code := 17
	c.CommandStatus = wire.CommandStatus{State: "timed_out", Final: true, ExitCode: &code}
	end := time.Now().Add(-time.Second)
	c.Retained.Deadline = &end
	s.pollCommandContext(context.Background(), task, c.ID, false)
	if b.calls != 0 || c.Polling() || c.State != "timed_out" || *c.ExitCode != 17 || c.Retained.Stdout.Error.Code != "output_timeout" {
		t.Fatal(c)
	}
}

type completingCommandRunner struct {
	calls   int
	offsets []int64
}

func (r *completingCommandRunner) Run(_ context.Context, _ string, args []string, _ io.Reader) ([]byte, error) {
	if len(args) < 3 || args[0] != "guestcontrol" || args[2] != "run" {
		return nil, fmt.Errorf("unexpected command poll operation")
	}
	value := func(key string) string {
		for i, arg := range args {
			if arg == key && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	if !strings.Contains(strings.Join(args, " "), "-- read --root") {
		return nil, fmt.Errorf("missing bounded read operation")
	}
	off, _ := strconv.ParseInt(value("--stdout-offset"), 10, 64)
	errOff, _ := strconv.ParseInt(value("--stderr-offset"), 10, 64)
	r.offsets = append(r.offsets, off)
	out, errout := []byte("partial output\n"), []byte(nil)
	status := wire.CommandStatus{State: "running"}
	if r.calls > 0 {
		out, errout = append(out, []byte("last stdout bytes\n")...), []byte("last stderr bytes\n")
		code := 0
		status = wire.CommandStatus{State: "exited", Final: true, ExitCode: &code}
	}
	r.calls++
	return wire.JSON(snapshotFixture(value("--id"), status, out, errout, off, errOff, wire.CommandReadLimit)), nil
}

func TestTerminalCommandPollIncludesFinalOutput(t *testing.T) {
	s, task, c := commandFixture(t, &fakeBackend{})
	runner := &completingCommandRunner{}
	b := &VBoxBackend{Config: s.cfg, Runner: runner}
	s.backend = &Backends{VBoxBackend: b}
	if err := os.MkdirAll(b.runtime(task), 0700); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s.pollCommandContext(context.Background(), task, c.ID, false)
	}
	if !c.Final || c.State != "exited" || c.Polling() {
		t.Fatal(c)
	}
	for file, want := range map[string]string{"stdout": "partial output\nlast stdout bytes\n", "stderr": "last stderr bytes\n"} {
		got, err := s.root.Read(filepath.Join("tasks", task.ID, "commands", c.ID, file), 1000)
		if err != nil || string(got) != want {
			t.Fatalf("terminal command lost %s: %q, %v", file, got, err)
		}
	}
	if runner.calls != 2 || runner.offsets[1] != int64(len("partial output\n")) {
		t.Fatal(runner)
	}
}

func BenchmarkExecutionSnapshot(b *testing.B) {
	task := newTask("lab", 60)
	for range 32 {
		task.lab().Commands = append(task.lab().Commands, &Command{ID: wire.ID()})
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = task.executionSnapshot()
	}
}
