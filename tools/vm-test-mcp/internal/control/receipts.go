package control

import "github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"

// Receipts retain identities and results, not copies of live inventories or
// output bodies. Direct Guest Control output is retained in its evidence file.
func compactResult(name string, result any) any {
	if result == nil {
		return nil
	}
	var v map[string]any
	if value, ok := result.(map[string]any); ok {
		v = *clone(&value)
	} else {
		return result
	}
	if _, ok := v["phase"]; ok {
		for _, key := range []string{"commands", "nodes", "router", "artifacts", "captures", "endpoints", "network", "cache_id", "derivation", "system", "execution"} {
			delete(v, key)
		}
		if cleanup, ok := v["cleanup"].(map[string]any); ok {
			delete(cleanup, "resources")
		}
	}
	if name == "node_exec" {
		delete(v, "stdout")
		delete(v, "stderr")
	}
	if name == "network_set" || name == "network_recover" {
		if task, ok := v["task"].(map[string]any); ok {
			v["task"] = compactResult("lab_get", task)
		}
	}
	if len(wire.JSON(v)) > wire.MaxJSON {
		return map[string]any{"state": "retained", "details": "Use the evidence or command detail reader"}
	}
	return v
}
