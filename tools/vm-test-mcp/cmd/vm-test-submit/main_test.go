package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestVersionDoesNotRequirePreparedClientConfiguration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"--version"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatal(code, stderr.String())
	}
	var build wire.BuildIdentity
	if err := json.Unmarshal(stdout.Bytes(), &build); err != nil || build.Version != wire.Version || build.BinaryState != "observed" || build.SHA256 == "" {
		t.Fatal(build, err)
	}
}

func TestCLIUsage(t *testing.T) {
	for _, test := range []struct {
		args []string
		code int
	}{
		{[]string{"--help"}, 0},
		{nil, 2},
		{[]string{"--unknown"}, 2},
		{[]string{"--config", "unused.yaml", "--upload-id", "manual-cache"}, 2},
		{[]string{"--config", "unused.yaml", "--apply", "t: t"}, 2},
		{[]string{"--config", "unused.yaml", "--smoke-project", "/tmp", "--test", ".#check"}, 2},
		{[]string{"--smoke-project", "/tmp", "--test", ".#check", "--request-id", "r1", "--upload-id", strings.Repeat("a", 32), "--upload-url", "http://127.0.0.1:1/files/cap", "--work-dir", "/tmp/work"}, 2},
		{[]string{"--project", "/tmp"}, 2},
		{[]string{"--config", "unused.yaml", "--timeout-seconds", "-1"}, 2},
		{[]string{"--config", "unused.yaml", "unexpected"}, 2},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(test.args, &stdout, &stderr); code != test.code || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", test.args, code, stdout.String(), stderr.String())
		}
	}
}

func TestManualFailureKeepsStdoutEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dir := t.TempDir()
	code := runCLI([]string{"--test", ".#test", "--request-id", "manual", "--upload-id", strings.Repeat("a", 32), "--upload-url", "http://127.0.0.1:1/files/synthetic", "--work-dir", dir, "--nix", filepath.Join(dir, "missing-nix")}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), filepath.Join(dir, "prepare.log")) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if shell, err := exec.LookPath("sh"); err == nil {
		evalDir := t.TempDir()
		fakeNix := filepath.Join(evalDir, "fake-nix")
		if err := os.WriteFile(fakeNix, []byte("#!"+shell+"\nprintf 'error: attribute missingCheck does not exist\\n' >&2\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
		stdout.Reset()
		stderr.Reset()
		workDir := filepath.Join(evalDir, "work")
		code = runCLI([]string{"--test", ".#checks.x86_64-linux.missingCheck", "--request-id", "eval-err", "--upload-id", strings.Repeat("b", 32), "--upload-url", "http://127.0.0.1:1/files/synthetic", "--work-dir", workDir, "--nix", fakeNix}, &stdout, &stderr)
		if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), filepath.Join(workDir, "prepare.log")) || !strings.Contains(stderr.String(), "attribute missingCheck does not exist") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	}
}
