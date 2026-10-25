package control

import (
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestCloneIdentityBindsResolvedSnapshotAndEffectiveHardware(t *testing.T) {
	spec := config.Template{Kind: "windows", VM: "source-alias", Snapshot: wire.UUID(), Username: "synthetic-private-user", Password: "synthetic-private-password", CPUs: 2, MemoryMiB: 4096}
	info := map[string]string{"firmware": "EFI", "graphicscontroller": "vboxsvga", "accelerate3d": "off", "cpus": "2", "memory": "4096", "CfgFile": "/private/machine/path", "GUI/Title": "private title"}
	source := wire.UUID()
	one := cloneIdentity(spec, source, info)
	if one.State != "observed_clone" || one.Observed.IsZero() || one.Hardware["firmware"] != "EFI" || len(one.SourceSHA256) != 64 || len(one.HardwareSHA256) != 64 {
		t.Fatal(one)
	}
	two := cloneIdentity(spec, wire.UUID(), info)
	if one.SourceSHA256 == two.SourceSHA256 || one.ConfiguredSHA256 != two.ConfiguredSHA256 {
		t.Fatal("alias reassignment lost resolved source identity")
	}
	info["accelerate3d"] = "on"
	three := cloneIdentity(spec, source, info)
	if one.SourceSHA256 != three.SourceSHA256 || one.HardwareSHA256 == three.HardwareSHA256 {
		t.Fatal("hardware changes lost identity")
	}
	spec.Snapshot = wire.UUID()
	four := cloneIdentity(spec, source, info)
	if four.SourceSHA256 == three.SourceSHA256 || four.ConfiguredSHA256 == three.ConfiguredSHA256 {
		t.Fatal("snapshot changes lost identity")
	}
	text := string(wire.JSON(one))
	for _, private := range []string{source, spec.Password, spec.Username, "/private/", "private title"} {
		if strings.Contains(text, private) {
			t.Fatal("identity disclosed private metadata")
		}
	}
}
