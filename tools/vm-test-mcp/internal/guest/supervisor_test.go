package guest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type fakeChild struct {
	done    chan struct{}
	drained chan struct{}
	once    sync.Once
	in      io.Reader
	out     io.Writer
}

func TestLaunchAndPipeDeliveryHaveSeparateConfirmation(t *testing.T) {
	root, id := t.TempDir(), wire.ID()
	request := filepath.Join(root, "request.json")
	if err := os.WriteFile(request, wire.JSON(wire.CommandSpec{ID: id, Argv: []string{"synthetic"}}), 0600); err != nil {
		t.Fatal(err)
	}
	launchEntered, allowLaunch, allowRead := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), root, request, func(ctx context.Context, spec wire.CommandSpec, in *os.File, out, errout io.Writer) (Child, error) {
			close(launchEntered)
			<-allowLaunch
			child := &fakeChild{done: make(chan struct{}), drained: make(chan struct{})}
			go func() { defer close(child.drained); <-allowRead; _, _ = io.Copy(out, in); _ = child.Kill(ctx) }()
			return child, nil
		})
	}()
	<-launchEntered
	var status wire.CommandStatus
	statusFile := filepath.Join(root, id, "status.json")
	if err := readJSON(statusFile, &status); err != nil || status.Started != nil {
		t.Fatal("launch not yet confirmed", status, err)
	}
	close(allowLaunch)
	input := filepath.Join(root, "input.json")
	// Multiple maximum-sized packets exceed the OS pipe capacity on both guests.
	for i := range 32 {
		if err := os.WriteFile(input, wire.JSON(wire.Input{Sequence: i, Data: strings.Repeat("x", wire.InputPacketLimit), EOF: i == 31}), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Input(root, id, input); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if readJSON(statusFile, &status) == nil && status.Started != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status.Started == nil || status.WrittenInput >= 32 || status.InputClosed {
		t.Fatal("durable packets were confused with pipe writes", status)
	}
	close(allowRead)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stdin did not drain")
	}
	if err := readJSON(statusFile, &status); err != nil || status.WrittenInput != 32 || !status.InputClosed {
		t.Fatal(status, err)
	}
}

func TestCommandPATHOverride(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "synthetic-tool")
	if e := os.WriteFile(exe, []byte("fixture"), 0700); e != nil {
		t.Fatal(e)
	}
	got, err := commandExecutable("synthetic-tool", "", map[string]string{"PATH": dir})
	if err != nil || got != exe {
		t.Fatal(got, err)
	}
	if _, err = commandExecutable("synthetic-tool", "", map[string]string{"PATH": t.TempDir()}); err == nil {
		t.Fatal("command ignored its PATH")
	}
}

func (p *fakeChild) Wait(ctx context.Context) (processExit, error) {
	select {
	case <-p.done:
		return exitedWithCode(0), nil
	case <-ctx.Done():
		return processExit{}, ctx.Err()
	}
}
func (p *fakeChild) Kill(context.Context) error { p.once.Do(func() { close(p.done) }); return nil }
func (p *fakeChild) Drain(ctx context.Context) error {
	select {
	case <-p.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *fakeChild) Close() error { return p.Kill(context.Background()) }
func fakeLaunch(_ context.Context, _ wire.CommandSpec, in *os.File, out, errout io.Writer) (Child, error) {
	p := &fakeChild{done: make(chan struct{}), drained: make(chan struct{}), in: in, out: out}
	go func() { defer close(p.drained); io.Copy(out, in); p.Kill(context.Background()) }()
	return p, nil
}
func TestOrderedInputEOFAndIndependentCancellation(t *testing.T) {
	root := t.TempDir()
	ids := []string{wire.ID(), wire.ID()}
	done := make([]chan error, 2)
	for i, id := range ids {
		req := filepath.Join(root, id+".json")
		os.WriteFile(req, wire.JSON(wire.CommandSpec{ID: id, Argv: []string{"synthetic"}}), 0600)
		done[i] = make(chan error, 1)
		go func() { done[i] <- run(context.Background(), root, req, fakeLaunch) }()
	}
	wait := func(id string, want string) {
		t.Helper()
		end := time.Now().Add(3 * time.Second)
		for time.Now().Before(end) {
			var s wire.CommandStatus
			if readJSON(filepath.Join(root, id, "status.json"), &s) == nil && s.State == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("state %s not reached", want)
	}
	for _, id := range ids {
		wait(id, "running")
	}
	for seq, text := range []string{"first", ":second"} {
		p := filepath.Join(root, "input.json")
		os.WriteFile(p, wire.JSON(wire.Input{Sequence: seq, Data: text, EOF: seq == 1}), 0600)
		if e := Input(root, ids[0], p); e != nil {
			t.Fatal(e)
		}
		if e := Input(root, ids[0], p); e != nil {
			t.Fatal("identical packet retry", e)
		}
	}
	select {
	case e := <-done[0]:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("EOF not observed")
	}
	out, e := os.ReadFile(filepath.Join(root, ids[0], "stdout"))
	if e != nil || string(out) != "first:second" {
		t.Fatal(string(out), e)
	}
	var final wire.CommandStatus
	hash := sha256.Sum256(out)
	if e = readJSON(filepath.Join(root, ids[0], "status.json"), &final); e != nil || final.Output.State != "complete" || final.Output.StdoutSHA256 != hex.EncodeToString(hash[:]) || !final.InputClosed || final.WrittenInput != 2 {
		t.Fatal("EOF or retained output identity was lost", final, e)
	}
	wait(ids[1], "running")
	if e = Cancel(root, ids[1]); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done[1]:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel not observed")
	}
	wait(ids[1], "cancelled")
}
