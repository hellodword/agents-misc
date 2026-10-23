//go:build linux

package guest

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type linuxChild struct {
	cmd   *exec.Cmd
	unit  string
	stdin *os.File
	done  chan struct{}
	exit  int
	err   error
}

func launch(ctx context.Context, s wire.CommandSpec, in io.Reader, out, errout io.Writer) (Child, error) {
	privileged, _ := readiness("")
	if !privileged {
		return nil, fmt.Errorf("command execution requires root, systemd and cgroup v2")
	}
	exe, err := commandExecutable(s.Argv[0], s.Cwd, s.Env)
	if err != nil {
		return nil, err
	}
	unit := "vm-test-" + s.ID
	seconds := s.TimeoutSeconds
	if seconds == 0 {
		seconds = 30 * 86400
	}
	args := []string{"--quiet", "--wait", "--pipe", "--collect", "--unit", unit, "--property", "Type=exec", "--property", "KillMode=control-group", "--property", "TimeoutStopSec=5s", "--property", "RuntimeMaxSec=" + strconv.Itoa(seconds)}
	if s.Cwd != "" {
		args = append(args, "--working-directory", s.Cwd)
	}
	keys := []string{}
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--setenv", k+"="+s.Env[k])
	}
	args = append(args, "--")
	args = append(args, exe)
	args = append(args, s.Argv[1:]...)
	c := exec.Command("systemd-run", args...)
	r, w, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	defer r.Close()
	c.Stdin = r
	c.Stdout = out
	c.Stderr = errout
	c.WaitDelay = 5 * time.Second
	if e := c.Start(); e != nil {
		w.Close()
		return nil, e
	}
	p := &linuxChild{cmd: c, unit: unit, stdin: w, done: make(chan struct{}), exit: -1}
	go func() { io.Copy(w, in); w.Close() }()
	go func() {
		p.err = c.Wait()
		if c.ProcessState != nil {
			p.exit = c.ProcessState.ExitCode()
		}
		w.Close()
		close(p.done)
	}()
	return p, nil
}
func (p *linuxChild) Wait() (int, error) {
	<-p.done
	return p.exit, p.err
}
func (p *linuxChild) Kill() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Cancellation can race with transient-unit creation. A missing unit is
	// retried while systemd-run is alive, so its eventual child cannot escape.
	for {
		select {
		case <-p.done:
			return nil
		default:
		}
		e := exec.CommandContext(ctx, "systemctl", "stop", p.unit).Run()
		if e == nil {
			return nil
		}
		select {
		case <-p.done:
			return nil
		case <-ctx.Done():
			_ = p.cmd.Process.Kill()
			last, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = exec.CommandContext(last, "systemctl", "stop", p.unit).Run()
			return e
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (p *linuxChild) Close() error { return p.stdin.Close() }
func Reboot() error                { return exec.Command("systemctl", "reboot").Start() }
