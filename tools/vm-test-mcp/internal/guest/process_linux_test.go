//go:build linux

package guest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestLinuxProcessGroupOwnershipAndPopulation(t *testing.T) {
	id := wire.ID()
	unit := "vm-test-supervisor-" + id + ".service"
	got, err := supervisorCgroup([]byte("0::/system.slice/"+unit+"/supervisor\n"), id)
	if err != nil || got != "/sys/fs/cgroup/system.slice/"+unit {
		t.Fatal(got, err)
	}
	for _, path := range []string{"/", "/system.slice/other.service/supervisor", "/system.slice/" + unit, "/system.slice/vm-test-supervisor-" + wire.ID() + ".service/supervisor"} {
		if _, err = supervisorCgroup([]byte("0::"+path+"\n"), id); err == nil {
			t.Fatal("accepted cgroup outside the command's own delegated unit", path)
		}
	}
	for data, want := range map[string]bool{"populated 0\nfrozen 0\n": false, "populated 1\n": true} {
		got, err := cgroupPopulated([]byte(data))
		if err != nil || got != want {
			t.Fatal(got, err)
		}
	}
	for _, data := range []string{"", "frozen 0\n", "populated 2\n"} {
		if _, err := cgroupPopulated([]byte(data)); err == nil {
			t.Fatal("missing or invalid population became an empty group", data)
		}
	}
	for version, want := range map[string]bool{"253.9": false, "254": true, "257.13-1~deb13u1": true, "": false, "unknown": false} {
		if supportedSystemd(version) != want {
			t.Fatal(version)
		}
	}
}

