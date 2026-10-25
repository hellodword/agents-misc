package guest

import (
	"net/netip"
	"testing"
)

func TestProbeReportsActualInterfaceAddresses(t *testing.T) {
	v := observeNetwork()
	if v.State != "observed" || v.Checked.IsZero() || len(v.Interfaces) == 0 {
		t.Fatal(v)
	}
	loopback := false
	for _, i := range v.Interfaces {
		for _, raw := range i.Addresses {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				t.Fatal("invalid observed prefix", raw, err)
			}
			loopback = loopback || prefix.Addr().IsLoopback()
		}
	}
	if !loopback {
		t.Fatal("guest network observation missed the live loopback interface", v)
	}
}
