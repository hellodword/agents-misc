// Package nixos owns Nix job requests, worker adaptation and verified evidence handoff.
package nixos

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/execution"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Client interface {
	Call(context.Context, string, any) (json.RawMessage, error)
	OpenArtifact(context.Context, string, string, string) (io.ReadCloser, error)
}
type Request struct {
	ManifestDigest                              string
	ID, InstanceID, CacheID, Derivation, System string
	TimeoutSeconds                              int
	Deadline                                    *time.Time
}
type Backend struct {
	PublishEvidence  func(context.Context, *execution.Artifact) error
	Client           Client
	CacheURL         func(string) string
	StateDir         string
	MaxEvidenceBytes int64
}

func (b *Backend) Call(ctx context.Context, action string, t *Request) (json.RawMessage, error) {
	if b.Client == nil {
		return nil, wire.Fail("worker_unavailable", "Configure and start the Nix worker to use this operation")
	}
	req := map[string]any{"task_id": t.ID, "instance_id": t.InstanceID}
	if action == "handoff" {
		req["manifest_digest"] = t.ManifestDigest
	}
	if action == "extend" {
		req["deadline"] = t.Deadline
	}
	if action == "start" {
		req["upload_id"] = t.CacheID
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
