package worker

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func fixture(t *testing.T, uid int) (*Worker, config.Worker) {
	t.Helper()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	c := config.Worker{StateDir: dir, Socket: filepath.Join(dir, "worker.sock"), ControllerUID: uid, Nix: "/synthetic/nix", CacheURL: "http://127.0.0.1:8765"}
	w, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	return w, c
}

func waitExecution(t *testing.T, w *Worker, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		j := w.jobs[id]
		complete := j != nil && j.Execution != nil && j.Execution.Finished != nil
		w.mu.Unlock()
		if complete {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker execution did not finish")
}

func TestStorageIdentitySurvivesRestartAndCannotConfirmAnotherStore(t *testing.T) {
	w, cfg := fixture(t, os.Getuid())
	identity := w.db.InstanceID
	id := wire.ID()
	w.Close()
	w, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.db.InstanceID != identity {
		t.Fatal("restart changed the durable worker identity")
	}
	result, err := w.Call("cleanup", Request{TaskID: id, InstanceID: identity})
	if err != nil || result.(map[string]any)["never_accepted"] != true {
		t.Fatal(result, err)
	}
	replacement, _ := fixture(t, os.Getuid())
	defer replacement.Close()
	if _, err = replacement.Call("cleanup", Request{TaskID: id, InstanceID: identity}); wire.Safe(err).Code != "worker_instance_changed" {
		t.Fatal("replacement storage falsely confirmed release", err)
	}
}

func TestExecutionRetainsPhaseAndObservedClientResult(t *testing.T) {
	for _, tc := range []struct {
		name, copy, features, build, stage, processStage, state, code string
		exit                                                          int
		signal                                                        bool
	}{
		{"import failure", "exit 7", "kvm", "exit 0", "import", "import", "failed", "backend_error", 7, false},
		{"policy rejection", "exit 0", "", "exit 99", "validate", "validate", "failed", "policy_denied", 0, false},
		{"build failure", "exit 0", "kvm", "exit 23", "build", "build", "failed", "backend_error", 23, false},
		{"client signal", "exit 0", "kvm", "kill -KILL $$", "build", "build", "cleanup_required", "backend_error", 0, true},
		{"invalid outputs", "exit 0", "kvm", "printf 'invalid'", "collect", "build", "failed", "backend_error", 0, false},
		{"success", "exit 0", "kvm", "printf '[]'", "collect", "build", "succeeded", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, cfg := fixture(t, os.Getuid())
			shell, err := exec.LookPath("sh")
			if err != nil {
				t.Fatal(err)
			}
			drv := "/nix/store/" + strings.Repeat("0", 32) + "-test.drv"
			metadata := wire.JSON(map[string]any{drv: map[string]any{"system": "x86_64-linux", "env": map[string]string{"vmTestUploadID": strings.Repeat("a", 32), "allowSubstitutes": "", "requiredSystemFeatures": tc.features}}})
			w.cfg.Nix = filepath.Join(w.root.Path, "fake-nix")
			script := fmt.Sprintf("#!%s\ncase \"$1\" in\ncopy) %s;;\nderivation) printf '%%s' '%s';;\nbuild) %s;;\n*) exit 98;;\nesac\n", shell, tc.copy, metadata, tc.build)
			if err = os.WriteFile(w.cfg.Nix, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			id := wire.ID()
			if _, err = w.Call("start", Request{UploadID: strings.Repeat("a", 32), InstanceID: w.db.InstanceID, TaskID: id, Derivation: drv, System: "x86_64-linux", Cache: "/files/synthetic-private-capability", TimeoutSeconds: 60}); err != nil {
				t.Fatal(err)
			}
			waitExecution(t, w, id)
			check := func() {
				t.Helper()
				v, err := w.Call("status", Request{InstanceID: w.db.InstanceID, TaskID: id})
				if err != nil {
					t.Fatal(err)
				}
				j := w.jobs[id]
				e := j.Execution
				if j.State != tc.state || e == nil || e.Stage != tc.stage || e.Finished == nil || e.Process == nil || e.Process.Stage != tc.processStage || !e.Process.Exited || e.Process.Source != "nix_client_wait" {
					t.Fatal(v)
				}
				if tc.code == "" {
					if e.Error != nil {
						t.Fatal(e.Error)
					}
				} else if e.Error == nil || e.Error.Code != tc.code {
					t.Fatal(e.Error)
				}
				if tc.signal {
					if e.Process.ExitCode != nil || e.Process.ExitSignal != "SIGKILL" || e.Process.CoreDumped || j.Cleanup != "required" || w.active != id {
						t.Fatal(v, w.active)
					}
				} else if e.Process.ExitCode == nil || *e.Process.ExitCode != tc.exit || e.Process.ExitSignal != "" || j.Cleanup != "complete" {
					t.Fatal(v)
				}
				if strings.Contains(string(wire.JSON(v)), "synthetic-private-capability") {
					t.Fatal("cache credential entered status")
				}
			}
			check()
			w.Close()
			w, err = New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			check()
		})
	}
}

func TestTerminalPersistenceFailureRetainsOccupancy(t *testing.T) {
	w, cfg := fixture(t, os.Getuid())
	id := wire.ID()
	j := &Job{Request: Request{TaskID: id}, State: "building", Cleanup: "pending", Execution: &wire.NixExecution{Stage: "build"}}
	w.jobs[id], w.active = j, id
	if err := w.save(j); err != nil {
		t.Fatal(err)
	}
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	j.Execution.Process = &wire.NixProcessResult{Stage: "build", Source: "nix_client_wait", Exited: true, ExitCode: new(7)}
	w.complete(j, fmt.Errorf("synthetic build failure"), false)
	if w.active != id || j.Cleanup != "required" || j.State != "cleanup_required" || j.Execution.PersistenceError == nil || j.Execution.Process.ExitCode == nil || *j.Execution.Process.ExitCode != 7 {
		t.Fatal(j, w.active)
	}
	if err := w.Reconcile(id); err == nil || w.active != id || j.Cleanup != "required" {
		t.Fatal("failed reconciliation released ownership", err, w.active, j.Cleanup)
	}
	w.Close()
	w, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.active != id || w.jobs[id].Execution.Error.Code != "worker_restarted" || w.jobs[id].Execution.Process != nil {
		t.Fatal("restart invented an unpersisted result", w.jobs[id])
	}
}

func TestClientLaunchFailureHasNoInventedExit(t *testing.T) {
	w, _ := fixture(t, os.Getuid())
	defer w.Close()
	id := wire.ID()
	if _, err := w.Call("start", Request{UploadID: strings.Repeat("a", 32), InstanceID: w.db.InstanceID, TaskID: id, Derivation: "/nix/store/" + strings.Repeat("0", 32) + "-test.drv", System: "x86_64-linux", Cache: "/files/synthetic", TimeoutSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	waitExecution(t, w, id)
	e := w.jobs[id].Execution
	if e == nil || e.Stage != "import" || e.Process == nil || e.Process.Exited || e.Process.ExitCode != nil || e.Process.ExitSignal != "" || e.Process.Source != "unconfirmed" || e.Error == nil {
		t.Fatal(e)
	}
}

func TestPreflightDaemonAndClientPolicy(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("worker preflight requires a non-root account")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, setting, wantError string
		value                    any
		otherAccount             bool
		daemonJSON               string
		daemonExit               int
	}{
		{name: "NixOS client defaults"},
		{name: "different account", otherAccount: true},
		{name: "explicit build users", setting: "build-users-group", value: "nixbld"},
		{name: "absent client build users", setting: "build-users-group", value: nil},
		{name: "sandbox disabled", setting: "sandbox", value: false, wantError: "client sandbox must be true"},
		{name: "relaxed sandbox", setting: "sandbox", value: "relaxed", wantError: "client sandbox must be true"},
		{name: "sandbox fallback", setting: "sandbox-fallback", value: true, wantError: "client sandbox fallback must be disabled"},
		{name: "client trust differs from daemon", setting: "trusted-users", value: []string{"*"}},
		{name: "daemon trusts worker", setting: "build-users-group", value: "nixbld", daemonJSON: `{"trusted":true}`, wantError: "worker must not be a trusted Nix daemon client"},
		{name: "daemon unavailable", daemonExit: 7, wantError: "inspect Nix daemon"},
		{name: "missing daemon trust", daemonJSON: `{"url":"daemon"}`, wantError: "Nix daemon did not report client trust"},
		{name: "null daemon trust", daemonJSON: `{"trusted":null}`, wantError: "Nix daemon did not report client trust"},
		{name: "invalid daemon trust", daemonJSON: `{"trusted":"false"}`, wantError: "decode Nix daemon information"},
		{name: "malformed daemon output", daemonJSON: `{`, wantError: "decode Nix daemon information"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uid := os.Getuid()
			if tc.otherAccount {
				uid++
			}
			w, _ := fixture(t, uid)
			defer w.Close()
			// Nix's ordinary-user defaults leave build-users-group empty. Daemon
			// policy is independent of this client-side configuration dump.
			cfg := map[string]map[string]any{
				"sandbox":           {"value": true},
				"sandbox-fallback":  {"value": false},
				"build-users-group": {"value": ""},
				"trusted-users":     {"value": []string{"root"}},
			}
			if tc.setting != "" {
				if tc.value == nil {
					delete(cfg, tc.setting)
				} else {
					cfg[tc.setting]["value"] = tc.value
				}
			}
			w.cfg.Nix = filepath.Join(w.root.Path, "fake-nix")
			daemonJSON := tc.daemonJSON
			if daemonJSON == "" {
				daemonJSON = `{"url":"daemon","version":"2.34.8","trusted":false}`
			}
			script := fmt.Sprintf("#!%s\ncase \"$*\" in\n'config show --json') printf '%%s\\n' '%s';;\n'store info --store daemon --json') printf '%%s\\n' '%s'; exit %d;;\n*) exit 97;;\nesac\n", shell, wire.JSON(cfg), daemonJSON, tc.daemonExit)
			if err := os.WriteFile(w.cfg.Nix, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := w.Preflight(ctx)
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("want %q, got %v", tc.wantError, err)
			}
		})
	}
}

func TestFixedDaemonBoundaryAndDerivationPolicy(t *testing.T) {
	for _, scenario := range []struct {
		name, features string
		signalled      bool
	}{
		{"valid", "kvm nixos-test", false},
		{"missing-kvm", "nixos-test", false},
		{"client-killed", "kvm nixos-test", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			features := scenario.features
			w, _ := fixture(t, os.Getuid())
			defer w.Close()
			shell, e := exec.LookPath("sh")
			if e != nil {
				t.Fatal(e)
			}
			drv := "/nix/store/" + strings.Repeat("0", 32) + "-test.drv"
			trace := filepath.Join(w.root.Path, "trace")
			exe := filepath.Join(w.root.Path, "fake-nix")
			script := "#!" + shell + "\nprintf '%s\\n' \"$@\" >> '" + trace + "'\ncase \"$1\" in\ncopy) exit 0;;\nderivation) printf '%s\\n' '" + string(wire.JSON(map[string]any{drv: map[string]any{"system": "x86_64-linux", "env": map[string]string{"vmTestUploadID": strings.Repeat("a", 32), "allowSubstitutes": "", "requiredSystemFeatures": features}}})) + "';;\nbuild) printf '[]\\n';;\n*) exit 1;;\nesac\n"
			if scenario.signalled {
				script = strings.Replace(script, "build) printf '[]\\n'", "build) kill -KILL $$", 1)
			}
			if e = os.WriteFile(exe, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			w.cfg.Nix = exe
			id := wire.ID()
			if _, e = w.Call("start", Request{UploadID: strings.Repeat("a", 32), InstanceID: w.db.InstanceID, TaskID: id, Derivation: drv, System: "x86_64-linux", Cache: "/files/synthetic-capability", TimeoutSeconds: 60}); e != nil {
				t.Fatal(e)
			}
			waitExecution(t, w, id)
			w.mu.Lock()
			state := w.jobs[id].State
			w.mu.Unlock()
			data, e := os.ReadFile(trace)
			if e != nil {
				t.Fatal(e)
			}
			if scenario.signalled {
				if state != "cleanup_required" || w.active != id {
					t.Fatal("unconfirmed daemon work released", state, w.active)
				}
			} else if strings.Contains(features, "kvm") {
				if state != "succeeded" || !strings.Contains(string(data), "--option\nsandbox\ntrue\n--option\nsandbox-fallback\nfalse") || !strings.Contains(string(data), "--store\ndaemon\n--builders\n\n") {
					t.Fatal(state, string(data))
				}
			} else if state != "failed" || strings.Contains(string(data), "\nbuild\n") {
				t.Fatal("unsafe derivation built", state, string(data))
			}
		})
	}
}

func TestDeadlineExtensionAndMethodArguments(t *testing.T) {
	w, _ := fixture(t, os.Getuid())
	defer w.Close()
	id := wire.ID()
	now := time.Now().UTC()
	j := &Job{Request: Request{TaskID: id}, State: "building", Started: now, Deadline: now.Add(time.Minute)}
	w.jobs[id] = j
	deadline := now.Add(2 * time.Minute)
	if _, e := w.Call("extend", Request{InstanceID: w.db.InstanceID, TaskID: id, Deadline: &deadline}); e != nil {
		t.Fatal(e)
	}
	if !j.Deadline.Equal(deadline) {
		t.Fatal(j.Deadline)
	}
	if _, e := w.Call("status", Request{InstanceID: w.db.InstanceID, TaskID: id, Cache: "/files/injected"}); e == nil {
		t.Fatal("method accepted unrelated input")
	}
	if _, e := w.Call("describe", Request{Derivation: "--option sandbox false"}); e == nil {
		t.Fatal("describe accepted command options")
	}
}

func TestVersionedDerivationJSON(t *testing.T) {
	drv := "/nix/store/" + strings.Repeat("0", 32) + "-test.drv"
	for _, noChroot := range []bool{false, true} {
		env := map[string]string{"vmTestUploadID": strings.Repeat("a", 32), "allowSubstitutes": "", "requiredSystemFeatures": "kvm nixos-test"}
		if noChroot {
			env["__json"] = `{"__noChroot":true,"requiredSystemFeatures":["kvm"]}`
		}
		data := wire.JSON(map[string]any{"version": 4, "derivations": map[string]any{filepath.Base(drv): map[string]any{"system": "x86_64-linux", "env": env}}})
		e := derivationPolicy(data, Request{UploadID: strings.Repeat("a", 32), Derivation: drv, System: "x86_64-linux"})
		if (e != nil) != noChroot {
			t.Fatal(noChroot, e)
		}
	}
}
func TestUnixPeerCredential(t *testing.T) {
	for _, uid := range []int{os.Getuid(), os.Getuid() + 10000} {
		w, _ := fixture(t, uid)
		listener, e := w.Listen()
		if e != nil {
			t.Fatal(e)
		}
		server := w.HTTPServer()
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		client := NewClient(w.cfg.Socket)
		_, e = client.Call(context.Background(), "describe", struct{}{})
		if (uid == os.Getuid()) != (e == nil) {
			t.Fatalf("UID %d: %v", uid, e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		server.Shutdown(ctx)
		cancel()
		if e = <-done; e != http.ErrServerClosed {
			t.Fatal(e)
		}
		w.Close()
	}
}
func TestRestartBlocksDaemonAndConfinesArtifacts(t *testing.T) {
	w, c := fixture(t, os.Getuid())
	id := wire.ID()
	j := &Job{Request: Request{TaskID: id}, State: "building", Cleanup: "pending"}
	if e := w.save(j); e != nil {
		t.Fatal(e)
	}
	w.Close()
	w, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if w.active != id || w.jobs[id].Cleanup != "required" {
		t.Fatal("daemon occupancy released on restart")
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600)
	root := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "escape"))
	if _, e = w.readFile(root, "escape/secret", 0); e == nil {
		t.Fatal("artifact symlink escaped")
	}
	if _, e = w.Call("reconcile", Request{TaskID: id}); e == nil {
		t.Fatal("remote reconciliation exposed")
	}
	if e = w.Reconcile(id); e != nil {
		t.Fatal(e)
	}
}
