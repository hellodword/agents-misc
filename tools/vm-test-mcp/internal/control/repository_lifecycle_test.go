package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestHealthyObservationsDoNotGrowDatabase(t *testing.T) {
	dir := t.TempDir()
	db, err := openRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var vacuum int
	if err = db.QueryRow("PRAGMA auto_vacuum").Scan(&vacuum); err != nil || vacuum != 2 {
		t.Fatal("incremental vacuum not active", vacuum, err)
	}
	id, node := wire.ID(), wire.ID()
	task := newTask("lab", 60)
	task.ID = id
	task.State = "running"
	task.lab().Nodes = []*Node{{ID: node, State: "running", Health: Health{Commands: Observation{State: "available"}}}}
	doc := task
	if err = db.Save(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	db.QueryRow("SELECT total_changes()").Scan(&before)
	health := &task.lab().Nodes[0].Health.Commands
	for i := 0; i < 1000; i++ {
		health.Checked = new(time.Now())
		health.LastSuccess = new(time.Now())
		health.Successes = i + 2
		if err = db.Save(context.Background(), doc); err != nil {
			t.Fatal(err)
		}
	}
	db.QueryRow("SELECT total_changes()").Scan(&after)
	if after != before {
		t.Fatalf("healthy probes wrote %d rows", after-before)
	}
	health.State = "unavailable"
	health.Error = wire.Safe(wire.Fail("fault", "synthetic fault"))
	if err = db.Save(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT total_changes()").Scan(&after)
	if after != before+2 {
		t.Fatal("fault transition must update its node and execution revision", before, after)
	}
}
func TestBodySweepAndProtectedPhysicalDeletion(t *testing.T) {
	dir := t.TempDir()
	db, err := openRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	id, command := wire.ID(), wire.ID()
	body := strings.Repeat("synthetic secret body ", 4000)
	task := storedCommandFixture(id, command)
	task.Artifacts = []*Artifact{{ID: wire.ID(), Size: 0, Pinned: true}}
	doc := task
	if err = db.CreateCommand(ctx, doc, command, wire.CommandSpec{Argv: []string{"example", body}}); err != nil {
		t.Fatal(err)
	}
	if err = db.AppendInput(ctx, doc, command, wire.Input{Data: body}); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteExecution(ctx, id); err == nil {
		t.Fatal("active execution was deleted")
	}
	if _, err = db.CommandSpec(ctx, id, command); err != nil {
		t.Fatal("protected deletion removed dependent data", err)
	}
	// Orphan and referenced files are equally old, but only the orphan is collectible.
	orphan, _, _, err := db.PublishBody([]byte("uncommitted data"))
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "bodies"))
	past := time.Now().Add(-2 * time.Hour)
	for _, e := range entries {
		if err = os.Chtimes(filepath.Join(dir, "bodies", e.Name()), past, past); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.SweepBodies(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, orphan)); !os.IsNotExist(err) {
		t.Fatal("orphan body was not collected")
	}
	if _, err = db.CommandSpec(ctx, id, command); err != nil {
		t.Fatal("referenced body lost", err)
	}
	task.State, task.Cleanup = "finished", "complete"
	if err = db.Save(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteExecution(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err = db.SweepBodies(ctx); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(filepath.Join(dir, "bodies"))
	if len(entries) != 0 {
		t.Fatal("expired bodies remain", len(entries))
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM commands").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("spec indexes remain")
	}
	if err = db.Compact(ctx); err != nil {
		t.Fatal(err)
	}
}
