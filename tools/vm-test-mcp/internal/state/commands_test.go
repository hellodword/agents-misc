package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func legacyDatabase(t *testing.T, task any) string {
	t.Helper()
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.Save(context.Background(), Document{Kind: "task", ID: "historical", Value: task}, Document{Kind: "operation", ID: "receipt", Value: map[string]any{"result": "kept"}}); err != nil {
		t.Fatal(err)
	}
	if err = d.Event(context.Background(), "historical", map[string]string{"message": "original event"}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Exec("DROP TABLE command_inputs; DROP TABLE command_specs; DROP TABLE transfer_objects; PRAGMA user_version=9"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCommandMigrationPreservesHistoryAndRestart(t *testing.T) {
	ctx := context.Background()
	spec := wire.CommandSpec{ID: "command", Argv: []string{"example", "x y"}, Env: map[string]string{"FIXTURE": "retained"}}
	packets := []wire.Input{{Sequence: 0, Data: "first\x00\n"}, {Sequence: 1, Data: "后续", EOF: true}}
	legacy := map[string]any{"id": "historical", "state": "finished", "extension": map[string]any{"keep": 17}, "commands": []any{map[string]any{"id": "command", "spec": spec, "input": packets, "exit_code": 37, "unknown_field": "preserved"}}}
	dir := legacyDatabase(t, legacy)
	for range 2 {
		d, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		var task struct {
			Extension map[string]int
			Commands  []struct {
				ExitCode int    `json:"exit_code"`
				Unknown  string `json:"unknown_field"`
				Stdin    InputQueue
			}
		}
		if err = d.Load(ctx, "task", "historical", &task); err != nil {
			t.Fatal(err)
		}
		c := task.Commands[0]
		if task.Extension["keep"] != 17 || c.ExitCode != 37 || c.Unknown != "preserved" || c.Stdin.NextSequence != 2 || c.Stdin.Bytes != int64(len(packets[0].Data)+len(packets[1].Data)) || !c.Stdin.Closed {
			t.Fatal(task)
		}
		got, err := d.CommandSpec(ctx, "historical", "command")
		if err != nil || string(wire.JSON(got)) != string(wire.JSON(spec)) {
			t.Fatal(got, err)
		}
		for i, want := range packets {
			page, err := d.Inputs(ctx, "historical", "command", i, 1)
			if err != nil || len(page) != 1 || page[0] != want {
				t.Fatal(page, err)
			}
		}
		events, next, err := d.Events(ctx, "historical", 0, 10)
		if err != nil || len(events) != 1 || next != 1 {
			t.Fatal(events, next, err)
		}
		var receipt map[string]string
		if err = d.Load(ctx, "operation", "receipt", &receipt); err != nil || receipt["result"] != "kept" {
			t.Fatal(receipt, err)
		}
		d.Close()
	}
}

func TestCommandMigrationRollbackPreservesOriginal(t *testing.T) {
	legacy := map[string]any{"commands": []any{map[string]any{"id": "good", "spec": wire.CommandSpec{}, "input": []wire.Input{{Sequence: 0, Data: "retained"}}}, map[string]any{"id": "bad", "spec": wire.CommandSpec{}, "input": []wire.Input{{Sequence: 4, Data: "untouched"}}}}}
	dir := legacyDatabase(t, legacy)
	if d, err := Open(dir); err == nil {
		d.Close()
		t.Fatal("inconsistent historical sequence accepted")
	}
	raw, err := sql.Open("sqlite3", dir+"/state.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version int
	var body []byte
	if err = raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 9 {
		t.Fatal(version, err)
	}
	if err = raw.QueryRow("SELECT body FROM documents WHERE kind='task'").Scan(&body); err != nil || string(body) != string(wire.JSON(legacy)) {
		t.Fatal(string(body), err)
	}
	// Repair only the synthetic invalid fixture, then verify migration can retry.
	legacy["commands"].([]any)[1].(map[string]any)["input"] = []wire.Input{{Sequence: 0, Data: "untouched"}}
	if _, err = raw.Exec("UPDATE documents SET body=? WHERE kind='task'", wire.JSON(legacy)); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	got, err := d.Inputs(context.Background(), "historical", "bad", 0, 1)
	if err != nil || len(got) != 1 || got[0].Data != "untouched" {
		t.Fatal(got, err)
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
	var original map[string]InputQueue
	if err = d.Load(ctx, "task", "one", &original); err != nil || original["stdin"].NextSequence != 0 {
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
