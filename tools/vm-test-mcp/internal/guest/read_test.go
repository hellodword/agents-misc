package guest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestSupervisorIdentityFixture(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "guest-identity-fixture" {
		return
	}
	_, _ = os.Stdout.WriteString("ready\n")
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestReadReconcilesAnActuallyExitedSupervisor(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSupervisorIdentityFixture$", "--", "guest-identity-fixture")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatal(line, err)
	}
	identity, alive, err := processIdentity(cmd.Process.Pid)
	if err != nil || !alive {
		t.Fatal(identity, alive, err)
	}
	root := t.TempDir()
	id := wire.ID()
	dir := filepath.Join(root, id)
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = atomic(filepath.Join(dir, "status.json"), wire.CommandStatus{State: "running", Supervisor: &identity}); err != nil {
		t.Fatal(err)
	}
	v, err := Read(root, id, 0, 0, 1024)
	if err != nil || v.Status.Final {
		t.Fatal("live supervisor was marked ended", v, err)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	v, err = Read(root, id, 0, 0, 1024)
	if err != nil || !v.Status.Final || v.Status.State != "uncertain" || v.Status.ExitCode != nil || v.Status.Termination.Source != "supervisor_exit" {
		t.Fatal(v, err)
	}
}

func TestReadPendingAndFinalBinaryOutputByOffset(t *testing.T) {
	root := t.TempDir()
	id := wire.ID()
	v, err := Read(root, id, 0, 0, 7)
	if err != nil || !v.Pending || v.Status != nil {
		t.Fatal(v, err)
	}
	dir := filepath.Join(root, id)
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	identity, alive, err := processIdentity(os.Getpid())
	if err != nil || !alive {
		t.Fatal(identity, alive, err)
	}
	status := wire.CommandStatus{State: "running", Supervisor: &identity}
	if err = atomic(filepath.Join(dir, "status.json"), status); err != nil {
		t.Fatal(err)
	}
	v, err = Read(root, id, 0, 0, 7)
	if err != nil || v.Pending || v.Status.Final || v.Stdout.State != "pending" {
		t.Fatal(v, err)
	}
	data := append([]byte{0xff, 0x00, 0xfe}, bytes.Repeat([]byte("字节-boundary\n"), 100)...)
	for name, content := range map[string][]byte{"stdout": data, "stderr": {}} {
		if err = os.WriteFile(filepath.Join(dir, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	code := 41
	status.State, status.Final, status.ExitCode = "exited", true, &code
	status.Output = &wire.CommandOutput{State: "complete", StdoutBytes: int64(len(data))}
	if err = atomic(filepath.Join(dir, "status.json"), status); err != nil {
		t.Fatal(err)
	}
	var got []byte
	var offset int64
	for {
		v, err = Read(root, id, offset, 0, 7)
		if err != nil || !v.Status.Final || *v.Status.ExitCode != 41 || v.Stdout.State != "available" || v.Stdout.Offset != offset || v.Stdout.Size != int64(len(data)) {
			t.Fatal(v, err)
		}
		chunk, err := base64.StdEncoding.DecodeString(v.Stdout.Data)
		if err != nil || len(chunk) > 7 {
			t.Fatal(v, err)
		}
		h := sha256.Sum256(chunk)
		if v.Stdout.SHA256 != hex.EncodeToString(h[:]) || v.Stdout.Next != offset+int64(len(chunk)) {
			t.Fatal("unverifiable chunk", v)
		}
		got = append(got, chunk...)
		offset = v.Stdout.Next
		if offset == v.Stdout.Size {
			break
		}
	}
	if !bytes.Equal(got, data) {
		t.Fatal("binary output changed")
	}
	v, err = Read(root, id, offset, 0, 7)
	if err != nil || v.Stdout.Data != "" || v.Stdout.Next != offset {
		t.Fatal(v, err)
	}
	if err = os.Truncate(filepath.Join(dir, "stdout"), 3); err != nil {
		t.Fatal(err)
	}
	v, err = Read(root, id, offset, 0, 7)
	if err != nil || v.Stdout.Error == nil || v.Stdout.Error.Code != "output_reset" || *v.Status.ExitCode != 41 {
		t.Fatal("stream reset discarded execution evidence", v, err)
	}
	if err = os.Remove(filepath.Join(dir, "stderr")); err != nil {
		t.Fatal(err)
	}
	v, err = Read(root, id, 0, 0, 7)
	if err != nil || v.Stderr.Error == nil || v.Stderr.Error.Code != "output_missing" || !v.Status.Final {
		t.Fatal(v, err)
	}
}

func TestReadRejectsReusedSupervisorIdentityWithoutTouchingCurrentProcess(t *testing.T) {
	root := t.TempDir()
	id := wire.ID()
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	identity, _, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	identity.Start = "different process generation"
	status := wire.CommandStatus{State: "running", Supervisor: &identity}
	if err = atomic(filepath.Join(dir, "status.json"), status); err != nil {
		t.Fatal(err)
	}
	v, err := Read(root, id, 0, 0, 1024)
	if err != nil || v.Status.State != "uncertain" || !v.Status.Final || v.Status.ExitCode != nil || v.Status.Termination.Source != "supervisor_exit" || v.Status.Termination.ProcessExited || v.Status.Termination.Error.Details["stage"] != "supervisor_exit" || v.Error != nil {
		t.Fatal(v, err)
	}
	var retained wire.CommandStatus
	if err = readJSON(filepath.Join(dir, "status.json"), &retained); err != nil || !retained.Final || retained.Error == "" {
		t.Fatal(retained, err)
	}
	if _, alive, err := processIdentity(os.Getpid()); err != nil || !alive {
		t.Fatal("identity observation affected unrelated process", err)
	}
}

func TestReadBoundsAndFinalStatusSurvivesMissingStreams(t *testing.T) {
	root := t.TempDir()
	id := wire.ID()
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	status := wire.CommandStatus{State: "failed", Final: true, Error: "Synthetic launch failure"}
	if err := atomic(filepath.Join(dir, "status.json"), status); err != nil {
		t.Fatal(err)
	}
	v, err := Read(root, id, 0, 0, wire.CommandReadLimit)
	if err != nil || v.Status.State != "failed" || v.Stdout.State != "unavailable" || v.Stderr.State != "unavailable" {
		t.Fatal(v, err)
	}
	for _, offset := range []int64{-1, 1} {
		_, err = Read(root, id, offset, 0, wire.CommandReadLimit+1)
		if err == nil {
			t.Fatal("accepted invalid read")
		}
	}
}
