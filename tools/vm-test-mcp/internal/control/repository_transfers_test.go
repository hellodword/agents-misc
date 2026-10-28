package control

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestTransferIndexCommitAndRestart(t *testing.T) {
	dir := t.TempDir()
	d, err := openRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	object := Object{Size: 7, SHA256: strings.Repeat("a", 64), File: "immutable-file"}
	if err = d.CreateTransfer(ctx, &Transfer{ID: "cache", State: "open"}, map[string]Object{"blob": object}); err != nil {
		t.Fatal(err)
	}
	_, err = d.Exec("CREATE TRIGGER reject_revision BEFORE UPDATE ON transfer_objects BEGIN SELECT RAISE(ABORT,'fixture');END")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.StoreTransferObject(ctx, "cache", "blob", Object{Size: 99, File: "new"}); err == nil {
		t.Fatal("failed publication accepted")
	}
	d.Close()
	d, err = openRepository(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	values, size, err := d.TransferObjects(ctx, "cache")
	if err != nil || size != 7 || values["blob"] != object {
		t.Fatal(values, size, err)
	}
	if _, err = d.Exec("DELETE FROM transfers WHERE id='cache'"); err != nil {
		t.Fatal(err)
	}
	values, _, err = d.TransferObjects(ctx, "cache")
	if err != nil || len(values) != 0 {
		t.Fatal("orphan index", values, err)
	}
}

func BenchmarkObjectPersistence(b *testing.B) {
	for _, count := range []int{0, 10000} {
		b.Run(fmt.Sprintf("%d_objects", count), func(b *testing.B) {
			d, err := openRepository(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer d.Close()
			objects := make(map[string]Object, count)
			object := Object{Size: 256, SHA256: strings.Repeat("a", 64), File: ".object-" + strings.Repeat("b", 32)}
			for i := range count {
				objects[fmt.Sprintf("nar/%d.nar", i)] = object
			}
			ctx := context.Background()
			if err := d.CreateTransfer(ctx, &Transfer{ID: "cache", State: "open"}, objects); err != nil {
				b.Fatal(err)
			}
			if err := d.DeclareUpload(ctx, &Transfer{ID: "cache", State: "open"}, []wire.UploadObject{{Name: "nar/new.nar", Size: object.Size, SHA256: object.SHA256}}); err != nil {
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
