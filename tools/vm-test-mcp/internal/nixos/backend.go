// Package nixos owns Nix job requests, worker adaptation and verified evidence handoff.
package nixos

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Client interface {
	Call(context.Context, string, any) (json.RawMessage, error)
}
type Request struct {
	ID, CacheID, Derivation, System string
	TimeoutSeconds                  int
	Deadline                        *time.Time
	Manifest                        []wire.HandoffFile
}
type Backend struct {
	Client           Client
	CacheURL         func(string) string
	StateDir         string
	MaxEvidenceBytes int64
}

func (b *Backend) Call(ctx context.Context, action string, t *Request) (json.RawMessage, error) {
	if b.Client == nil {
		return nil, wire.Fail("worker_unavailable", "Configure and start the Nix worker to use this operation")
	}
	req := map[string]any{"task_id": t.ID}
	if action == "handoff" {
		req["manifest_digest"] = wire.ManifestDigest(t.Manifest)
	}
	if action == "extend" {
		req["deadline"] = t.Deadline
	}
	if action == "start" {
		req["derivation"] = t.Derivation
		req["system"] = t.System
		req["timeout_seconds"] = t.TimeoutSeconds
		req["cache"] = b.CacheURL(t.CacheID)
	}
	raw, err := b.Client.Call(ctx, action, req)
	if err != nil {
		return nil, err
	}
	if action == "status" {
		return b.workerEvidence(ctx, t, raw, false)
	}
	if action == "cleanup" {
		for {
			var status struct {
				Cleanup string `json:"cleanup"`
			}
			if err = json.Unmarshal(raw, &status); err != nil {
				return nil, err
			}
			if status.Cleanup != "pending" {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			raw, err = b.Client.Call(ctx, "status", req)
			if err != nil {
				return nil, err
			}
		}
		collected, e := b.workerEvidence(ctx, t, raw, true)
		if e == nil {
			return collected, nil
		}
		return nil, e
	}
	return raw, nil
}
