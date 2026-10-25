//go:build linux

package guest

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func firstbootExit(code int) *int { return &code }

func firstbootTestOperations(t *testing.T, run firstbootRunner) firstbootOperations {
	t.Helper()
	return firstbootOperations{
		hostname: func(ctx context.Context, name string) error {
			if !strings.HasPrefix(name, "vmtest-") || len(name) != 19 {
				t.Fatal(name)
			}
			return ctx.Err()
		},
		run: run, lookup: func(s string) (string, error) { return s, nil },
	}
}

func firstbootRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "firstboot-pending"), []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFirstbootStagesKeepFailureUnreadyAndPermitRetry(t *testing.T) {
	root := firstbootRoot(t)
	// Simulate a prior crash after prepared was installed but before pending was
	// removed. It cannot allow commands while a retry is still initializing.
	if err := os.WriteFile(filepath.Join(root, "prepared"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	run := func(_ context.Context, name string, args []string) (firstbootResult, error) {
		if name != "ssh-keygen" || len(args) != 1 || args[0] != "-A" {
			t.Fatal(name, args)
		}
		return firstbootResult{ExitCode: firstbootExit(7), Stderr: "synthetic SSH key failure"}, errors.New("exit 7")
	}
	ops := firstbootTestOperations(t, run)
	err := initializeClone(context.Background(), root, ops, &log, time.Second, time.Second)
	var failure *wire.Error
	if !errors.As(err, &failure) || failure.Code != "firstboot_failed" || wire.Safe(err).Category != "execution" || wire.Safe(err).Retry != "after_change" {
		t.Fatal(err)
	}
	if _, ready := readiness(root); ready {
		t.Fatal("partial initialization became ready")
	}
	if _, err = os.Stat(filepath.Join(root, "firstboot-pending")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "synthetic SSH key failure") || !strings.Contains(log.String(), `"exit_code":7`) {
		t.Fatal(log.String())
	}
	var steps []string
	ops.run = func(_ context.Context, name string, args []string) (firstbootResult, error) {
		steps = append(steps, name)
		return firstbootResult{ExitCode: firstbootExit(0)}, nil
	}
	if err = initializeClone(context.Background(), root, ops, &log, time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != "ssh-keygen" {
		t.Fatal(steps)
	}
	if _, ready := readiness(root); !ready {
		t.Fatal("completed initialization not ready")
	}
	var prepared struct {
		Version     string    `json:"version"`
		Initialized time.Time `json:"initialized"`
	}
	if err = readJSON(filepath.Join(root, "prepared"), &prepared); err != nil || prepared.Version != wire.Version || prepared.Initialized.IsZero() {
		t.Fatal(prepared, err)
	}
}

func TestFirstbootBudgetsCancellationAndMissingOptionalSSH(t *testing.T) {
	for _, reason := range []string{"deadline", "parent_cancel"} {
		t.Run(reason, func(t *testing.T) {
			root := firstbootRoot(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if reason == "parent_cancel" {
				cancel()
			}
			var log bytes.Buffer
			run := func(ctx context.Context, _ string, _ []string) (firstbootResult, error) {
				<-ctx.Done()
				return firstbootResult{}, ctx.Err()
			}
			started := time.Now()
			err := initializeClone(ctx, root, firstbootTestOperations(t, run), &log, 25*time.Millisecond, 15*time.Millisecond)
			if err == nil || time.Since(started) > time.Second {
				t.Fatal(err, time.Since(started))
			}
			key := `"timed_out":true`
			if reason == "parent_cancel" {
				key = `"cancelled":true`
			}
			if !strings.Contains(log.String(), key) {
				t.Fatal(log.String())
			}
			if _, ready := readiness(root); ready {
				t.Fatal("failed firstboot is ready")
			}
		})
	}
	root := firstbootRoot(t)
	var log bytes.Buffer
	calls := 0
	ops := firstbootTestOperations(t, func(context.Context, string, []string) (firstbootResult, error) {
		calls++
		return firstbootResult{ExitCode: firstbootExit(0)}, nil
	})
	ops.lookup = func(string) (string, error) { return "", exec.ErrNotFound }
	err := initializeClone(context.Background(), root, ops, &log, time.Second, time.Second)
	if err != nil || calls != 0 || !strings.Contains(log.String(), "not_installed") {
		t.Fatal(calls, log.String(), err)
	}
}

func TestFirstbootCommandRetainsStderrAndReapsDeadline(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	result, err := firstbootCommand(ctx, exe, []string{"-test.run=^TestFirstbootProcess$", "--", "error"})
	if err == nil || result.ExitCode == nil || *result.ExitCode != 7 || len(result.Stderr) != 8192 || !result.Truncated || result.TimedOut {
		t.Fatal(result, err)
	}
	short, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err = firstbootCommand(short, exe, []string{"-test.run=^TestFirstbootProcess$", "--", "block"})
	if err == nil || !result.TimedOut || result.ExitCode != nil || result.ExitSignal != "SIGKILL" {
		t.Fatal(result, err)
	}
}

func TestFirstbootHostnamePersistenceAndFailure(t *testing.T) {
	for _, outcome := range []string{"success", "persist_failure", "kernel_failure", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			root := firstbootRoot(t)
			path := filepath.Join(t.TempDir(), "hostname")
			if err := os.WriteFile(path, []byte("prior\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if outcome == "persist_failure" {
				path = filepath.Join(path, "cannot-create-here")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if outcome == "cancelled" {
				cancel()
			}
			var applied string
			ops := firstbootTestOperations(t, nil)
			ops.lookup = func(string) (string, error) { return "", exec.ErrNotFound }
			ops.hostname = func(ctx context.Context, name string) error {
				return installHostname(ctx, path, name, func(value []byte) error {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != string(value)+"\n" {
						t.Fatal("kernel name applied before static name persisted", string(data), err)
					}
					applied = string(value)
					if outcome == "kernel_failure" {
						return os.ErrPermission
					}
					return nil
				})
			}
			var log bytes.Buffer
			err := initializeClone(ctx, root, ops, &log, time.Second, time.Second)
			var step firstbootResult
			if decode := wire.Decode(bytes.Split(log.Bytes(), []byte("\n"))[0], &step); decode != nil || step.Stage != "hostname" || step.ExitCode != nil {
				t.Fatal(step, decode, log.String())
			}
			_, ready := readiness(root)
			if outcome == "success" {
				info, statErr := os.Stat(path)
				if err != nil || !ready || applied == "" || step.State != "complete" || statErr != nil || info.Mode().Perm() != 0644 {
					t.Fatal(applied, ready, step, err, statErr)
				}
				return
			}
			if err == nil || ready || step.State != "failed" || step.Error == nil {
				t.Fatal(ready, step, err)
			}
			if (outcome == "persist_failure" || outcome == "cancelled") && applied != "" {
				t.Fatal("incomplete persistence changed kernel hostname", applied)
			}
			if outcome == "cancelled" {
				data, readErr := os.ReadFile(path)
				if readErr != nil || string(data) != "prior\n" {
					t.Fatal("cancelled initialization changed hostname", string(data), readErr)
				}
			}
		})
	}
}

func TestFirstbootProcess(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "error":
		os.Stderr.WriteString(strings.Repeat("diagnostic", 1000))
		os.Exit(7)
	case "block":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}
