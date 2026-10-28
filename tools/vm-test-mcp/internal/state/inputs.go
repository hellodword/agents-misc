package state

import "github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"

// InputQueue describes controller acceptance. Guest WrittenInput independently
// acknowledges writes to the child stdin pipe; delivery never removes the retained input history.
type InputQueue struct {
	NextSequence int   `json:"next_sequence"`
	Delivered    int   `json:"delivered_sequence"`
	Written      int   `json:"written_sequence"`
	Bytes        int64 `json:"bytes"`
	Closed       bool  `json:"closed"`
}

func (q *InputQueue) Accept(p wire.Input) {
	q.NextSequence++
	q.Bytes += int64(len(p.Data))
	q.Closed = p.EOF
}
