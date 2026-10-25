package control

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type NodeIdentity struct {
	State            string            `json:"state"`
	Observed         time.Time         `json:"observed"`
	ConfiguredSHA256 string            `json:"configured_sha256"`
	SourceSHA256     string            `json:"source_sha256"`
	Hardware         map[string]string `json:"hardware"`
	HardwareSHA256   string            `json:"hardware_sha256"`
}

func identityHash(v any) string { h := sha256.Sum256(wire.JSON(v)); return hex.EncodeToString(h[:]) }
func configuredTemplateIdentity(t config.Template) string {
	return identityHash(struct {
		VM, Snapshot, Kind, WorkDir string
		CPUs, Memory                int
		Media                       config.Media
	}{t.VM, t.Snapshot, t.Kind, t.WorkDir, t.CPUs, t.MemoryMiB, t.Media})
}
func cloneIdentity(template config.Template, source string, info map[string]string) *NodeIdentity {
	hardware := map[string]string{}
	for _, key := range []string{"ostype", "firmware", "chipset", "cpus", "memory", "graphicscontroller", "vram", "accelerate3d", "ioapic", "longmode", "paravirtprovider"} {
		if v, ok := info[key]; ok {
			hardware[key] = v
		}
	}
	return &NodeIdentity{State: "observed_clone", Observed: time.Now().UTC(), ConfiguredSHA256: configuredTemplateIdentity(template), SourceSHA256: identityHash([]string{source, template.Snapshot}), Hardware: hardware, HardwareSHA256: identityHash(hardware)}
}

type backendIdentity struct {
	State          string              `json:"state"`
	Observed       time.Time           `json:"observed"`
	Build          *wire.BuildIdentity `json:"build,omitempty"`
	RuntimeVersion string              `json:"runtime_version,omitempty"`
}

func (b *VBoxBackend) BackendIdentity(kind string) any {
	if v, ok := b.backendIdentities.Load(kind); ok {
		return v
	}
	return map[string]any{"state": "unobserved"}
}
