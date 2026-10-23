package guest

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type fakeChild struct {
	done chan struct{}
	once sync.Once
	in   io.Reader
	out  io.Writer
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

func (p *fakeChild) Wait() (int, error) { <-p.done; return 0, nil }
func (p *fakeChild) Kill() error        { p.once.Do(func() { close(p.done) }); return nil }
func (p *fakeChild) Close() error       { return p.Kill() }
func fakeLaunch(_ context.Context, _ wire.CommandSpec, in io.Reader, out, errout io.Writer) (Child, error) {
	p := &fakeChild{done: make(chan struct{}), in: in, out: out}
	go func() { io.Copy(out, in); p.Kill() }()
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
