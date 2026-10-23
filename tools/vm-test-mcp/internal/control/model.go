package control

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type NodeRequest struct {
	Name      string       `json:"name"`
	Template  string       `json:"template"`
	IPv4      string       `json:"ipv4,omitempty"`
	CPUs      int          `json:"cpus,omitempty"`
	MemoryMiB int          `json:"memory_mib,omitempty"`
	GUI       bool         `json:"gui,omitempty"`
	Media     config.Media `json:"media,omitempty"`
	Camera    string       `json:"camera,omitempty"`
}
type LabRequest struct {
	RequestID      string        `json:"request_id"`
	Nodes          []NodeRequest `json:"nodes"`
	LAN            string        `json:"lan,omitempty"`
	DHCP           *bool         `json:"dhcp,omitempty"`
	DNS            []string      `json:"dns,omitempty"`
	Internet       *bool         `json:"internet,omitempty"`
	TimeoutSeconds int           `json:"timeout_seconds,omitempty"`
}
type Node struct {
	ID string `json:"id"`
	NodeRequest
	UUID         string                  `json:"uuid"`
	MAC          string                  `json:"mac"`
	State        string                  `json:"state"`
	Intent       bool                    `json:"intent"`
	Cleaned      bool                    `json:"cleaned"`
	Link         bool                    `json:"link"`
	AutoResize   bool                    `json:"auto_resize"`
	LastInput    time.Time               `json:"last_input,omitempty"`
	Guest        map[string]any          `json:"guest,omitempty"`
	Health       Health                  `json:"health"`
	Preparation  Preparation             `json:"preparation"`
	Expected     *ExpectedState          `json:"expected,omitempty"`
	CleanupSteps map[string]*CleanupStep `json:"cleanup_steps,omitempty"`
}
type Command struct {
	ID     string           `json:"id"`
	NodeID string           `json:"node_id"`
	Spec   wire.CommandSpec `json:"spec"`
	wire.CommandStatus
	Observation  Observation  `json:"observation"`
	Cancellation string       `json:"cancellation,omitempty"`
	Input        []wire.Input `json:"input"`
	Created      time.Time    `json:"created"`
}
type Endpoint struct {
	ID           string `json:"id"`
	NodeID       string `json:"node_id"`
	Protocol     string `json:"protocol"`
	GuestPort    int    `json:"guest_port"`
	HostPort     int    `json:"host_port"`
	RouterPort   int    `json:"router_port"`
	LoopbackPort int    `json:"loopback_port"`
	State        string `json:"state"`
}
type Capture struct {
	ID         string `json:"id"`
	NodeID     string `json:"node_id"`
	Kind       string `json:"kind"`
	State      string `json:"state"`
	ArtifactID string `json:"artifact_id,omitempty"`
}
type Artifact struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
	Created time.Time `json:"created"`
	Path    string    `json:"path"`
}
type Task struct {
	ID             string        `json:"id"`
	Kind           string        `json:"kind"`
	State          string        `json:"state"`
	Created        time.Time     `json:"created"`
	Started        *time.Time    `json:"started,omitempty"`
	Deadline       *time.Time    `json:"deadline,omitempty"`
	Ended          *time.Time    `json:"ended,omitempty"`
	TimeoutSeconds int           `json:"timeout_seconds"`
	Conclusion     string        `json:"conclusion,omitempty"`
	Summary        string        `json:"summary,omitempty"`
	Reason         string        `json:"reason,omitempty"`
	Cleanup        string        `json:"cleanup"`
	CleanupErrors  []*wire.Error `json:"cleanup_errors,omitempty"`
	Failure        *wire.Error   `json:"failure,omitempty"`
	Purged         bool          `json:"purged"`
	Nodes          []*Node       `json:"nodes"`
	Router         *Node         `json:"router,omitempty"`
	RouterPassword string        `json:"router_password"`
	RouterISO      string        `json:"router_iso"`
	RouterBootID   string        `json:"router_boot_id"`
	NetworkUpdated time.Time     `json:"network_updated"`
	GUI            *GUIOwnership `json:"gui_ownership,omitempty"`
	LAN            string        `json:"lan,omitempty"`
	DHCP           bool          `json:"dhcp"`
	DNS            []string      `json:"dns"`
	Internet       bool          `json:"internet"`
	Network        string        `json:"network"`
	Commands       []*Command    `json:"commands"`
	Endpoints      []*Endpoint   `json:"endpoints"`
	Captures       []*Capture    `json:"captures"`
	Artifacts      []*Artifact   `json:"artifacts"`
	CacheID        string        `json:"cache_id,omitempty"`
	Derivation     string        `json:"derivation,omitempty"`
	System         string        `json:"system,omitempty"`
}

