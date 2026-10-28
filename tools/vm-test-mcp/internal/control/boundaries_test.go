package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestMaliciousNarinfoAndCacheSeal(t *testing.T) {
	name := strings.Repeat("0", 32) + ".narinfo"
	nar := []byte("synthetic NAR bytes; daemon validates NAR structure")
	h := sha256.Sum256(nar)
	hash := hex.EncodeToString(h[:])
	valid := "StorePath: /nix/store/" + strings.Repeat("0", 32) + "-fixture.drv\nURL: nar/fixture.nar\nCompression: none\nFileHash: sha256:" + nixHash(hash) + "\nFileSize: 49\nNarHash: sha256:" + nixHash(hash) + "\nNarSize: 49\nReferences: \n"
	for _, bad := range []string{strings.Replace(valid, "nar/fixture.nar", "../../etc/passwd", 1), strings.Replace(valid, "nar/fixture.nar", "http://127.0.0.1/private", 1), valid + "URL: nar/evil.nar\n", strings.Replace(valid, "fixture.drv", "../../escape", 1), strings.Replace(valid, "References: ", "References: ../../../tmp ", 1)} {
		if _, e := parseNarInfo(name, []byte(bad)); e == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	s, _ := testServer(t)
	v := successful(t, s, "upload_create", TransferRequest{Change: Change{"cache"}, Kind: "cache"}).Result.(map[string]any)
	id := v["id"].(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.transfers[id]
	_, _, e := s.root.Write(filepath.Join("transfers", id, name), strings.NewReader(valid), 65536)
	if e != nil {
		t.Fatal(e)
	}
	cache.Objects[name] = Object{Size: int64(len(valid)), File: name}
	if e = s.seal(cache); e == nil {
		t.Fatal("missing referenced NAR accepted")
	}
}

type blockedPrepare struct{ fakeBackend }

func (f *blockedPrepare) Prepare(ctx context.Context, _ *Task, _ *Node) error {
	<-ctx.Done()
	return ctx.Err()
}
func TestDeadlineIncludesBackendPreparation(t *testing.T) {
	c := testConfig(t)
	c.Tasks.DefaultTimeout = config.Duration(1 * time.Second)
	s, e := New(c, &blockedPrepare{})
	if e != nil {
		t.Fatal(e)
	}
	s.Start()
	defer s.Close()
	lab := createLab(t, s, "slow-prepare")
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		v, _ := s.task(lab.ID)
		return v.State == "timed_out" && v.Cleanup == "complete"
	})
}

type mediumRunner struct{ bad bool }

func (r mediumRunner) Run(_ context.Context, _ string, args []string, _ io.Reader) ([]byte, error) {
	if args[0] != "showmediuminfo" || len(args) != 3 || args[1] != "disk" {
		return nil, fmt.Errorf("unexpected medium command")
	}
	format := "VDI"
	if r.bad {
		format = "RAW"
	}
	return []byte("Storage format: " + format + "\nParent UUID: 11111111-1111-4111-8111-111111111111\nType: normal (differencing)\nLocation: " + args[2] + "\n"), nil
}
func TestDiskWhitelistUsesSupportedMediumOutput(t *testing.T) {
	b := &VBoxBackend{Config: testConfig(t), Runner: mediumRunner{}}
	task := newTask("lab", 60)
	node := &Node{UUID: wire.UUID()}
	info := map[string]string{"storagecontrollername0": "SATA", "SATA-0-0": filepath.Join(b.vmDir(task, node), "Snapshots", "disk.vdi")}
	if e := b.sanitize(context.Background(), task, node, info); e != nil {
		t.Fatal(e)
	}
	b.Runner = mediumRunner{bad: true}
	if e := b.sanitize(context.Background(), task, node, info); e == nil {
		t.Fatal("raw medium accepted")
	}
	b.Runner = mediumRunner{}
	info["SATA-0-0"] = "/outside/disk.vdi"
	if e := b.sanitize(context.Background(), task, node, info); e == nil {
		t.Fatal("foreign disk accepted")
	}
}

type seedRunner struct {
	listing string
	closed  []string
}

