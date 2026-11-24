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
	ObservedStdout  int64
	ExitCode        int
	TimedOut        bool
	StdoutTruncated bool
	StderrTruncated bool
	ExecutionUS     int64
	UserUS          *int64
	SystemUS        *int64
	ProcessID       int
	Reaped          bool
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
	start := time.Now()
	out, err := runner.Run(ctx, exe, args, input)
	var failed *Failure
	if errors.As(err, &failed) {
		return failed.Result, err
	}
	result := Result{Stdout: out, ExecutionUS: time.Since(start).Microseconds()}
	if err != nil {
		result.ExitCode = -1
	}
	return result, err
}

type bounded struct {
	buffer    bytes.Buffer
	max       int
	observed  int64
	truncated bool
}

// Keep bytes.Buffer's ReaderFrom private: io.Copy must go through Write so
// subprocess pipes cannot bypass the output limit.
func (b *bounded) Len() int      { return b.buffer.Len() }
func (b *bounded) Bytes() []byte { return b.buffer.Bytes() }

func (b *bounded) Write(p []byte) (int, error) {
	n := len(p)
	b.observed += int64(n)
	if n > b.max-b.Len() {
		b.truncated = true
	}
	if b.Len() < b.max {
		b.buffer.Write(p[:min(n, b.max-b.Len())])
	}
	return n, nil
}

type headTail struct {
	head     []byte
	tail     []byte
	headMax  int
	tailMax  int
	observed int64
}

func (w *headTail) Write(p []byte) (int, error) {
	total := len(p)
	w.observed += int64(total)
	if len(w.head) < w.headMax {
		take := min(len(p), w.headMax-len(w.head))
		w.head = append(w.head, p[:take]...)
		p = p[take:]
	}
	if len(p) == 0 || w.tailMax <= 0 {
		return total, nil
	}
	if len(p) >= w.tailMax {
		w.tail = append(w.tail[:0], p[len(p)-w.tailMax:]...)
		return total, nil
	}
	if overflow := len(w.tail) + len(p) - w.tailMax; overflow > 0 {
		copy(w.tail, w.tail[overflow:])
		w.tail = w.tail[:len(w.tail)-overflow]
	}
	w.tail = append(w.tail, p...)
	return total, nil
}

func (w *headTail) Finalize() ([]byte, bool) {
	limit := int64(w.headMax + w.tailMax)
	if w.observed <= limit {
		out := make([]byte, 0, len(w.head)+len(w.tail))
		out = append(out, w.head...)
		out = append(out, w.tail...)
		return out, false
	}
	tailKeep := w.tailMax
	marker := fmt.Sprintf("\n... [vm-test-mcp omitted %d middle bytes] ...\n", w.observed-int64(len(w.head)+tailKeep))
	if len(marker) < w.tailMax {
		tailKeep = w.tailMax - len(marker)
		marker = fmt.Sprintf("\n... [vm-test-mcp omitted %d middle bytes] ...\n", w.observed-int64(len(w.head)+tailKeep))
		if len(w.head)+len(marker)+tailKeep > w.headMax+w.tailMax {
			tailKeep = max(0, w.headMax+w.tailMax-len(w.head)-len(marker))
			marker = fmt.Sprintf("\n... [vm-test-mcp omitted %d middle bytes] ...\n", w.observed-int64(len(w.head)+tailKeep))
		}
	}
	tailSlice := w.tail
	if len(tailSlice) > tailKeep {
		tailSlice = tailSlice[len(tailSlice)-tailKeep:]
	}
	out := make([]byte, 0, len(w.head)+len(marker)+len(tailSlice))
	out = append(out, w.head...)
	out = append(out, marker...)
	out = append(out, tailSlice...)
	return out, true
}

func CaptureHeadTail(ctx context.Context, runner Runner, exe string, args []string, headMax, tailMax int) (Result, error) {
	if _, ok := runner.(Exec); ok {
		return runExec(ctx, exe, args, nil, &headTail{headMax: headMax, tailMax: tailMax})
	}
	res, err := Capture(ctx, runner, exe, args, nil)
	ht := &headTail{headMax: headMax, tailMax: tailMax}
	_, _ = ht.Write(res.Stdout)
	if res.ObservedStdout > ht.observed {
		ht.observed = res.ObservedStdout
	}
	out, truncated := ht.Finalize()
	res.ObservedStdout = ht.observed
	res.Stdout = out
	res.StdoutTruncated = res.StdoutTruncated || truncated
	return res, err
}

func (Exec) Run(ctx context.Context, exe string, args []string, input io.Reader) ([]byte, error) {
	result, err := (Exec{}).RunResult(ctx, exe, args, input)
	return result.Stdout, err
}
func (Exec) RunResult(ctx context.Context, exe string, args []string, input io.Reader) (Result, error) {
	return runExec(ctx, exe, args, input, nil)
}

func runExec(ctx context.Context, exe string, args []string, input io.Reader, ht *headTail) (Result, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, exe, args...)
	// Keep machine-readable diagnostics in English without forcing an ASCII
	// character set: VirtualBox decodes guest paths from the host locale.
	c.Env = append(os.Environ(), "LC_ALL=C.UTF-8", "LANGUAGE=C")
	c.Stdin = input
	c.WaitDelay = 2 * time.Second
	var out *bounded
	if ht != nil {
		c.Stdout = ht
	} else {
		out = &bounded{max: 1 << 20}
		c.Stdout = out
	}
	errout := &bounded{max: 8192}
	c.Stderr = errout
	err := c.Run()
	var stdoutBytes []byte
	var observedStdout int64
	var stdoutTruncated bool
	if ht != nil {
		stdoutBytes, stdoutTruncated = ht.Finalize()
		observedStdout = ht.observed
	} else {
		stdoutBytes, stdoutTruncated, observedStdout = out.Bytes(), out.truncated, out.observed
	}
	result := Result{Stdout: stdoutBytes, Stderr: errout.Bytes(), ObservedStdout: observedStdout, ExitCode: -1, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), StdoutTruncated: stdoutTruncated, StderrTruncated: errout.truncated, ExecutionUS: time.Since(started).Microseconds()}
	if c.ProcessState != nil {
		result.ProcessID, result.Reaped = c.ProcessState.Pid(), true
		result.ExitCode = c.ProcessState.ExitCode()
		user, system := c.ProcessState.UserTime().Microseconds(), c.ProcessState.SystemTime().Microseconds()
		result.UserUS, result.SystemUS = &user, &system
	}
	if err != nil {
		return result, &Failure{Result: result, Cause: errors.Join(err, ctx.Err())}
	}
	return result, nil
}
