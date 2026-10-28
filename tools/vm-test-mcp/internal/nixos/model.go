package nixos

import "github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"

// JobState is controller-owned Nix execution and handoff state, independent of
// the worker's process ledger and the shared scheduler envelope.
type JobState struct {
	CacheID         string             `json:"cache_id,omitempty"`
	Derivation      string             `json:"derivation,omitempty"`
	System          string             `json:"system,omitempty"`
	NixExecution    *wire.NixExecution `json:"nix_execution,omitempty"`
	HandoffComplete bool               `json:"handoff_complete"`
}
