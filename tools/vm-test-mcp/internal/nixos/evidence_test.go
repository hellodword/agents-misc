package nixos

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type evidenceClient struct {
	id, body, hash string
	reads          int
}

func (c *evidenceClient) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	switch method {
	case "log":
		return wire.JSON(map[string]any{"data": "", "next_offset": 0, "eof": true}), nil
	case "files":
		return wire.JSON(map[string]any{"items": []any{map[string]any{"id": c.id, "name": "result", "size": int64(len(c.body)), "sha256": c.hash}}, "next_cursor": 1, "eof": true}), nil
	case "file":
		c.reads++
		return wire.JSON(map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(c.body)), "next_offset": len(c.body), "eof": true}), nil
	default:
		panic(method)
	}
}
func TestVerifiedEvidenceIsImmutableAcrossHandoffRetry(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	digest := sha256.Sum256([]byte("evidence"))
	c := &evidenceClient{id: wire.ID(), body: "evidence", hash: hex.EncodeToString(digest[:])}
	b := &Backend{Client: c, StateDir: dir, MaxEvidenceBytes: 1024}
	job := &Request{ID: wire.ID()}
	raw := wire.JSON(map[string]any{"state": "succeeded", "cleanup": "complete"})
	if _, err := b.workerEvidence(context.Background(), job, raw, true); err != nil {
		t.Fatal(err)
	}
	c.body = "modified"
	if _, err := b.workerEvidence(context.Background(), job, raw, true); err != nil {
		t.Fatal("durable evidence should satisfy retry", err)
	}
	if c.reads != 1 {
		t.Fatal("retried evidence was overwritten", c.reads)
	}
	path := filepath.Join(dir, "tasks", job.ID, "artifacts", c.id)
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "evidence" {
		t.Fatal(string(body), err)
	}
	c.id = wire.ID()
	if _, err = b.workerEvidence(context.Background(), job, raw, true); wire.Safe(err).Code != "evidence_integrity" {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "tasks", job.ID, "artifacts", c.id)); !os.IsNotExist(err) {
		t.Fatal("corrupt uncommitted object retained", err)
	}
}
