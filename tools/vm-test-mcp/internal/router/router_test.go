package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/network"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestPolicyRequestCannotMasqueradeAsAppliedState(t *testing.T) {
	dir := t.TempDir()
	p := network.Policy{LAN: "10.77.0.0/24", WAN: "10.0.2.0/24", LeaseSeconds: 15, DHCP: true, Internet: true, DNS: []string{"1.1.1.1"}, Nodes: []network.Lease{{Name: "one", MAC: "08:00:27:11:22:33", IPv4: "10.77.0.2"}}}
	if e := os.WriteFile(filepath.Join(dir, "policy.json"), wire.JSON(p), 0600); e != nil {
		t.Fatal(e)
	}
	rules := []string{}
	calls := []string{}
	healthy := true
	run := func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if !healthy && name == "systemctl" {
			return fmt.Errorf("dns down")
		}
		return nil
	}
	nft := func(_ context.Context, s string) error { rules = append(rules, s); return nil }
	if e := apply(context.Background(), p, dir, run, nft); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(rules[0], "chain forward") {
		t.Fatal("initial policy skipped")
	}
	data, e := os.ReadFile(filepath.Join(dir, "dnsmasq.conf"))
	if e != nil || !strings.Contains(string(data), "one.lab.test,10.77.0.2") {
		t.Fatal("DHCP/DNS not configured", e)
	}
	if e = apply(context.Background(), p, dir, run, nft); e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(rules[1], "flush set") {
		t.Fatal("heartbeat replaced full policy")
	}
	healthy = false
	if e = apply(context.Background(), p, dir, run, nft); e == nil || len(rules) != 2 {
		t.Fatal("failed helper renewed lease")
	}
	healthy = true
	p.Internet = false
	if e = apply(context.Background(), p, dir, run, nft); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "conntrack -D --mark 0") {
		t.Fatal("egress conntrack not revoked")
	}
	if strings.Contains(rules[2], "jump public") {
		t.Fatal("disabled egress remains allowed")
	}
}
