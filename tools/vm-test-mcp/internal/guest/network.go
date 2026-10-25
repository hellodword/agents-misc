package guest

import (
	"net"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func observeNetwork() wire.GuestNetwork {
	v := wire.GuestNetwork{State: "observed", Checked: time.Now().UTC(), Interfaces: []wire.GuestInterface{}}
	interfaces, err := net.Interfaces()
	if err != nil {
		v.State, v.Error = "unavailable", "Could not enumerate guest interfaces"
		return v
	}
	if len(interfaces) > 64 {
		interfaces, v.Truncated = interfaces[:64], true
	}
	for _, i := range interfaces {
		entry := wire.GuestInterface{Name: i.Name, MAC: i.HardwareAddr.String(), Up: i.Flags&net.FlagUp != 0, Addresses: []string{}}
		addresses, err := i.Addrs()
		if err != nil {
			entry.Error = "Could not enumerate interface addresses"
			v.State = "partial"
		}
		if len(addresses) > 64 {
			addresses, v.Truncated = addresses[:64], true
		}
		for _, addr := range addresses {
			entry.Addresses = append(entry.Addresses, addr.String())
		}
		v.Interfaces = append(v.Interfaces, entry)
	}
	return v
}
