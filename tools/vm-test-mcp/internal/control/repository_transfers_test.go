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

// Measure complete manifest publication rather than repeated idempotent writes.
// The remaining-count read is the readiness check on each incoming object.
func BenchmarkObjectPersistence(b *testing.B) {
	for _, count := range []int{1000, 20000} {
		b.Run(fmt.Sprintf("%d_objects", count), func(b *testing.B) {
			d, err := openRepository(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer d.Close()
			ctx := context.Background()
			items := make([]wire.UploadObject, count)
			for i := range items {
				items[i] = wire.UploadObject{Name: fmt.Sprintf("nar/%d.nar", i), Size: 256, SHA256: fmt.Sprintf("%064x", i)}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				doc := &Transfer{ID: wire.ID(), State: "open"}
				if err = d.CreateTransfer(ctx, doc, nil); err != nil {
					b.Fatal(err)
				}
				if err = d.DeclareUpload(ctx, doc, items); err != nil {
					b.Fatal(err)
				}
				for i, item := range items {
					object := Object{Size: item.Size, SHA256: item.SHA256, File: inputObjectFile(item.SHA256)}
					if err = d.StoreTransferObject(ctx, doc.ID, item.Name, object); err != nil {
						b.Fatal(err)
					}
					var remaining int
					if err = d.QueryRow("SELECT remaining FROM transfers WHERE id=?", doc.ID).Scan(&remaining); err != nil || remaining != count-i-1 {
						b.Fatal(remaining, err)
					}
				}
				if _, err = d.Exec("DELETE FROM transfers WHERE id=?", doc.ID); err != nil {
					b.Fatal(err)
				}
				if _, err = d.Exec("DELETE FROM input_objects"); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(count), "objects/op")
		})
	}
}
