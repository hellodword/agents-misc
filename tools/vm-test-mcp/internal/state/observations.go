package state

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
)

// Keep the full observation at a real transition (including fault evidence),
// but do not journal every healthy sample, lease renewal or polling schedule.
func semantic(body []byte) []byte {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return body
	}
	var scrub func(any)
	scrub = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, key := range []string{"checked", "last_success", "successes", "failures", "polling", "updated", "network_updated", "network_lease_expires"} {
				delete(x, key)
			}
			if observation, ok := x["network_observation"].(map[string]any); ok && observation["error"] == nil {
				delete(x, "network_observation")
			}
			if guest, ok := x["guest"].(map[string]any); ok {
				delete(guest, "time")
				if service, ok := guest["command_service"].(map[string]any); ok {
					delete(service, "heartbeat")
				}
			}
			for _, child := range x {
				scrub(child)
			}
		case []any:
			for _, child := range x {
				scrub(child)
			}
		}
	}
	scrub(v)
	b, _ := json.Marshal(v)
	return b
}
func unchangedObservation(ctx context.Context, tx *sql.Tx, table, id string, body []byte) bool {
	var previous []byte
	return tx.QueryRowContext(ctx, "SELECT body FROM "+table+" WHERE id=?", id).Scan(&previous) == nil && bytes.Equal(semantic(previous), semantic(body))
}
