//go:build windows

package guest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/windows"
)

func TestWindowsProcessFixture(t *testing.T) {
	mode := os.Getenv("VM_TEST_WINDOWS_FIXTURE")
	if mode == "" {
		return
	}
	fail := func(err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(91)
		}
	}
	switch mode {
	case "supervise":
		fail(Execute(context.Background(), os.Getenv("VM_TEST_ROOT"), os.Getenv("VM_TEST_REQUEST")))
	case "hold":
		identity, alive, err := processIdentity(os.Getpid())
		fail(err)
		if !alive {
			os.Exit(92)
		}
		fail(atomic(os.Getenv("VM_TEST_CHILD_RECORD"), identity))
		time.Sleep(time.Minute)
	case "inherit":
		executable, err := os.Executable()
		fail(err)
		cmd := exec.Command(executable, "-test.run=^TestWindowsProcessFixture$")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "VM_TEST_WINDOWS_FIXTURE=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "VM_TEST_WINDOWS_FIXTURE=hold")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		fail(cmd.Start())
		// The descendant writes its creation identity after it actually starts.
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err = os.Stat(os.Getenv("VM_TEST_CHILD_RECORD")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(93)
			}
			time.Sleep(10 * time.Millisecond)
		}
		_, err = os.Stdout.WriteString("inherited-output")
		fail(err)
		os.Exit(27)
	default:
		os.Exit(94)
	}
	os.Exit(0)
}

func windowsFixtureSpec(t *testing.T, mode, record string) wire.CommandSpec {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec, _, err := commandEnvironment(wire.CommandSpec{
		ID: wire.ID(), Argv: []string{executable, "-test.run=^TestWindowsProcessFixture$"},
		TimeoutSeconds: 30, Context: "service",
		Env: map[string]string{"VM_TEST_WINDOWS_FIXTURE": mode, "VM_TEST_CHILD_RECORD": record},
	})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func assertWindowsProcessStopped(t *testing.T, expected wire.ProcessIdentity) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		actual, alive, err := processIdentity(expected.PID)
		if os.IsNotExist(err) || err == nil && (!alive || actual != expected) {
			return
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatalf("owned process did not stop: expected=%#v actual=%#v alive=%v error=%v", expected, actual, alive, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWindowsInheritedOutputEndsWithJob(t *testing.T) {
	record := filepath.Join(t.TempDir(), "descendant.json")
	spec := windowsFixtureSpec(t, "inherit", record)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out, errout bytes.Buffer
	child, err := launch(ctx, spec, strings.NewReader(""), &out, &errout)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	result, err := child.Wait(ctx)
	if err != nil || result.code == nil || *result.code != 27 || result.signal != "" {
		t.Fatalf("parent exit: %#v %v", result, err)
	}
	var descendant wire.ProcessIdentity
	if err = readJSON(record, &descendant); err != nil {
		t.Fatal(err)
	}
	actual, alive, err := processIdentity(descendant.PID)
	if err != nil || !alive || actual != descendant {
		t.Fatalf("descendant must still own inherited output: %#v %v %v", actual, alive, err)
	}
	if err = child.Kill(ctx); err != nil {
		t.Fatal(err)
	}
	if err = child.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if out.String() != "inherited-output" || errout.Len() != 0 {
		t.Fatalf("output: %q stderr: %q", out.String(), errout.String())
	}
	assertWindowsProcessStopped(t, descendant)
}

func TestWindowsJobTerminationAccessDeniedIsUnconfirmed(t *testing.T) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	var queryOnly windows.Handle
	// JOB_OBJECT_QUERY deliberately omits JOB_OBJECT_TERMINATE. The real
	// Windows access check, rather than a fake adapter, must reject this handle.
	const jobObjectQuery = 0x0004
	process := windows.CurrentProcess()
	if err = windows.DuplicateHandle(process, job, process, &queryOnly, jobObjectQuery, false, 0); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(queryOnly)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = (&windowsChild{job: queryOnly}).Kill(ctx)
	var failure *wire.Error
	if !errors.As(err, &failure) || failure.Code != "termination_unconfirmed" || failure.Details["request_delivered"] != false || failure.Details["system_code"] != uint32(windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("denied termination lost its outcome: %#v %v", failure, err)
	}
}

func TestWindowsSupervisorLossAndTerminalWriteConflict(t *testing.T) {
	for _, mode := range []string{"supervisor_exit", "locked_terminal_file"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			record := filepath.Join(root, "child.json")
			spec := windowsFixtureSpec(t, "hold", record)
			request := filepath.Join(root, "request.json")
			if err := atomic(request, spec); err != nil {
				t.Fatal(err)
			}
			supervisor := windowsFixtureSpec(t, "supervise", record)
			supervisor.Env["VM_TEST_ROOT"], supervisor.Env["VM_TEST_REQUEST"] = root, request
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, supervisor.Argv[0], supervisor.Argv[1:]...)
			for k, v := range supervisor.Env {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
			cmd.WaitDelay = time.Second
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			statusFile := filepath.Join(root, spec.ID, "status.json")
			var status wire.CommandStatus
			var descendant wire.ProcessIdentity
			deadline := time.Now().Add(10 * time.Second)
			for {
				if readJSON(statusFile, &status) == nil && status.State == "running" && readJSON(record, &descendant) == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("supervisor did not become ready: %#v", status)
				}
				time.Sleep(10 * time.Millisecond)
			}
			var lock windows.Handle
			if mode == "supervisor_exit" {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else {
				name, err := windows.UTF16PtrFromString(statusFile)
				if err != nil {
					t.Fatal(err)
				}
				lock, err = windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if lock != 0 {
						windows.CloseHandle(lock)
					}
				}()
				if err = Cancel(root, spec.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := cmd.Wait(); err == nil || ctx.Err() != nil {
				t.Fatalf("supervisor must end without its terminal record: %v context=%v", err, ctx.Err())
			}
			observed, err := Read(root, spec.ID, 0, 0, 4096)
			if err != nil {
				t.Fatal(err)
			}
			final := observed.Status
			if final == nil || !final.Final || final.State != "uncertain" || final.ExitCode != nil || final.Termination.Reason != "supervisor_lost" || final.Termination.ProcessExited || final.Termination.GroupStopped || final.Output.State != "incomplete" {
				t.Fatalf("lost evidence must remain unconfirmed: %#v", final)
			}
			if mode == "locked_terminal_file" {
				if observed.Error == nil || observed.Error.Code != "supervision_failed" || observed.Error.Details["stage"] != "terminal_write" {
					t.Fatalf("terminal replacement failure must remain visible: %#v", observed.Error)
				}
				windows.CloseHandle(lock)
				lock = 0
				if _, err = Read(root, spec.ID, 0, 0, 4096); err != nil {
					t.Fatal(err)
				}
			}
			if err = readJSON(statusFile, &status); err != nil || !status.Final || status.State != "uncertain" {
				t.Fatalf("reconciled terminal record was not durable: %#v %v", status, err)
			}
			assertWindowsProcessStopped(t, descendant)
		})
	}
}
