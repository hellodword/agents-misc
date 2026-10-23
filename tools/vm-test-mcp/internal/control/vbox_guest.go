package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
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

func (b *VBoxBackend) ensureSupervisor(ctx context.Context, t *Task, n *Node) error {
	c := b.guestConfig(t, n)
	local, err := config.GuestBinary(c.Kind)
	if err != nil {
		return wire.Fail("guest_binary_missing", "Packaged guest supervisor was not found")
	}
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return err
	}
	expected := hex.EncodeToString(h.Sum(nil))
	probe := func() error {
		raw, err := b.guestExec(ctx, t, n, false, "probe", "--root", c.WorkDir)
		if err != nil {
			return err
		}
		var v struct {
			Version    string `json:"version"`
			SHA256     string `json:"sha256"`
			Privileged bool   `json:"privileged"`
			Prepared   bool   `json:"prepared"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Version != wire.Version || v.SHA256 != expected || !v.Privileged || !v.Prepared {
			return wire.Fail("guest_preparation_required", "Prepare the baseline with the packaged guest preparation command")
		}
		return nil
	}
	if err = probe(); err == nil {
		return nil
	}
	// The explicit baseline preparation creates the install/work directories,
	// command privilege boundary and first-boot identity hook. Updating this
	// command binary neither changes the source VM nor installs optional tools.
	if err = b.copyTo(ctx, t, n, local, c.Supervisor); err != nil {
		return err
	}
	return probe()
}
