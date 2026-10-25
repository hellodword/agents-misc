package control

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func guestTimeout(ctx context.Context) string {
	budget := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(deadline)-time.Second)
	}
	return strconv.FormatInt(max(1, budget.Milliseconds()), 10)
}
func execView(result hostexec.Result, err error) map[string]any {
	state := "completed"
	termination := "exited"
	var exitCode any = 0
	var exitMinimum any
	var failure *wire.Error
	if err != nil {
		state = "uncertain"
		termination, exitCode = "unconfirmed", nil
		failure = wire.Safe(err)
		// VBoxManage guestcontrol run maps guest exits 1..93 to 33..125;
		// 126 confirms a normal exit >=94 without preserving its exact value.
		// Timeout status 20 cannot distinguish a killed from a stuck process.
		if !result.TimedOut {
			switch {
			case result.ExitCode >= 33 && result.ExitCode <= 125:
				state, termination, exitCode, failure = "completed", "exited", result.ExitCode-32, nil
			case result.ExitCode == 126:
				state, termination, exitMinimum, failure = "completed", "exited", 94, nil
			case result.ExitCode == 18 || result.ExitCode == 19:
				state, termination, failure = "completed", "abnormal", nil
			}
		}
	}
	return map[string]any{"state": state, "stdout": base64.StdEncoding.EncodeToString(result.Stdout), "stderr": base64.StdEncoding.EncodeToString(result.Stderr), "stdout_truncated": result.StdoutTruncated, "stderr_truncated": result.StderrTruncated, "backend_exit_code": result.ExitCode, "exit_code": exitCode, "exit_code_min": exitMinimum, "termination": termination, "termination_confirmed": state == "completed", "error": failure}
}

func (b *VBoxBackend) guestCommand(ctx context.Context, t *Task, n *Node, argv []string, cwd string, env map[string]string) (hostexec.Result, error) {
	options := []string{"--exe", argv[0], "--timeout", guestTimeout(ctx), "--wait-stdout", "--wait-stderr"}
	if cwd != "" {
		options = append(options, "--cwd", cwd)
	}
	keys := []string{}
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		options = append(options, "--putenv", key+"="+env[key])
	}
	options = append(options, "--")
	return b.guestCaptured(ctx, t, n, "run", append(options, argv[1:]...)...)
}

func (b *VBoxBackend) guestCaptured(ctx context.Context, t *Task, n *Node, operation string, args ...string) (hostexec.Result, error) {
	started := time.Now()
	value, _ := b.guestSlots.LoadOrStore(n.UUID, make(chan struct{}, 4))
	slots := value.(chan struct{})
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		err := wire.Fail("resource_busy", "Guest Control capacity was occupied for this operation's time budget")
		b.recordCall(started, time.Since(started), "guestcontrol", operation, t.ID, n.ID, hostexec.Result{}, err)
		return hostexec.Result{}, err
	}
	if err := b.admitGuestSession(ctx, t, n); err != nil {
		b.recordCall(started, time.Since(started), "guestcontrol", operation, t.ID, n.ID, hostexec.Result{}, err)
		return hostexec.Result{}, err
	}
	var result hostexec.Result
	defer func() { b.retireGuestSession(n, operation, result) }()
	wait := time.Since(started)
	c := b.guestConfig(t, n)
	stage, err := os.MkdirTemp(b.runtime(t), "auth-")
	if err != nil {
		return hostexec.Result{}, err
	}
	defer os.RemoveAll(stage)
	password := filepath.Join(stage, "password")
	if err = os.WriteFile(password, []byte(c.Password), 0600); err != nil {
		return hostexec.Result{}, err
	}
	argv := []string{"guestcontrol", n.UUID, operation, "--username", c.Username, "--passwordfile", password}
	if c.Domain != "" {
		argv = append(argv, "--domain", c.Domain)
	}
	exe, err := config.Program(b.Config.VirtualBox.Executable, "VBoxManage")
	if err != nil {
		return hostexec.Result{}, err
	}
	result, err = hostexec.Capture(ctx, b.Runner, exe, append(argv, args...), nil)
	b.recordCall(started, wait, "guestcontrol", operation, t.ID, n.ID, result, err)
	result.Stderr = []byte(b.redact(string(result.Stderr)))
	return result, b.executionError("guestcontrol/"+operation, err)
}
