package guest

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	syncatomic "sync/atomic"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func readJSON(file string, v any) error {
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, wire.MaxJSON+1))
	if e != nil {
		return e
	}
	return wire.Decode(b, v)
}
func atomic(file string, v any) error {
	name := file + "." + wire.ID() + ".partial"
	f, e := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer os.Remove(name)
	_, e = f.Write(wire.JSON(v))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	// Windows Guest Control may briefly open the old status without delete sharing.
	for i := 0; i < 20; i++ {
		if e = os.Rename(name, file); e == nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return e
}

type Child interface {
	Wait(context.Context) (processExit, error)
	Kill(context.Context) error
	Drain(context.Context) error
	Close() error
}

// A signal is an observed exit, but has no process exit code. Keep those two
// outcomes distinct from a failed or interrupted wait.
type processExit struct {
	code       *int
	signal     string
	coreDumped bool
}

func exitedWithCode(code int) processExit { return processExit{code: &code} }
func (e processExit) confirmed() bool     { return e.code != nil || e.signal != "" }

type launchFunc func(context.Context, wire.CommandSpec, *os.File, io.Writer, io.Writer) (Child, error)
type limitWriter struct {
	f         *os.File
	n         syncatomic.Int64
	truncated syncatomic.Bool
	hash      hash.Hash
}

func (w *limitWriter) Write(b []byte) (int, error) {
	n := len(b)
	remaining := max(int64(0), wire.CommandOutputLimit-w.n.Load())
	if int64(n) > remaining {
		w.truncated.Store(true)
	}
	if remaining > 0 {
		written, err := w.f.Write(b[:min(int64(n), remaining)])
		w.n.Add(int64(written))
		_, _ = w.hash.Write(b[:written])
		if err != nil {
			return written, err
		}
	}
	return n, nil
}

type supervisionLimits struct{ start, stop, wait, drain time.Duration }

var commandLimits = supervisionLimits{30 * time.Second, 5 * time.Second, 5 * time.Second, 2 * time.Second}

func Run(ctx context.Context, root, request string) error     { return submit(ctx, root, request) }
func Execute(ctx context.Context, root, request string) error { return run(ctx, root, request, launch) }
func run(ctx context.Context, root, request string, start launchFunc) error {
	return supervise(ctx, root, request, start, commandLimits, atomic)
}

// Each backend phase receives its own bounded context. The outer timer also
// bounds a malfunctioning adapter; such a result remains unconfirmed.
func boundedPhase(budget time.Duration, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- fn(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func phaseFailure(phase string, err error) *wire.Error {
	if err == nil {
		return nil
	}
	return &wire.Error{Code: "supervision_failed", Message: "Command supervision could not confirm a phase", Details: map[string]any{"stage": phase, "cause": wire.Safe(err)}}
}

type inputProgress struct {
	next   syncatomic.Int64
	closed syncatomic.Bool
}

func feedInput(ctx context.Context, dir string, stdin *os.File, progress *inputProgress, updates chan<- struct{}) error {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var inputBytes int64
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		seq := int(progress.next.Load())
		var packet wire.Input
		if err := readJSON(filepath.Join(dir, fmt.Sprintf("input-%08d.json", seq)), &packet); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if packet.Sequence != seq {
			return wire.Fail("input_order", "Guest input sequence does not match its queue")
		}
		inputBytes += int64(len(packet.Data))
		if seq >= wire.InputPacketCount || len(packet.Data) > wire.InputPacketLimit || inputBytes > wire.CommandInputLimit {
			return wire.Fail("input_limit", "Guest stdin exceeded its packet or byte budget")
		}
		if _, err := io.WriteString(stdin, packet.Data); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if packet.EOF {
			_ = stdin.Close()
		}
		progress.next.Add(1)
		progress.closed.Store(packet.EOF)
		select {
		case updates <- struct{}{}:
		default:
		}
		if packet.EOF {
			return nil
		}
	}
}

func supervise(parent context.Context, root, request string, start launchFunc, limits supervisionLimits, write func(string, any) error) error {
	var spec wire.CommandSpec
	if err := readJSON(request, &spec); err != nil {
		return err
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	dir := filepath.Join(root, spec.ID)
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	status := wire.CommandStatus{State: "starting", Stage: "starting", Output: &wire.CommandOutput{State: "open"}}
	identity, _, identityErr := processIdentity(os.Getpid())
	if identityErr == nil {
		status.Supervisor = &identity
	}
	statusPath := filepath.Join(dir, "status.json")
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	if spec.TimeoutSeconds > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, time.Duration(spec.TimeoutSeconds)*time.Second)
		defer stop()
	}
	if d, ok := ctx.Deadline(); ok {
		d = d.UTC()
		status.Deadline = &d
	}
	persist := func() error { now := time.Now().UTC(); status.Updated = &now; return write(statusPath, status) }
	if err := persist(); err != nil {
		return err
	}
	failStart := func(err error) error {
		status.State, status.Stage, status.Final, status.Error = "failed", "finished", true, "Could not start supervised process"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			status.State, status.Error = "uncertain", "Process startup could not be confirmed"
		}
		status.Output.State = "incomplete"
		status.Termination = &wire.Termination{Reason: "start_failed", Requested: time.Now().UTC(), Source: "guest_supervisor", Error: phaseFailure("start", err)}
		now := time.Now().UTC()
		status.Finished = &now
		return errors.Join(err, persist())
	}
	if identityErr != nil {
		return failStart(identityErr)
	}
	var environmentErr error
	spec, status.Execution, environmentErr = commandEnvironment(spec)
	if environmentErr != nil {
		return failStart(environmentErr)
	}
	if err := persist(); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(dir, "stdout"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return failStart(err)
	}
	defer out.Close()
	errout, err := os.OpenFile(filepath.Join(dir, "stderr"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return failStart(err)
	}
	defer errout.Close()
	stdout, stderr := &limitWriter{f: out, hash: sha256.New()}, &limitWriter{f: errout, hash: sha256.New()}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return failStart(err)
	}
	defer stdinR.Close()
	defer stdinW.Close()
	startCtx, stopStart := context.WithTimeout(ctx, limits.start)
	defer stopStart()
	type launchResult struct {
		child Child
		err   error
	}
	launched := make(chan launchResult)
	abandoned := make(chan struct{})
	go func() {
		child, err := start(startCtx, spec, stdinR, stdout, stderr)
		select {
		case launched <- launchResult{child, err}:
		case <-abandoned:
			if child != nil {
				_ = boundedPhase(limits.stop, child.Kill)
				_ = child.Close()
			}
		}
	}()
	var child Child
	select {
	case result := <-launched:
		child, err = result.child, result.err
	case <-startCtx.Done():
		close(abandoned)
		return failStart(startCtx.Err())
	}
	if err != nil {
		if child != nil {
			_ = boundedPhase(limits.stop, child.Kill)
			_ = child.Close()
		}
		return failStart(err)
	}
	if child == nil {
		return failStart(wire.Fail("backend_protocol_error", "Process adapter returned no child"))
	}
	defer child.Close()
	type exitResult struct {
		processExit
		err error
	}
	exit := make(chan exitResult, 1)
	waitCtx, stopWait := context.WithCancel(context.Background())
	defer stopWait()
	go func() { result, e := child.Wait(waitCtx); exit <- exitResult{result, e} }()
	inputCtx, stopInput := context.WithCancel(ctx)
	defer stopInput()
	progress := &inputProgress{}
	updates := make(chan struct{}, 1)
	inputDone := make(chan error, 1)
	go func() { inputDone <- feedInput(inputCtx, dir, stdinW, progress, updates) }()
	readProgress := func() { status.WrittenInput = int(progress.next.Load()); status.InputClosed = progress.closed.Load() }
	began := time.Now().UTC()
	status.Started = &began
	status.State, status.Stage = "running", "running"
	reason := "exited"
	var failure error
	var result *exitResult
	inputFinished := false
	if err = persist(); err != nil {
		reason, failure = "status_write_failed", err
	} else {
		tick := time.NewTicker(50 * time.Millisecond)
	waiting:
		for {
			select {
			case r := <-exit:
				result = &r
				break waiting
			case <-ctx.Done():
				reason = "supervisor_cancelled"
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					reason = "timeout"
				}
				break waiting
			case err := <-inputDone:
				inputFinished = true
				if err != nil {
					reason, failure = "input_failed", err
					break waiting
				}
			case <-updates:
				readProgress()
				if err = persist(); err != nil {
					reason, failure = "status_write_failed", err
					break waiting
				}
			case <-tick.C:
				if _, err := os.Stat(filepath.Join(dir, "cancel")); err == nil {
					reason = "cancelled"
					break waiting
				}
			}
		}
		tick.Stop()
	}
	stopInput()
	_ = stdinR.Close()
	_ = stdinW.Close()
	status.Stage = "stopping"
	status.Termination = &wire.Termination{Reason: reason, Requested: time.Now().UTC(), Source: "guest_supervisor"}
	_ = persist() // A terminal write is retried after process and stream settlement.
	killErr := boundedPhase(limits.stop, child.Kill)
	status.Termination.GroupStopped = killErr == nil
	if killErr == nil {
		status.Termination.RequestDelivered = new(true)
	} else {
		var known *wire.Error
		if errors.As(killErr, &known) {
			if delivered, ok := known.Details["request_delivered"].(bool); ok {
				status.Termination.RequestDelivered = &delivered
			}
		}
	}
	if killErr != nil {
		status.Termination.Error = phaseFailure("terminate", killErr)
	}
	if result == nil {
		timer := time.NewTimer(limits.wait)
		select {
		case r := <-exit:
			result = &r
		case <-timer.C:
		}
		timer.Stop()
	}
	stopWait()
	if result != nil && result.confirmed() {
		status.ExitCode = result.code
		status.ExitSignal, status.CoreDumped = result.signal, result.coreDumped
		status.Termination.ProcessExited = true
	} else if status.Termination.Error == nil {
		cause := error(context.DeadlineExceeded)
		if result != nil {
			cause = result.err
		}
		status.Termination.Error = phaseFailure("wait", cause)
	}
	status.Stage = "draining"
	_ = persist()
	drainErr := boundedPhase(limits.drain, child.Drain)
	if !inputFinished {
		timer := time.NewTimer(limits.drain)
		select {
		case <-inputDone:
		case <-timer.C:
			drainErr = errors.Join(drainErr, wire.Fail("input_unsettled", "Guest input reader did not settle"))
		}
		timer.Stop()
	}
	drainErr = errors.Join(drainErr, out.Sync(), errout.Sync())
	readProgress()
	status.Output = &wire.CommandOutput{State: "complete", StdoutBytes: stdout.n.Load(), StderrBytes: stderr.n.Load(), StdoutTruncated: stdout.truncated.Load(), StderrTruncated: stderr.truncated.Load()}
	if drainErr != nil {
		status.Output.State = "incomplete"
		status.Output.Error = phaseFailure("drain", drainErr)
	} else {
		status.Output.StdoutSHA256 = hex.EncodeToString(stdout.hash.Sum(nil))
		status.Output.StderrSHA256 = hex.EncodeToString(stderr.hash.Sum(nil))
	}
	status.State = "exited"
	if !status.Termination.ProcessExited || !status.Termination.GroupStopped {
		status.State, status.Error = "uncertain", "Process termination could not be fully confirmed"
	} else {
		switch reason {
		case "timeout":
			status.State = "timed_out"
		case "cancelled", "supervisor_cancelled":
			status.State = "cancelled"
		case "input_failed", "status_write_failed":
			status.State, status.Error = "failed", "Command supervision failed"
		}
	}
	if failure != nil && status.Termination.Error == nil {
		status.Termination.Error = phaseFailure(reason, failure)
	}
	now := time.Now().UTC()
	status.Final, status.Stage, status.Finished = true, "finished", &now
	return errors.Join(failure, killErr, drainErr, persist())
}
func Input(root, id, request string) error {
	if !wire.IDPattern.MatchString(id) {
		return wire.Fail("invalid_argument", "Invalid command ID")
	}
	var p wire.Input
	if e := readJSON(request, &p); e != nil {
		return e
	}
	if p.Sequence < 0 || p.Sequence >= wire.InputPacketCount || len(p.Data) > wire.InputPacketLimit {
		return wire.Fail("invalid_argument", "Invalid input")
	}
	dir := filepath.Join(root, id)
	file := filepath.Join(dir, fmt.Sprintf("input-%08d.json", p.Sequence))
	var old wire.Input
	if err := readJSON(file, &old); err == nil {
		if old != p {
			return wire.Fail("input_conflict", "Sequence already has different content")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	var status wire.CommandStatus
	if e := readJSON(filepath.Join(dir, "status.json"), &status); e != nil {
		return e
	}
	if status.State != "running" && status.State != "starting" {
		return wire.Fail("invalid_state", "Command is not running")
	}
	data := wire.JSON(p)
	temporary := file + "." + wire.ID() + ".partial"
	defer os.Remove(temporary)
	f, e := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	// Publish only a complete, durable packet. Link is atomic and never replaces
	// an existing sequence, on both NTFS and Linux filesystems.
	e = os.Link(temporary, file)
	if os.IsExist(e) {
		var old wire.Input
		if e = readJSON(file, &old); e != nil {
			return e
		}
		if old != p {
			return wire.Fail("input_conflict", "Sequence already has different content")
		}
		return nil
	}
	if e != nil {
		return e
	}
	return e
}
func Cancel(root, id string) error {
	if !wire.IDPattern.MatchString(id) {
		return wire.Fail("invalid_argument", "Invalid command ID")
	}
	f, e := os.OpenFile(filepath.Join(root, id, "cancel"), os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	e = f.Sync()
	f.Close()
	return e
}
func Probe(roots ...string) map[string]any {
	host, _ := os.Hostname()
	build := wire.Identity()
	v := map[string]any{"version": wire.Version, "hostname": host, "build": build, "sha256": build.SHA256}
	root := DefaultRoot
	if len(roots) > 0 && roots[0] != "" {
		root = roots[0]
	}
	privileged, prepared := readiness(root)
	v["privileged"], v["prepared"] = privileged, prepared
	if boot, err := bootIdentity(); err == nil {
		v["boot_id"] = boot
	}
	v["network"] = observeNetwork()
	v["command_protocol"] = wire.CommandProtocol
	v["command_service"] = commandServiceObservation(root)
	v["command_environment"] = environmentContract()
	return v
}

// Archive runs inside the disposable guest. Host code stores the resulting tar
// as opaque data and never extracts its paths or follows its symlinks.
func Archive(source, output string) error {
	base, e := filepath.Abs(source)
	if e != nil {
		return e
	}
	out, e := filepath.Abs(output)
	if e != nil {
		return e
	}
	if out == base || strings.HasPrefix(out, base+string(filepath.Separator)) {
		return errors.New("archive output is inside input")
	}
	f, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	defer tw.Close()
	count := 0
	e = filepath.WalkDir(base, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		count++
		if count > 100000 {
			return fmt.Errorf("archive entry limit")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, e = os.Readlink(p)
			if e != nil {
				return e
			}
		}
		h, e := tar.FileInfoHeader(info, link)
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(filepath.Dir(base), p)
		if e != nil {
			return e
		}
		h.Name = filepath.ToSlash(relative)
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if info.Mode().IsRegular() {
			r, e := os.Open(p)
			if e != nil {
				return e
			}
			_, e = io.Copy(tw, r)
			r.Close()
			return e
		}
		return nil
	})
	if e != nil {
		return e
	}
	if e = tw.Close(); e != nil {
		return e
	}
	return f.Sync()
}