func awaitLinuxFixture(ctx context.Context, observe func() bool) error {
	for !observe() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

// This helper only runs inside the explicitly requested, owned systemd unit.
func TestLinuxProcessFixture(t *testing.T) {
	mode := os.Getenv("VM_TEST_LINUX_CASE")
	if mode == "" {
		return
	}
	root, id := os.Getenv("VM_TEST_LINUX_ROOT"), os.Getenv("VM_TEST_LINUX_ID")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	record := filepath.Join(root, "descendant.json")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if mode == "hold" {
		identity, alive, err := processIdentity(os.Getpid())
		if err != nil || !alive {
			t.Fatal(identity, alive, err)
		}
		if err = atomic(record, identity); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		return
	}
	if mode == "parent" {
		child := exec.Command(exe, "-test.run=^TestLinuxProcessFixture$")
		child.Env = append(os.Environ(), "VM_TEST_LINUX_CASE=hold")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err = child.Start(); err != nil {
			t.Fatal(err)
		}
		if err = awaitLinuxFixture(ctx, func() bool { _, err := os.Stat(record); return err == nil }); err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(os.Stdout, "inherited-output")
		os.Exit(27)
	}
	spec := wire.CommandSpec{ID: id, Cwd: root, Env: map[string]string{"VM_TEST_LINUX_CASE": "hold", "VM_TEST_LINUX_ROOT": root, "VM_TEST_LINUX_ID": id}, TimeoutSeconds: 10}
	switch mode {
	case "argv":
		spec.Argv = []string{"/usr/bin/printf", "%s\n", "$$", "${HOME}", "a b", "中文", "%n"}
	case "eof":
		spec.Argv = []string{"/bin/cat"}
	case "exit":
		spec.Argv = []string{"/bin/sh", "-c", "exit 7"}
	case "signal":
		spec.Argv = []string{"/bin/sh", "-c", "kill -KILL $$"}
	case "timeout", "cancel", "supervisor_lost":
		spec.Argv = []string{exe, "-test.run=^TestLinuxProcessFixture$"}
		if mode == "timeout" {
			spec.TimeoutSeconds = 1
		}
	case "inherited":
		spec.Argv = []string{exe, "-test.run=^TestLinuxProcessFixture$"}
		spec.Env["VM_TEST_LINUX_CASE"] = "parent"
	default:
		t.Fatal("unknown native fixture", mode)
	}
	request := filepath.Join(root, "request.json")
	if err = atomic(request, spec); err != nil {
		t.Fatal(err)
	}
	inputDone := make(chan error, 1)
	go func() {
		err := awaitLinuxFixture(ctx, func() bool { _, err := os.Stat(filepath.Join(root, id, "status.json")); return err == nil })
		if err == nil && mode == "eof" {
			err = atomic(filepath.Join(root, id, "input-00000000.json"), wire.Input{Data: "中文\nEOF", EOF: true})
		}
		if err == nil && mode == "cancel" {
			err = awaitLinuxFixture(ctx, func() bool { _, err := os.Stat(record); return err == nil })
			if err == nil {
				err = Cancel(root, id)
			}
		}
		inputDone <- err
	}()
	if err = Execute(ctx, root, request); err != nil {
		t.Error(err)
	}
	if err = <-inputDone; err != nil {
		t.Fatal(err)
	}
}

// Opt in only inside a disposable Linux VM. The usual baseline runs pure
// ownership/exit regressions without mutating the development host's cgroups.
func TestLinuxNativeProcessLifecycle(t *testing.T) {
	if os.Getenv("VM_TEST_NATIVE_LINUX") != "1" {
		t.Skip("requires an explicitly selected disposable Linux systemd VM")
	}
	check, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	observed, err := linuxExecutionRequirements(check)
	cancel()
	if err != nil {
		t.Fatal(observed, err)
	}
	t.Logf("execution requirements: %s", wire.JSON(observed))
	for _, mode := range []string{"argv", "eof", "exit", "signal", "cancel", "timeout", "inherited", "supervisor_lost"} {
		t.Run(mode, func(t *testing.T) {
			root, id := t.TempDir(), wire.ID()
			unit := "vm-test-supervisor-" + id + ".service"
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			args := []string{"--quiet", "--wait", "--pipe", "--collect", "--expand-environment=no", "--unit=" + unit, "--property=Type=exec", "--property=Delegate=yes", "--property=DelegateSubgroup=supervisor", "--property=KillMode=control-group", "--property=TimeoutStopSec=3s", "--property=RuntimeMaxSec=25s", "--setenv=VM_TEST_LINUX_CASE=" + mode, "--setenv=VM_TEST_LINUX_ROOT=" + root, "--setenv=VM_TEST_LINUX_ID=" + id, "--", exe, "-test.run=^TestLinuxProcessFixture$"}
			defer func() {
				stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = exec.CommandContext(stop, "systemctl", "stop", unit).Run()
			}()
			command := exec.CommandContext(ctx, "systemd-run", args...)
			command.WaitDelay = time.Second
			type fixtureResult struct {
				out []byte
				err error
			}
			done := make(chan fixtureResult, 1)
			go func() { out, err := command.CombinedOutput(); done <- fixtureResult{out, err} }()
			if mode == "supervisor_lost" {
				if err = awaitLinuxFixture(ctx, func() bool { _, err := os.Stat(filepath.Join(root, "descendant.json")); return err == nil }); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.CommandContext(ctx, "systemctl", "kill", "--signal=KILL", "--kill-whom=main", unit).CombinedOutput(); err != nil {
					t.Fatalf("kill owned supervisor: %s %v", out, err)
				}
			}
			result := <-done
			if mode != "supervisor_lost" && result.err != nil {
				t.Fatalf("native fixture: %s %v", result.out, result.err)
			}
			snapshot, err := Read(root, id, 0, 0, 65536)
			if err != nil || snapshot.Status == nil {
				t.Fatal(snapshot, err)
			}
			status := snapshot.Status
			t.Logf("final command status: %s", wire.JSON(status))
			if !status.Final {
				t.Fatal(status)
			}
			if mode == "supervisor_lost" {
				if status.State != "uncertain" || status.ExitCode != nil || status.ExitSignal != "" || status.Termination.ProcessExited || status.Termination.GroupStopped || status.Output.State != "incomplete" {
					t.Fatal(status)
				}
			} else {
				state := "exited"
				if mode == "cancel" {
					state = "cancelled"
				}
				if mode == "timeout" {
					state = "timed_out"
				}
				if status.State != state || !status.Termination.ProcessExited || !status.Termination.GroupStopped || status.Output.State != "complete" {
					t.Fatal(status)
				}
				if mode == "signal" || mode == "cancel" || mode == "timeout" {
					if status.ExitCode != nil || status.ExitSignal != "SIGKILL" || status.CoreDumped {
						t.Fatal(status)
					}
				} else {
					code := 0
					if mode == "exit" {
						code = 7
					}
					if mode == "inherited" {
						code = 27
					}
					if status.ExitCode == nil || *status.ExitCode != code || status.ExitSignal != "" {
						t.Fatal(status)
					}
				}
				if want, ok := map[string]string{"argv": "$$\n${HOME}\na b\n中文\n%n\n", "eof": "中文\nEOF", "inherited": "inherited-output"}[mode]; ok {
					data, err := os.ReadFile(filepath.Join(root, id, "stdout"))
					if err != nil || string(data) != want {
						t.Fatalf("output=%q want=%q error=%v", data, want, err)
					}
				}
			}
			var descendant wire.ProcessIdentity
			if err = readJSON(filepath.Join(root, "descendant.json"), &descendant); err == nil {
				if err = awaitLinuxFixture(ctx, func() bool {
					actual, alive, err := processIdentity(descendant.PID)
					return os.IsNotExist(err) || err == nil && (!alive || actual != descendant)
				}); err != nil {
					t.Fatal("descendant remained alive", descendant, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) || strings.Contains("cancel timeout inherited supervisor_lost", mode) {
				t.Fatal("descendant evidence", err)
			}
		})
	}
}
