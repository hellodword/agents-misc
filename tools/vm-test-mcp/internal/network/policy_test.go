package network

import (
	"net/netip"
	"strings"
	"testing"
)

func validPolicy() Policy {
	return Policy{LAN: "10.77.0.0/24", WAN: "10.0.2.0/24", DHCP: true, Internet: true, DNS: []string{"1.1.1.1"}, Protected: []string{"8.8.8.8/32"}, LeaseSeconds: 15, Nodes: []Lease{{Name: "win", MAC: "08:00:27:01:02:03", IPv4: "10.77.0.2"}}, Entries: []Entry{{Protocol: "tcp", Port: 20000, IPv4: "10.77.0.2", GuestPort: 80}}}
}
func TestAddressClassification(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "169.254.169.254", "100.100.100.200", "10.1.2.3", "192.0.0.8", "192.0.0.11", "192.88.99.2", "198.18.0.1", "224.0.0.1", "255.255.255.255"} {
		if Public(netip.MustParseAddr(a)) {
			t.Fatal(a)
		}
	}
	for _, a := range []string{"1.1.1.1", "8.8.8.8", "192.0.0.9", "192.0.0.10", "192.31.196.1"} {
		if !Public(netip.MustParseAddr(a)) {
			t.Fatal(a)
		}
	}
}
func TestPolicyBoundaries(t *testing.T) {
	p := validPolicy()
	nft, e := p.NFT()
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(nft, "timeout 15s") || !strings.Contains(nft, "ip daddr @protected drop") || strings.Contains(nft, "ct state { established, related } accept") {
		t.Fatal("lease or destination protection missing")
	}
	p.Internet = false
	nft, e = p.NFT()
	if e != nil || strings.Contains(nft, "oifname wan jump public") {
		t.Fatal("disabled egress still authorized")
	}
	if !strings.Contains(nft, "ct mark 1") {
		t.Fatal("published ingress lost")
	}
	p.DHCP = false
	dns, e := p.DNSMasq()
	if e != nil || strings.Contains(dns, "dhcp-range") {
		t.Fatal("DHCP not disabled")
	}
	for _, change := range []func(*Policy){func(p *Policy) { p.LAN = "8.8.8.0/24" }, func(p *Policy) { p.LAN = p.WAN }, func(p *Policy) { p.Nodes[0].IPv4 = "10.77.0.255" }, func(p *Policy) { p.Nodes[0].Name = "a\nserver=127.0.0.1" }, func(p *Policy) { p.Nodes = append(p.Nodes, p.Nodes[0]) }, func(p *Policy) { p.Entries[0].IPv4 = "10.1.1.1" }, func(p *Policy) { p.Protected = []string{"0.0.0.0/0; accept"} }} {
		p := validPolicy()
		change(&p)
		if p.Validate() == nil {
			t.Fatal("accepted invalid router policy")
		}
	}
}
