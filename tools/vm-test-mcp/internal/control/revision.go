package control

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type VersionState struct {
	Revision    int64  `json:"revision"`
	StateDigest string `json:"state_digest"`
}

func (v *VersionState) advance(value any) {
	sum := sha256.Sum256(wire.JSON(value))
	digest := hex.EncodeToString(sum[:])
	if v.StateDigest != digest {
		v.Revision++
		v.StateDigest = digest
	}
}
func (t *Task) PrepareSave() {
	t.advance(taskCondition(t))
	if t.Kind != "lab" {
		return
	}
	if !t.Purged {
		t.ResourceCounts = map[string]int{}
		for _, n := range append(append([]*Node{}, t.lab().Nodes...), t.lab().Router) {
			if n != nil {
				t.ResourceCounts[n.resourceState(t.Terminal())]++
			}
		}
	}
	for _, c := range t.lab().Commands {
		c.advance(commandCondition(c))
	}
}
func (o *Operation) PrepareSave() {
	if o.State == "uncertain" {
		o.IntentStage = "unknown"
	}
	if o.State != "pending" && o.Ended == nil {
		now := time.Now().UTC()
		o.Ended = &now
	}
	o.advance([]any{o.State, o.Error})
}
func (t *Transfer) PrepareSave() { t.advance([]any{t.State, t.TaskID, t.ManifestDigest}) }
