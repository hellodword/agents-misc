package wire

import "time"

// GuestNetwork is an observation of interfaces, not proof of DHCP origin,
// routing, DNS, reachability or an application's ability to bind an address.
type GuestNetwork struct {
	State      string           `json:"state"`
	Checked    time.Time        `json:"checked"`
	Interfaces []GuestInterface `json:"interfaces"`
	Error      string           `json:"error,omitempty"`
	Truncated  bool             `json:"truncated,omitempty"`
}

type GuestInterface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses"`
	Error     string   `json:"error,omitempty"`
}
