//go:build linux

package guest

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/unix"
)

type linuxChild struct {
	group   *os.Root
	parent  *os.Root
	done    chan struct{}
	exit    processExit
	err     error
	streams *outputStreams
}

func launch(ctx context.Context, s wire.CommandSpec, in *os.File, out, errout io.Writer) (Child, error) {
	privileged, _ := readiness("")
	if !privileged {
		return nil, fmt.Errorf("command execution requires root, systemd and cgroup v2")
	}
	exe, err := commandExecutable(s.Argv[0], s.Cwd, s.Env)
	if err != nil {
		return nil, err
	}
	parent, group, fd, err := commandCgroup(s.ID)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	started := false
	defer func() {
		if !started {
			group.Close()
			_ = parent.Remove("command")
			parent.Close()
		}
	}()
	c := exec.Command(exe, s.Argv[1:]...)
	c.Dir = s.Cwd
	// The supervisor is a system service. Preserve that service environment and
	// apply the resolved account defaults and explicit command overrides.
	c.Env = os.Environ()
	keys := []string{}
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.Env = append(c.Env, k+"="+s.Env[k])
	}
	// clone3 places the child in its cgroup before it can execute or fork. The
	// supervisor stays in the sibling subgroup managed by DelegateSubgroup=.
	c.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(fd.Fd())}
	c.Stdin = in
	ro, wo, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	defer wo.Close()
	re, we, e := os.Pipe()
	if e != nil {
		ro.Close()
		return nil, e
	}
	defer we.Close()
	c.Stdout, c.Stderr = wo, we
	if err := ctx.Err(); err != nil {
		ro.Close()
		re.Close()
		return nil, err
	}
	if e := c.Start(); e != nil {
		ro.Close()
		re.Close()
		return nil, e
	}
	p := &linuxChild{parent: parent, group: group, done: make(chan struct{})}
	p.streams = drainStreams(ro, re, out, errout)
	go func() {
		p.err = c.Wait()
		if c.ProcessState != nil {
			status, ok := c.ProcessState.Sys().(syscall.WaitStatus)
			if ok && status.Exited() {
				p.exit, p.err = exitedWithCode(status.ExitStatus()), nil
			} else if ok && status.Signaled() {
				name := unix.SignalName(status.Signal())
				if name == "" {
					name = "SIG" + strconv.Itoa(int(status.Signal()))
				}
				p.exit, p.err = processExit{signal: name, coreDumped: status.CoreDump()}, nil
			}
		}
		close(p.done)
	}()
	started = true
	return p, nil
}
func (p *linuxChild) Wait(ctx context.Context) (processExit, error) {
	select {
	case <-p.done:
		return p.exit, p.err
	case <-ctx.Done():
		return processExit{}, ctx.Err()
	}
}
func (p *linuxChild) Kill(ctx context.Context) error {
	failure := func(delivered bool, err error) error {
		return &wire.Error{Code: "termination_unconfirmed", Message: "Linux command cgroup termination could not be confirmed", Details: map[string]any{"request_delivered": delivered, "cause": wire.Safe(err)}}
	}
	if err := writeCgroupKill(p.group); err != nil {
		return failure(false, err)
	}
	// A reaped main process does not prove that its descendants have exited.
	// cgroup.kill includes nested groups and concurrent forks; populated=0 is
	// the independent confirmation that no live process remains in that tree.
	for {
		data, err := p.group.ReadFile("cgroup.events")
		if err != nil {
			return failure(true, err)
		}
		populated, err := cgroupPopulated(data)
		if err != nil {
			return failure(true, err)
		}
		if !populated {
			return nil
		}
		select {
		case <-ctx.Done():
			return failure(true, ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
}
func (p *linuxChild) Drain(ctx context.Context) error { return p.streams.Wait(ctx) }
func (p *linuxChild) Close() error {
	_ = writeCgroupKill(p.group)
	p.streams.Close()
	p.group.Close()
	// systemd owns the parent unit and removes any empty nested cgroups on
	// supervisor exit. Do not recursively walk directories created by commands.
	_ = p.parent.Remove("command")
	return p.parent.Close()
}

func supervisorCgroup(data []byte, id string) (string, error) {
	if !wire.IDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid command cgroup identity")
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		path = filepath.Clean(path)
		parent := filepath.Dir(path)
		if filepath.IsAbs(path) && filepath.Base(path) == "supervisor" && filepath.Base(parent) == "vm-test-supervisor-"+id+".service" {
			return filepath.Join("/sys/fs/cgroup", parent), nil
		}
	}
	return "", wire.Fail("guest_execution_unavailable", "Linux execution requires its own delegated supervisor unit and subgroup")
}

func commandCgroup(id string) (*os.Root, *os.Root, *os.File, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, nil, nil, err
	}
	path, err := supervisorCgroup(data, id)
	if err != nil {
		return nil, nil, nil, err
	}
	parent, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, nil, err
	}
	var group *os.Root
	var fd *os.File
	ok := false
	created := false
	defer func() {
		if !ok {
			if fd != nil {
				fd.Close()
			}
			if group != nil {
				group.Close()
			}
			if created {
				_ = parent.Remove("command")
			}
			parent.Close()
		}
	}()
	if err = parent.Mkdir("command", 0755); err != nil {
		return nil, nil, nil, err
	}
	created = true
	if group, err = parent.OpenRoot("command"); err != nil {
		return nil, nil, nil, err
	}
	if fd, err = group.Open("."); err != nil {
		return nil, nil, nil, err
	}
	var stat unix.Statfs_t
	if err = unix.Fstatfs(int(fd.Fd()), &stat); err != nil || stat.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, nil, nil, wire.Fail("guest_execution_unavailable", "Command process group is not a cgroup v2 directory")
	}
	kill, err := group.OpenFile("cgroup.kill", os.O_WRONLY, 0)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Linux command execution requires writable cgroup.kill: %w", err)
	}
	if err = kill.Close(); err != nil {
		return nil, nil, nil, err
	}
	ok = true
	return parent, group, fd, nil
}

func writeCgroupKill(group *os.Root) error {
	f, err := group.OpenFile("cgroup.kill", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString("1")
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func cgroupPopulated(data []byte) (bool, error) {
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, value, ok := strings.Cut(line, " "); ok && key == "populated" {
			switch value {
			case "0":
				return false, nil
			case "1":
				return true, nil
			}
		}
	}
	return false, fmt.Errorf("cgroup population was not observed")
}

func Reboot() error { return exec.Command("systemctl", "reboot").Start() }
