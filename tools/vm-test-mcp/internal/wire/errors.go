package wire

// Retry describes a prerequisite, never permission to replay a side effect.
// A failed receipt keeps its identity; corrected/new actions use a new key only
// after the previous receipt and live execution have been reconciled.
func classifyError(original *Error) *Error {
	e := *original
	if e.Category != "" {
		return &e
	}
	e.Category, e.Retry, e.NextAction = "unknown", "inspect", "Inspect the operation receipt, live task state and bounded diagnostics before another mutation"
	switch e.Code {
	case "invalid_argument":
		e.Category, e.Retry, e.NextAction = "validation", "after_change", "Inspect tools/list constraints; correct parameters and use a new request_id for changed arguments"
	case "unauthorized", "forbidden", "host_denied", "origin_denied", "policy_denied":
		e.Category, e.Retry, e.NextAction = "authorization", "after_change", "Use authorized credentials and permitted resources; inspect the effective service policy"
	case "invalid_state", "guest_not_ready", "network_not_ready", "guest_protocol_mismatch", "guest_service_unavailable", "guest_service_mismatch", "guest_execution_unavailable", "desktop_unavailable", "unsupported_context", "not_found", "purged", "expired":
		e.Category, e.Retry, e.NextAction = "precondition", "after_change", "Inspect live task/node/transfer state and meet the documented operation precondition"
	case "resource_busy", "unavailable", "router_unavailable", "node_unavailable":
		e.Category, e.Retry, e.NextAction = "capacity", "after_recovery", "Inspect queue, polling and cleanup state; wait for confirmed capacity or recovery before a new action"
	case "limit_exceeded", "endpoint_limit", "input_limit", "guest_session_limit":
		e.Category, e.Retry, e.NextAction = "capacity", "after_change", "Inspect the reported limit and reduce the request or complete and release this batch"
	case "request_conflict", "input_conflict", "input_order", "input_closed", "cleanup_conflict", "gui_conflict", "conflict":
		e.Category, e.Retry, e.NextAction = "conflict", "after_change", "Reconcile retained identities and live ownership; do not overwrite prior inputs or external state"
	case "initialization_failed", "firstboot_failed":
		e.Category, e.Retry, e.NextAction = "execution", "after_change", "Inspect the recorded preparation step and diagnostics; correct guest or template prerequisites before a fresh execution"
	case "backend_timeout", "backend_cancelled", "backend_error", "worker_unavailable", "worker_protocol_error", "worker_restarted", "supervision_failed", "terminal_write", "missing_terminal_status", "supervisor_lost", "termination_unconfirmed":
		e.Category, e.Retry, e.NextAction = "execution", "reconcile", "Reconcile the existing request_id and live execution; an observation error does not confirm termination"
	}
	return &e
}
