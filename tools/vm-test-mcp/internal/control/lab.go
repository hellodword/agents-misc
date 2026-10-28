package control

import "time"

// LabState belongs to the VirtualBox domain. Nodes, networking, command and
// resource cleanup records never cross the Nix worker boundary.
type LabState struct {
	Nodes               []*Node         `json:"nodes"`
	Router              *Node           `json:"router,omitempty"`
	RouterPassword      string          `json:"router_password"`
	RouterISO           string          `json:"router_iso"`
	RouterBootID        string          `json:"router_boot_id"`
	NetworkUpdated      time.Time       `json:"network_updated"`
	NetworkLeaseExpires time.Time       `json:"network_lease_expires"`
	NetworkPolicyHash   string          `json:"network_policy_hash"`
	NetworkObservation  *NetworkAttempt `json:"network_observation,omitempty"`
	GUI                 *GUIOwnership   `json:"gui_ownership,omitempty"`
	LAN                 string          `json:"lan,omitempty"`
	DHCP                bool            `json:"dhcp"`
	DNS                 []string        `json:"dns"`
	Internet            bool            `json:"internet"`
	Network             string          `json:"network"`
	Commands            []*Command      `json:"commands"`
	Endpoints           []*Endpoint     `json:"endpoints"`
	Captures            []*Capture      `json:"captures"`
}