func (r *seedRunner) Run(_ context.Context, _ string, args []string, _ io.Reader) ([]byte, error) {
	if len(args) == 2 && args[0] == "list" && args[1] == "dvds" {
		return []byte(r.listing), nil
	}
	if len(args) == 3 && args[0] == "closemedium" && args[1] == "dvd" {
		r.closed = append(r.closed, args[2])
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected medium command")
}

func TestSeedCleanupClosesOnlyOwnedMedium(t *testing.T) {
	r := &seedRunner{}
	b := &VBoxBackend{Config: testConfig(t), Runner: r}
	task := newTask("lab", 60)
	owned, foreign := wire.UUID(), wire.UUID()
	r.listing = "UUID: " + foreign + "\nLocation: /outside/seed.iso\n\nUUID: " + owned + "\nLocation: " + filepath.Join(b.runtime(task), "seed.iso") + "\n"
	if e := b.closeSeed(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	if len(r.closed) != 1 || r.closed[0] != owned {
		t.Fatal(r.closed)
	}
	r.closed = nil
	r.listing = "UUID: invalid\nLocation: " + filepath.Join(b.runtime(task), "seed.iso") + "\n"
	if e := b.closeSeed(context.Background(), task); e == nil || len(r.closed) != 0 {
		t.Fatal("unverified medium identity accepted")
	}
}

type routerRecoveryRunner struct {
	uuid, configPath, state string
	calls                   []string
}

func (r *routerRecoveryRunner) Run(_ context.Context, _ string, args []string, _ io.Reader) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	switch args[0] {
	case "showvminfo":
		return []byte(fmt.Sprintf("UUID=%q\nCfgFile=%q\nVMState=%q\n", r.uuid, r.configPath, r.state)), nil
	case "startvm", "controlvm":
		r.state = "running"
	case "guestcontrol":
		if r.state != "running" {
			return nil, fmt.Errorf("router unavailable")
		}
	default:
		return nil, fmt.Errorf("unexpected recovery command")
	}
	return []byte(`{"ready":true,"boot_id":"11111111-1111-4111-8111-111111111111"}`), nil
}

func TestRouterRestartRequiresExplicitRecoveryAndOwnership(t *testing.T) {
	for _, power := range []string{"poweroff", "paused"} {
		t.Run(power, func(t *testing.T) {
			b := &VBoxBackend{Config: testConfig(t)}
			task := newTask("lab", 60)
			task.LAN = b.Config.VirtualBox.Network.LAN
			task.DNS = b.Config.VirtualBox.Network.DNS
			task.Router = &Node{ID: wire.ID(), UUID: wire.UUID(), MAC: "08:00:27:12:34:56"}
			task.RouterPassword = wire.ID() + wire.ID()
			if e := os.MkdirAll(b.runtime(task), 0700); e != nil {
				t.Fatal(e)
			}
			r := &routerRecoveryRunner{uuid: task.Router.UUID, configPath: filepath.Join(b.vmDir(task, task.Router), "router.vbox"), state: power}
			b.Runner = r
			if _, e := b.Network(context.Background(), task, nil, false); e == nil || r.state != power {
				t.Fatal("heartbeat restarted router", e, r.calls)
			}
			r.calls = nil
			if _, e := b.Network(context.Background(), task, nil, true); e != nil {
				t.Fatal(e)
			}
			calls := strings.Join(r.calls, "\n")
			restored, applied := strings.Index(calls, "-- recover --request"), strings.Index(calls, "-- apply --request")
			if r.state != "running" || restored < 0 || applied <= restored {
				t.Fatal("policy applied before router initialization", calls)
			}
			r.configPath, r.state, r.calls = "/outside/foreign.vbox", "poweroff", nil
			if _, e := b.Network(context.Background(), task, nil, true); e == nil || r.state != "poweroff" || len(r.calls) != 1 {
				t.Fatal("foreign VM accepted for recovery", e, r.calls)
			}
		})
	}
}

type lostInput struct {
	fakeBackend
	lost bool
}

func (f *lostInput) Action(ctx context.Context, t *Task, n *Node, a NodeAction) (json.RawMessage, error) {
	f.mu.Lock()
	if a.Action == "command_input" && !f.lost {
		f.lost = true
		f.mu.Unlock()
		return nil, fmt.Errorf("guest channel failed")
	}
	f.mu.Unlock()
	return f.fakeBackend.Action(ctx, t, n, a)
}
func TestPersistedInputRetriesAfterDeliveryFailure(t *testing.T) {
	f := &lostInput{}
	s, e := New(testConfig(t), f)
	if e != nil {
		t.Fatal(e)
	}
	s.Start()
	defer s.Close()
	lab := createLab(t, s, "stdin-outbox")
	a := StartRequest{NodeChange: NodeChange{Change: Change{"start-input"}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: lab.Nodes[0].ID}}, Argv: []string{"reader"}}
	id := successful(t, s, "command_start", a).Result.(map[string]any)["id"].(string)
	op := successful(t, s, "command_input", InputRequest{Change: Change{"persist-input"}, CommandRequest: CommandRequest{TaskID: TaskID{lab.ID}, CommandID: id}, Sequence: 0, Data: "ordered", EOF: true})
	if op.Result.(map[string]any)["delivery"] != "pending" {
		t.Fatal(op)
	}
	waitFor(t, func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.input) > 0 })
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.input[0].Sequence != 0 || f.input[0].Data != "ordered" || !f.input[0].EOF {
		t.Fatal(f.input)
	}
}
func TestDeadlineExtensionNetworkAndPurge(t *testing.T) {
	s, f := testServer(t)
	lab := createLab(t, s, "network")
	s.mu.Lock()
	task, _ := s.task(lab.ID)
	old := *task.Deadline
	s.mu.Unlock()
	successful(t, s, "lab_extend", ExtendRequest{TaskChange: TaskChange{Change: Change{"extend"}, TaskID: TaskID{lab.ID}}, Seconds: 10, Reason: "Complete network recovery assertions"})
	s.mu.Lock()
	if !task.Deadline.Equal(old.Add(10*time.Second)) || task.Hold == nil || !task.Hold.Until.Equal(*task.Deadline) || task.Hold.Reason != "Complete network recovery assertions" {
		t.Fatal("wrong extended deadline")
	}
	s.mu.Unlock()
	f.mu.Lock()
	f.failNetwork = true
	f.mu.Unlock()
	o := operation(t, s, "network_set", NetworkRequest{TaskChange: TaskChange{Change: Change{"network-off"}, TaskID: TaskID{lab.ID}}, Internet: new(false)})
	if o.State != "failed" {
		t.Fatal(o)
	}
	s.mu.Lock()
	if task.Network != "degraded" {
		t.Fatal(task.Network)
	}
	s.mu.Unlock()
	f.mu.Lock()
	f.failNetwork = false
	f.mu.Unlock()
	o = operation(t, s, "network_set", NetworkRequest{TaskChange: TaskChange{Change: Change{"no-auto-recovery"}, TaskID: TaskID{lab.ID}}, Internet: new(true)})
	if o.Error.Code != "network_degraded" {
		t.Fatal(o)
	}
	successful(t, s, "network_recover", TaskChange{Change: Change{"recover"}, TaskID: TaskID{lab.ID}})
	s.mu.Lock()
	deadline := time.Now().Add(-time.Second)
	task.Deadline = &deadline
	s.mu.Unlock()
	waitFor(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return task.State == "timed_out" && task.Cleanup == "complete"
	})
	successful(t, s, "execution_delete", TaskChange{Change: Change{"purge"}, TaskID: TaskID{lab.ID}})
	s.mu.Lock()
	defer s.mu.Unlock()
	if !task.Purged || task.State != "timed_out" {
		t.Fatal("lost summary")
	}
}
func TestRestartRetainsOperationReceipt(t *testing.T) {
	c := testConfig(t)
	f := &fakeBackend{}
	s, e := New(c, f)
	if e != nil {
		t.Fatal(e)
	}
	op := &Operation{ID: wire.ID(), RequestID: "uncertain", Tool: "node_input", State: "pending", Created: time.Now().UTC()}
	if e = s.db.Save(context.Background(), state.Document{Kind: "operation", ID: op.ID, Value: op}); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = New(c, f)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	value, e := s.accept(context.Background(), "operation_get", wire.JSON(OperationRequest{op.ID}))
	if e != nil {
		t.Fatal(e)
	}
	if value.(*Operation).State != "uncertain" {
		t.Fatal(value)
	}
}

