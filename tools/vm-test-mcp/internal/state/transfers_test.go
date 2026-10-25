package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func transferDatabase10(t *testing.T, docs ...Document) string {
	t.Helper()
	dir := t.TempDir()
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	if err := d.CreateCommand(ctx, Document{Kind: "task", ID: "history", Value: map[string]any{"state": "finished", "exit_code": 17}}, "command", wire.CommandSpec{Argv: []string{"retained"}}); err != nil {
		t.Fatal(err)
	}
	if err := d.Event(ctx, "history", map[string]string{"message": "original"}); err != nil {
		t.Fatal(err)
	}
	if err := d.Save(ctx, docs...); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec("DROP TABLE transfer_objects; PRAGMA user_version=10"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTransferMigrationPreservesHistoryAndRestart(t *testing.T) {
	ctx := context.Background()
	objects := map[string]any{"nar/synthetic.nar": map[string]any{"size": 7, "sha256": strings.Repeat("a", 64), "extension": []int{3, 9}}}
	dir := transferDatabase10(t,
		Document{Kind: "transfer", ID: "cache", Value: map[string]any{"id": "cache", "state": "sealed", "objects": objects, "extra": "retained"}},
		Document{Kind: "transfer", ID: "purged", Value: map[string]any{"state": "purged", "objects": objects}},
	)
	for range 2 {
		d, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"cache", "purged"} {
			got, size, err := d.TransferObjects(ctx, id)
			want := TransferObject{Size: 7, SHA256: strings.Repeat("a", 64), File: "nar/synthetic.nar"}
			if err != nil || size != 7 || len(got) != 1 || got[want.File] != want {
				t.Fatal(got, size, err)
			}
			var raw []byte
			if err := d.QueryRow("SELECT body FROM transfer_objects WHERE transfer_id=?", id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var record struct{ Extension []int }
			if err := json.Unmarshal(raw, &record); err != nil || !reflect.DeepEqual(record.Extension, []int{3, 9}) {
				t.Fatal(string(raw), err)
			}
		}
		var header map[string]any
		if err := d.Load(ctx, "transfer", "cache", &header); err != nil || header["extra"] != "retained" || header["state"] != "sealed" {
			t.Fatal(header, err)
		}
		var task map[string]any
		if err := d.Load(ctx, "task", "history", &task); err != nil || task["exit_code"] != float64(17) {
			t.Fatal(task, err)
		}
		spec, err := d.CommandSpec(ctx, "history", "command")
		if err != nil || !reflect.DeepEqual(spec.Argv, []string{"retained"}) {
			t.Fatal(spec, err)
		}
		events, sequence, err := d.Events(ctx, "history", 0, 10)
		if err != nil || len(events) != 1 || sequence != 1 {
			t.Fatal(events, sequence, err)
		}
		d.Close()
	}
}

func TestTransferMigrationRollbackAndRetry(t *testing.T) {
	good := map[string]any{"objects": map[string]TransferObject{"blob": {Size: 7, SHA256: "synthetic"}}}
	bad := map[string]any{"objects": map[string]TransferObject{"blob": {Size: -1, SHA256: "invalid"}}}
	dir := transferDatabase10(t, Document{Kind: "transfer", ID: "first", Value: good}, Document{Kind: "transfer", ID: "second", Value: bad})
	if d, err := Open(dir); err == nil {
		d.Close()
		t.Fatal("invalid metadata migrated")
	}
	raw, err := sql.Open("sqlite3", filepath.Join(dir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 10 {
		t.Fatal(version, err)
	}
	for id, want := range map[string]any{"first": good, "second": bad} {
		var body []byte
		if err := raw.QueryRow("SELECT body FROM documents WHERE kind='transfer' AND id=?", id).Scan(&body); err != nil || string(body) != string(wire.JSON(want)) {
			t.Fatal(id, string(body), err)
		}
	}
	// Repair only the invalid synthetic fixture. Both records must migrate on retry.
	if _, err := raw.Exec("UPDATE documents SET body=? WHERE kind='transfer' AND id='second'", wire.JSON(good)); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, id := range []string{"first", "second"} {
		objects, size, err := d.TransferObjects(context.Background(), id)
		if err != nil || size != 7 || objects["blob"].File != "blob" {
			t.Fatal(objects, size, err)
		}
	}
}

func BenchmarkTransferObjectPersistence(b *testing.B) {
	for _, count := range []int{0, 10000} {
		b.Run(fmt.Sprintf("%d_objects", count), func(b *testing.B) {
			d, err := Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer d.Close()
			objects := make(map[string]TransferObject, count)
			object := TransferObject{Size: 256, SHA256: strings.Repeat("a", 64), File: ".object-" + strings.Repeat("b", 32)}
			for i := range count {
				objects[fmt.Sprintf("nar/%d.nar", i)] = object
			}
			ctx := context.Background()
			if err := d.CreateTransfer(ctx, Document{Kind: "transfer", ID: "cache", Value: map[string]any{"state": "open"}}, objects); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := d.StoreTransferObject(ctx, "cache", "nar/new.nar", object); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(len(wire.JSON(object))), "metadata-B/op")
		})
	}
}
