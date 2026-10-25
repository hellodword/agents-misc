package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/network"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/telemetry"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type WorkerClient interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}
type VBoxBackend struct {
	Config            config.Config
	Runner            hostexec.Runner
	Worker            WorkerClient
	CacheURL          func(string) string
	Performance       *telemetry.Monitor
	cloneLocks        sync.Map
	guestSlots        sync.Map
	guestSessions     sync.Map
	bootConsoles      sync.Map
	backendIdentities sync.Map
}

func (b *VBoxBackend) run(ctx context.Context, args ...string) ([]byte, error) {
	exe, err := config.Program(b.Config.VirtualBox.Executable, "VBoxManage")
	if err != nil {
		return nil, wire.Fail("virtualbox_unavailable", "VBoxManage was not found in PATH")
	}
	started := time.Now()
	result, err := hostexec.Capture(ctx, b.Runner, exe, args, nil)
	b.recordCall(started, 0, "virtualbox", args[0], "", "", result, err)
	return result.Stdout, b.executionError(args[0], err)
}
func (b *VBoxBackend) redact(value string) string {
	values := []string{b.Config.Service.Token, b.Config.Service.StateDir}
	for _, t := range b.Config.VirtualBox.Templates {
		values = append(values, t.Password, t.VM, t.Snapshot)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	for _, secret := range values {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	return value
}
func (b *VBoxBackend) executionError(stage string, err error) error {
	if err == nil {
		return nil
	}
	var known *wire.Error
	if errors.As(err, &known) {
		return err
	}
	code := "backend_error"
	details := map[string]any{"stage": stage}
	var failure *hostexec.Failure
	if errors.As(err, &failure) {
		details["exit_code"] = failure.ExitCode
		details["stderr"] = b.redact(string(failure.Stderr))
		details["timed_out"] = failure.TimedOut
		details["execution_us"] = failure.ExecutionUS
		details["user_us"] = failure.UserUS
		details["system_us"] = failure.SystemUS
		if failure.TimedOut {
			code = "backend_timeout"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "backend_timeout"
	}
	if errors.Is(err, context.Canceled) {
		code = "backend_cancelled"
	}
	return &wire.Error{Code: code, Message: "VirtualBox operation could not be confirmed", Details: details}
}
func machineInfo(data []byte) (map[string]string, error) {
	v := map[string]string{}
	nic := "1"
	for _, line := range strings.Split(string(data), "\n") {
		k, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(k, "\"") {
			if s, e := strconv.Unquote(k); e == nil {
				k = s
			}
		}
		// Running VMs report VideoMode as "width,height,depth"@x,y status.
		// Keep this composite display value raw; it is not a quoted string.
		if strings.HasPrefix(value, "\"") && k != "VideoMode" {
			if s, e := strconv.Unquote(value); e == nil {
				value = s
			} else {
				return nil, e
			}
		}
		if strings.HasPrefix(k, "nic") {
			if n, e := strconv.Atoi(strings.TrimPrefix(k, "nic")); e == nil && n >= 1 && n <= 8 {
				nic = strconv.Itoa(n)
			}
		}
		if strings.HasPrefix(k, "Forwarding(") {
			k += "@" + nic
		}
		if strings.HasPrefix(k, "rec_screen") {
			original := k
			for index := 1; ; index++ {
				if _, exists := v[k]; !exists {
					break
				}
				k = original + "@" + strconv.Itoa(index)
			}
		}
		if _, exists := v[k]; exists {
			return nil, fmt.Errorf("duplicate machine field")
		}
		v[k] = value
	}
	return v, nil
}
func (b *VBoxBackend) info(ctx context.Context, uuid string) (map[string]string, error) {
	raw, e := b.run(ctx, "showvminfo", uuid, "--machinereadable")
	if e != nil {
		return nil, e
	}
	info, e := machineInfo(raw)
	if e == nil && info["UUID"] != uuid {
		e = fmt.Errorf("unexpected VM identity")
	}
	return info, e
}
func (b *VBoxBackend) Available(ctx context.Context, kind string) (string, error) {
	if kind == "nixos" {
		b.backendIdentities.Store(kind, backendIdentity{State: "unobserved", Observed: time.Now().UTC()})
		if b.Worker == nil {
			return "nix_disabled", nil
		}
		raw, e := b.Worker.Call(ctx, "describe", struct{}{})
		if e != nil {
			return "worker_unavailable", e
		}
		var v struct {
			Busy  bool                `json:"busy"`
			Build *wire.BuildIdentity `json:"build"`
		}
		if e = json.Unmarshal(raw, &v); e != nil {
			return "worker_unavailable", e
		}
		identity := backendIdentity{State: "observed", Observed: time.Now().UTC(), Build: v.Build}
		if v.Build == nil {
			identity.State = "build_unavailable"
		}
		b.backendIdentities.Store(kind, identity)
		if v.Busy {
			return "worker_busy", nil
		}
		if b.Config.VirtualBoxEnabled() {
			raw, e = b.run(ctx, "list", "runningvms")
			if e != nil {
				return "virtualbox_unavailable", e
			}
			if strings.TrimSpace(string(raw)) != "" {
				return "virtualbox_running", nil
			}
		}
		return "", nil
	}
	if !b.Config.VirtualBoxEnabled() {
		return "virtualbox_disabled", nil
	}
	b.backendIdentities.Store(kind, backendIdentity{State: "unobserved", Observed: time.Now().UTC()})
	raw, e := b.run(ctx, "--version")
	if e != nil {
		return "virtualbox_unavailable", e
	}
	version := strings.TrimSpace(string(raw))
	if len(version) > 128 || !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+[A-Za-z0-9._+-]*$`).MatchString(version) {
		return "virtualbox_unavailable", wire.Fail("backend_protocol_error", "VBoxManage did not return a recognizable version")
	}
	b.backendIdentities.Store(kind, backendIdentity{State: "observed", Observed: time.Now().UTC(), RuntimeVersion: version})
	return "", e
}
func (b *VBoxBackend) runtime(t *Task) string {
	return filepath.Join(b.Config.Service.StateDir, "tasks", t.ID, "runtime")
}
func (b *VBoxBackend) vmDir(t *Task, n *Node) string {
	return filepath.Join(b.runtime(t), "vm-"+n.UUID)
}
func (b *VBoxBackend) owned(t *Task, n *Node, info map[string]string) error {
	cfg := info["CfgFile"]
	rel, e := filepath.Rel(b.vmDir(t, n), cfg)
	if e != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return wire.Fail("ownership_mismatch", "VM configuration is outside its registered resource directory")
	}
	return nil
}
func on(v bool) string {
	if v {
		return "on"
	}
	return "off"
}
func (b *VBoxBackend) Prepare(ctx context.Context, t *Task, n *Node) error {
	ctx, cancel := context.WithTimeout(ctx, b.Config.VirtualBox.StartupTimeout.Value())
	defer cancel()
	reportProgress(ctx, "creating", nil, nil)
	template := b.Config.VirtualBox.Templates[n.Template]
	router := n.ID == t.Router.ID
	if e := os.MkdirAll(b.runtime(t), 0700); e != nil {
		return e
	}
	var e error
	var baseID string
	if router {
		// Nix result paths normally contain symlinks. Register the real medium
		// path so VirtualBox's attachment readback can be compared exactly.
		real, err := filepath.EvalSymlinks(t.RouterISO)
		if err != nil {
			return wire.Fail("router_unavailable", "Configured router Live ISO could not be resolved")
		}
		copyTask := *t
		copyTask.RouterISO = real
		t = &copyTask
		st, err := os.Stat(t.RouterISO)
		if err != nil || !st.Mode().IsRegular() {
			return wire.Fail("router_unavailable", "Configured router Live ISO is not a regular file")
		}
		_, e = b.run(ctx, "createvm", "--name", "vm-"+n.UUID, "--uuid", n.UUID, "--basefolder", b.runtime(t), "--ostype", "Linux_64", "--register")
	} else {
		var err error
		baseID, err = b.resolveVM(ctx, template.VM)
		if err != nil {
			return err
		}
		value, _ := b.cloneLocks.LoadOrStore(baseID, &sync.Mutex{})
		lock := value.(*sync.Mutex)
		if err := lockContext(ctx, lock); err != nil {
			return err
		}
		e = func() error {
			base, err := b.info(ctx, baseID)
			if err != nil {
				return err
			}
			if base["VMState"] != "poweroff" {
				return wire.Fail("resource_busy", "Baseline must be powered off")
			}
			_, err = b.run(ctx, "clonevm", baseID, "--snapshot", template.Snapshot, "--mode", "machine", "--options", "link", "--name", "vm-"+n.UUID, "--uuid", n.UUID, "--basefolder", b.runtime(t), "--register")
			return err
		}()
		lock.Unlock()
	}
	if e != nil {
		return e
	}
	info, e := b.info(ctx, n.UUID)
	if e != nil {
		return e
	}
	if e = b.owned(t, n, info); e != nil {
		return e
	}
	if e = b.sanitize(ctx, t, n, info); e != nil {
		return e
	}
	args := []string{"modifyvm", n.UUID, "--cpus", strconv.Itoa(n.CPUs), "--memory", strconv.Itoa(n.MemoryMiB), "--nic1", "intnet", "--intnet1", "vm-test-" + t.ID, "--mac-address1", strings.ReplaceAll(n.MAC, ":", ""), "--cable-connected1", "on", "--nic-promisc1", "deny", "--clipboard-mode", "disabled", "--clipboard-file-transfers", "disabled", "--drag-and-drop", "disabled", "--vrde", "off", "--teleporter", "off", "--usb-ohci", "off", "--usb-ehci", "off", "--usb-xhci", on(template.Media.Camera), "--audio-enabled", on(template.Media.AudioInput || template.Media.AudioOutput), "--audio-in", on(n.Media.AudioInput), "--audio-out", on(n.Media.AudioOutput), "--accelerate-3d", on(n.Media.Accelerate3D), "--recording", "off"}
	for i := 2; i <= 8; i++ {
		args = append(args, fmt.Sprintf("--nic%d", i), "none")
	}
	for i := 1; i <= 4; i++ {
		if router && i == 1 {
			console, err := newBootConsole(b.consoleDir(t))
			if err != nil {
				return wire.Fail("console_unavailable", "Router boot console could not be prepared")
			}
			b.bootConsoles.Store(n.UUID, console)
			args = append(args, "--uart1", "0x3f8", "4", "--uart-mode1", "client", console.socket)
			continue
		}
		args = append(args, fmt.Sprintf("--uart%d", i), "off")
	}
	for i := 1; i <= 2; i++ {
		args = append(args, fmt.Sprintf("--lpt%d", i), "off")
	}
	if router {
		args = append(args, "--nictype1", "82540EM", "--nictype2", "82540EM", "--firmware", "efi64", "--x86-long-mode", "on", "--ioapic", "on", "--boot1", "dvd", "--boot2", "none", "--boot3", "none", "--boot4", "none", "--graphicscontroller", "vmsvga", "--vram", "16", "--nic2", "nat", "--nat-net2", b.Config.VirtualBox.Network.WAN, "--cable-connected2", "on", "--mac-address2", "080027"+t.ID[6:12])
		// libslirp translates UDP forwarding replies back to the host's
		// loopback relay. Without this, it drops those replies. The router's
		// nftables policy still rejects guest-initiated host access and only
		// permits this return path for registered, connection-marked entries.
		args = append(args, "--nat-localhostreachable2", "on")
	}
	if _, e = b.run(ctx, args...); e != nil {
		return e
	}
	if router {
		if _, e = b.run(ctx, "storagectl", n.UUID, "--name", "VMTestMedia", "--add", "sata", "--portcount", "2"); e != nil {
			return e
		}
		if _, e = b.run(ctx, "storageattach", n.UUID, "--storagectl", "VMTestMedia", "--port", "0", "--device", "0", "--type", "dvddrive", "--medium", t.RouterISO); e != nil {
			return e
		}
		if e = b.seed(ctx, t, n); e != nil {
			return e
		}
	}
	info, e = b.info(ctx, n.UUID)
	if e != nil {
		return e
	}
	if e = b.verify(t, n, info); e != nil {
		return e
	}
	if !router {
		reportProgress(ctx, "configured", &NodeStatus{Identity: cloneIdentity(template, baseID, info)}, nil)
		if _, e = b.run(ctx, "setextradata", n.UUID, "GUI/AutoresizeGuest", "false"); e != nil {
			return e
		}
	}
	reportProgress(ctx, "starting", nil, nil)
	if _, e = b.start(ctx, n); e != nil {
		return e
	}
	ready := ctx
	var startup Health
	var polling preparationPolling
	for {
		probe, stop := context.WithTimeout(ready, probeTimeout)
		stage := "guest_control"
		status := &NodeStatus{}
		var failure error
		if router {
			_, e = b.guestExec(probe, t, n, "probe")
		} else {
			var err error
			status, err = b.nodeStatus(probe, t, n)
			e = err
			if err == nil {
				if status.GuestError != nil {
					e = status.GuestError
				} else {
					stage = "initializing"
					if status.ServiceError != nil {
						e = status.ServiceError
					}
				}
				if status.State != "running" {
					e = wire.Fail("node_stopped", "Node stopped during preparation")
				}
				if e == nil && (status.Guest["prepared"] != true || status.Guest["privileged"] != true) {
					e = wire.Fail("guest_not_ready", "Guest initialization is in progress")
				}
			}
			if status != nil && (status.PreparationFailed || oneOf(status.State, "aborted", "poweroff", "stuck")) {
				failure = e
			} else {
				failure = startup.startupFailure(time.Now().UTC(), status, err, b.Config.VirtualBox.UnavailableAfter.Value())
			}
		}
		stop()
		var schedule PollSchedule
		if e != nil && failure == nil {
			schedule = polling.next(n.ID, stage, time.Now().UTC())
		}
		if status == nil {
			status = &NodeStatus{}
		}
		status.Polling = &schedule
		reportProgress(ctx, stage, status, e)
		if failure != nil {
			return failure
		}
		if e == nil {
			break
		}
		if schedule.wait(ready) != nil {
			if ready.Err() == context.Canceled {
				return &wire.Error{Code: "backend_cancelled", Message: "Preparation was cancelled before readiness", Details: map[string]any{"node_id": n.ID, "cause": wire.Safe(context.Cause(ready)), "last_error": wire.Safe(e)}}
			}
			return &wire.Error{Code: "guest_not_ready", Message: "Node did not become ready before its startup deadline", Details: map[string]any{"node_id": n.ID, "last_error": wire.Safe(e)}}
		}
	}
	reportProgress(ctx, "ready", nil, nil)
	// Keep both DVDs attached: a diskless router needs its seed on every boot.
	if !router && n.Media.Camera {
		if _, e = b.run(ctx, "controlvm", n.UUID, "webcam", "attach", template.Cameras[n.Camera]); e != nil {
			return e
		}
	}
	return nil
}

var attachment = regexp.MustCompile(`^(.+)-([0-9]+)-([0-9]+)$`)

func (b *VBoxBackend) sanitize(ctx context.Context, t *Task, n *Node, info map[string]string) error {
	keys := make([]string, 0, len(info))
	controllers := map[string]bool{}
	for k := range info {
		keys = append(keys, k)
		if strings.HasPrefix(k, "storagecontrollername") {
			controllers[info[k]] = true
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.HasPrefix(k, "SharedFolderNameMachineMapping") {
			if _, e := b.run(ctx, "sharedfolder", "remove", n.UUID, "--name", info[k]); e != nil {
				return e
			}
		}
		if strings.HasPrefix(k, "USBFilterName") {
			if _, e := b.run(ctx, "usbfilter", "remove", "0", "--target", n.UUID); e != nil {
				return e
			}
		}
		if strings.HasPrefix(k, "Forwarding(") {
			name := strings.Split(info[k], ",")[0]
			_, slot, ok := strings.Cut(k, "@")
			if !ok {
				slot = "1"
			}
			if _, e := b.run(ctx, "modifyvm", n.UUID, "--nat-pf"+slot, "delete", name); e != nil {
				return e
			}
		}
		m := attachment.FindStringSubmatch(k)
		if m == nil || !controllers[m[1]] || info[k] == "none" || info[k] == "emptydrive" {
			continue
		}
		// Only attachment entries are path-valued; ImageUUID and controller
		// attributes do not match an absolute location.
		if !filepath.IsAbs(info[k]) {
			return wire.Fail("unsafe_device", "Only local differencing disks are allowed")
		}
		_, dvd := info[fmt.Sprintf("%s-tempeject-%s-%s", m[1], m[2], m[3])]
		if dvd || strings.HasSuffix(strings.ToLower(info[k]), ".iso") {
			if _, e := b.run(ctx, "storageattach", n.UUID, "--storagectl", m[1], "--port", m[2], "--device", m[3], "--medium", "none"); e != nil {
				return e
			}
			continue
		}
		raw, e := b.run(ctx, "showmediuminfo", "disk", info[k])
		if e != nil {
			return e
		}
		medium := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok {
				medium[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
		rel, e := filepath.Rel(b.vmDir(t, n), info[k])
		if e != nil || strings.HasPrefix(rel, "..") || medium["Storage format"] != "VDI" || !wire.UUIDPattern.MatchString(medium["Parent UUID"]) || medium["Type"] != "normal (differencing)" || medium["Location"] != info[k] {
			return wire.Fail("unsafe_device", "Clones require task-owned differencing VDI disks")
		}
	}
	for k := range info {
		if strings.Contains(strings.ToLower(k), "pci") && info[k] != "" && info[k] != "off" {
			return wire.Fail("unsafe_device", "PCI passthrough is not allowed")
		}
	}
	return nil
}
func (b *VBoxBackend) verify(t *Task, n *Node, i map[string]string) error {
	expected := map[string]string{"nic1": "intnet", "intnet1": "vm-test-" + t.ID, "clipboard": "disabled", "draganddrop": "disabled", "vrde": "off", "audio_in": on(n.Media.AudioInput), "audio_out": on(n.Media.AudioOutput), "accelerate3d": on(n.Media.Accelerate3D)}
	for slot := 2; slot <= 8; slot++ {
		expected[fmt.Sprintf("nic%d", slot)] = "none"
	}
	if t.Router != nil && n.ID == t.Router.ID {
		expected["nic2"] = "nat"
		expected["VMTestMedia-0-0"] = t.RouterISO
		expected["VMTestMedia-1-0"] = filepath.Join(b.runtime(t), "seed.iso")
	}
	expected["teleporterenabled"] = "off"
	expected["usb"], expected["ehci"] = "off", "off"
	expected["xhci"] = on(b.Config.VirtualBox.Templates[n.Template].Media.Camera)
	for slot := 1; slot <= 4; slot++ {
		expected[fmt.Sprintf("uart%d", slot)] = "off"
	}
	if t.Router != nil && n.ID == t.Router.ID {
		console, ok := b.bootConsoles.Load(n.UUID)
		if !ok {
			return wire.Fail("unsafe_device", "Router console ownership could not be verified")
		}
		expected["uart1"] = "0x03f8,4"
		expected["uartmode1"] = "client," + console.(*bootConsole).socket
	}
	for slot := 1; slot <= 2; slot++ {
		expected[fmt.Sprintf("lpt%d", slot)] = "off"
	}
	for k, v := range expected {
		if i[k] != v {
			return wire.Fail("unsafe_device", "Clone device verification failed")
		}
	}
	for k := range i {
		if strings.HasPrefix(k, "SharedFolderName") || strings.HasPrefix(k, "USBFilterName") || strings.HasPrefix(k, "Forwarding(") {
			return wire.Fail("unsafe_device", "Host sharing or USB filters remain")
		}
	}
	return nil
}
func (b *VBoxBackend) seedData(t *Task) map[string]any {
	return map[string]any{"password": t.RouterPassword, "lan_mac": t.Router.MAC, "wan_mac": "08:00:27:" + t.ID[6:8] + ":" + t.ID[8:10] + ":" + t.ID[10:12], "lan": t.LAN, "wan": b.Config.VirtualBox.Network.WAN}
}
func (b *VBoxBackend) seed(ctx context.Context, t *Task, n *Node) error {
	dir := filepath.Join(b.runtime(t), "seed")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := os.WriteFile(filepath.Join(dir, "seed.json"), wire.JSON(b.seedData(t)), 0600); e != nil {
		return e
	}
	iso := filepath.Join(b.runtime(t), "seed.iso")
	builder, err := config.Program(b.Config.VirtualBox.ISOBuilder, "xorriso")
	if err != nil {
		return wire.Fail("backend_unavailable", "Packaged ISO builder was not found")
	}
	if _, e := b.Runner.Run(ctx, builder, []string{"-as", "mkisofs", "-quiet", "-V", "VMTESTSEED", "-o", iso, dir}, nil); e != nil {
		return b.executionError("iso_builder", e)
	}
	if e := os.Chmod(iso, 0600); e != nil {
		return e
	}
	_, e := b.run(ctx, "storageattach", n.UUID, "--storagectl", "VMTestMedia", "--port", "1", "--device", "0", "--type", "dvddrive", "--medium", iso)
	return e
}
func (b *VBoxBackend) guestConfig(t *Task, n *Node) config.Template {
	if t.Router != nil && n.ID == t.Router.ID {
		return config.Template{Kind: "linux", Username: "root", Password: t.RouterPassword, Supervisor: "/run/current-system/sw/bin/vm-test-router-helper", WorkDir: "/run/vm-test"}
	}
	return b.Config.VirtualBox.Templates[n.Template]
}
func guestJoin(root string, parts ...string) string {
	sep := "/"
	if strings.Contains(root, "\\") {
		sep = "\\"
	}
	return strings.TrimRight(root, "/\\") + sep + strings.Join(parts, sep)
}
func (b *VBoxBackend) guest(ctx context.Context, t *Task, n *Node, operation string, args ...string) ([]byte, error) {
	result, err := b.guestCaptured(ctx, t, n, operation, args...)
	return result.Stdout, err
}

func (b *VBoxBackend) guestExec(ctx context.Context, t *Task, n *Node, args ...string) ([]byte, error) {
	c := b.guestConfig(t, n)
	options := []string{"--exe", c.Supervisor, "--timeout", guestTimeout(ctx), "--wait-stdout", "--wait-stderr"}
	// VBoxManage supplies argv[0] from --exe. Arguments after -- start at argv[1].
	options = append(options, "--")
	options = append(options, args...)
	return b.guest(ctx, t, n, "run", options...)
}
func (b *VBoxBackend) copyTo(ctx context.Context, t *Task, n *Node, local, remote string) error {
	_, e := b.guest(ctx, t, n, "copyto", "--target-directory", remote, "--", local)
	return e
}
func (b *VBoxBackend) copyFrom(ctx context.Context, t *Task, n *Node, remote, local string) error {
	stage, e := os.MkdirTemp(b.runtime(t), "download-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	// Use an explicit filename: Windows Guest Control can treat an existing
	// --target-directory as the file destination and reject the copy.
	const downloaded = "content"
	if _, e = b.guest(ctx, t, n, "copyfrom", "--", remote, filepath.Join(stage, downloaded)); e != nil {
		return e
	}
	root, e := os.OpenRoot(stage)
	if e != nil {
		return e
	}
	defer root.Close()
	safe := &safefs.Root{Root: root}
	f, e := safe.Regular(downloaded)
	if e != nil {
		return e
	}
	defer f.Close()
	return b.publish(local, f)
}
func (b *VBoxBackend) publish(local string, r io.Reader) error {
	root, e := os.OpenRoot(b.Config.Service.StateDir)
	if e != nil {
		return e
	}
	defer root.Close()
	rel, e := filepath.Rel(b.Config.Service.StateDir, local)
	if e != nil {
		return e
	}
	safe := &safefs.Root{Root: root}
	_, _, e = safe.Write(filepath.ToSlash(rel), r, b.Config.Storage.MaxUploadBytes)
	return e
}
func (b *VBoxBackend) requestFile(ctx context.Context, t *Task, n *Node, name string, value any) (string, error) {
	c := b.guestConfig(t, n)
	stage, e := os.MkdirTemp(b.runtime(t), "upload-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(stage)
	local := filepath.Join(stage, name)
	if e = os.WriteFile(local, wire.JSON(value), 0600); e != nil {
		return "", e
	}
	remote := guestJoin(c.WorkDir, name)
	if e = b.copyTo(ctx, t, n, local, remote); e != nil {
		return "", e
	}
	return remote, nil
}
func (b *VBoxBackend) Delete(ctx context.Context, t *Task, n *Node) error {
	var info map[string]string
	exists := false
	err := cleanupStep(ctx, "ownership", func() error {
		raw, err := b.run(ctx, "list", "vms")
		if err != nil {
			return err
		}
		exists = strings.Contains(string(raw), "{"+n.UUID+"}")
		if !exists {
			return nil
		}
		info, err = b.info(ctx, n.UUID)
		if err != nil {
			return err
		}
		return b.owned(t, n, info)
	})
	if err != nil {
		return err
	}
	if exists {
		if err = cleanupStep(ctx, "poweroff", func() error {
			if !oneOf(info["VMState"], "poweroff", "aborted") {
				var err error
				if info["VMState"] == "saved" {
					_, err = b.run(ctx, "discardstate", n.UUID)
				} else {
					_, err = b.run(ctx, "controlvm", n.UUID, "poweroff")
				}
				if err != nil {
					return err
				}
			}
			for {
				var err error
				info, err = b.info(ctx, n.UUID)
				if err != nil {
					return err
				}
				if oneOf(info["VMState"], "poweroff", "aborted") {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(500 * time.Millisecond):
				}
			}
		}); err != nil {
			return err
		}
		if err = cleanupStep(ctx, "session_unlock", func() error { return b.waitUnlocked(ctx, t, n) }); err != nil {
			return err
		}
		if err = cleanupStep(ctx, "ownership_recheck", func() error {
			var err error
			info, err = b.info(ctx, n.UUID)
			if err != nil {
				return err
			}
			if err = b.owned(t, n, info); err != nil {
				return err
			}
			if !oneOf(info["VMState"], "poweroff", "aborted") {
				return wire.Fail("cleanup_conflict", "VM power state changed while its session was being released")
			}
			return nil
		}); err != nil {
			return err
		}
		// Optical media may be administrator-owned. Detach them before deleting
		// the VM, and never close borrowed images in the global medium registry.
		if err = cleanupStep(ctx, "detach_media", func() error {
			for key, value := range info {
				m := attachment.FindStringSubmatch(key)
				if m == nil || value == "none" || value == "emptydrive" {
					continue
				}
				if _, ok := info[fmt.Sprintf("%s-tempeject-%s-%s", m[1], m[2], m[3])]; ok || value == t.RouterISO || strings.HasSuffix(strings.ToLower(value), ".iso") {
					if _, err := b.run(ctx, "storageattach", n.UUID, "--storagectl", m[1], "--port", m[2], "--device", m[3], "--medium", "none"); err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if err = cleanupStep(ctx, "unregister", func() error { _, err := b.run(ctx, "unregistervm", n.UUID, "--delete"); return err }); err != nil {
			return err
		}
	}
	if err = cleanupStep(ctx, "verify", func() error {
		raw, err := b.run(ctx, "list", "vms")
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "{"+n.UUID+"}") {
			return wire.Fail("cleanup_required", "Clone remains registered")
		}
		return nil
	}); err != nil {
		return err
	}
	b.guestSlots.Delete(n.UUID)
	b.guestSessions.Delete(n.UUID)
	if t.Router != nil && n.ID == t.Router.ID {
		if err = cleanupStep(ctx, "console", func() error { return b.stopConsole(ctx, n.UUID) }); err != nil {
			return err
		}
		return cleanupStep(ctx, "seed", func() error { return b.closeSeed(ctx, t) })
	}
	return nil
}
func (b *VBoxBackend) closeSeed(ctx context.Context, t *Task) error {
	raw, e := b.run(ctx, "list", "dvds")
	if e != nil {
		return e
	}
	id := ""
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if key == "UUID" {
			id = value
		}
		if key == "Location" && value == filepath.Join(b.runtime(t), "seed.iso") {
			if !wire.UUIDPattern.MatchString(id) {
				return wire.Fail("ownership_mismatch", "Seed medium identity is invalid")
			}
			_, e = b.run(ctx, "closemedium", "dvd", id)
			if e != nil {
				return e
			}
		}
	}
	return nil
}
func (b *VBoxBackend) Action(ctx context.Context, t *Task, n *Node, a NodeAction) (json.RawMessage, error) {
	if n == nil || (t.Router != nil && n.ID == t.Router.ID && !oneOf(a.Action, "diagnostics", "vm_info", "screenshot", "boot_log")) {
		return nil, wire.Fail("policy_denied", "Router is not a test target")
	}
	c := b.guestConfig(t, n)
	var raw []byte
	var e error
	control := func(args ...string) ([]byte, error) {
		return b.run(ctx, append([]string{"controlvm", n.UUID}, args...)...)
	}
	switch a.Action {
	case "boot_log":
		if t.Router == nil || n.ID != t.Router.ID {
			return nil, wire.Fail("policy_denied", "Boot console collection belongs to the private router")
		}
		return b.consoleLog(ctx, t, a.Local)
	case "node_status":
		status, err := b.nodeStatus(ctx, t, n)
		return wire.JSON(status), err
	case "node_exec":
		result, err := b.guestCommand(ctx, t, n, a.Argv, a.Cwd, a.Env)
		return wire.JSON(execView(result, b.executionError("guest_command", err))), nil
	case "vm_info":
		info, err := b.info(ctx, n.UUID)
		if err != nil {
			return nil, err
		}
		fields := map[string]string{}
		for _, key := range []string{"VMState", "VMStateChangeTime", "memory", "cpus", "GuestAdditionsVersion", "GuestAdditionsRunLevel"} {
			fields[key] = info[key]
		}
		return wire.JSON(fields), nil
	case "start":
		raw, e = b.start(ctx, n)
	case "poweroff", "pause", "resume", "reset":
		raw, e = control(a.Action)
	case "shutdown":
		raw, e = control("acpipowerbutton")
		if e == nil {
			e = b.waitPowerOff(ctx, n)
		}
	case "network_link":
		raw, e = control("setlinkstate1", on(a.Enabled))
	case "reboot":
		raw, e = b.guestExec(ctx, t, n, "reboot")
	case "command_start":
		var p string
		p, e = b.requestFile(ctx, t, n, a.Spec.ID+".json", a.Spec)
		if e == nil {
			raw, e = b.guestExec(ctx, t, n, "run", "--root", c.WorkDir, "--request", p)
		}
	case "command_input":
		var p string
		p, e = b.requestFile(ctx, t, n, a.CommandID+"-input-"+strconv.Itoa(a.Input.Sequence)+".json", a.Input)
		if e == nil {
			raw, e = b.guestExec(ctx, t, n, "input", "--root", c.WorkDir, "--id", a.CommandID, "--request", p)
		}
	case "command_cancel":
		raw, e = b.guestExec(ctx, t, n, "cancel", "--root", c.WorkDir, "--id", a.CommandID)
	case "command_poll":
		raw, e = b.guestExec(ctx, t, n, "read", "--root", c.WorkDir, "--id", a.CommandID, "--stdout-offset", strconv.FormatInt(a.StdoutOffset, 10), "--stderr-offset", strconv.FormatInt(a.StderrOffset, 10), "--limit", strconv.Itoa(wire.CommandReadLimit))
		return raw, e
	case "file_put":
		e = b.copyTo(ctx, t, n, a.Local, a.Path)
	case "file_get":
		out := guestJoin(c.WorkDir, "archive-"+wire.ID()+".tar")
		if _, e = b.guestExec(ctx, t, n, "archive", "--path", a.Path, "--output", out); e == nil {
			e = b.copyFrom(ctx, t, n, out, a.Local)
		}
	case "diagnostics":
		raw, e = b.run(ctx, "showvminfo", n.UUID, "--log", "0")
		if e == nil {
			e = b.publish(a.Local, strings.NewReader(string(raw)))
		}
	case "screenshot":
		stage, err := os.MkdirTemp(b.runtime(t), "screen-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(stage)
		file := filepath.Join(stage, "screen.png")
		if _, e = control("screenshotpng", file, strconv.Itoa(a.Screen)); e == nil {
			f, err := os.Open(file)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			e = b.publish(a.Local, f)
		}
	case "display":
		if a.AutoResize != nil {
			_, e = b.run(ctx, "setextradata", n.UUID, "GUI/AutoresizeGuest", strconv.FormatBool(*a.AutoResize))
		}
		if e == nil && a.Width > 0 {
			raw, e = control("setvideomodehint", strconv.Itoa(a.Width), strconv.Itoa(a.Height), "32", strconv.Itoa(a.Screen))
		}
	case "input_scancodes":
		if len(a.Scancodes) == 0 {
			return nil, wire.Fail("invalid_argument", "No scancodes")
		}
		raw, e = control(append([]string{"keyboardputscancode"}, a.Scancodes...)...)
	case "input_move", "input_click", "input_drag", "input_scroll", "input_release":
		values := []int{a.Screen, a.X, a.Y, a.EndX, a.EndY, a.Button, a.DZ, a.DW, 0}
		if a.Relative {
			values[8] = 1
		}
		args := []string{}
		for _, v := range values {
			args = append(args, strconv.Itoa(v))
		}
		raw, e = b.native(ctx, t, n, strings.TrimPrefix(a.Action, "input_"), args...)
		if e == nil {
			return raw, nil
		}
	case "media_camera":
		device, ok := c.Cameras[a.Device]
		if !ok {
			return nil, wire.Fail("policy_denied", "Camera is not configured")
		}
		action := "detach"
		if a.Enabled {
			action = "attach"
		}
		raw, e = control("webcam", action, device)
	case "media_audio_input":
		raw, e = control("audioin", on(a.Enabled))
	case "media_audio_output":
		raw, e = control("audioout", on(a.Enabled))
	case "capture_start", "capture_stop":
		e = b.capture(ctx, t, n, a)
	default:
		return nil, wire.Fail("invalid_argument", "Unsupported backend action")
	}
	_ = raw
	if e != nil {
		return nil, e
	}
	return wire.JSON(map[string]any{"delivered": true}), nil
}
func (b *VBoxBackend) recoverRouter(ctx context.Context, t *Task) error {
	n := t.Router
	info, e := b.info(ctx, n.UUID)
	if e != nil {
		return e
	}
	if e = b.owned(t, n, info); e != nil {
		return e
	}
	switch info["VMState"] {
	case "poweroff", "aborted", "saved":
		_, e = b.run(ctx, "startvm", n.UUID, "--type", "headless")
	case "paused":
		_, e = b.run(ctx, "controlvm", n.UUID, "resume")
	case "running":
	default:
		return wire.Fail("resource_busy", "Router is changing power state")
	}
	if e != nil {
		return e
	}
	// The attached seed regenerates credentials after each Live ISO boot.
	// Reinitialize through Guest Control with public egress initially disabled.
	for {
		remote, err := b.requestFile(ctx, t, n, "recovery.json", b.seedData(t))
		if err == nil {
			_, err = b.guestExec(ctx, t, n, "recover", "--request", remote)
		}
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
func (b *VBoxBackend) Network(ctx context.Context, t *Task, protected []string, recover bool) (string, error) {
	if recover {
		reportNetworkProgress(ctx, "router_recovery")
		if e := b.recoverRouter(ctx, t); e != nil {
			return "", networkStage(e, "router_recovery")
		}
	}
	reportNetworkProgress(ctx, "router_probe")
	raw, err := b.guestExec(ctx, t, t.Router, "probe")
	if err != nil {
		return "", networkStage(err, "router_probe")
	}
	var status struct {
		Ready  bool   `json:"ready"`
		BootID string `json:"boot_id"`
	}
	if json.Unmarshal(raw, &status) != nil || !status.Ready || !wire.UUIDPattern.MatchString(status.BootID) {
		return "", networkStage(wire.Fail("network_degraded", "Router identity could not be verified"), "router_identity")
	}
	if !recover && t.RouterBootID != "" && t.RouterBootID != status.BootID {
		return "", networkStage(wire.Fail("network_degraded", "Router restarted; explicit network_recover is required"), "router_identity")
	}
	p := taskPolicy(t, b.Config.VirtualBox.Network.WAN, b.Config.VirtualBox.Network.LeaseSeconds, protected)
	if e := p.Validate(); e != nil {
		return "", e
	}
	reportNetworkProgress(ctx, "policy_upload")
	remote, e := b.requestFile(ctx, t, t.Router, "policy.json", p)
	if e != nil {
		return "", networkStage(e, "policy_upload")
	}
	reportNetworkProgress(ctx, "policy_apply")
	_, e = b.guestExec(ctx, t, t.Router, "apply", "--request", remote)
	return status.BootID, networkStage(e, "policy_apply")
}
func (b *VBoxBackend) Endpoint(ctx context.Context, t *Task, e *Endpoint, open bool) error {
	name := "vm-test-" + e.ID
	args := []string{"controlvm", t.Router.UUID, "natpf2"}
	info, err := b.info(ctx, t.Router.UUID)
	if err != nil {
		return err
	}
	for k, v := range info {
		if strings.HasPrefix(k, "Forwarding(") && strings.Split(v, ",")[0] == name {
			if _, err = b.run(ctx, append(args, "delete", name)...); err != nil {
				return err
			}
		}
	}
	if !open {
		return nil
	}
	ip, _ := network.WANAddresses(b.Config.VirtualBox.Network.WAN)
	_, err = b.run(ctx, append(args, fmt.Sprintf("%s,%s,127.0.0.1,%d,%s,%d", name, e.Protocol, e.LoopbackPort, ip, e.RouterPort))...)
	return err
}
func (b *VBoxBackend) capture(ctx context.Context, t *Task, n *Node, a NodeAction) error {
	c := a.Capture
	file := filepath.Join(b.runtime(t), "capture-"+c.ID)
	run := func(args ...string) error {
		_, e := b.run(ctx, append([]string{"controlvm", n.UUID}, args...)...)
		return e
	}
	start := a.Action == "capture_start"
	switch c.Kind {
	case "nic":
		if start {
			if e := run("nictracefile1", file); e != nil {
				return e
			}
			return run("nictrace1", "on")
		}
		if e := run("nictrace1", "off"); e != nil {
			return e
		}
	case "video":
		if start {
			if e := run("recording", "filename", file+".webm"); e != nil {
				return e
			}
			if e := run("recording", "screens", "0"); e != nil {
				return e
			}
			if e := run("recording", "maxfilesize", "1024"); e != nil {
				return e
			}
			return run("recording", "start")
		}
		if e := run("recording", "stop"); e != nil {
			return e
		}
		file += ".webm"
	case "metrics":
		if start {
			_, e := b.run(ctx, "metrics", "setup", "--period", "1", "--samples", "3600", n.UUID, "CPU/*,RAM/*,Net/*,Disk/*,Guest/*")
			return e
		}
		raw, e := b.run(ctx, "metrics", "query", n.UUID, "CPU/*,RAM/*,Net/*,Disk/*,Guest/*")
		if e != nil {
			return e
		}
		if _, e = b.run(ctx, "metrics", "disable", n.UUID, "CPU/*,RAM/*,Net/*,Disk/*,Guest/*"); e != nil {
			return e
		}
		return b.publish(a.Local, bytesReader(raw))
	}
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	return b.publish(a.Local, f)
}
func (b *VBoxBackend) Nix(ctx context.Context, action string, t *Task) (json.RawMessage, error) {
	if b.Worker == nil {
		return nil, wire.Fail("worker_unavailable", "Configure and start the Nix worker to use this operation")
	}
	req := map[string]any{"task_id": t.ID}
	if action == "extend" {
		req["deadline"] = t.Deadline
	}
	if action == "start" {
		req["derivation"] = t.Derivation
		req["system"] = t.System
		req["timeout_seconds"] = t.TimeoutSeconds
		req["cache"] = b.CacheURL(t.CacheID)
	}
	raw, err := b.Worker.Call(ctx, action, req)
	if err != nil {
		return nil, err
	}
	if action == "status" {
		return b.workerEvidence(ctx, t, raw, false)
	}
	if action == "cleanup" {
		for {
			var status struct {
				Cleanup string `json:"cleanup"`
			}
			if err = json.Unmarshal(raw, &status); err != nil {
				return nil, err
			}
			if status.Cleanup != "pending" {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			raw, err = b.Worker.Call(ctx, "status", req)
			if err != nil {
				return nil, err
			}
		}
		collected, e := b.workerEvidence(ctx, t, raw, true)
		if e == nil {
			return collected, nil
		}
		var status struct {
			Cleanup   string             `json:"cleanup"`
			Execution *wire.NixExecution `json:"execution"`
		}
		if json.Unmarshal(raw, &status) == nil && status.Cleanup == "complete" {
			return wire.JSON(map[string]any{"cleanup": "complete", "evidence_error": true, "execution": status.Execution}), nil
		}
		return nil, e
	}
	return raw, nil
}

func taskPolicy(t *Task, wan string, seconds int, protected []string) network.Policy {
	p := network.Policy{LAN: t.LAN, WAN: wan, DHCP: t.DHCP, Internet: t.Internet, DNS: t.DNS, Protected: protected, LeaseSeconds: seconds}
	for _, n := range t.Nodes {
		p.Nodes = append(p.Nodes, network.Lease{Name: n.Name, MAC: n.MAC, IPv4: n.IPv4})
	}
	for _, e := range t.Endpoints {
		if e.State != "closed" {
			n, _ := t.Node(e.NodeID)
			p.Entries = append(p.Entries, network.Entry{Protocol: e.Protocol, Port: e.RouterPort, IPv4: n.IPv4, GuestPort: e.GuestPort})
		}
	}
	return p
}
