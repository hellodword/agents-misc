package wire

import "time"

type CollectionStatus struct {
	State       string `json:"state"`
	Pending     int    `json:"pending"`
	Active      int    `json:"active"`
	Complete    int    `json:"complete"`
	Partial     int    `json:"partial"`
	Unavailable int    `json:"unavailable"`
}

type ExecutionResult struct {
	Termination string `json:"termination"`
	Conclusion  string `json:"conclusion"`
	Summary     string `json:"summary"`
	Reason      string `json:"reason"`
	Error       *Error `json:"error"`
}

type ExecutionHold struct {
	Reason  string    `json:"reason"`
	Updated time.Time `json:"updated"`
	Until   time.Time `json:"until"`
}

type ExecutionQueue struct {
	Position            int    `json:"position"`
	BlockingExecutionID string `json:"blocking_execution_id"`
	Reason              string `json:"reason"`
}

type CleanupSummary struct {
	State     string         `json:"state"`
	Resources map[string]int `json:"resources"`
}

// ExecutionView is the shared projection used by reads, waits and result files.
// Backend-specific detail reads extend it without changing the result receipt.
type ExecutionView struct {
	ID                   string           `json:"id"`
	Kind                 string           `json:"kind"`
	Phase                string           `json:"phase"`
	Created              time.Time        `json:"created"`
	Started              *time.Time       `json:"started"`
	Deadline             *time.Time       `json:"deadline"`
	Ended                *time.Time       `json:"ended"`
	TimeoutSeconds       int              `json:"timeout_seconds"`
	Revision             int64            `json:"revision"`
	Result               *ExecutionResult `json:"result"`
	Collection           CollectionStatus `json:"collection"`
	Activity             map[string]int   `json:"activity"`
	Purged               bool             `json:"purged"`
	Hold                 *ExecutionHold   `json:"hold"`
	Cleanup              *CleanupSummary  `json:"cleanup,omitempty"`
	Execution            *NixExecution    `json:"execution,omitempty"`
	Resources            *NixResources    `json:"resources,omitempty"`
	Evidence             *NixEvidence     `json:"evidence,omitempty"`
	Queue                *ExecutionQueue  `json:"queue,omitempty"`
	WaitingReason        string           `json:"waiting_reason,omitempty"`
	EvidencePendingUntil *time.Time       `json:"evidence_pending_until,omitempty"`
}
