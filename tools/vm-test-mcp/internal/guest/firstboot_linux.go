//go:build linux

package guest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/unix"
)

const firstbootBudget = 90 * time.Second
const firstbootStepBudget = 30 * time.Second

func firstbootUnit(root string) string {
	return "[Unit]\nDescription=Initialize VM test clone identity\nBefore=vboxadd-service.service virtualbox-guest-utils.service vboxservice.service ssh.service sshd.service\nConditionPathExists=" + root + "/firstboot-pending\n[Service]\nType=oneshot\nTimeoutStartSec=100s\nTimeoutStopSec=5s\nKillMode=control-group\nExecStart=" + installPath + " firstboot --root \"" + root + "\"\n[Install]\nWantedBy=multi-user.target\n"
}

type firstbootResult struct {
	Stage      string      `json:"stage"`
	Started    time.Time   `json:"started"`
	BudgetMS   int64       `json:"budget_ms"`
	ElapsedMS  int64       `json:"elapsed_ms"`
	ExitCode   *int        `json:"exit_code,omitempty"`
	ExitSignal string      `json:"exit_signal,omitempty"`
	CoreDumped bool        `json:"core_dumped,omitempty"`
	TimedOut   bool        `json:"timed_out"`
	Cancelled  bool        `json:"cancelled"`
	Stderr     string      `json:"stderr"`
	Truncated  bool        `json:"stderr_truncated"`
	State      string      `json:"state"`
	Error      *wire.Error `json:"error,omitempty"`
}
type firstbootRunner func(context.Context, string, []string) (firstbootResult, error)

type firstbootOperations struct {
	hostname func(context.Context, string) error
	run      firstbootRunner
	lookup   func(string) (string, error)
}

// Journald retains step diagnostics, including failures before Guest Control can
// connect. There is no second readiness/status journal to reconcile on startup.
func FirstBoot(parent context.Context, root string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("firstboot requires root")
	}
	ops := firstbootOperations{
		hostname: func(ctx context.Context, name string) error {
			return installHostname(ctx, "/etc/hostname", name, syscall.Sethostname)
		},
		run: firstbootCommand, lookup: exec.LookPath,
	}
	return initializeClone(parent, root, ops, os.Stderr, firstbootBudget, firstbootStepBudget)
}

func initializeClone(parent context.Context, root string, ops firstbootOperations, log io.Writer, total, perStep time.Duration) error {
	ctx, stop := context.WithTimeout(parent, total)
	defer stop()
	if _, err := os.Stat(filepath.Join(root, "firstboot-pending")); err != nil {
		return err
	}
	step := func(stage string, action func(context.Context) (firstbootResult, error)) error {
		call, cancel := context.WithTimeout(ctx, perStep)
		defer cancel()
		started := time.Now().UTC()
		deadline, _ := call.Deadline()
		result, err := action(call)
		err = errors.Join(err, call.Err())
		result.Stage, result.Started, result.BudgetMS, result.ElapsedMS = stage, started, max(0, deadline.Sub(started).Milliseconds()), time.Since(started).Milliseconds()
		result.TimedOut, result.Cancelled = errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled)
		result.State = "complete"
		if err != nil {
			result.State = "failed"
			result.Error = wire.Safe(err)
		}
		// Each runner result caps stderr. Firstboot contains no credentials.
		fmt.Fprintln(log, string(wire.JSON(result)))
		if err != nil {
			return &wire.Error{Code: "firstboot_failed", Message: "Clone initialization failed at " + stage, Details: map[string]any{"step": result, "cause": wire.Safe(err)}}
		}
		return nil
	}
	if err := step("hostname", func(call context.Context) (firstbootResult, error) {
		return firstbootResult{}, ops.hostname(call, "vmtest-"+wire.ID()[:12])
	}); err != nil {
		return err
	}
	if _, err := ops.lookup("ssh-keygen"); err == nil {
		if err = step("ssh_host_keys", func(call context.Context) (firstbootResult, error) {
			return ops.run(call, "ssh-keygen", []string{"-A"})
		}); err != nil {
			return err
		}
	} else if errors.Is(err, exec.ErrNotFound) {
		fmt.Fprintln(log, string(wire.JSON(map[string]any{"stage": "ssh_host_keys", "state": "not_installed"})))
	} else {
		return &wire.Error{Code: "firstboot_failed", Message: "Cannot locate SSH host key generator", Details: map[string]any{"stage": "ssh_host_keys", "cause": wire.Safe(err)}}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Keep pending until the durable marker is installed. A crash in between is
	// retryable, and readiness never accepts prepared+pending as complete.
	if err := atomic(filepath.Join(root, "prepared"), map[string]any{"version": wire.Version, "initialized": time.Now().UTC()}); err != nil {
		return err
	}
	dir, err := os.Open(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(root, "firstboot-pending")); err != nil {
		return err
	}
	if err = dir.Sync(); err != nil {
		// A failed completion write must not make the next probe report ready.
		pendingErr := os.WriteFile(filepath.Join(root, "firstboot-pending"), []byte("pending\n"), 0600)
		markerErr := os.Remove(filepath.Join(root, "prepared"))
		return errors.Join(err, pendingErr, markerErr)
	}
	fmt.Fprintln(log, string(wire.JSON(map[string]any{"stage": "ready", "state": "complete", "version": wire.Version})))
	return nil
}

// Persist the static name and set the kernel name directly. Early identity
// initialization must not wait for a D-Bus activated hostname service.
func installHostname(ctx context.Context, path, name string, apply func([]byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".vm-test-hostname-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0644); err == nil {
		_, err = f.WriteString(name + "\n")
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close(), ctx.Err())
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = errors.Join(dir.Sync(), dir.Close(), ctx.Err())
	if err != nil {
		return err
	}
	return apply([]byte(name))
}

type firstbootStderr struct {
	data      []byte
	truncated bool
}

func (b *firstbootStderr) Write(p []byte) (int, error) {
	n := len(p)
	remaining := max(0, 8192-len(b.data))
	if n > remaining {
		b.truncated = true
	}
	b.data = append(b.data, p[:min(n, remaining)]...)
	return n, nil
}
func firstbootCommand(ctx context.Context, name string, args []string) (firstbootResult, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	c.WaitDelay = time.Second
	stderr := &firstbootStderr{}
	c.Stderr = stderr
	err := c.Run()
	result := firstbootResult{Stderr: string(stderr.data), Truncated: stderr.truncated, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), Cancelled: errors.Is(ctx.Err(), context.Canceled)}
	if c.ProcessState != nil {
		status := c.ProcessState.Sys().(syscall.WaitStatus)
		if status.Exited() {
			code := status.ExitStatus()
			result.ExitCode = &code
		} else if status.Signaled() {
			result.ExitSignal, result.CoreDumped = unix.SignalName(status.Signal()), status.CoreDump()
			if result.ExitSignal == "" {
				result.ExitSignal = fmt.Sprintf("SIG%d", status.Signal())
			}
		}
	}
	return result, errors.Join(err, ctx.Err())
}
