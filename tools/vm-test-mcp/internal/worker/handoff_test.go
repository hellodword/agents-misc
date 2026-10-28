package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestImportEvidenceRedactsCapabilityAcrossWriteBoundaries(t *testing.T) {
	secret := "http://executor.invalid/files/scoped-capability.signature"
	input := "copying from '" + secret + "'...\n" + secret + " complete\n"
	want := strings.ReplaceAll(input, secret, "[redacted cache]")
	for split := 0; split <= len(input); split++ {
		var out strings.Builder
		log := &importLog{w: &out, secret: secret}
		for _, part := range []string{input[:split], input[split:]} {
			if n, err := log.Write([]byte(part)); err != nil || n != len(part) {
				t.Fatal(split, n, err)
			}
		}
		if err := log.Flush(); err != nil || out.String() != want {
			t.Fatal(split, out.String(), err)
		}
	}
	var out strings.Builder
	log := &importLog{w: &out, secret: secret}
	log.Write([]byte(secret[:len(secret)-1]))
	log.Flush()
	if out.String() != "[redacted cache]" {
		t.Fatal("interrupted output exposed credential prefix", out.String())
	}
}

func TestHandoffIntegrityAndRecovery(t *testing.T) {
	w, cfg := fixture(t, os.Getuid())
	id := wire.ID()
	if err := w.root.MkdirAll(id, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.root.Path, id, "task.log"), []byte("immutable output"), 0600); err != nil {
		t.Fatal(err)
	}
	j := &Job{Started: time.Now().UTC(), Request: Request{TaskID: id}, State: "succeeded", Cleanup: "complete", Files: []File{{ID: wire.ID(), Name: "task.log", Root: filepath.Join(w.root.Path, id), Relative: "task.log", Size: 16}}}
	if err := w.indexFiles(j); err != nil {
		t.Fatal(err)
	}
	w.jobs[id] = j
	if err := w.save(j); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Call("handoff", Request{TaskID: id, ManifestDigest: "incorrect"}); wire.Safe(err).Code != "evidence_integrity" {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.root.Path, id, "task.log")); err != nil {
		t.Fatal("mismatch lost evidence", err)
	}
	digest := wire.ManifestDigest([]wire.HandoffFile{{ID: j.Files[0].ID, Size: j.Files[0].Size, SHA256: j.Files[0].SHA256}})
	// Recreate a crash after acknowledgement persistence and before file deletion.
	j.HandoffComplete = true
	j.ManifestDigest = digest
	if err := w.save(j); err != nil {
		t.Fatal(err)
	}
	w.Close()
	w, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err = w.Call("handoff", Request{TaskID: id, ManifestDigest: digest}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(w.root.Path, id)); !os.IsNotExist(err) {
		t.Fatal("worker copies remain", err)
	}
	var n int
	w.db.QueryRow("SELECT count(*) FROM worker_files WHERE task_id=?", id).Scan(&n)
	if n != 0 {
		t.Fatal("worker file index remains")
	}
	if _, err = w.Call("handoff", Request{TaskID: id, ManifestDigest: digest}); err != nil {
		t.Fatal("handoff retry failed", err)
	}
	if _, err = w.Call("forget", Request{TaskID: id}); err != nil {
		t.Fatal(err)
	}
	w.db.QueryRowContext(context.Background(), "SELECT count(*) FROM worker_jobs").Scan(&n)
	if n != 0 {
		t.Fatal("worker summary retained")
	}
}

func TestWorkerRetentionProtectsUnfinishedHandoff(t *testing.T) {
	w, _ := fixture(t, os.Getuid())
	defer w.Close()
	now := time.Now().UTC()
	old := now.Add(-31 * 24 * time.Hour)
	for _, released := range []bool{false, true} {
		j := &Job{Request: Request{TaskID: wire.ID()}, Started: old, State: "succeeded", Cleanup: "complete", HandoffComplete: true, CopiesReleased: released}
		if err := w.save(j); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.expireCompleted(now); err != nil {
		t.Fatal(err)
	}
	var count int
	w.db.QueryRow("SELECT count(*) FROM worker_jobs").Scan(&count)
	if count != 1 {
		t.Fatal("worker TTL did not preserve unfinished handoff", count)
	}
	var released bool
	w.db.QueryRow("SELECT json_extract(body,'$.copies_released') FROM worker_jobs").Scan(&released)
	if released {
		t.Fatal("wrong worker record retained")
	}
}
