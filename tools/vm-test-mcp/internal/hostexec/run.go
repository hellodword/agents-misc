package hostexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

type Runner interface {
	Run(context.Context, string, []string, io.Reader) ([]byte, error)
}
type Exec struct{}
type Result struct {
	Stdout          []byte
	Stderr          []byte
	ExitCode        int
	TimedOut        bool
	StdoutTruncated bool
	StderrTruncated bool
}
type Failure struct {
	Result
	Cause error
}

func (e *Failure) Error() string {
	return fmt.Sprintf("configured executable failed (exit=%d, timeout=%t)", e.ExitCode, e.TimedOut)
}
func (e *Failure) Unwrap() error { return e.Cause }

func Capture(ctx context.Context, runner Runner, exe string, args []string, input io.Reader) (Result, error) {
	if r, ok := runner.(interface {
		RunResult(context.Context, string, []string, io.Reader) (Result, error)
	}); ok {
		return r.RunResult(ctx, exe, args, input)
	}
	out, err := runner.Run(ctx, exe, args, input)
	var failed *Failure
	if errors.As(err, &failed) {
		return failed.Result, err
	}
	result := Result{Stdout: out}
	if err != nil {
		result.ExitCode = -1
	}
	return result, err
}

type bounded struct {
	buffer    bytes.Buffer
	max       int
	truncated bool
}

// Keep bytes.Buffer's ReaderFrom private: io.Copy must go through Write so
// subprocess pipes cannot bypass the output limit.
func (b *bounded) Len() int      { return b.buffer.Len() }
func (b *bounded) Bytes() []byte { return b.buffer.Bytes() }

func (b *bounded) Write(p []byte) (int, error) {
	n := len(p)
	if n > b.max-b.Len() {
		b.truncated = true
	}
	if b.Len() < b.max {
		b.buffer.Write(p[:min(n, b.max-b.Len())])
	}
	return n, nil
}
func (Exec) Run(ctx context.Context, exe string, args []string, input io.Reader) ([]byte, error) {
	result, err := (Exec{}).RunResult(ctx, exe, args, input)
	return result.Stdout, err
}
func (Exec) RunResult(ctx context.Context, exe string, args []string, input io.Reader) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, exe, args...)
	// Keep machine-readable diagnostics in English without forcing an ASCII
	// character set: VirtualBox decodes guest paths from the host locale.
	c.Env = append(os.Environ(), "LC_ALL=C.UTF-8", "LANGUAGE=C")
	c.Stdin = input
	c.WaitDelay = 2 * time.Second
	out := &bounded{max: 1 << 20}
	errout := &bounded{max: 8192}
	c.Stdout = out
	c.Stderr = errout
	err := c.Run()
	result := Result{Stdout: out.Bytes(), Stderr: errout.Bytes(), ExitCode: -1, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), StdoutTruncated: out.truncated, StderrTruncated: errout.truncated}
	if c.ProcessState != nil {
		result.ExitCode = c.ProcessState.ExitCode()
	}
	if err != nil {
		return result, &Failure{Result: result, Cause: errors.Join(err, ctx.Err())}
	}
	return result, nil
}
