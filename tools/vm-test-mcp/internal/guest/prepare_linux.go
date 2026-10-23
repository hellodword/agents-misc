//go:build linux

package guest

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
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
	if err = exec.CommandContext(ctx, "systemd-run", "--quiet", "--collect", "--unit", "vm-test-supervisor-"+spec.ID, "--property", "Type=exec", "--property", "KillMode=control-group", "--", exe, "execute", "--root", root, "--request", request).Run(); err != nil {
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
func Service(string) error { return fmt.Errorf("Linux commands use system systemd units") }
func readiness(root string) (bool, bool) {
	_, cgroup := os.Stat("/sys/fs/cgroup/cgroup.controllers")
	_, systemd := os.Stat("/run/systemd/system")
	_, prepared := os.Stat(filepath.Join(root, "prepared"))
	return os.Geteuid() == 0 && cgroup == nil && systemd == nil, prepared == nil
}

// Preparation is an administrator-invoked operation inside the source guest.
// It deliberately powers the guest off for the administrator's snapshot.
func Prepare(root string) error {
	privileged, _ := readiness(root)
	if !privileged {
		return fmt.Errorf("preparation requires root, systemd and cgroup v2")
	}
	if !filepath.IsAbs(root) || strings.ContainsAny(root, "\r\n\"%\\") {
		return fmt.Errorf("invalid work directory")
	}
	for _, name := range []string{"VBoxService", "systemd-run", "systemctl", "hostnamectl"} {
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
	unit := "[Unit]\nDescription=Initialize VM test clone identity\nBefore=vboxadd-service.service virtualbox-guest-utils.service vboxservice.service ssh.service sshd.service\nConditionPathExists=" + root + "/firstboot-pending\n[Service]\nType=oneshot\nExecStart=" + installPath + " firstboot --root \"" + root + "\"\n[Install]\nWantedBy=multi-user.target\n"
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

func FirstBoot(root string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("firstboot requires root")
	}
	if _, err := os.Stat(filepath.Join(root, "firstboot-pending")); err != nil {
		return err
	}
	if err := exec.Command("hostnamectl", "set-hostname", "vmtest-"+wire.ID()[:12]).Run(); err != nil {
		return err
	}
	if _, err := exec.LookPath("ssh-keygen"); err == nil {
		if err = exec.Command("ssh-keygen", "-A").Run(); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(root, "prepared"), []byte(wire.Version), 0600); err != nil {
		return err
	}
	return os.Remove(filepath.Join(root, "firstboot-pending"))
}
