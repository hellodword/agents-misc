package control

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/execution"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/nixos"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
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
	UploadIDs      []string      `json:"upload_ids,omitempty"`
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
	UUID          string                  `json:"uuid"`
	MAC           string                  `json:"mac"`
	State         string                  `json:"state"`
	Intent        bool                    `json:"intent"`
	Cleaned       bool                    `json:"cleaned"`
	Link          bool                    `json:"link"`
	AutoResize    bool                    `json:"auto_resize"`
	LastInput     time.Time               `json:"last_input,omitempty"`
	Guest         map[string]any          `json:"guest,omitempty"`
	Health        Health                  `json:"health"`
	Polling       PollSchedule            `json:"polling"`
	GuestSessions *GuestSessionState      `json:"guest_sessions,omitempty"`
	Identity      *NodeIdentity           `json:"identity,omitempty"`
	Preparation   Preparation             `json:"preparation"`
	Expected      *ExpectedState          `json:"expected,omitempty"`
	CleanupSteps  map[string]*CleanupStep `json:"cleanup_steps,omitempty"`
}
type Command struct {
	VersionState
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	wire.CommandStatus
	Observation    Observation      `json:"observation"`
	Cancellation   string           `json:"cancellation,omitempty"`
	Release        *CommandRelease  `json:"release,omitempty"`
	StatusDeadline *time.Time       `json:"status_deadline,omitempty"`
	Retained       RetainedOutput   `json:"retained_output"`
	Schedule       PollSchedule     `json:"polling"`
	IdlePolls      int              `json:"-"`
	Stdin          state.InputQueue `json:"stdin"`
	Created        time.Time        `json:"created"`
}
type CommandRelease struct {
	Source string    `json:"source"`
	Time   time.Time `json:"time"`
}

func (c *Command) Observing() bool {
	return !c.Final && oneOf(c.State, "starting", "running", "uncertain")
}

func (c *Command) Polling() bool { return c.Release == nil && (c.Observing() || !c.Retained.done()) }

type RetainedOutput struct {
	Deadline *time.Time     `json:"deadline,omitempty"`
	Stdout   RetainedStream `json:"stdout"`
	Stderr   RetainedStream `json:"stderr"`
}
type RetainedStream struct {
	State  string      `json:"state"`
	Bytes  int64       `json:"bytes"`
	SHA256 string      `json:"sha256,omitempty"`
	Error  *wire.Error `json:"error,omitempty"`
}

func (s RetainedStream) done() bool { return oneOf(s.State, "complete", "incomplete") }
func (r RetainedOutput) done() bool { return r.Stdout.done() && r.Stderr.done() }
func (r *RetainedOutput) stop(err error) {
	for _, stream := range []*RetainedStream{&r.Stdout, &r.Stderr} {
		if !stream.done() {
			stream.State, stream.Error = "incomplete", wire.Safe(err)
		}
	}
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
	ArtifactID string `json:"evidence_id,omitempty"`
}
type Artifact = execution.Artifact

type executionRecord struct {
	DetailExpires  *time.Time     `json:"detail_expires,omitempty"`
	ResourceCounts map[string]int `json:"resource_counts,omitempty"`
	VersionState
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
	Hold           *Hold         `json:"hold,omitempty"`
}

type Task struct {
	executionRecord
	domain        executionDomain
	Artifacts     []*Artifact `json:"artifacts"`
	repository    *repository
	pendingEvents []any
}

// Hold explains an explicit extension; it is not an idle-detection lease.
type Hold struct {
	Reason  string    `json:"reason"`
	Updated time.Time `json:"updated"`
	Until   time.Time `json:"until"`
}

// Execution adapters need resource identity and status, not artifact history.
// Command payloads live in immutable files and are loaded by explicit detail reads.
func (t *Task) executionSnapshot() *Task {
	v := *t
	v.Artifacts = nil
	v.pendingEvents = nil
	v.domain = t.domain.snapshot()
	return &v
}

func (n *Node) resourceState(ended bool) string {
	if n.Cleaned {
		return "released"
	}
	if !n.Intent {
		if ended {
			return "never_allocated"
		}
		return "planned"
	}
	for _, step := range n.CleanupSteps {
		if step.State == "failed" {
			return "release_failed"
		}
	}
	return "allocated"
}

func (c *Command) publicStatus() any {
	return struct {
		ID             string           `json:"id"`
		NodeID         string           `json:"node_id"`
		Created        time.Time        `json:"created"`
		Observation    Observation      `json:"observation"`
		Cancellation   string           `json:"cancellation,omitempty"`
		Release        *CommandRelease  `json:"release,omitempty"`
		StatusDeadline *time.Time       `json:"status_deadline,omitempty"`
		Retained       RetainedOutput   `json:"retained_output"`
		Schedule       PollSchedule     `json:"polling"`
		Stdin          state.InputQueue `json:"stdin"`
		wire.CommandStatus
	}{c.ID, c.NodeID, c.Created, c.Observation, c.Cancellation, c.Release, c.StatusDeadline, c.Retained, c.Schedule, c.Stdin, c.CommandStatus}
}

