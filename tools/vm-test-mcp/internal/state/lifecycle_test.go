package state

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
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var vacuum int
	if err = db.QueryRow("PRAGMA auto_vacuum").Scan(&vacuum); err != nil || vacuum != 2 {
		t.Fatal("incremental vacuum not active", vacuum, err)
	}
	id, node := wire.ID(), wire.ID()
	task := map[string]any{"id": id, "kind": "lab", "state": "running", "cleanup": "pending", "created": time.Now().UTC(), "nodes": []any{map[string]any{"id": node, "state": "running", "health": map[string]any{"checked": "first", "last_success": "first", "successes": 1, "state": "available"}}}}
	doc := Document{Kind: "task", ID: id, Value: task}
	if err = db.Save(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	var before, after int64
	db.QueryRow("SELECT total_changes()").Scan(&before)
	health := task["nodes"].([]any)[0].(map[string]any)["health"].(map[string]any)
	for i := 0; i < 1000; i++ {
		health["checked"] = time.Now()
		health["last_success"] = time.Now()
		health["successes"] = i + 2
		if err = db.Save(context.Background(), doc); err != nil {
			t.Fatal(err)
		}
	}
	db.QueryRow("SELECT total_changes()").Scan(&after)
	if after != before {
		t.Fatalf("healthy probes wrote %d rows", after-before)
	}
	health["state"] = "unavailable"
	health["error"] = map[string]any{"code": "fault"}
	if err = db.Save(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT total_changes()").Scan(&after)
	if after != before+1 {
		t.Fatal("fault transition must update only its node", before, after)
	}
}
func TestBodySweepAndProtectedPhysicalDeletion(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	id, command := wire.ID(), wire.ID()
	body := strings.Repeat("synthetic secret body ", 4000)
	task := map[string]any{"id": id, "kind": "lab", "state": "finished", "cleanup": "complete", "created": time.Now().UTC(), "artifacts": []any{map[string]any{"id": wire.ID(), "size": 0, "pinned": true}}}
	doc := Document{Kind: "task", ID: id, Value: task}
	if err = db.CreateCommand(ctx, doc, command, wire.CommandSpec{Argv: []string{"example", body}}); err != nil {
		t.Fatal(err)
	}
	if err = db.AppendInput(ctx, doc, command, wire.Input{Data: body}); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteExecution(ctx, id); err == nil {
		t.Fatal("pin did not prevent deletion")
	}
	if _, err = db.CommandSpec(ctx, id, command); err != nil {
		t.Fatal("protected deletion removed dependent data", err)
	}
	// Orphan and referenced files are equally old, but only the orphan is collectible.
	orphan, _, _, err := db.publishBody([]byte("uncommitted data"))
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
	if _, err = db.Exec("UPDATE evidence SET pinned=0"); err != nil {
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
	db.QueryRow("SELECT count(*) FROM command_specs").Scan(&count)
	if count != 0 {
		t.Fatal("spec indexes remain")
	}
	if err = db.Compact(ctx); err != nil {
		t.Fatal(err)
	}
}
