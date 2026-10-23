// Package network is shared by the controller and the router policy helper.
package network

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// IANA IPv4 Special-Purpose Registry, retrieved 2026-10-22. The two globally
// reachable PCP/TURN anycast addresses in 192.0.0/24 are deliberately excluded.
// Deprecated 6to4 space is not globally reachable; multicast is not unicast.
var NonPublic = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/29", "192.0.0.8/32", "192.0.0.11/32", "192.0.0.12/30", "192.0.0.16/28", "192.0.0.32/27", "192.0.0.64/26", "192.0.0.128/25", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4"}

type Lease struct {
	Name string `json:"name"`
	MAC  string `json:"mac"`
	IPv4 string `json:"ipv4"`
}
type Entry struct {
	Protocol  string `json:"protocol"`
	Port      int    `json:"port"`
	IPv4      string `json:"ipv4"`
	GuestPort int    `json:"guest_port"`
}
type Policy struct {
	LAN          string   `json:"lan"`
	WAN          string   `json:"wan"`
	DHCP         bool     `json:"dhcp"`
	Internet     bool     `json:"internet"`
	DNS          []string `json:"dns"`
	Protected    []string `json:"protected"`
	LeaseSeconds int      `json:"lease_seconds"`
	Nodes        []Lease  `json:"nodes"`
	Entries      []Entry  `json:"entries"`
}

func Gateway(lan string) string { p, _ := netip.ParsePrefix(lan); return p.Addr().Next().String() }
func WANAddresses(wan string) (string, string) {
	p, _ := netip.ParsePrefix(wan)
	a := p.Addr()
	gateway := a.Next().Next()
	for range 15 {
		a = a.Next()
	}
	return a.String(), gateway.String()
}
func HostProtection(extra []string) ([]string, error) {
	a, e := net.InterfaceAddrs()
	if e != nil {
		return nil, e
	}
	out := append([]string{}, extra...)
	for _, s := range a {
		p, e := netip.ParsePrefix(s.String())
		if e != nil {
			return nil, e
		}
		if p.Addr().Is4() {
			out = append(out, p.Addr().String()+"/32")
		}
	}
	sort.Strings(out)
	return out, nil
}
func Public(a netip.Addr) bool {
	if !a.Is4() {
		return false
	}
	for _, s := range NonPublic {
		if netip.MustParsePrefix(s).Contains(a) {
			return false
		}
	}
	return true
}
func (p Policy) Validate() error {
	bad := func() error { return wire.Fail("invalid_argument", "Invalid router policy") }
	lan, e := netip.ParsePrefix(p.LAN)
	if e != nil || !config.PrivateLAN(lan) {
		return bad()
	}
	wan, e := netip.ParsePrefix(p.WAN)
	if e != nil || !wan.Addr().Is4() || wan != wan.Masked() || wan.Bits() > 28 || lan.Overlaps(wan) {
		return bad()
	}
	if p.LeaseSeconds < 5 || p.LeaseSeconds > 60 || len(p.Nodes) > 64 || len(p.Entries) > 128 || len(p.Protected) > 2048 || len(p.DNS) < 1 || len(p.DNS) > 8 {
		return bad()
	}
	for _, s := range p.Protected {
		q, e := netip.ParsePrefix(s)
		if e != nil || !q.Addr().Is4() || q != q.Masked() {
			return bad()
		}
	}
	for _, s := range p.DNS {
		a, e := netip.ParseAddr(s)
		if e != nil || !Public(a) {
			return bad()
		}
	}
	seen := map[string]bool{Gateway(p.LAN): true, lan.Addr().String(): true}
	names := map[string]bool{}
	macs := map[string]bool{}
	for _, n := range p.Nodes {
		a, e := netip.ParseAddr(n.IPv4)
		mac, me := net.ParseMAC(n.MAC)
		if e != nil || !lan.Contains(a) || !lan.Contains(a.Next()) || seen[n.IPv4] || !wire.NamePattern.MatchString(n.Name) || names[n.Name] || me != nil || len(mac) != 6 || macs[n.MAC] {
			return bad()
		}
		seen[n.IPv4] = true
		names[n.Name] = true
		macs[n.MAC] = true
	}
	ports := map[string]bool{}
	for _, v := range p.Entries {
		k := fmt.Sprintf("%s:%d", v.Protocol, v.Port)
		if (v.Protocol != "tcp" && v.Protocol != "udp") || v.Port < 1024 || v.Port > 65535 || v.GuestPort < 1 || v.GuestPort > 65535 || !seen[v.IPv4] || v.IPv4 == Gateway(p.LAN) || ports[k] {
			return bad()
		}
		ports[k] = true
	}
	return nil
}
func (p Policy) DNSMasq() (string, error) {
	if e := p.Validate(); e != nil {
		return "", e
	}
	var b strings.Builder
	fmt.Fprintf(&b, "interface=lan\nbind-interfaces\nno-resolv\ndomain=lab.test\nlocal=/lab.test/\ndomain-needed\nbogus-priv\nno-hosts\nleasefile-ro\n")
	for _, d := range p.DNS {
		fmt.Fprintf(&b, "server=%s\n", d)
	}
	if p.DHCP {
		fmt.Fprintf(&b, "dhcp-range=%s,static\ndhcp-option=option:router,%s\ndhcp-option=option:dns-server,%s\n", Gateway(p.LAN), Gateway(p.LAN), Gateway(p.LAN))
	}
	for _, n := range p.Nodes {
		fmt.Fprintf(&b, "host-record=%s.lab.test,%s\n", n.Name, n.IPv4)
		if p.DHCP {
			fmt.Fprintf(&b, "dhcp-host=%s,%s,%s,infinite\n", n.MAC, n.IPv4, n.Name)
		}
	}
	return b.String(), nil
}

