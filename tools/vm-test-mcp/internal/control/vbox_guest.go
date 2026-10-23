package control

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (b *VBoxBackend) resolveVM(ctx context.Context, name string) (string, error) {
	if wire.UUIDPattern.MatchString(name) {
		return name, nil
	}
	raw, err := b.run(ctx, "list", "vms")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		quoted, tail, ok := strings.Cut(strings.TrimSpace(line), " {")
		if !ok {
			continue
		}
		value, err := strconv.Unquote(quoted)
		id := strings.TrimSuffix(tail, "}")
		if err == nil && value == name && wire.UUIDPattern.MatchString(id) {
			return id, nil
		}
	}
	return "", wire.Fail("baseline_not_found", "Configured baseline VM was not found")
}

func (b *VBoxBackend) start(ctx context.Context, n *Node) ([]byte, error) {
	mode := "headless"
	if n.GUI {
		mode = "gui"
	}
	args := []string{"startvm", n.UUID, "--type", mode}
	env := b.Config.VirtualBox.Templates[n.Template].StartupEnv
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--putenv", key+"="+env[key])
	}
	return b.run(ctx, args...)
}

func (b *VBoxBackend) waitPowerOff(ctx context.Context, n *Node) error {
	for {
		info, err := b.info(ctx, n.UUID)
		if err != nil {
			return err
		}
		if info["VMState"] == "poweroff" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (b *VBoxBackend) nodeStatus(ctx context.Context, t *Task, n *Node) (*NodeStatus, error) {
	info, err := b.info(ctx, n.UUID)
	if err != nil {
		return nil, err
	}
	status := &NodeStatus{State: info["VMState"]}
	if status.State != "running" {
		return status, nil
	}
	c := b.guestConfig(t, n)
	probe := []string{"/bin/true"}
	if c.Kind == "windows" {
		probe = []string{`C:\Windows\System32\cmd.exe`, "/d", "/c", "exit", "0"}
	}
	if _, err = b.guestCommand(ctx, t, n, probe, "", nil); err != nil {
		status.GuestError = wire.Safe(err)
		return status, nil
	}
	raw, err := b.guestExec(ctx, t, n, false, "probe", "--root", c.WorkDir)
	if err != nil {
		status.ServiceError = wire.Safe(err)
	} else if json.Unmarshal(raw, &status.Guest) != nil || status.Guest == nil {
		status.ServiceError = &wire.Error{Code: "guest_protocol_error", Message: "Guest probe did not return a status object"}
	} else if status.Guest["prepared"] != true || status.Guest["privileged"] != true {
		status.ServiceError = &wire.Error{Code: "guest_not_ready", Message: "Guest initialization or command service is not ready"}
	}
	if c.Kind == "linux" {
		args := []string{"/bin/systemctl", "show", "--no-pager", "--property=Version", "--value"}
		if status.Guest["prepared"] != true {
			args = []string{"/bin/systemctl", "show", "vm-test-firstboot.service", "--no-pager", "--property=ActiveState", "--property=SubState", "--property=Result"}
		}
		result, callErr := b.guestCommand(ctx, t, n, args, "", nil)
		if callErr != nil {
			status.ServiceError = wire.Safe(callErr)
		} else if strings.Contains(string(result.Stdout), "ActiveState=failed") {
			status.PreparationFailed = true
			status.ServiceError = &wire.Error{Code: "initialization_failed", Message: "Guest first-boot service failed", Details: map[string]any{"status": string(result.Stdout)}}
		}
	}
	return status, nil
}
