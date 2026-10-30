package control

import (
	"encoding/json"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// Durable receipts refer to resources. Live views and expiring capabilities are
// produced when reading the receipt, never saved with an idempotency key.
type resourceReceipt struct {
	ID     string `json:"id,omitempty"`
	State  string `json:"state,omitempty"`
	NodeID string `json:"node_id,omitempty"`
}
type evidenceReceipt struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}
type endpointReceipt struct {
	resourceReceipt
	Protocol  string `json:"protocol"`
	GuestPort int    `json:"guest_port"`
	HostPort  int    `json:"host_port"`
}

func compactResult(name string, result any) any {
	if result == nil {
		return nil
	}
	var out any
	switch name {
	case "command_start", "command_cancel", "upload_create", "lab_finish", "lab_cancel", "nix_job_cancel", "lab_cleanup", "nix_job_recover", "lab_extend", "nix_job_extend":
		out = &resourceReceipt{}
	case "endpoint_open", "endpoint_close":
		out = &endpointReceipt{}
	case "capture_start":
		out = &Capture{}
	case "capture_stop":
		out = &struct {
			ID string `json:"capture_id"`
		}{}
	case "node_screenshot", "guest_file_get":
		out = &evidenceReceipt{}
	case "lab_diagnose":
		out = &struct {
			ID string `json:"evidence_id"`
		}{}
	case "node_exec":
		out = &struct {
			ID                   string `json:"evidence_id"`
			State                string `json:"state"`
			TerminationConfirmed bool   `json:"termination_confirmed"`
			ExitCode             *int   `json:"exit_code,omitempty"`
		}{}
	case "command_input":
		out = &struct {
			Accepted  bool   `json:"accepted"`
			Persisted bool   `json:"persisted"`
			Sequence  int    `json:"sequence"`
			Delivery  string `json:"delivery"`
		}{}
	case "network_set", "network_recover":
		out = &struct {
			Refresh bool `json:"guest_network_refresh_required"`
		}{}
	case "lab_gui_set":
		out = &struct {
			Resolution string `json:"max_guest_resolution"`
		}{}
	case "node_control":
		out = &struct {
			NodeID string `json:"node_id"`
			State  string `json:"state"`
			Ready  bool   `json:"ready"`
		}{}
	case "evidence_pin", "evidence_unpin", "evidence_delete":
		out = &struct {
			ID      string `json:"id"`
			Pinned  bool   `json:"pinned"`
			Deleted bool   `json:"deleted"`
		}{}
	case "upload_delete":
		out = &struct {
			ID      string `json:"upload_id"`
			Deleted bool   `json:"deleted"`
		}{}
	case "execution_delete":
		out = &struct {
			ID      string `json:"id"`
			Deleted bool   `json:"deleted"`
		}{}
	case "guest_file_put", "node_network_link", "node_display", "node_input", "node_media":
		out = &struct {
			Delivered bool `json:"delivered"`
		}{}
	default:
		return nil
	}
	if json.Unmarshal(wire.JSON(result), out) != nil {
		return nil
	}
	return out
}
func (s *Server) operationView(o *Operation) any {
	result := o.Result
	if o.Tool == "upload_create" && o.State == "succeeded" {
		var ref struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(wire.JSON(result), &ref)
		if t, err := s.transfer(ref.ID); err == nil {
			result = s.transferView(t)
		} else {
			result = map[string]any{"id": ref.ID, "unavailable": wire.Safe(err)}
		}
	}
	return map[string]any{"id": o.ID, "request_id": o.RequestID, "tool": o.Tool, "state": o.State, "created": o.Created, "ended": o.Ended, "execution_id": o.TaskID, "target_id": o.TargetID, "result": result, "error": o.Error, "revision": o.Revision}
}
