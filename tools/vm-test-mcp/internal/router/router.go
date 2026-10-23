package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/network"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Seed struct {
	Password string `json:"password"`
	LANMAC   string `json:"lan_mac"`
	WANMAC   string `json:"wan_mac"`
	LAN      string `json:"lan"`
	WAN      string `json:"wan"`
}

func run(ctx context.Context, exe string, args ...string) error {
	return exec.CommandContext(ctx, exe, args...).Run()
}
func Init(ctx context.Context, seedFile string) error {
	b, e := os.ReadFile(seedFile)
	if e != nil {
		return e
	}
	var s Seed
	if e = wire.Decode(b, &s); e != nil {
		return e
	}
	if len(s.Password) != 64 || strings.Trim(s.Password, "0123456789abcdef") != "" {
		return fmt.Errorf("invalid seed credential")
	}
	p := network.Policy{LAN: s.LAN, WAN: s.WAN, DNS: []string{"1.1.1.1"}, LeaseSeconds: 15}
	if e = p.Validate(); e != nil {
		return e
	}
	for _, v := range []struct{ name, mac string }{{"lan", s.LANMAC}, {"wan", s.WANMAC}} {
		mac, e := net.ParseMAC(v.mac)
		if e != nil || len(mac) != 6 {
			return fmt.Errorf("invalid seed MAC")
		}
		interfaces, e := net.Interfaces()
		if e != nil {
			return e
		}
		found := false
		for _, i := range interfaces {
			if bytes.Equal(i.HardwareAddr, mac) {
				found = true
				if i.Name != v.name {
					if e = run(ctx, "ip", "link", "set", i.Name, "down"); e != nil {
						return e
					}
					if e = run(ctx, "ip", "link", "set", i.Name, "name", v.name); e != nil {
						return e
					}
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("seed NIC absent")
		}
	}
	if e = os.MkdirAll("/run/vm-test", 0700); e != nil {
		return e
	}
	if e = Apply(ctx, p); e != nil {
		return e
	}
	password := exec.CommandContext(ctx, "chpasswd")
	password.Stdin = strings.NewReader("root:" + s.Password + "\n")
	if e = password.Run(); e != nil {
		return e
	}
	return os.WriteFile("/run/vm-test/initialized", []byte("ready\n"), 0600)
}
func Apply(ctx context.Context, p network.Policy) error {
	return apply(ctx, p, "/run/vm-test", run, nft)
}
func apply(ctx context.Context, p network.Policy, dir string, run func(context.Context, string, ...string) error, nft func(context.Context, string) error) error {
	if e := p.Validate(); e != nil {
		return e
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	lock, e := os.OpenFile(filepath.Join(dir, "policy.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); e != nil {
		return e
	}
	oldData, _ := os.ReadFile(filepath.Join(dir, "applied.json"))
	data := wire.JSON(p)
	same := sha256.Sum256(oldData) == sha256.Sum256(data)
	if same {
		if e = run(ctx, "systemctl", "is-active", "--quiet", "vm-test-dnsmasq.service"); e != nil {
			return e
		}
		lease := fmt.Sprintf("flush set inet vm_test alive\nadd element inet vm_test alive { 0.0.0.0/0 timeout %ds }\n", p.LeaseSeconds)
		return nft(ctx, lease)
	}
	rules, e := p.NFT()
	if e != nil {
		return e
	}
	if e = nft(ctx, rules); e != nil {
		return e
	}
	lan := netip.MustParsePrefix(p.LAN)
	wan := netip.MustParsePrefix(p.WAN)
	ip, gateway := network.WANAddresses(p.WAN)
	for _, a := range [][]string{{"link", "set", "lan", "up"}, {"link", "set", "wan", "up"}, {"address", "replace", fmt.Sprintf("%s/%d", network.Gateway(p.LAN), lan.Bits()), "dev", "lan"}, {"address", "replace", fmt.Sprintf("%s/%d", ip, wan.Bits()), "dev", "wan"}, {"route", "replace", "default", "via", gateway, "dev", "wan"}} {
		if e = run(ctx, "ip", a...); e != nil {
			return e
		}
	}
	var old network.Policy
	_ = json.Unmarshal(oldData, &old)
	if old.Internet && !p.Internet {
		_ = run(ctx, "conntrack", "-D", "--mark", "0")
	}
	if !bytes.Equal(wire.JSON(old.Entries), wire.JSON(p.Entries)) {
		_ = run(ctx, "conntrack", "-D", "--mark", "1")
	}
	dns, e := p.DNSMasq()
	if e != nil {
		return e
	}
	existing, _ := os.ReadFile(filepath.Join(dir, "dnsmasq.conf"))
	if !bytes.Equal(existing, []byte(dns)) {
		if e = replace(filepath.Join(dir, "dnsmasq.conf"), []byte(dns)); e != nil {
			return e
		}
		if e = run(ctx, "systemctl", "restart", "vm-test-dnsmasq.service"); e != nil {
			return e
		}
	} else if e = run(ctx, "systemctl", "is-active", "--quiet", "vm-test-dnsmasq.service"); e != nil {
		return e
	}
	return replace(filepath.Join(dir, "applied.json"), data)
}
func replace(file string, data []byte) error {
	tmp := filepath.Join(filepath.Dir(file), "."+wire.ID())
	if e := os.WriteFile(tmp, data, 0600); e != nil {
		return e
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, file)
}
func nft(ctx context.Context, rules string) error {
	c := exec.CommandContext(ctx, "nft", "-f", "-")
	c.Stdin = strings.NewReader(rules)
	return c.Run()
}
func Probe(ctx context.Context) error {
	if _, e := os.Stat("/run/vm-test/initialized"); e != nil {
		return e
	}
	return run(ctx, "systemctl", "is-active", "--quiet", "vm-test-dnsmasq.service")
}
func ReadPolicy(file string) (network.Policy, error) {
	p := network.Policy{}
	b, e := os.ReadFile(file)
	if e != nil {
		return p, e
	}
	e = wire.Decode(b, &p)
	return p, e
}
func Context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}