func (t *Task) activity() map[string]int {
	v := map[string]int{"commands_active": 0, "endpoints_open": 0, "captures_active": 0}
	if t.Kind != "lab" {
		return v
	}
	for _, c := range t.lab().Commands {
		if c.Polling() {
			v["commands_active"]++
		}
	}
	for _, e := range t.lab().Endpoints {
		if e.State == "open" {
			v["endpoints_open"]++
		}
	}
	for _, c := range t.lab().Captures {
		if !oneOf(c.State, "stopped", "released") {
			v["captures_active"]++
		}
	}
	return v
}

func (t *Task) summaryView() map[string]any {
	resources := t.ResourceCounts
	if resources == nil {
		resources = map[string]int{}
	}
	if t.Kind == "lab" && len(t.lab().Nodes) > 0 || t.Kind == "lab" && t.lab().Router != nil {
		resources = map[string]int{}
		for _, n := range append(append([]*Node{}, t.lab().Nodes...), t.lab().Router) {
			if n != nil {
				resources[n.resourceState(t.Terminal())]++
			}
		}
	}
	v := map[string]any{
		"id": t.ID, "kind": t.Kind, "phase": t.phase(), "created": t.Created,
		"started": t.Started, "deadline": t.Deadline, "ended": t.Ended,
		"cleanup":  map[string]any{"state": t.cleanupState(), "resources": resources},
		"activity": t.activity(), "purged": t.Purged, "hold": t.Hold,
		"result": nil, "revision": t.Revision,
	}
	if t.Kind == "nixos" {
		delete(v, "cleanup")
		var execution *wire.NixExecution
		if observed := t.nix().NixExecution; observed != nil {
			current := *observed
			current.Resources, current.Evidence = t.nixResources(), t.nix().Evidence
			execution = &current
		}
		v["execution"] = execution
		v["resources"] = t.nixResources()
		v["evidence"] = t.nix().Evidence
	}
	if t.Terminal() {
		v["result"] = map[string]any{"termination": t.State, "conclusion": t.Conclusion, "reason": t.Reason}
	}
	return v
}