func (t *Task) Node(id string) (*Node, error) {
	for _, n := range t.Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, wire.Fail("not_found", "Node does not belong to this task")
}
func (t *Task) Command(id string) (*Command, error) {
	for _, c := range t.Commands {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, wire.Fail("not_found", "Command does not belong to this task")
}
func decodeMap(value any, target map[string]any) { _ = json.Unmarshal(wire.JSON(value), &target) }
func (t *Task) phase() string {
	if t.Terminal() {
		return "ended"
	}
	return t.State
}
func (t *Task) result() any {
	if !t.Terminal() {
		return nil
	}
	return map[string]any{"conclusion": t.Conclusion, "summary": t.Summary, "reason": t.Reason, "termination": t.State, "error": t.Failure}
}
func (t *Task) cleanupState() string {
	if t.Cleanup == "required" {
		return "blocked"
	}
	return t.Cleanup
}
func (t *Task) Public() any {
	v := map[string]any{}
	decodeMap(t, v)
	for _, key := range []string{"router", "router_password", "router_iso", "router_boot_id", "gui_ownership", "state", "conclusion", "summary", "reason", "cleanup_errors", "failure"} {
		delete(v, key)
	}
	v["phase"] = t.phase()
	v["result"] = t.result()
	if t.State == "queued" {
		v["waiting_reason"] = t.Reason
	}
	resources := []any{}
	for _, n := range append(append([]*Node{}, t.Nodes...), t.Router) {
		if n != nil {
			resources = append(resources, map[string]any{"id": n.ID, "name": n.Name, "released": n.Cleaned, "steps": n.CleanupSteps})
		}
	}
	v["cleanup"] = map[string]any{"state": t.cleanupState(), "errors": t.CleanupErrors, "resources": resources}
	nodes := []any{}
	for _, n := range t.Nodes {
		nodes = append(nodes, n.public())
	}
	v["nodes"] = nodes
	if list, ok := v["artifacts"].([]any); ok {
		for _, item := range list {
			delete(item.(map[string]any), "path")
		}
	}
	// HTTP encodes after the caller releases s.mu; resource journals must not
	// retain references to maps that cleanup is still updating.
	snapshot := map[string]any{}
	decodeMap(v, snapshot)
	return snapshot
}
func (t *Task) Terminal() bool {
	return t.State == "finished" || t.State == "cancelled" || t.State == "timed_out" || t.State == "failed" || t.State == "interrupted"
}

type Operation struct {
	ID        string      `json:"id"`
	RequestID string      `json:"request_id"`
	Tool      string      `json:"tool"`
	Hash      string      `json:"hash"`
	State     string      `json:"state"`
	Created   time.Time   `json:"created"`
	TaskID    string      `json:"task_id,omitempty"`
	Result    any         `json:"result,omitempty"`
	Error     *wire.Error `json:"error,omitempty"`
}
type Transfer struct {
	ID      string            `json:"id"`
	TaskID  string            `json:"task_id,omitempty"`
	Kind    string            `json:"kind"`
	State   string            `json:"state"`
	Created time.Time         `json:"created"`
	Objects map[string]Object `json:"objects"`
}
type Object struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Runtime struct {
	ctx         context.Context
	cancel      context.CancelFunc
	gate        sync.RWMutex
	nodes       sync.Map
	relays      map[string]*Relay
	cleanup     sync.Mutex
	diagnostics sync.Mutex
}
type NodeAction struct {
	Action         string            `json:"action"`
	Path           string            `json:"path,omitempty"`
	Local          string            `json:"-"`
	Spec           *wire.CommandSpec `json:"spec,omitempty"`
	CommandID      string            `json:"command_id,omitempty"`
	Input          *wire.Input       `json:"input,omitempty"`
	Screen         int               `json:"screen,omitempty"`
	Width          int               `json:"width,omitempty"`
	Height         int               `json:"height,omitempty"`
	AutoResize     *bool             `json:"auto_resize,omitempty"`
	X              int               `json:"x,omitempty"`
	Y              int               `json:"y,omitempty"`
	EndX           int               `json:"end_x,omitempty"`
	EndY           int               `json:"end_y,omitempty"`
	Button         int               `json:"button,omitempty"`
	DZ             int               `json:"dz,omitempty"`
	DW             int               `json:"dw,omitempty"`
	Relative       bool              `json:"relative,omitempty"`
	Scancodes      []string          `json:"scancodes,omitempty"`
	Device         string            `json:"device,omitempty"`
	Enabled        bool              `json:"enabled,omitempty"`
	Capture        *Capture          `json:"capture,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	Argv           []string          `json:"argv,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

// Backend receives only already-resolved, controller-owned identities. Tests
// inject an adapter; production constructs VBoxBackend explicitly.
type Backend interface {
	GUI(context.Context, *string) (string, error)
	Available(context.Context, string) (string, error)
	Prepare(context.Context, *Task, *Node) error
	Delete(context.Context, *Task, *Node) error
	Action(context.Context, *Task, *Node, NodeAction) (json.RawMessage, error)
	Network(context.Context, *Task, []string, bool) (string, error)
	Endpoint(context.Context, *Task, *Endpoint, bool) error
	Nix(context.Context, string, *Task) (json.RawMessage, error)
}