type recordingRunner struct {
	mu    sync.Mutex
	calls [][]string
}

func TestVideoCaptureCollectsVBox72ScreenFile(t *testing.T) {
	b := &VBoxBackend{Config: testConfig(t), Runner: &recordingRunner{}}
	task := newTask("lab", 60)
	capture := &Capture{ID: wire.ID(), Kind: "video"}
	if err := os.MkdirAll(b.runtime(task), 0700); err != nil {
		t.Fatal(err)
	}
	// This is the path reported by the real VirtualBox recording log, distinct
	// from the base filename passed to `controlvm recording filename`.
	for screen, content := range []string{"first screen video", "second screen video"} {
		name := filepath.Join(b.runtime(task), fmt.Sprintf("capture-%s-screen%d.webm", capture.ID, screen))
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(b.Config.Service.StateDir, "tasks", task.ID, "artifacts", wire.ID())
	if err := b.capture(context.Background(), task, &Node{UUID: wire.UUID()}, NodeAction{Action: "capture_stop", Capture: capture, Local: dest}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "first screen video" {
		t.Fatal(string(got), err)
	}
}

type downloadRunner struct {
	source, outside string
	symlink         bool
}

func (r downloadRunner) Run(_ context.Context, _ string, args []string, _ io.Reader) ([]byte, error) {
	if len(args) < 3 || args[0] != "guestcontrol" || args[2] != "copyfrom" {
		return nil, fmt.Errorf("unexpected download command")
	}
	for i, arg := range args {
		if arg != "--" {
			continue
		}
		if len(args[i+1:]) != 2 || args[i+1] != r.source {
			return nil, fmt.Errorf("copyfrom requires the source and an explicit destination file")
		}
		destination := args[i+2]
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			return nil, fmt.Errorf("destination must not exist before copying")
		}
		if r.symlink {
			return nil, os.Symlink(r.outside, destination)
		}
		return nil, os.WriteFile(destination, []byte(`{"state":"exited","exit_code":0}`), 0600)
	}
	return nil, fmt.Errorf("missing argument separator")
}

func TestGuestDownloadUsesExplicitFileAndRejectsSymlinks(t *testing.T) {
	for _, source := range []string{`C:\ProgramData\vm-test\work\command\status.json`, "/var/lib/vm-test/command/status.json"} {
		for _, symlink := range []bool{false, true} {
			t.Run(fmt.Sprintf("source=%s/symlink=%t", source, symlink), func(t *testing.T) {
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, []byte("must not be published"), 0600); err != nil {
					t.Fatal(err)
				}
				b := &VBoxBackend{Config: testConfig(t), Runner: downloadRunner{source: source, outside: outside, symlink: symlink}}
				task := newTask("lab", 60)
				node := &Node{UUID: wire.UUID(), NodeRequest: NodeRequest{Template: "windows"}}
				if err := os.MkdirAll(b.runtime(task), 0700); err != nil {
					t.Fatal(err)
				}
				local := filepath.Join(b.Config.Service.StateDir, "status.json")
				err := b.copyFrom(context.Background(), task, node, source, local)
				if symlink {
					if err == nil {
						t.Fatal("guest download followed a symlink")
					}
					if _, err := os.Stat(local); !os.IsNotExist(err) {
						t.Fatal("unsafe download was published")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(local)
					if err != nil || string(data) != `{"state":"exited","exit_code":0}` {
						t.Fatal("download did not publish the status", string(data), err)
					}
				}
				entries, err := os.ReadDir(b.runtime(task))
				if err != nil || len(entries) != 0 {
					t.Fatal("temporary download or credentials were retained", entries, err)
				}
			})
		}
	}
}

func TestHostCommandPreservesUnicodeLocale(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LC_ALL", "C")
	t.Setenv("LANGUAGE", "fr")
	// Bash counts characters using the C library locale, as VirtualBox does
	// when decoding host argv. An ASCII locale instead counts six UTF-8 bytes.
	out, err := (hostexec.Exec{}).Run(context.Background(), bash, []string{"-c", `value=$1; printf '%s\n' "${#value}" "$LANGUAGE"`, "fixture", "中文"}, nil)
	if err != nil || string(out) != "2\nC\n" {
		t.Fatalf("host command did not preserve Unicode and English diagnostics: %q, %v", out, err)
	}
}

func (r *recordingRunner) Run(_ context.Context, _ string, args []string, _ io.Reader) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, append([]string{}, args...))
	return []byte(`{}`), nil
}
func TestVBoxArgumentsAreStructuredAndRouterIsPrivate(t *testing.T) {
	c := testConfig(t)
	r := &recordingRunner{}
	b := &VBoxBackend{Config: c, Runner: r}
	task := newTask("lab", 60)
	task.Router = &Node{ID: wire.ID()}
	n := &Node{ID: wire.ID(), UUID: wire.UUID(), NodeRequest: NodeRequest{Template: "windows"}}
	task.Nodes = []*Node{n}
	os.MkdirAll(b.runtime(task), 0700)
	payload := `C:\guest\--target-directory=$(touch /host-owned);.txt`
	_, e := b.Action(context.Background(), task, n, NodeAction{Action: "file_put", Path: payload, Local: "/safe/service-file"})
	if e != nil {
		t.Fatal(e)
	}
	r.mu.Lock()
	args := r.calls[len(r.calls)-1]
	r.mu.Unlock()
	found := false
	for _, v := range args {
		if v == payload {
			found = true
		}
	}
	if !found {
		t.Fatal("guest path was split or interpreted")
	}
	if _, e = b.Action(context.Background(), task, task.Router, NodeAction{Action: "reset"}); e == nil {
		t.Fatal("router exposed as test node")
	}
	bad := map[string]string{"CfgFile": "/real-user-vm/machine.vbox"}
	if b.owned(task, n, bad) == nil {
		t.Fatal("foreign VM configuration accepted")
	}
}

