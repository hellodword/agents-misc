package guest

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type faultChild struct {
	exit                               chan struct{}
	release                            chan struct{}
	once                               sync.Once
	code                               int
	signal                             string
	killErr                            error
	keepWaiting, blockKill, blockDrain bool
	onWait                             func()
}

func (p *faultChild) Wait(ctx context.Context) (processExit, error) {
	if p.onWait != nil {
		p.onWait()
	}
	select {
	case <-p.exit:
		if p.signal != "" {
			return processExit{signal: p.signal}, nil
		}
		return exitedWithCode(p.code), nil
	case <-ctx.Done():
		return processExit{}, ctx.Err()
	}
}
func (p *faultChild) Kill(context.Context) error {
	if p.blockKill {
		<-p.release
	}
	if p.killErr != nil {
		return p.killErr
	}
	if !p.keepWaiting {
		p.once.Do(func() { close(p.exit) })
	}
	return nil
}
func (p *faultChild) Drain(context.Context) error {
	if p.blockDrain {
		<-p.release
		return errors.New("delayed output pipe")
	}
	return nil
}
func (p *faultChild) Close() error { return nil }

func TestSupervisorRecordsSignalWithoutInventingExitCode(t *testing.T) {
	p := &faultChild{exit: make(chan struct{}), signal: "SIGKILL"}
	p.once.Do(func() { close(p.exit) })
	status, err, _ := supervisionFixture(t, context.Background(), p, atomic)
	if err != nil || !status.Final || status.State != "exited" || status.ExitCode != nil || status.ExitSignal != "SIGKILL" || !status.Termination.ProcessExited || !status.Termination.GroupStopped || status.Output.State != "complete" {
		t.Fatal(status, err)
	}
}

func supervisionFixture(t *testing.T, ctx context.Context, p *faultChild, write func(string, any) error) (wire.CommandStatus, error, time.Duration) {
	t.Helper()
	root := t.TempDir()
	id := wire.ID()
	request := filepath.Join(root, "request.json")
	if err := os.WriteFile(request, wire.JSON(wire.CommandSpec{ID: id, Argv: []string{"synthetic"}}), 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := supervise(ctx, root, request, func(context.Context, wire.CommandSpec, *os.File, io.Writer, io.Writer) (Child, error) { return p, nil }, supervisionLimits{time.Second, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}, write)
	elapsed := time.Since(start)
	var status wire.CommandStatus
	if e := readJSON(filepath.Join(root, id, "status.json"), &status); e != nil {
		t.Fatal(e)
	}
	return status, err, elapsed
}
func TestSupervisionBoundsWaitKillAndDrain(t *testing.T) {
	for _, name := range []string{"wait", "refused_kill", "blocked_kill", "blocked_drain"} {
		t.Run(name, func(t *testing.T) {
			p := &faultChild{exit: make(chan struct{}), release: make(chan struct{}), code: 17}
			defer close(p.release)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "wait":
				p.keepWaiting = true
				p.onWait = cancel
			case "refused_kill":
				p.killErr = errors.New("kill refused")
				p.once.Do(func() { close(p.exit) })
			case "blocked_kill":
				p.blockKill = true
				p.onWait = cancel
			case "blocked_drain":
				p.blockDrain = true
				p.once.Do(func() { close(p.exit) })
			}
			// Cancellation starts after launch reaches Wait. The normal-exit
			// cases have no competing execution timeout; their independent phase
			// deadlines alone must bound a refused Kill or inherited output pipe.
			status, err, elapsed := supervisionFixture(t, ctx, p, atomic)
			if elapsed > time.Second || !status.Final || status.Stage != "finished" || status.Termination == nil {
				t.Fatal("supervision did not converge", status, err, elapsed)
			}
			switch name {
			case "wait", "blocked_kill":
				if status.State != "uncertain" || status.ExitCode != nil || status.Termination.ProcessExited || status.Termination.Error == nil {
					t.Fatal("invented termination", status)
				}
			case "refused_kill":
				if status.State != "uncertain" || status.ExitCode == nil || *status.ExitCode != 17 || status.Termination.GroupStopped || status.Termination.Error == nil {
					t.Fatal("lost actual exit or kill error", status)
				}
			case "blocked_drain":
				if status.State != "exited" || status.ExitCode == nil || *status.ExitCode != 17 || status.Output.State != "incomplete" || status.Output.Error == nil {
					t.Fatal("output failure changed known exit", status)
				}
			}
		})
	}
}

