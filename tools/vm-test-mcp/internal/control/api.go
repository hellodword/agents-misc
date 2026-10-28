package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/telemetry"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Change struct {
	RequestID string `json:"request_id"`
}
type TaskID struct {
	TaskID string `json:"task_id"`
}
type NodeID struct {
	TaskID
	NodeID string `json:"node_id"`
}
type TaskChange struct {
	Change
	TaskID
}
type NodeChange struct {
	Change
	NodeID
}
type ListRequest struct {
	Cursor int `json:"cursor,omitempty"`
	Limit  int `json:"limit,omitempty"`
}
type TaskListRequest struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Phase  string `json:"phase,omitempty"`
}
type EventsRequest struct {
	TaskID
	Cursor int64 `json:"cursor,omitempty"`
	Limit  int   `json:"limit,omitempty"`
}
type LogRequest struct {
	TaskID
	Offset int64 `json:"offset,omitempty"`
	Limit  int   `json:"limit,omitempty"`
}
type OperationRequest struct {
	OperationID string `json:"operation_id"`
}
type ObserveRequest struct {
	Seconds int `json:"seconds,omitempty"`
}
type FinishRequest struct {
	TaskChange
	Conclusion string `json:"conclusion"`
	Summary    string `json:"summary"`
}
type ExtendRequest struct {
	TaskChange
	Seconds int    `json:"seconds"`
	Reason  string `json:"reason"`
}
type NixRequest struct {
	Change
	CacheID        string `json:"cache_id"`
	Derivation     string `json:"derivation"`
	System         string `json:"system"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}
type StartRequest struct {
	Mode string `json:"mode,omitempty"`
	NodeChange
	Argv           []string          `json:"argv"`
	Context        string            `json:"context,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}
type CommandRequest struct {
	TaskID
	CommandID string `json:"command_id"`
}
type CommandGetRequest struct {
	CommandRequest
	InputFrom  *int `json:"input_from,omitempty"`
	InputLimit int  `json:"input_limit,omitempty"`
}
type ReadRequest struct {
	CommandRequest
	Stream string `json:"stream"`
	Offset int64  `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}
type InputRequest struct {
	Change
	CommandRequest
	Sequence int    `json:"sequence"`
	Data     string `json:"data,omitempty"`
	EOF      bool   `json:"eof,omitempty"`
}
type CancelCommandRequest struct {
	Change
	CommandRequest
}
type TransferRequest struct {
	Change
	TaskID string `json:"task_id,omitempty"`
	Kind   string `json:"kind"`
}
type TransferGetRequest struct {
	TransferID string `json:"transfer_id"`
}
type PutRequest struct {
	NodeChange
	TransferID string `json:"transfer_id"`
	Path       string `json:"path"`
}
type GetRequest struct {
	NodeChange
	Path string `json:"path"`
}
type PowerRequest struct {
	NodeChange
	Action string `json:"action"`
}
type NetworkRequest struct {
	TaskChange
	Internet *bool            `json:"internet,omitempty"`
	DHCP     *bool            `json:"dhcp,omitempty"`
	DNS      []string         `json:"dns,omitempty"`
	Nodes    []AddressRequest `json:"nodes,omitempty"`
}
type AddressRequest struct {
	NodeID string `json:"node_id"`
	IPv4   string `json:"ipv4"`
}
type GUIRequest struct {
	TaskChange
	MaxGuestResolution string `json:"max_guest_resolution"`
}
type LinkRequest struct {
	NodeChange
	Enabled bool `json:"enabled"`
}
type EndpointRequest struct {
	NodeChange
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
}
type CloseEndpointRequest struct {
	TaskChange
	EndpointID string `json:"endpoint_id"`
}
type ScreenshotRequest struct {
	NodeChange
	Screen int `json:"screen,omitempty"`
}
type DisplayRequest struct {
	NodeChange
	Screen     int   `json:"screen,omitempty"`
	Width      int   `json:"width,omitempty"`
	Height     int   `json:"height,omitempty"`
	AutoResize *bool `json:"auto_resize,omitempty"`
}
type InputNodeRequest struct {
	NodeChange
	Action    string   `json:"action"`
	Screen    int      `json:"screen,omitempty"`
	X         int      `json:"x,omitempty"`
	Y         int      `json:"y,omitempty"`
	EndX      int      `json:"end_x,omitempty"`
	EndY      int      `json:"end_y,omitempty"`
	Button    int      `json:"button,omitempty"`
	DZ        int      `json:"dz,omitempty"`
	DW        int      `json:"dw,omitempty"`
	Relative  bool     `json:"relative,omitempty"`
	Scancodes []string `json:"scancodes,omitempty"`
}
type MediaRequest struct {
	NodeChange
	Device  string `json:"device"`
	Enabled bool   `json:"enabled"`
	Camera  string `json:"camera,omitempty"`
}
type CaptureRequest struct {
	NodeChange
	Kind string `json:"kind"`
}
type CaptureGetRequest struct {
	TaskID
	CaptureID string `json:"capture_id"`
}
type CaptureStopRequest struct {
	Change
	CaptureGetRequest
}
type ArtifactListRequest struct {
	TaskID
	ListRequest
}
type ArtifactGetRequest struct {
	TaskID
	ArtifactID string `json:"artifact_id"`
}

type DiagnoseRequest struct {
	TaskChange
	NodeIDs []string `json:"node_ids,omitempty"`
}
type ExecRequest struct {
	NodeChange
	Argv           []string          `json:"argv"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}
type Definition struct {
	Name, Description string
	Args              any
	Mutating          bool
}

var Definitions = []Definition{
	{"wait", "Wait up to 600 seconds for any business condition or new failure; transport heartbeats stay outside tool results", WaitRequest{}, false},
	{"nix_job_get", "Read a compact Nix execution and cleanup summary", TaskID{}, false},
	{"nix_job_list", "Page through Nix jobs", TaskListRequest{}, false},
	{"nix_job_cancel", "Cancel a Nix job and await worker resource release", TaskChange{}, true},
	{"nix_job_cleanup", "Retry Nix evidence handoff and resource cleanup", TaskChange{}, true},
	{"lab_inspect", "Read lab nodes, networking, resource ledger and diagnostics", TaskID{}, false},
	{"command_details", "Read command argv and explicitly paged stdin history", CommandGetRequest{}, false},
	{"evidence_pin", "Retain evidence and its metadata until explicitly unpinned", EvidenceChange{}, true},
	{"evidence_unpin", "Release an evidence retention pin", EvidenceChange{}, true},
	{"evidence_delete", "Delete unpinned evidence after confirmed resource cleanup", EvidenceChange{}, true},
	{"upload_renew", "Renew a scoped upload capability", RenewRequest{}, true},
	{"upload_objects", "Read a page of immutable upload object metadata", UploadObjectsRequest{}, false},
	{"service_get", "Describe templates, backend availability and effective limits", struct{}{}, false},
	{"service_observe", "Read bounded host pressure, controller cgroup quotas and call timings from the last 1..240 seconds (default 30); missing data is explicit and no argv, environment or output is included", ObserveRequest{}, false},
	{"lab_create", "Queue independent linked-clone nodes and a private router", LabRequest{}, true},
	{"nix_job_create", "Queue an uploaded derivation closure for sandboxed host KVM execution", NixRequest{}, true},
	{"lab_list", "Page through compact task summaries; phase defaults to active (queued, preparing, running or blocked cleanup). Use phase=ended or all for history; cursor is the last scanned task ID", TaskListRequest{}, false}, {"lab_get", "Read a compact laboratory summary and revision; use lab_inspect for the resource ledger", TaskID{}, false},
	{"node_get", "Read node capabilities, observations and expected state", NodeID{}, false},
	{"lab_diagnose", "Collect bounded host and guest evidence, including the private router boot console before Guest Additions is ready", DiagnoseRequest{}, true},
	{"node_exec", "Run a short guest command through Guest Control independently of the command service", ExecRequest{}, true},
	{"lab_cleanup", "Release remaining resources of an ended VirtualBox lab and verify cleanup", TaskChange{}, true},
	{"execution_events", "Read events after a cursor", EventsRequest{}, false}, {"execution_log", "Read task log bytes at an offset", LogRequest{}, false}, {"operation_get", "Read a durable operation receipt", OperationRequest{}, false},
	{"lab_finish", "Submit a running lab conclusion and release resources; wait for cleanup.state=complete", FinishRequest{}, true}, {"lab_cancel", "Cancel a queued or executing task; wait for cleanup.state=complete", TaskChange{}, true}, {"lab_extend", "Extend an executing task with a concrete continued-work reason; independent startup deadlines do not change", ExtendRequest{}, true}, {"execution_delete", "Remove retained data for a cleaned terminal task", TaskChange{}, true},
	{"command_start", "Start argv; finite mode waits for exit, process-group release and output, service/interactive for confirmed start", StartRequest{}, true}, {"command_get", "Read compact command status and revision", CommandRequest{}, false}, {"command_read", "Read base64 output by byte offset; at_end is the current prefix, eof requires complete retention", ReadRequest{}, false}, {"command_input", "Persist ordered UTF-8 stdin or EOF (64KiB per packet, 64MiB and 4096 packets per command)", InputRequest{}, true}, {"command_cancel", "Request cancellation of one supervised process group; inspect command_get for confirmed termination", CancelCommandRequest{}, true},
	{"upload_create", "Prepare a file upload for a queued/preparing/running lab, or an unbound standard Nix cache before nix_job_create", TransferRequest{}, true}, {"upload_get", "Inspect compact upload state and revision; use upload_objects for checksums and upload_renew for credentials", TransferGetRequest{}, false},
	{"guest_file_put", "Copy a sealed upload to a guest path", PutRequest{}, true}, {"guest_file_get", "Archive a guest file or directory as an artifact", GetRequest{}, true},
	{"node_control", "Start, shut down, reboot, power off, pause, resume or reset a lab node", PowerRequest{}, true},
	{"network_set", "Set public egress, DHCP, DNS and node address registrations within the lab LAN", NetworkRequest{}, true}, {"network_recover", "Revalidate router, host protection and declared endpoints", TaskChange{}, true},
	{"node_network_link", "Connect or disconnect the node's lab network cable", LinkRequest{}, true},
	{"lab_gui_set", "Set the lab's global GUI resolution policy (any or auto); restore on cleanup", GUIRequest{}, true},
	{"endpoint_open", "Publish a node TCP or UDP service to allowed client sources", EndpointRequest{}, true}, {"endpoint_close", "Revoke a published endpoint", CloseEndpointRequest{}, true},
	{"node_screenshot", "Collect a framebuffer screenshot", ScreenshotRequest{}, true}, {"node_display", "Request a guest display size", DisplayRequest{}, true}, {"node_input", "Deliver serialized native input with a durable receipt", InputNodeRequest{}, true}, {"node_media", "Change an administrator-approved media device", MediaRequest{}, true},
	{"capture_start", "Start video, NIC packet or metrics capture", CaptureRequest{}, true}, {"capture_get", "Read capture status", CaptureGetRequest{}, false}, {"capture_stop", "Stop capture and retain evidence", CaptureStopRequest{}, true},
	{"evidence_list", "Page through retained artifacts", ArtifactListRequest{}, false}, {"evidence_get", "Issue a scoped artifact download credential", ArtifactGetRequest{}, false},
}

// Schema is generated from the same closed structs used by strict decoding.
func Schema(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		return Schema(t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.Anonymous {
				v := Schema(f.Type)
				for k, x := range v["properties"].(map[string]any) {
					props[k] = x
				}
				required = append(required, v["required"].([]string)...)
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			if tag[0] == "-" {
				continue
			}
			name := tag[0]
			if name == "" {
				name = f.Name
			}
			props[name] = Schema(f.Type)
			if !strings.Contains(f.Tag.Get("json"), "omitempty") {
				required = append(required, name)
			}
		}
		result := map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
		constrainSchema(t, result)
		return result
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": Schema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": Schema(t.Elem())}
	default:
		panic("unsupported schema type: " + t.String())
	}
}

var requestPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func (s *Server) accept(ctx context.Context, name string, raw []byte) (any, error) {
	var def *Definition
	for i := range Definitions {
		if Definitions[i].Name == name {
			def = &Definitions[i]
			break
		}
	}
	if def == nil {
		return nil, wire.Fail("not_found", "Unknown tool")
	}
	v := reflect.New(reflect.TypeOf(def.Args))
	if e := wire.Decode(raw, v.Interface()); e != nil {
		return nil, e
	}
	if e := validateArguments(reflect.TypeOf(def.Args), raw); e != nil {
		return nil, e
	}
	args := v.Elem().Interface()
	if !def.Mutating {
		return s.query(ctx, name, args)
	}
	field := v.Elem().FieldByName("RequestID")
	request := field.String()
	if !requestPattern.MatchString(request) {
		return nil, wire.Fail("invalid_argument", "request_id must contain 1..128 identifier characters")
	}
	h := sha256.Sum256(append([]byte(name+"\n"), wire.JSON(args)...))
	hash := hex.EncodeToString(h[:])
	accepted := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, wire.Fail("unavailable", "Service is stopping")
	}
	if op := s.requestOperation(request); op != nil {
		if op.Hash != hash {
			return nil, wire.Fail("request_conflict", "request_id was already used with different parameters")
		}
		return clone(op), nil
	}
	if name == "nix_job_create" {
		// A missing worker must not consume an idempotency key or queue slot.
		// Check outside the mutex, then recheck the key after reacquiring it.
		s.mu.Unlock()
		probe, cancel := context.WithTimeout(ctx, 3*time.Second)
		reason, err := s.backend.Available(probe, "nixos")
		cancel()
		s.mu.Lock()
		if op := s.requestOperation(request); op != nil {
			if op.Hash != hash {
				return nil, wire.Fail("request_conflict", "request_id was already used with different parameters")
			}
			return clone(op), nil
		}
		if reason == "nix_disabled" || reason == "worker_unavailable" || (err != nil && reason == "") {
			return nil, wire.Fail("worker_unavailable", "Start the configured Nix worker and retry this request")
		}
		if s.closed {
			return nil, wire.Fail("unavailable", "Service is stopping")
		}
	}
	op := &Operation{ID: wire.ID(), RequestID: request, Tool: name, Hash: hash, State: "pending", Created: time.Now().UTC()}
	var binding struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(raw, &binding)
	op.TaskID = binding.TaskID
	if name == "lab_create" || name == "nix_job_create" {
		op.TaskID = wire.ID()
	}
	if oneOf(name, "lab_create", "nix_job_create", "upload_create") {
		if e := s.admission(); e != nil {
			return nil, e
		}
	}
	if e := s.db.Admit(ctx, state.Document{Kind: "operation", ID: op.ID, Value: op}, wire.JSON(args)); e != nil {
		return nil, e
	}
	s.operations[op.ID] = op
	s.requests[request] = op
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		started := time.Now()
		result, e := s.mutate(name, args)
		s.performance.Record(telemetry.Call{Started: accepted.UTC(), Finished: time.Now().UTC(), Scope: "operation", Operation: name, TaskID: binding.TaskID, WaitUS: started.Sub(accepted).Microseconds(), ExecutionUS: time.Since(started).Microseconds(), Failed: e != nil})
		s.mu.Lock()
		defer s.mu.Unlock()
		op.State = "succeeded"
		op.Result = compactResult(name, result)
		if name == "node_exec" && e == nil {
			if value, ok := result.(map[string]any); ok && value["state"] == "uncertain" {
				op.State = "uncertain"
			}
		}
		if e == nil && (name == "lab_create" || name == "nix_job_create") {
			if task, ok := result.(map[string]any); ok {
				op.TaskID, _ = task["id"].(string)
			}
		}
		if e != nil {
			op.State = "failed"
			op.Error = wire.Safe(e)
			if oneOf(name, "node_input", "node_control", "command_start", "command_cancel") && oneOf(op.Error.Code, "backend_error", "backend_timeout", "backend_cancelled") {
				op.State = "uncertain"
			}
		}
		s.persist(state.Document{Kind: "operation", ID: op.ID, Value: op})
	}()
	return clone(op), nil
}
func clone[T any](v *T) *T { var out T; _ = json.Unmarshal(wire.JSON(v), &out); return &out }
func window(offset int64, limit int) (int, error) {
	if offset < 0 || limit < 0 || limit > 65536 {
		return 0, wire.Fail("invalid_argument", "Invalid offset or limit")
	}
	if limit == 0 {
		limit = 65536
	}
	return limit, nil
}
func page(cursor, limit, total int) (int, int, error) {
	if cursor < 0 || cursor > total || limit < 0 || limit > 200 {
		return 0, 0, wire.Fail("invalid_argument", "Invalid cursor or limit")
	}
	if limit == 0 {
		limit = 50
	}
	end := min(cursor+limit, total)
	return cursor, end, nil
}

func (s *Server) requestOperation(request string) *Operation {
	if o := s.requests[request]; o != nil {
		return o
	}
	var o Operation
	if s.db.OperationForRequest(s.ctx, request, &o) == nil {
		return &o
	}
	return nil
}

type EvidenceChange struct {
	Change
	ArtifactGetRequest
}
type RenewRequest struct {
	Change
	TransferGetRequest
}
type UploadObjectsRequest struct {
	TransferGetRequest
	ListRequest
}