func TestGuestSubmissionArgvAndTransportBudget(t *testing.T) {
	c := testConfig(t)
	r := &recordingRunner{}
	b := &VBoxBackend{Config: c, Runner: r}
	task := newTask("lab", 60)
	task.Router = &Node{ID: wire.ID()}
	n := &Node{ID: wire.ID(), UUID: wire.UUID(), NodeRequest: NodeRequest{Template: "windows"}}
	if e := os.MkdirAll(b.runtime(task), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := b.guestExec(context.Background(), task, n, "run", "--root", `C:\work`); e != nil {
		t.Fatal(e)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	args := r.calls[0]
	for i, v := range args {
		if v == "--" {
			if strings.Join(args[i+1:], "|") != `run|--root|C:\work` {
				t.Fatal("unexpected argv[1]", args)
			}
		}
		if v == "--timeout" && args[i+1] != "30000" {
			t.Fatal("submission transport is not bounded", args)
		}
	}
}
func TestPublicTaskHidesHostIdentifiers(t *testing.T) {
	s, _ := testServer(t)
	lab := createLab(t, s, "public")
	data, _ := json.Marshal(lab.Public())
	for _, secret := range []string{lab.RouterPassword, lab.Router.UUID, lab.Nodes[0].UUID} {
		if strings.Contains(string(data), secret) {
			t.Fatal("host identifier leaked")
		}
	}
}

func TestPurgeRemovesPayloadFromReceipts(t *testing.T) {
	s, _ := testServer(t)
	lab := createLab(t, s, "purge-payload")
	marker := "synthetic-private-command-input"
	a := StartRequest{NodeChange: NodeChange{Change: Change{"payload-command"}, NodeID: NodeID{TaskID: TaskID{lab.ID}, NodeID: lab.Nodes[0].ID}}, Argv: []string{"app", marker}, Env: map[string]string{"SYNTHETIC": marker}}
	successful(t, s, "command_start", a)
	successful(t, s, "lab_finish", FinishRequest{TaskChange: TaskChange{Change: Change{"payload-finish"}, TaskID: TaskID{lab.ID}}, Conclusion: "inconclusive", Summary: "synthetic retention check"})
	waitFor(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); v, _ := s.task(lab.ID); return v.Cleanup == "complete" })
	successful(t, s, "execution_delete", TaskChange{Change: Change{"payload-purge"}, TaskID: TaskID{lab.ID}})
	for _, kind := range []string{"task", "operation"} {
		rows, e := s.db.List(context.Background(), kind)
		if e != nil {
			t.Fatal(e)
		}
		for _, row := range rows {
			if strings.Contains(string(row), marker) {
				t.Fatal("purged payload retained in " + kind)
			}
		}
	}
}
