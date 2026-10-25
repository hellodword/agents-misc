package guest

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestLinuxEnvironmentDefaultsAndExplicitOverrides(t *testing.T) {
	home := t.TempDir()
	spec, observed, err := resolveEnvironment(wire.CommandSpec{Env: map[string]string{"CACHE": "fixture"}}, "linux", "root", home, []string{"PATH=/prepared/bin", "UNRELATED=private"})
	if err != nil || spec.Env["HOME"] != home || spec.Env["USER"] != "root" || spec.Env["LOGNAME"] != "root" || spec.Env["PATH"] != "/prepared/bin" || spec.Cwd != home || spec.Context != "service" || observed.Identity != "root" {
		t.Fatal(spec, observed, err)
	}
	custom := t.TempDir()
	spec, observed, err = resolveEnvironment(wire.CommandSpec{Cwd: custom, Env: map[string]string{"HOME": custom, "PATH": "/task/bin"}}, "linux", "root", home, nil)
	if err != nil || spec.Cwd != custom || spec.Env["HOME"] != custom || spec.Env["PATH"] != "/task/bin" || observed.Cwd != custom {
		t.Fatal(spec, observed, err)
	}
	if _, _, err = resolveEnvironment(wire.CommandSpec{Context: "desktop"}, "linux", "root", home, nil); wire.Safe(err).Code != "unsupported_context" {
		t.Fatal(err)
	}
}

func TestWindowsEnvironmentUsesSelectedIdentityAndCaseInsensitiveOverrides(t *testing.T) {
	for _, context := range []string{"service", "desktop"} {
		spec, execution, err := resolveEnvironment(wire.CommandSpec{Context: context, Env: map[string]string{"Path": `C:\Task\bin`, "TMP": "task-temporary"}}, "windows", "fixture", `C:\Users\fixture`, []string{`Path=C:\Windows\System32`, `SystemRoot=C:\Windows`, `USERPROFILE=C:\Users\fixture`})
		if err != nil || spec.Env["PATH"] != `C:\Task\bin` || spec.Env["SYSTEMROOT"] != `C:\Windows` || spec.Env["USERPROFILE"] != spec.Cwd || execution.Context != context || execution.Identity != "fixture" {
			t.Fatal(spec, execution, err)
		}
	}
	if _, _, err := resolveEnvironment(wire.CommandSpec{Env: map[string]string{"PATH": "one", "Path": "two"}}, "windows", "fixture", `C:\Users\fixture`, nil); err == nil {
		t.Fatal("ambiguous PATH overrides accepted")
	}
}

func TestSupervisorPassesAndRecordsResolvedEnvironment(t *testing.T) {
	root := t.TempDir()
	id := wire.ID()
	request := filepath.Join(root, "request.json")
	if err := os.WriteFile(request, wire.JSON(wire.CommandSpec{ID: id, Argv: []string{"synthetic"}, Cwd: root, Env: map[string]string{"TASK_MARKER": "synthetic-private-value"}}), 0600); err != nil {
		t.Fatal(err)
	}
	launch := func(ctx context.Context, s wire.CommandSpec, in io.Reader, out, stderr io.Writer) (Child, error) {
		if s.Cwd != root || s.Context != "service" || s.Env["TASK_MARKER"] != "synthetic-private-value" || s.Env["PATH"] == "" {
			t.Fatal(s)
		}
		p := &faultChild{exit: make(chan struct{}), code: 0}
		p.once.Do(func() { close(p.exit) })
		return p, nil
	}
	if err := run(context.Background(), root, request, launch); err != nil {
		t.Fatal(err)
	}
	var status wire.CommandStatus
	if err := readJSON(filepath.Join(root, id, "status.json"), &status); err != nil {
		t.Fatal(err)
	}
	if status.Execution == nil || status.Execution.Context != "service" || status.Execution.Identity == "" || status.Execution.Cwd != root {
		t.Fatal(status)
	}
	if strings.Contains(string(wire.JSON(status)), "synthetic-private-value") {
		t.Fatal("status exposed environment values")
	}
}

func TestSubmissionRejectionIsFinalWithoutInventingExitAndCannotReplaceRecord(t *testing.T) {
	root := t.TempDir()
	id := wire.ID()
	if err := rejectSubmission(root, id, "desktop", wire.Fail("desktop_unavailable", "No desktop token")); err != nil {
		t.Fatal(err)
	}
	var v wire.CommandStatus
	file := filepath.Join(root, id, "status.json")
	if err := readJSON(file, &v); err != nil || !v.Final || v.State != "failed" || v.ExitCode != nil || v.Termination.Source != "guest_command_service" || v.Termination.Error.Code != "desktop_unavailable" {
		t.Fatal(v, err)
	}
	if v.Started != nil || v.Output == nil || v.Output.State != "complete" || v.Output.StdoutBytes != 0 || v.Output.StderrBytes != 0 {
		t.Fatal("rejection invented a start or lost known empty output", v)
	}
	snapshot, err := Read(root, id, 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range []wire.StreamChunk{snapshot.Stdout, snapshot.Stderr} {
		if stream.State != "available" || stream.Size != 0 || stream.Data != "" || stream.SHA256 != v.Output.StdoutSHA256 {
			t.Fatal("rejected execution output cannot be verified", stream, v.Output)
		}
	}
	before, _ := os.ReadFile(file)
	if err := rejectSubmission(root, id, "service", wire.Fail("other", "later")); err == nil {
		t.Fatal("existing execution replaced")
	}
	after, _ := os.ReadFile(file)
	if string(before) != string(after) {
		t.Fatal("rejection changed existing evidence")
	}
}
