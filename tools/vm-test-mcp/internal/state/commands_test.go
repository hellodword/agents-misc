package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// The new contract requires an empty state directory; rejection must not
// rewrite or delete any old schema, payload or version.
func TestLegacySchemaRefusalPreservesDatabase(t *testing.T) {
	for _, version := range []int{9, 10, 11} {
		dir := t.TempDir()
		raw, err := sql.Open("sqlite3", dir+"/state.sqlite")
		if err != nil {
			t.Fatal(err)
		}
		_, err = raw.Exec("CREATE TABLE documents(body BLOB);INSERT INTO documents VALUES('historical');PRAGMA user_version=" + fmt.Sprint(version))
		if err != nil {
			t.Fatal(err)
		}
		if d, err := Open(dir); err == nil {
			d.Close()
			t.Fatal("legacy state accepted")
		}
		var retained string
		var got int
		if err = raw.QueryRow("SELECT body FROM documents").Scan(&retained); err != nil || retained != "historical" {
			t.Fatal(retained, err)
		}
		if err = raw.QueryRow("PRAGMA user_version").Scan(&got); err != nil || got != version {
			t.Fatal(got, err)
		}
		raw.Close()
	}
}
func TestCommandFilesSurviveRestartAndDetectCorruption(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	doc := Document{Kind: "task", ID: "task", Value: map[string]any{"id": "task", "kind": "lab", "state": "running", "cleanup": "pending"}}
	spec := wire.CommandSpec{ID: "command", Argv: []string{"example", "x y"}, Env: map[string]string{"FIXTURE": "retained"}}
	if err = d.CreateCommand(ctx, doc, "command", spec); err != nil {
		t.Fatal(err)
	}
	packet := wire.Input{Sequence: 0, Data: "中文\x00\n", EOF: true}
	if err = d.AppendInput(ctx, doc, "command", packet); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.CommandSpec(ctx, "task", "command")
	if err != nil || string(wire.JSON(got)) != string(wire.JSON(spec)) {
		t.Fatal(got, err)
	}
	packets, err := d.Inputs(ctx, "task", "command", 0, 1)
	if err != nil || len(packets) != 1 || packets[0] != packet {
		t.Fatal(packets, err)
	}
	var file string
	if err = d.QueryRow("SELECT file FROM command_inputs").Scan(&file); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, file), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Inputs(ctx, "task", "command", 0, 1); err == nil {
		t.Fatal("corrupted input silently read")
	}
}

func TestInputTransactionRollbackRecoveryAndScopedPurge(t *testing.T) {
	ctx := context.Background()
	d, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, id := range []string{"one", "other"} {
		doc := Document{Kind: "task", ID: id, Value: map[string]any{"stdin": InputQueue{}}}
		if err = d.CreateCommand(ctx, doc, "command", wire.CommandSpec{ID: "command", Argv: []string{"fixture"}}); err != nil {
			t.Fatal(err)
		}
		if err = d.AppendInput(ctx, doc, "command", wire.Input{Sequence: 0, Data: "retained"}); err != nil {
			t.Fatal(err)
		}
	}
	doc := Document{Kind: "task", ID: "one", Value: map[string]any{"stdin": InputQueue{NextSequence: 2, Closed: true}}}
	if err = d.AppendInput(ctx, doc, "command", wire.Input{Sequence: 0, EOF: true}); err == nil {
		t.Fatal("conflicting input replaced")
	}
	var original struct{ Stdin InputQueue }
	if err = d.Load(ctx, "task", "one", &original); err != nil || original.Stdin.NextSequence != 0 {
		t.Fatal(original, err)
	}
	doc.Value = make(chan int)
	if err = d.AppendInput(ctx, doc, "command", wire.Input{Sequence: 1, EOF: true}); err == nil {
		t.Fatal("unserializable task accepted")
	}
	got, err := d.Inputs(ctx, "one", "command", 0, 16)
	if err != nil || len(got) != 1 || got[0].Data != "retained" {
		t.Fatal(got, err)
	}
	doc.Value = map[string]any{"stdin": InputQueue{NextSequence: 2, Closed: true}}
	if err = d.AppendInput(ctx, doc, "command", wire.Input{Sequence: 1, EOF: true}); err != nil {
		t.Fatal(err)
	}
	if err = d.PurgeCommandData(ctx, Document{Kind: "task", ID: "one", Value: map[string]any{"purged": true}}); err != nil {
		t.Fatal(err)
	}
	got, err = d.Inputs(ctx, "one", "command", 0, 16)
	if err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
	got, err = d.Inputs(ctx, "other", "command", 0, 16)
	if err != nil || len(got) != 1 || got[0].Data != "retained" {
		t.Fatal(got, err)
	}
}

func BenchmarkStatusPersistence(b *testing.B) {
	for _, payload := range []int{0, 32 << 20} {
		b.Run(fmtBytes(payload), func(b *testing.B) {
			d, err := Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer d.Close()
			ctx := context.Background()
			q := InputQueue{}
			doc := Document{Kind: "task", ID: "task", Value: map[string]any{"stdin": &q, "state": "running"}}
			if err = d.CreateCommand(ctx, doc, "command", wire.CommandSpec{ID: "command", Argv: []string{"fixture"}}); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < payload/wire.InputPacketLimit; i++ {
				p := wire.Input{Sequence: i, Data: strings.Repeat("x", wire.InputPacketLimit)}
				q.Accept(p)
				if err = d.AppendInput(ctx, doc, "command", p); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err = d.Save(ctx, doc); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func fmtBytes(n int) string { v, _ := json.Marshal(n); return string(v) + "B_stdin" }
