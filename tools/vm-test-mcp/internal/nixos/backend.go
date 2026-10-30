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
	method := action
	if action == "logs" {
		method = "status"
	}
	raw, err := b.Client.Call(ctx, method, req)
	if err != nil {
		return nil, err
	}
	if action == "logs" {
		return b.workerEvidence(ctx, t, raw, false)
	}
	if action == "resources" || action == "resources_retry" || action == "evidence" {
		for {
			var status struct {
				Execution     *wire.NixExecution `json:"execution"`
				NeverAccepted bool               `json:"never_accepted"`
				Cleanup       string             `json:"cleanup"`
			}
			if err = json.Unmarshal(raw, &status); err != nil {
				return nil, err
			}
			if status.NeverAccepted {
				return raw, nil
			}
			if status.Execution == nil {
				return nil, wire.Fail("worker_protocol_error", "Worker omitted lifecycle state")
			}
			if action != "evidence" && (status.Execution.Resources.Released() && status.Cleanup == "complete" || status.Execution.Resources.State == "blocked") {
				return raw, nil
			}
			if action == "evidence" {
				if status.Execution.Evidence.State == "blocked" {
					return nil, status.Execution.Evidence.Error
				}
				if status.Execution.Evidence.State == "transferring" || status.Execution.Evidence.State == "complete" {
					return b.workerEvidence(ctx, t, raw, true)
				}
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
	}
	return raw, nil
}