// NFT replaces this service's complete table in one nft transaction. Every
// permitted WAN packet, including established replies and router-origin traffic,
// must pass the expiring lease. Admin protection precedes IANA classification.
func (p Policy) NFT() (string, error) {
	if e := p.Validate(); e != nil {
		return "", e
	}
	var b strings.Builder
	protected := append([]string{}, p.Protected...)
	protected = append(protected, p.WAN)
	fmt.Fprintln(&b, "destroy table inet vm_test\ntable inet vm_test {")
	fmt.Fprintf(&b, "set protected { type ipv4_addr; flags interval; auto-merge; elements = { %s }; }\n", strings.Join(protected, ", "))
	fmt.Fprintf(&b, "set nonpublic { type ipv4_addr; flags interval; auto-merge; elements = { %s }; }\n", strings.Join(NonPublic, ", "))
	fmt.Fprintf(&b, "set alive { type ipv4_addr; flags interval,timeout; timeout %ds; elements = { 0.0.0.0/0 timeout %ds }; }\n", p.LeaseSeconds, p.LeaseSeconds)
	fmt.Fprintln(&b, "chain public { ip daddr @protected drop; ip daddr @nonpublic drop; meta l4proto { tcp, udp } ip daddr @alive accept; ct state related ip daddr @alive accept; drop; }")
	fmt.Fprintln(&b, "chain input { type filter hook input priority 0; policy drop; iifname lo accept; iifname lan udp dport { 53, 67 } accept; iifname lan tcp dport 53 accept;")
	if p.Internet {
		fmt.Fprintln(&b, "iifname wan ip saddr @protected drop; iifname wan ip saddr @nonpublic drop; iifname wan ct state { established, related } ip saddr @alive accept;")
	}
	fmt.Fprintln(&b, "}")
	fmt.Fprintln(&b, "chain output { type filter hook output priority 0; policy drop; oifname lo accept; oifname lan accept;")
	if p.Internet {
		fmt.Fprintln(&b, "oifname wan jump public;")
	}
	fmt.Fprintln(&b, "}")
	fmt.Fprintln(&b, "chain forward { type filter hook forward priority 0; policy drop;")
	fmt.Fprintf(&b, "iifname wan oifname lan ct mark 1 ip saddr @alive accept; iifname lan oifname wan ct mark 1 ip saddr %s ip daddr @alive accept;\n", p.LAN)
	if p.Internet {
		fmt.Fprintf(&b, "iifname lan oifname wan ip saddr %s jump public;\niifname wan oifname lan ip saddr @protected drop; iifname wan oifname lan ip saddr @nonpublic drop; iifname wan oifname lan ct state { established, related } ip saddr @alive accept;\n", p.LAN)
	}
	fmt.Fprintln(&b, "}")
	fmt.Fprintln(&b, "chain prerouting { type nat hook prerouting priority dstnat; policy accept;")
	for _, v := range p.Entries {
		fmt.Fprintf(&b, "iifname wan %s dport %d ct mark set 1 dnat ip to %s:%d;\n", v.Protocol, v.Port, v.IPv4, v.GuestPort)
	}
	fmt.Fprintln(&b, "}")
	fmt.Fprintf(&b, "chain postrouting { type nat hook postrouting priority srcnat; policy accept; oifname wan masquerade; oifname lan ct mark 1 snat ip to %s; }\n}\n", Gateway(p.LAN))
	return b.String(), nil
}