func TestTimeoutHasIndependentReasonAndActualExit(t *testing.T) {
	p := &faultChild{exit: make(chan struct{}), code: 23}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err, _ := supervisionFixture(t, ctx, p, atomic)
	if err != nil || status.State != "timed_out" || status.ExitCode == nil || *status.ExitCode != 23 || status.Termination.Reason != "timeout" || !status.Termination.ProcessExited || !status.Termination.GroupStopped || status.Output.State != "complete" {
		t.Fatal(status, err)
	}
}

func TestInheritedOutputPipeHasBoundedDrainAndPreservesBytes(t *testing.T) {
	ro, wo, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	re, we, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer wo.Close()
	defer we.Close()
	out, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	streams := drainStreams(ro, re, out, io.Discard)
	defer streams.Close()
	if _, err = wo.Write([]byte("confirmed output")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err = streams.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("inherited writer was treated as drained", err)
	}
	select {
	case <-streams.done:
	case <-time.After(time.Second):
		t.Fatal("closing pipe did not settle readers")
	}
	data, err := os.ReadFile(out.Name())
	if err != nil || string(data) != "confirmed output" {
		t.Fatal(string(data), err)
	}
}

func TestTerminalWriteFailureCanBeReconciledWithoutInventingExit(t *testing.T) {
	root := t.TempDir()
	id := wire.ID()
	request := filepath.Join(root, "request.json")
	if err := os.WriteFile(request, wire.JSON(wire.CommandSpec{ID: id, Argv: []string{"synthetic"}}), 0600); err != nil {
		t.Fatal(err)
	}
	p := &faultChild{exit: make(chan struct{}), code: 29}
	p.once.Do(func() { close(p.exit) })
	err := supervise(context.Background(), root, request, func(context.Context, wire.CommandSpec, *os.File, io.Writer, io.Writer) (Child, error) { return p, nil }, commandLimits, func(file string, value any) error {
		if value.(wire.CommandStatus).Final {
			return os.ErrPermission
		}
		return atomic(file, value)
	})
	if !errors.Is(err, os.ErrPermission) {
		t.Fatal("terminal write failure was hidden", err)
	}
	if err = Reconcile(root, id, err); err != nil {
		t.Fatal(err)
	}
	var status wire.CommandStatus
	if err = readJSON(filepath.Join(root, id, "status.json"), &status); err != nil || !status.Final || status.State != "uncertain" || status.ExitCode == nil || *status.ExitCode != 29 || status.Error == "" {
		t.Fatal(status, err)
	}
	before := string(wire.JSON(status))
	if err = Reconcile(root, id, errors.New("later observer")); err != nil {
		t.Fatal(err)
	}
	if err = readJSON(filepath.Join(root, id, "status.json"), &status); err != nil || string(wire.JSON(status)) != before {
		t.Fatal("final evidence overwritten", status, err)
	}
}

func TestLateStartupIsBoundedAndCannotLeaveAnUnownedChild(t *testing.T) {
	root := t.TempDir()
	id := wire.ID()
	request := filepath.Join(root, "request.json")
	if err := os.WriteFile(request, wire.JSON(wire.CommandSpec{ID: id, Argv: []string{"synthetic"}}), 0600); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	p := &faultChild{exit: make(chan struct{}), code: 0}
	start := time.Now()
	err := supervise(context.Background(), root, request, func(context.Context, wire.CommandSpec, *os.File, io.Writer, io.Writer) (Child, error) {
		<-release
		return p, nil
	}, supervisionLimits{30 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}, atomic)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("startup did not converge", err)
	}
	select {
	case <-p.exit:
	case <-time.After(time.Second):
		t.Fatal("late child escaped its cancelled launch")
	}
	var status wire.CommandStatus
	if err = readJSON(filepath.Join(root, id, "status.json"), &status); err != nil || !status.Final || status.State != "uncertain" || status.ExitCode != nil {
		t.Fatal(status, err)
	}
}
