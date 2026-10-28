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
	req := &nixos.Request{ID: t.ID, CacheID: t.CacheID, Derivation: t.Derivation, System: t.System, TimeoutSeconds: t.TimeoutSeconds, Deadline: t.Deadline, Manifest: []wire.HandoffFile{}}
	for _, a := range t.Artifacts {
		if a.Source == "nix_worker" {
			req.Manifest = append(req.Manifest, wire.HandoffFile{ID: a.ID, Size: a.Size, SHA256: a.SHA256})
		}
	}
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
			Busy  bool                `json:"busy"`
			Build *wire.BuildIdentity `json:"build"`
		}
		if e = json.Unmarshal(raw, &v); e != nil {
			return "worker_unavailable", e
		}
		identity := backendIdentity{State: "observed", Observed: time.Now().UTC(), Build: v.Build}
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
