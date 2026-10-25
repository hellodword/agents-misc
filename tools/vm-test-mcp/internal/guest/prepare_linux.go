//go:build linux

package guest

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/unix"
)

const DefaultRoot = "/var/lib/vm-test/work"
const installPath = "/opt/vm-test/vm-test-guest"

func submit(ctx context.Context, root, request string) error {
	privileged, prepared := readiness(root)
	if !privileged || !prepared {
		return fmt.Errorf("prepare the Linux baseline before submitting commands")
	}
	var spec wire.CommandSpec
	if err := readJSON(request, &spec); err != nil {
		return err
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// The short Guest Control invocation exits once the supervisor is running.
	// Its lifetime is owned by systemd, not a detached Guest Control session.
	seconds := spec.TimeoutSeconds
	if seconds == 0 {
		seconds = 30 * 86400
	}
	// systemd owns supervisor failure and the final cleanup of both subgroups.
	// Its lifetime includes the bounded startup/stop/drain phases.
	command := exec.CommandContext(ctx, "systemd-run", "--quiet", "--collect", "--expand-environment=no", "--unit", "vm-test-supervisor-"+spec.ID,
		"--property=Type=exec", "--property=KillMode=control-group", "--property=TimeoutStopSec=5s",
		"--property=Delegate=yes", "--property=DelegateSubgroup=supervisor", "--property=RuntimeMaxSec="+strconv.Itoa(seconds+60),
		"--", exe, "execute", "--root", root, "--request", request)
	command.WaitDelay = time.Second
	if err = command.Run(); err != nil {
		return err
	}
	for {
		if _, err = os.Stat(filepath.Join(root, spec.ID, "status.json")); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func Service(string, string) error { return fmt.Errorf("Linux commands use system systemd units") }
func commandServiceObservation(string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	observed, err := linuxExecutionRequirements(ctx)
	v := map[string]any{"state": "per_command", "mode": "systemd_delegated_cgroup", "build": wire.Identity(), "requirements": observed}
	if err != nil {
		v["state"], v["error"] = "unavailable", wire.Safe(err)
	}
	return v
}

func linuxExecutionRequirements(ctx context.Context) (map[string]any, error) {
	v := map[string]any{"minimum_systemd": 254, "cgroup_v2": false, "cgroup_kill": false, "clone3": false}
	fail := func(reason string) (map[string]any, error) {
		return v, &wire.Error{Code: "guest_execution_unavailable", Message: reason, Details: map[string]any{"requirements": v}}
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return fail("Linux commands require cgroup v2")
	}
	v["cgroup_v2"] = true
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return fail("Linux command cgroup capabilities could not be observed")
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			_, err = os.Stat(filepath.Join("/sys/fs/cgroup", path, "cgroup.kill"))
			v["cgroup_kill"] = err == nil
		}
	}
	if v["cgroup_kill"] != true {
		return fail("Linux commands require cgroup.kill in their service cgroup (Linux 5.14 or newer)")
	}
	// An undersized clone3 request cannot create a child. EINVAL proves that
	// this syscall is available instead of denied or absent in the environment.
	_, _, errno := unix.Syscall(unix.SYS_CLONE3, 0, 0, 0)
	v["clone3"] = errno == unix.EINVAL
	if errno != unix.EINVAL {
		v["clone3_errno"] = int(errno)
		return fail("Linux commands require an available clone3 syscall")
	}
	command := exec.CommandContext(ctx, "systemctl", "show", "--property=Version", "--value")
	command.WaitDelay = 100 * time.Millisecond
	data, err = command.Output()
	if err != nil {
		return fail("The running systemd version could not be observed")
	}
	version := strings.TrimSpace(string(data))
	v["systemd"] = version
	if !supportedSystemd(version) {
		return fail("Linux commands require systemd 254 or newer for delegated supervisor subgroups")
	}
	return v, nil
}

func supportedSystemd(version string) bool {
	end := 0
	for end < len(version) && version[end] >= '0' && version[end] <= '9' {
		end++
	}
	major, err := strconv.Atoi(version[:end])
	return err == nil && major >= 254
}
func readiness(root string) (bool, bool) {
	_, cgroup := os.Stat("/sys/fs/cgroup/cgroup.controllers")
	_, systemd := os.Stat("/run/systemd/system")
	_, prepared := os.Stat(filepath.Join(root, "prepared"))
	_, pending := os.Stat(filepath.Join(root, "firstboot-pending"))
	return os.Geteuid() == 0 && cgroup == nil && systemd == nil, prepared == nil && os.IsNotExist(pending)
}

// Preparation is an administrator-invoked operation inside the source guest.
// It deliberately powers the guest off for the administrator's snapshot.
func Prepare(root string) error {
	privileged, _ := readiness(root)
	if !privileged {
		return fmt.Errorf("preparation requires root, systemd and cgroup v2")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := linuxExecutionRequirements(ctx); err != nil {
		return err
	}
	if !filepath.IsAbs(root) || strings.ContainsAny(root, "\r\n\"%\\") {
		return fmt.Errorf("invalid work directory")
	}
	for _, name := range []string{"VBoxService", "systemd-run", "systemctl"} {
		if _, err := exec.LookPath(name); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(installPath), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if filepath.Clean(exe) != installPath {
		in, err := os.Open(exe)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(installPath+".new", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		ce := out.Close()
		if err != nil {
			return err
		}
		if ce != nil {
			return ce
		}
		if err = os.Rename(installPath+".new", installPath); err != nil {
			return err
		}
	}
	unit := firstbootUnit(root)
	if err = os.WriteFile("/etc/systemd/system/vm-test-firstboot.service", []byte(unit), 0644); err != nil {
		return err
	}
	if err = exec.Command("systemctl", "enable", "vm-test-firstboot.service").Run(); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, "firstboot-pending"), []byte("pending\n"), 0600); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(root, "prepared")); err != nil && !os.IsNotExist(err) {
		return err
	}
	// All linked clones boot with a new machine-id and (when installed) SSH keys.
	keys, err := filepath.Glob("/etc/ssh/ssh_host_*_key*")
	if err != nil {
		return err
	}
	for _, p := range keys {
		if err = os.Remove(p); err != nil {
			return err
		}
	}
	if err = os.WriteFile("/etc/machine-id", []byte("uninitialized\n"), 0444); err != nil {
		return err
	}
	if _, err = os.Lstat("/var/lib/dbus/machine-id"); err == nil {
		if err = os.Remove("/var/lib/dbus/machine-id"); err != nil {
			return err
		}
		if err = os.Symlink("/etc/machine-id", "/var/lib/dbus/machine-id"); err != nil {
			return err
		}
	}
	return exec.Command("systemctl", "poweroff").Run()
}
