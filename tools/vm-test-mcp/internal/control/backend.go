package control

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/nixos"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Backends struct {
	*VBoxBackend
	NixBackend *nixos.Backend
}

func (b *Backends) Nix(ctx context.Context, action string, t *Task) (json.RawMessage, error) {
	req := &nixos.Request{ManifestDigest: t.nix().ManifestDigest, ID: t.ID, InstanceID: t.nix().WorkerInstance, CacheID: t.nix().CacheID, Derivation: t.nix().Derivation, System: t.nix().System, TimeoutSeconds: t.TimeoutSeconds, Deadline: t.Deadline}

	return b.NixBackend.Call(ctx, action, req)
}
func (b *Backends) Available(ctx context.Context, kind string) (string, error) {
	if kind == "nixos" {
		b.backendIdentities.Store(kind, backendIdentity{State: "unobserved", Observed: time.Now().UTC()})
		if b.NixBackend == nil || b.NixBackend.Client == nil {
			return "nix_disabled", nil
		}
		raw, e := b.NixBackend.Client.Call(ctx, "describe", struct{}{})
		if e != nil {
			return "worker_unavailable", e
		}
		var v struct {
			InstanceID string              `json:"instance_id"`
			Busy       bool                `json:"busy"`
			Build      *wire.BuildIdentity `json:"build"`
		}
		if e = json.Unmarshal(raw, &v); e != nil {
			return "worker_unavailable", e
		}
		identity := backendIdentity{State: "observed", Observed: time.Now().UTC(), Build: v.Build, InstanceID: v.InstanceID}
		if v.Build == nil {
			identity.State = "build_unavailable"
		}
		b.backendIdentities.Store(kind, identity)
		if v.Busy {
			return "worker_busy", nil
		}
		if b.Config.VirtualBoxEnabled() {
			raw, e = b.run(ctx, "list", "runningvms")
			if e != nil {
				return "virtualbox_unavailable", e
			}
			if strings.TrimSpace(string(raw)) != "" {
				return "virtualbox_running", nil
			}
		}
		return "", nil
	}
	return b.VBoxBackend.Available(ctx, kind)
}

func (b *Backends) WorkerInstance() string {
	if v, ok := b.backendIdentities.Load("nixos"); ok {
		return v.(backendIdentity).InstanceID
	}
	return ""
}

// Storage measurement is called outside the controller mutex. An unreachable
// configured worker leaves admission blocked instead of reporting zero usage.
func (b *Backends) EvidenceUsage(ctx context.Context) (int64, error) {
	if b.NixBackend == nil || b.NixBackend.Client == nil {
		return 0, nil
	}
	raw, err := b.NixBackend.Client.Call(ctx, "describe", struct{}{})
	if err != nil {
		return 0, err
	}
	var v struct {
		EvidenceBytes *int64 `json:"evidence_bytes"`
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		return 0, err
	}
	if v.EvidenceBytes == nil {
		return 0, wire.Fail("worker_protocol_error", "Worker did not report retained evidence usage")
	}
	return *v.EvidenceBytes, nil
}
