package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type availabilityRunner struct{ running bool }

func (r availabilityRunner) Run(_ context.Context, _ string, args []string, _ io.Reader) ([]byte, error) {
	switch strings.Join(args, " ") {
	case "--version":
		return []byte("7.2.20"), nil
	case "list runningvms":
		if r.running {
			return []byte(`"fixture" {11111111-1111-4111-8111-111111111111}`), nil
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected availability command")
	}
}

type idleWorker struct{}

func (idleWorker) Call(context.Context, string, any) (json.RawMessage, error) {
	return wire.JSON(map[string]any{"busy": false}), nil
}

func TestRouterISOEnablesBackendAndParticipatesInNixExclusion(t *testing.T) {
	c := testConfig(t)
	c.VirtualBox.Templates = nil
	b := &VBoxBackend{Config: c, Runner: availabilityRunner{running: true}, Worker: idleWorker{}}
	if reason, err := b.Available(context.Background(), "lab"); err != nil || reason != "" {
		t.Fatal("router ISO did not enable VirtualBox", reason, err)
	}
	if reason, err := b.Available(context.Background(), "nixos"); err != nil || reason != "virtualbox_running" {
		t.Fatal("Nix would overlap a running VirtualBox VM", reason, err)
	}
	b.Config.VirtualBox.Router.ISO = ""
	if reason, err := b.Available(context.Background(), "lab"); err != nil || reason != "virtualbox_disabled" {
		t.Fatal("empty backend configuration was enabled", reason, err)
	}
}

func TestRouterOnlyLabLifecycle(t *testing.T) {
	for _, failNetwork := range []bool{false, true} {
		t.Run(fmt.Sprintf("network_failure=%t", failNetwork), func(t *testing.T) {
			c := testConfig(t)
			c.VirtualBox.Templates = nil
			f := &fakeBackend{gui: "auto", failNetwork: failNetwork}
			s, err := New(c, f)
			if err != nil {
				t.Fatal(err)
			}
			s.Start()
			defer s.Close()
			op := successful(t, s, "lab_create", LabRequest{RequestID: "router-only", Nodes: []NodeRequest{}})
			id := op.Result.(map[string]any)["id"].(string)
			waitFor(t, func() bool {
				s.mu.Lock()
				defer s.mu.Unlock()
				lab, _ := s.task(id)
				return lab.State == "running" || lab.Terminal()
			})
			if !failNetwork {
				f.mu.Lock()
				gui := f.gui
				f.mu.Unlock()
				if gui != "auto" {
					t.Fatal("router-only lab changed the global GUI policy")
				}
				off := false
				successful(t, s, "network_set", NetworkRequest{TaskChange: TaskChange{Change: Change{"router-network"}, TaskID: TaskID{id}}, Internet: &off, DHCP: &off, DNS: []string{"9.9.9.9"}})
				successful(t, s, "task_finish", FinishRequest{TaskChange: TaskChange{Change: Change{"router-finish"}, TaskID: TaskID{id}}, Conclusion: "passed", Summary: "Router configuration verified"})
			}
			waitFor(t, func() bool {
				s.mu.Lock()
				defer s.mu.Unlock()
				lab, _ := s.task(id)
				return lab.Cleanup == "complete" && s.active == ""
			})
			s.mu.Lock()
			lab, _ := s.task(id)
			snapshot := clone(lab)
			s.mu.Unlock()
			if len(snapshot.Nodes) != 0 || !snapshot.Router.Cleaned || snapshot.GUI != nil {
				t.Fatal("router-only ownership or cleanup differs", snapshot)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.created) != 1 || len(f.deleted) != 1 || f.created[0] != f.deleted[0] {
				t.Fatal("router VM was not released", f.created, f.deleted)
			}
		})
	}
}

// A stateful adapter exercises the complete diskless creation/deletion path.
// It refuses incompatible boot settings, a second SATA controller and deletion
// with borrowed media attached.
type liveRouterRunner struct {
	info           map[string]string
	uuid, cfg, iso string
	registered     bool
	controllers    int
	calls          [][]string
}

func (r *liveRouterRunner) Run(_ context.Context, exe string, args []string, _ io.Reader) ([]byte, error) {
	r.calls = append(r.calls, append([]string{}, args...))
	option := func(key string) string {
		for i, v := range args {
			if v == key && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	if exe == "/fake/xorriso" {
		return nil, os.WriteFile(option("-o"), []byte("synthetic seed"), 0600)
	}
	switch args[0] {
	case "createvm":
		r.registered = true
		r.info = map[string]string{"UUID": r.uuid, "CfgFile": r.cfg, "VMState": "poweroff", "ostype": option("--ostype")}
	case "modifyvm":
		aliases := map[string]string{"clipboard-mode": "clipboard", "drag-and-drop": "draganddrop", "teleporter": "teleporterenabled", "usb-ohci": "usb", "usb-ehci": "ehci", "usb-xhci": "xhci", "audio-in": "audio_in", "audio-out": "audio_out", "accelerate-3d": "accelerate3d"}
		for i := 2; i+1 < len(args); i += 2 {
			k := strings.TrimPrefix(args[i], "--")
			if a, ok := aliases[k]; ok {
				k = a
			}
			r.info[k] = args[i+1]
		}
	case "storagectl":
		r.controllers++
		if r.controllers > 1 {
			return nil, fmt.Errorf("one AHCI controller is supported")
		}
		r.info["storagecontrollername0"] = option("--name")
	case "storageattach":
		key := option("--storagectl") + "-" + option("--port") + "-" + option("--device")
		r.info[key] = option("--medium")
		r.info[option("--storagectl")+"-tempeject-"+option("--port")+"-"+option("--device")] = "off"
	case "showvminfo":
		var out strings.Builder
		for k, v := range r.info {
			fmt.Fprintf(&out, "%s=%q\n", k, v)
		}
		if r.info["VMState"] == "running" {
			out.WriteString("VideoMode=\"800,600,32\"@0,0 1\n")
		}
		return []byte(out.String()), nil
	case "startvm":
		if r.info["ostype"] != "Linux_64" || r.info["firmware"] != "efi64" || r.info["x86-long-mode"] != "on" || r.info["ioapic"] != "on" {
			return nil, fmt.Errorf("router ISO requires a 64-bit EFI guest with IOAPIC")
		}
		r.info["VMState"] = "running"
	case "controlvm":
		r.info["VMState"] = "poweroff"
	case "guestcontrol":
		return []byte(`{"ready":true,"boot_id":"11111111-1111-4111-8111-111111111111"}`), nil
	case "list":
		if args[1] == "vms" && r.registered {
			return []byte(`"fixture" {` + r.uuid + "}\n"), nil
		}
	case "unregistervm":
		for _, v := range r.info {
			if v == r.iso {
				return nil, fmt.Errorf("borrowed ISO still attached during delete")
			}
		}
		r.registered = false
	default:
		return nil, fmt.Errorf("unexpected command %s", args[0])
	}
	return nil, nil
}

func TestDisklessRouterOwnsVMAndSeedButNeverBootISO(t *testing.T) {
	b := &VBoxBackend{Config: testConfig(t)}
	task := newTask("lab", 60)
	task.LAN = b.Config.VirtualBox.Network.LAN
	task.RouterPassword = wire.ID() + wire.ID()
	realISO := filepath.Join(t.TempDir(), "router.iso")
	if err := os.WriteFile(realISO, []byte("borrowed ISO"), 0644); err != nil {
		t.Fatal(err)
	}
	task.RouterISO = filepath.Join(t.TempDir(), "result.iso")
	if err := os.Symlink(realISO, task.RouterISO); err != nil {
		t.Fatal(err)
	}
	task.Router = &Node{ID: wire.ID(), UUID: wire.UUID(), MAC: "08:00:27:01:02:03", NodeRequest: NodeRequest{Name: "router", CPUs: 1, MemoryMiB: 256}, Intent: true}
	r := &liveRouterRunner{uuid: task.Router.UUID, cfg: filepath.Join(b.vmDir(task, task.Router), "router.vbox"), iso: realISO}
	b.Runner = r
	if err := b.Prepare(context.Background(), task, task.Router); err != nil {
		t.Fatal(err)
	}
	if r.info["memory"] != "256" || r.info["VMTestMedia-1-0"] != filepath.Join(b.runtime(task), "seed.iso") {
		t.Fatal("router sizing or retained seed", r.info)
	}
	if r.info["nat-localhostreachable2"] != "on" {
		t.Fatal("UDP forwarding replies cannot return to the host loopback relay")
	}
	if r.info["VMTestMedia-0-0"] != realISO {
		t.Fatal("Nix-style ISO symlink was not resolved")
	}
	if _, err := os.Stat(filepath.Join(b.runtime(task), "seed.iso")); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(context.Background(), task, task.Router); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(task.RouterISO)
	if err != nil || string(data) != "borrowed ISO" || r.registered {
		t.Fatal("boot ISO or registration ownership violated", err)
	}
	for _, args := range r.calls {
		if oneOf(args[0], "clonevm", "createmedium") {
			t.Fatal("router used baseline or persistent disk")
		}
	}
}

func TestMachineInfoRunningDisplayAndOwnershipFields(t *testing.T) {
	const display = `"800,600,32"@0,0 1`
	info, err := machineInfo([]byte("UUID=\"11111111-1111-4111-8111-111111111111\"\nCfgFile=\"/owned/router.vbox\"\nVMState=\"running\"\nVideoMode=" + display + "\n"))
	if err != nil || info["VMState"] != "running" || info["CfgFile"] != "/owned/router.vbox" || info["VideoMode"] != display {
		t.Fatal("running VM metadata could not be read", info, err)
	}
	for _, raw := range []string{
		"UUID=\"first\"\nUUID=\"second\"\n",
		"CfgFile=\"/owned/router.vbox\"\nCfgFile=\"/other/router.vbox\"\n",
		"CfgFile=\"/owned/router.vbox\"@0,0 1\n",
		"VMState=\"running\n",
	} {
		if _, err := machineInfo([]byte(raw)); err == nil {
			t.Fatalf("ambiguous or malformed ownership metadata accepted: %q", raw)
		}
	}
}