func (t *Task) Node(id string) (*Node, error) {
	if t.Kind != "lab" {
		return nil, wire.Fail("not_found", "Node requires a lab")
	}
	for _, n := range t.lab().Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, wire.Fail("not_found", "Node does not belong to this task")
}
func (t *Task) Command(id string) (*Command, error) {
	if t.Kind != "lab" {
		return nil, wire.Fail("not_found", "Command requires a lab")
	}
	for _, c := range t.lab().Commands {
		if c.ID == id {
			return c, nil
		}
	}
	if t.repository != nil {
		return t.repository.command(context.Background(), t.ID, id)
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
	// Build the public projection before serialization: history may contain large
	// stdin packets and secrets in argv/env that status discovery does not need.
	v := t.summaryView()
	v["timeout_seconds"] = t.TimeoutSeconds
	if t.Kind != "lab" {
		v["result"] = t.result()
		snapshot := map[string]any{}
		decodeMap(v, snapshot)
		return snapshot
	}
	v["lan"], v["dhcp"], v["dns"] = t.lab().LAN, t.lab().DHCP, t.lab().DNS
	v["internet"] = t.lab().Internet
	v["network"] = map[string]any{"state": t.lab().Network, "policy_updated": t.lab().NetworkUpdated, "lease_expires": t.lab().NetworkLeaseExpires, "router_identity": routerIdentity(t), "observation": t.lab().NetworkObservation}
	v["result"] = t.result()
	if t.State == "queued" {
		v["waiting_reason"] = t.Reason
	}
	resources := []any{}
	for _, n := range append(append([]*Node{}, t.lab().Nodes...), t.lab().Router) {
		if n != nil {
			resources = append(resources, map[string]any{"id": n.ID, "name": n.Name, "state": n.resourceState(t.Terminal()), "steps": n.CleanupSteps})
		}
	}
	v["cleanup"] = map[string]any{"state": t.cleanupState(), "errors": t.CleanupErrors, "resources": resources}
	nodes := []any{}
	for _, n := range t.lab().Nodes {
		nodes = append(nodes, n.public())
	}
	v["nodes"] = nodes
	if t.lab().Router != nil {
		// Router progress is observable, but it is not a public command target.
		health := t.lab().Router.Health
		health.normalize()
		v["router"] = map[string]any{"id": t.lab().Router.ID, "state": t.lab().Router.State, "preparation": t.lab().Router.Preparation, "health": health, "polling": t.lab().Router.Polling}
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

type BodyRef struct {
	File   string `json:"file"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Operation struct {
	IntentStage string  `json:"intent_stage"`
	Intent      BodyRef `json:"intent"`
	Payload     BodyRef `json:"payload"`
	TargetID    string  `json:"target_id,omitempty"`
	VersionState
	Ended          *time.Time  `json:"ended,omitempty"`
	WorkerInstance string      `json:"worker_instance,omitempty"`
	ID             string      `json:"id"`
	RequestID      string      `json:"request_id"`
	Tool           string      `json:"tool"`
	Hash           string      `json:"hash"`
	State          string      `json:"state"`
	Created        time.Time   `json:"created"`
	TaskID         string      `json:"execution_id,omitempty"`
	Result         any         `json:"result,omitempty"`
	Error          *wire.Error `json:"error,omitempty"`
}
type Transfer struct {
	VersionState
	ManifestDigest string `json:"manifest_digest,omitempty"`
	ReservedBytes  int64  `json:"reserved_bytes"`
	Derivation     string `json:"derivation,omitempty"`
	System         string `json:"system,omitempty"`

	Updated time.Time         `json:"updated"`
	ID      string            `json:"id"`
	TaskID  string            `json:"execution_id,omitempty"`
	Kind    string            `json:"kind"`
	State   string            `json:"state"`
	Created time.Time         `json:"created"`
	Objects map[string]Object `json:"-"`
	Bytes   int64             `json:"-"`
}
type Object = state.TransferObject
type Runtime struct {
	nixObserving, nixCollecting bool
	nixNextObservation          time.Time
	ctx                         context.Context
	cancel                      context.CancelFunc
	gate                        sync.RWMutex
	nodes                       sync.Map
	relays                      map[string]*Relay
	cleanup                     sync.Mutex
	diagnostics                 sync.Mutex
}
type NodeAction struct {
	RequestID      string            `json:"-"`
	Action         string            `json:"action"`
	Path           string            `json:"path,omitempty"`
	Local          string            `json:"-"`
	Spec           *wire.CommandSpec `json:"spec,omitempty"`
	CommandID      string            `json:"command_id,omitempty"`
	StdoutOffset   int64             `json:"stdout_offset,omitempty"`
	StderrOffset   int64             `json:"stderr_offset,omitempty"`
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

// Domain state is exclusive: an execution contains either a lab or a Nix job.
type NixJobState nixos.JobState

type executionDomain interface {
	snapshot() executionDomain
	condition() any
}

func (t *Task) lab() *LabState    { return t.domain.(*LabState) }
func (t *Task) nix() *NixJobState { return t.domain.(*NixJobState) }

type storedExecution struct {
	executionRecord
	Lab       *LabState    `json:"lab,omitempty"`
	Job       *NixJobState `json:"job,omitempty"`
	Artifacts []*Artifact  `json:"artifacts,omitempty"`
}

func (t *Task) MarshalJSON() ([]byte, error) {
	v := storedExecution{executionRecord: t.executionRecord, Artifacts: t.Artifacts}
	switch domain := t.domain.(type) {
	case *LabState:
		v.Lab = domain
	case *NixJobState:
		v.Job = domain
	}
	return json.Marshal(v)
}
func (t *Task) UnmarshalJSON(data []byte) error {
	var v storedExecution
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	t.executionRecord, t.Artifacts = v.executionRecord, v.Artifacts
	if v.Kind == "lab" {
		if v.Lab == nil {
			v.Lab = &LabState{}
		}
		t.domain = v.Lab
	} else {
		if v.Job == nil {
			v.Job = &NixJobState{}
		}
		t.domain = v.Job
	}
	return nil
}
func (l *LabState) snapshot() executionDomain    { return clone(l) }
func (j *NixJobState) snapshot() executionDomain { return clone(j) }
func (l *LabState) condition() any {
	nodes := []any{}
	for _, n := range append(append([]*Node{}, l.Nodes...), l.Router) {
		if n != nil {
			nodes = append(nodes, []any{n.ID, n.State, n.Preparation.Stage, n.Health.VirtualBox.State, n.Health.GuestControl.State, n.Health.Commands.State, n.Cleaned, n.CleanupSteps})
		}
	}
	return []any{l.Network, nodes}
}
func (j *NixJobState) condition() any { return []any{j.NixExecution, j.Evidence, j.HandoffComplete} }

func (t *Task) nixResources() wire.NixResources {
	if t.nix().NixExecution != nil {
		return t.nix().NixExecution.Resources
	}
	if !t.nix().Dispatched {
		return wire.NixResources{State: "unallocated"}
	}
	return wire.NixResources{State: "owned"}
}
func (t *Task) settled() bool {
	return t.Terminal() && t.Cleanup == "complete" && (t.Kind == "lab" || t.nix().Evidence.State == "complete")
}
