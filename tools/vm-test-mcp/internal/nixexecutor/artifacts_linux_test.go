package nixexecutor

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/unix"
)

func TestSealedSessionInventorySurvivesRestartAndReleasesOwnedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := safefs.Private(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	db, err := state.OpenExecutor(root.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &Executor{root: root, db: db, sessions: map[string]*runtimeSession{}}
	s := &Session{ID: wire.ID(), WorkerInstance: wire.ID(), TaskID: wire.ID(), ExecutorInstance: db.InstanceID, Resources: wire.NixResources{State: "owned"}}
	if err := e.save(s); err != nil {
		t.Fatal(err)
	}
	a := Request{SessionID: s.ID, WorkerInstance: s.WorkerInstance, TaskID: s.TaskID}
	base := path.Join("builds", s.ID, "nix-build-test.drv-0.chroot", "build", "vm-test-evidence")
	if err = root.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	for i := range 205 {
		if err = os.WriteFile(filepath.Join(root.Path, base, fmt.Sprintf("node-%03d.log", i)), []byte("retained result"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sibling := filepath.Join(root.Path, "unrelated")
	if err = os.WriteFile(sibling, []byte("different session"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, op := range []func() error{
		func() error { return os.Symlink(sibling, filepath.Join(root.Path, base, "outside-link")) },
		func() error { return os.Link(sibling, filepath.Join(root.Path, base, "outside-hardlink")) },
		func() error { return unix.Mkfifo(filepath.Join(root.Path, base, "pipe"), 0600) },
	} {
		if err = op(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = e.Call("evidence_list", a); wire.Safe(err).Code != "resources_active" {
		t.Fatal("unsealed inventory", err)
	}
	s.Resources.State = "released"
	if err = e.save(s); err != nil {
		t.Fatal(err)
	}
	e.sessions = map[string]*runtimeSession{}
	v, err := e.Call("evidence_list", a)
	if err != nil {
		t.Fatal(err)
	}
	first := v.(ArtifactPage)
	if len(first.Files) != 200 || first.NextCursor != 200 {
		t.Fatal("first inventory page", first)
	}
	// Reconstruct the executor as after a process restart, including its ledger
	// lookup. Object references remain bound to the original session.
	e.sessions = map[string]*runtimeSession{}
	a.Cursor = first.NextCursor
	v, err = e.Call("evidence_list", a)
	if err != nil {
		t.Fatal(err)
	}
	last := v.(ArtifactPage)
	if len(last.Files) != 5 || last.NextCursor != 0 {
		t.Fatal("last inventory page", last)
	}
	a.ArtifactID = first.Files[0].ID
	file, size, err := e.openArtifact(a)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	file.Close()
	if err != nil || string(data) != "retained result" || size != int64(len(data)) {
		t.Fatal("retained bytes", string(data), size, err)
	}
	other := a
	other.TaskID = wire.ID()
	if _, _, err = e.openArtifact(other); wire.Safe(err).Code != "ownership_mismatch" {
		t.Fatal("cross-task reference", err)
	}
	other = a
	other.ArtifactID = "../unrelated"
	if _, _, err = e.openArtifact(other); err == nil {
		t.Fatal("path accepted as an object identity")
	}
	if _, err = e.Call("evidence_release", a); err != nil {
		t.Fatal(err)
	}
	if _, err = e.Call("evidence_release", a); err != nil {
		t.Fatal("release retry", err)
	}
	if _, err = root.Stat(path.Join("builds", s.ID)); !os.IsNotExist(err) {
		t.Fatal("build directory retained", err)
	}
	if data, err = os.ReadFile(sibling); err != nil || string(data) != "different session" {
		t.Fatal("sibling changed", err)
	}
	if _, _, err = e.openArtifact(a); wire.Safe(err).Code != "evidence_released" {
		t.Fatal("released object readable", err)
	}
}
