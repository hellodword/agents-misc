package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/network"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/telemetry"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (s *Server) query(ctx context.Context, name string, args any) (result any, err error) {
	started := time.Now()
	s.mu.Lock()
	acquired := time.Now()
	defer func() {
		s.performance.Record(telemetry.Call{Started: started.UTC(), Finished: time.Now().UTC(), Scope: "query", Operation: name, WaitUS: acquired.Sub(started).Microseconds(), ExecutionUS: time.Since(acquired).Microseconds(), Failed: err != nil})
	}()
	defer s.mu.Unlock()
	switch a := args.(type) {
	case LabCollectionRequest:
		return s.collection(name, CollectionRequest(a))
	case CollectionRequest:
		return s.collection(name, a)
	case CommandInputReadRequest:
		return s.inputHistory(a)
	case UploadObjectsRequest:
		return s.uploadObjects(a)
	case ObserveRequest:
		if a.Seconds == 0 {
			a.Seconds = 30
		}
		if a.Seconds < 1 || a.Seconds > 240 {
			return nil, wire.Fail("invalid_argument", "seconds must be 1..240")
		}
		now := time.Now().UTC()
		return s.performance.Window(now.Add(-time.Duration(a.Seconds)*time.Second), now), nil
	case struct{}:
		templates := map[string]any{}
		for name, template := range s.cfg.VirtualBox.Templates {
			var v map[string]any
			_ = json.Unmarshal(wire.JSON(template), &v)
			cameras := []string{}
			for id := range template.Cameras {
				cameras = append(cameras, id)
			}
			sort.Strings(cameras)
			v["cameras"] = cameras
			v["identity"] = map[string]any{"state": "configured", "configured_sha256": configuredTemplateIdentity(template), "observed": "node_get.identity after cloning; guest.build after probe"}
			v["optional_capabilities"] = map[string]any{"os_sleep": "unsupported", "os_hibernation": "unsupported", "hypervisor_pause_resume": "supported", "new_user_secret_service": "unverified", "scope": "No template acceptance is inferred from backend availability or command readiness"}
			templates[name] = v
		}
		backends := map[string]any{}
		s.mu.Unlock()
		for _, kind := range []string{"lab", "nixos"} {
			c, cancel := context.WithTimeout(ctx, 3*time.Second)
			reason, e := s.backend.Available(c, kind)
			cancel()
			view := map[string]any{"available": e == nil && reason == "", "waiting_reason": reason, "scope": "Current admission prerequisites, not execution or optional capability acceptance"}
			if provider, ok := s.backend.(interface{ BackendIdentity(string) any }); ok {
				view["identity"] = provider.BackendIdentity(kind)
			}
			if e != nil {
				view["error"] = wire.Safe(e)
			}
			backends[kind] = view
		}
		s.mu.Lock()
		return map[string]any{"version": wire.Version, "build": wire.Identity(), "performance": s.performance.Latest(), "templates": templates, "backends": backends, "active_execution_id": s.active, "limits": map[string]any{"minimum_codex_version": "0.163.0", "wait_seconds": 600, "tool_timeout_sec": 660, "heartbeat_seconds": 15, "storage": s.cfg.Storage, "storage_blocked": s.storageBlocked, "default_seconds": s.cfg.Tasks.DefaultTimeout.Seconds(), "max_seconds": s.cfg.Tasks.MaxDuration.Seconds(), "max_upload_bytes": s.cfg.Storage.MaxUploadBytes, "max_nodes": 64, "startup_timeout": s.cfg.VirtualBox.StartupTimeout.Value().String(), "startup_parallelism": s.cfg.VirtualBox.StartupParallelism, "shutdown_timeout": s.cfg.VirtualBox.ShutdownTimeout.Value().String(), "cleanup_timeout": s.cfg.Tasks.CleanupTimeout.Value().String(), "probe_interval": probeInterval.String(), "probe_timeout": probeTimeout.String(), "unavailable_after": s.cfg.VirtualBox.UnavailableAfter.Value().String(), "failure_threshold": 3, "recovery_successes": 2, "diagnostic_timeout": diagnosticBudget.String(), "node_exec_default_seconds": 10, "node_exec_max_seconds": 30, "max_active_endpoints_per_lab": 128, "command_protocol": wire.CommandProtocol, "stdin_packet_bytes": wire.InputPacketLimit, "stdin_packets": wire.InputPacketCount, "stdin_bytes": wire.CommandInputLimit, "output_bytes_per_stream": wire.CommandOutputLimit, "guest_read_bytes_per_stream": wire.CommandReadLimit}, "endpoint_address": s.cfg.VirtualBox.Network.Listen}, nil
	case TaskListRequest:
		return s.listTasksKind(a, name)
	case TaskID:
		t, e := s.task(a.TaskID)
		if e != nil {
			return nil, e
		}
		if name == "nix_job_get" && t.Kind != "nixos" || name == "lab_get" && t.Kind != "lab" {
			return nil, wire.Fail("not_found", "Execution belongs to another domain")
		}
		v := t.Public().(map[string]any)
		if name != "lab_inspect" {
			v = t.summaryView()
			v["revision"] = s.revision(map[bool]string{true: "lab", false: "nix_job"}[t.Kind == "lab"]+"/"+t.ID, taskCondition(t))
			if t.Kind == "nixos" {
				v["execution"] = t.nix().NixExecution
			}
		}
		s.queueView(t, v)
		if until := s.performancePending[t.ID]; !until.IsZero() {
			v["evidence_pending_until"] = until
		}
		return *clone(&v), nil
	case NodeID:
		t, err := s.task(a.TaskID.TaskID)
		if err != nil {
			return nil, err
		}
		n, err := t.Node(a.NodeID)
		if err != nil {
			return nil, err
		}
		return n.public(), nil
	case OperationRequest:
		o, err := s.operation(a.OperationID)
		if err != nil {
			return nil, err
		}
		return s.operationView(o), nil
	case EventsRequest:
		if _, e := s.task(a.TaskID.TaskID); e != nil {
			return nil, e
		}
		if a.Cursor < 0 || a.Limit < 0 || a.Limit > 200 {
			return nil, wire.Fail("invalid_argument", "Invalid cursor or limit")
		}
		if a.Limit == 0 {
			a.Limit = 50
		}
		items, next, e := s.db.Events(ctx, a.TaskID.TaskID, a.Cursor, a.Limit)
		return map[string]any{"items": items, "next_cursor": next}, e
	case LogRequest:
		if _, e := s.task(a.TaskID.TaskID); e != nil {
			return nil, e
		}
		return s.readLog(path.Join("tasks", a.TaskID.TaskID, "task.log"), a.Offset, a.Limit)
	case CommandRequest:
		task, err := s.task(a.TaskID.TaskID)
		if err != nil {
			return nil, err
		}
		c, err := task.Command(a.CommandID)
		if err != nil {
			return nil, err
		}
		var v map[string]any
		_ = json.Unmarshal(wire.JSON(c.publicStatus()), &v)
		v["revision"] = s.revision("command/"+c.ID, commandCondition(c))
		return v, nil
	case CommandGetRequest:
		t, e := s.task(a.TaskID.TaskID)
		if e != nil {
			return nil, e
		}
		c, e := t.Command(a.CommandID)
		if e != nil {
			return nil, e
		}
		if name == "command_get" {
			return c.publicStatus(), nil
		}
		spec, err := s.db.CommandSpec(ctx, t.ID, c.ID)
		if err != nil {
			return nil, err
		}
		detail := map[string]any{"command_id": c.ID, "lab_id": t.ID, "spec": spec}
		return detail, nil
	case ReadRequest:
		t, e := s.task(a.TaskID.TaskID)
		if e != nil {
			return nil, e
		}
		c, e := t.Command(a.CommandID)
		if e != nil {
			return nil, e
		}
		if a.Stream != "stdout" && a.Stream != "stderr" {
			return nil, wire.Fail("invalid_argument", "stream must be stdout or stderr")
		}
		v, e := s.readLog(path.Join("tasks", t.ID, "commands", a.CommandID, a.Stream), a.Offset, a.Limit)
		if e != nil {
			return nil, e
		}
		retained := c.Retained.Stdout
		if a.Stream == "stderr" {
			retained = c.Retained.Stderr
		}
		result := v.(map[string]any)
		result["encoding"], result["offset"] = "base64", a.Offset
		result["at_end"] = result["eof"]
		result["eof"] = retained.State == "complete" && result["next_offset"].(int64) >= retained.Bytes
		result["retained"], result["execution_final"] = retained, c.Final
		return result, nil
	case TransferGetRequest:
		t, err := s.transfer(a.TransferID)
		if err != nil {
			return nil, wire.Fail("not_found", "Transfer not found")
		}
		v := s.transferView(t)
		v["revision"] = s.revision("upload/"+t.ID, []any{t.State, t.Bytes, s.uploading[t.ID] != nil})
		return v, nil
	case CaptureGetRequest:
		var c Capture
		if err := s.db.child(s.ctx, "captures", a.CaptureID, &c); err != nil {
			return nil, err
		}
		return &c, nil
	case ArtifactListRequest:
		return s.evidenceList(a)
	case ArtifactGetRequest:
		artifact, err := s.db.evidence(ctx, a.ArtifactID)
		if err != nil {
			return nil, err
		}
		v := artifactView(artifact)
		v["url"], v["expires"] = s.capURL("artifact", artifact.ID, "read")
		return v, nil
	}
	return nil, wire.Fail("invalid_argument", "Unsupported query")
}
func artifactView(a *Artifact) map[string]any {
	return map[string]any{"id": a.ID, "name": a.Name, "size": a.Size, "sha256": a.SHA256, "created": a.Created, "pinned": a.Pinned, "execution_id": a.ExecutionID, "execution_kind": a.ExecutionKind}
}

func (s *Server) mutate(name string, args any, op *Operation) (any, error) {
	if strings.HasPrefix(name, "lab_") && name != "lab_create" || strings.HasPrefix(name, "nix_job_") && name != "nix_job_create" {
		var target TaskID
		_ = json.Unmarshal(wire.JSON(args), &target)
		s.mu.Lock()
		t, err := s.task(target.TaskID)
		if err == nil && (strings.HasPrefix(name, "lab_") != (t.Kind == "lab")) {
			err = wire.Fail("not_found", "Execution belongs to another domain")
		}
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}

	switch a := args.(type) {
	case UploadDeleteRequest:
		return s.deleteUpload(a.TransferID)
	case EvidenceChange:
		return s.changeEvidence(name, a)
	case DiagnoseRequest:
		return s.diagnose(s.ctx, a.TaskID.TaskID, a.NodeIDs, false)
	case ExecRequest:
		return s.execNode(a)
	case TransferRequest:
		return s.createTransfer(a, op.TargetID)
	case FinishRequest:
		if a.Conclusion != "passed" && a.Conclusion != "failed" && a.Conclusion != "inconclusive" {
			return nil, wire.Fail("invalid_argument", "Invalid conclusion")
		}
		if strings.TrimSpace(a.Summary) == "" || len(a.Summary) > 8192 {
			return nil, wire.Fail("invalid_argument", "Conclusion summary is required")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		t, r, e := s.running(a.TaskID.TaskID)
		if e != nil {
			return nil, e
		}
		t.State = "finished"
		t.Conclusion = a.Conclusion
		t.Summary = a.Summary
		r.cancel()
		s.save(t)
		return t.Public(), nil
	case TaskChange:
		if name == "lab_cleanup" || name == "nix_job_cleanup" {
			return s.cleanupTask(a.TaskID.TaskID)
		}
		if name == "network_recover" {
			return s.changeNetwork(a.TaskID.TaskID, NetworkRequest{}, true)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		t, e := s.task(a.TaskID.TaskID)
		if e != nil {
			return nil, e
		}
		if name == "execution_delete" {
			delete(s.performancePending, t.ID)
			if err := s.purge(t); err != nil {
				return nil, err
			}
			if err := s.db.DeleteExecution(s.ctx, t.ID); err != nil {
				return nil, err
			}
			s.forget(t.ID)
			return map[string]any{"id": t.ID, "deleted": true}, nil
		}
		if t.Terminal() {
			return t.Public(), nil
		}
		queued := t.State == "queued"
		t.State = "cancelled"
		t.Conclusion = "inconclusive"
		if queued {
			now := time.Now().UTC()
			t.Ended = &now
			if uploads := s.retainTransfers(t.ID); len(uploads) == 0 {
				t.Cleanup = "complete"
				s.completeTransfers(t.ID)
			} else {
				s.wg.Add(1)
				go func() { defer s.wg.Done(); s.cleanup(t) }()
			}
		} else if r := s.runtime[t.ID]; r != nil {
			r.cancel()
		}
		s.save(t)
		return t.Public(), nil
	case ExtendRequest:
		return s.extendTask(a)
	case NetworkRequest:
		return s.changeNetwork(a.TaskID.TaskID, a, false)
	case GUIRequest:
		return s.changeGUI(a)
	case LinkRequest:
		return s.action(a.NodeID, NodeAction{Action: "network_link", Enabled: a.Enabled})
	case StartRequest:
		return s.startCommand(a, op)
	case InputRequest:
		return s.commandInput(a, op.Payload)
	case CancelCommandRequest:
		s.mu.Lock()
		t, e := s.task(a.TaskID.TaskID)
		if e != nil {
			s.mu.Unlock()
			return nil, e
		}
		c, e := t.Command(a.CommandID)
		if e != nil {
			s.mu.Unlock()
			return nil, e
		}
		if !c.Observing() {
			result := clone(c)
			s.mu.Unlock()
			return result, nil
		}
		id := NodeID{TaskID: a.TaskID, NodeID: c.NodeID}
		c.Cancellation = "requested"
		s.save(t)
		s.mu.Unlock()
		return s.action(id, NodeAction{Action: "command_cancel", CommandID: c.ID})
	case PutRequest:
		return s.putGuest(a)
	case GetRequest:
		if !guestPath(a.Path) {
			return nil, wire.Fail("invalid_argument", "Invalid guest path")
		}
		return s.collect(a.NodeID, "guest-archive.tar", NodeAction{Action: "file_get", Path: a.Path}, op.TargetID)
	case PowerRequest:
		if !oneOf(a.Action, "start", "shutdown", "reboot", "poweroff", "pause", "resume", "reset") {
			return nil, wire.Fail("invalid_argument", "Invalid power action")
		}
		return s.power(a)
	case EndpointRequest:
		return s.createEndpoint(a, op.TargetID)
	case CloseEndpointRequest:
		return s.closeEndpoint(a)
	case ScreenshotRequest:
		if a.Screen < 0 || a.Screen > 63 {
			return nil, wire.Fail("invalid_argument", "Invalid display index")
		}
		return s.collect(a.NodeID, "screenshot.png", NodeAction{Action: "screenshot", Screen: a.Screen}, op.TargetID)
	case DisplayRequest:
		size := a.Width != 0 || a.Height != 0
		if a.Screen < 0 || a.Screen > 63 || (size && (a.Width < 320 || a.Width > 16384 || a.Height < 200 || a.Height > 16384)) || (!size && a.AutoResize == nil) {
			return nil, wire.Fail("invalid_argument", "Invalid display size")
		}
		return s.action(a.NodeID, NodeAction{Action: "display", Screen: a.Screen, Width: a.Width, Height: a.Height, AutoResize: a.AutoResize})
	case InputNodeRequest:
		if !oneOf(a.Action, "move", "click", "drag", "scroll", "release", "scancodes") || a.Screen < 0 || a.Screen > 63 || !oneOfInt(a.Button, 0, 1, 2, 4) || a.DZ < -100 || a.DZ > 100 || a.DW < -100 || a.DW > 100 || len(a.Scancodes) > 256 {
			return nil, wire.Fail("invalid_argument", "Invalid native input")
		}
		for _, code := range a.Scancodes {
			if !scanCode.MatchString(code) {
				return nil, wire.Fail("invalid_argument", "Scancodes must be two hex digits")
			}
		}
		if oneOf(a.Action, "click", "drag") && a.Button == 0 {
			a.Button = 1
		}
		return s.action(a.NodeID, NodeAction{Action: "input_" + a.Action, Screen: a.Screen, X: a.X, Y: a.Y, EndX: a.EndX, EndY: a.EndY, Button: a.Button, DZ: a.DZ, DW: a.DW, Relative: a.Relative, Scancodes: a.Scancodes})
	case MediaRequest:
		return s.media(a)
	case CaptureRequest:
		return s.startCapture(a, op.TargetID)
	case CaptureStopRequest:
		s.mu.Lock()
		t, r, e := s.running(a.TaskID.TaskID)
		s.mu.Unlock()
		if e != nil {
			return nil, e
		}
		ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
		defer cancel()
		if e = readGate(ctx, &r.gate); e != nil {
			return nil, e
		}
		defer r.gate.RUnlock()
		e = s.stopCapture(ctx, t, r, a.CaptureID, false)
		return map[string]any{"capture_id": a.CaptureID}, e
	}
	return nil, wire.Fail("invalid_argument", "Unsupported operation")
}

var scanCode = regexp.MustCompile(`^[A-Fa-f0-9]{2}$`)

func oneOf(v string, values ...string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
func oneOfInt(v int, values ...int) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}
func guestPath(p string) bool {
	return len(p) > 0 && wire.ValidText(p, 32768) && (strings.HasPrefix(p, "/") || len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'))
}
func (s *Server) duration(v int) (int, error) {
	if v == 0 {
		v = s.cfg.Tasks.DefaultTimeout.Seconds()
	}
	if v < 1 || v > s.cfg.Tasks.MaxDuration.Seconds() {
		return 0, wire.Fail("limit_exceeded", "Task duration exceeds configured bounds")
	}
	return v, nil
}
func newTask(kind string, seconds int) *Task {
	t := &Task{executionRecord: executionRecord{ID: wire.ID(), Kind: kind, State: "queued", Created: time.Now().UTC(), TimeoutSeconds: seconds, Cleanup: "pending"}, Artifacts: []*Artifact{}}
	if kind == "lab" {
		t.domain = &LabState{Nodes: []*Node{}, Commands: []*Command{}, Endpoints: []*Endpoint{}, Captures: []*Capture{}}
	} else {
		t.domain = &NixJobState{}
	}
	return t
}

func (s *Server) buildLab(a LabRequest, op *Operation) (*Task, error) {
	duration, e := s.duration(a.TimeoutSeconds)
	if e != nil {
		return nil, e
	}
	if a.Nodes == nil || len(a.Nodes) > 64 {
		return nil, wire.Fail("invalid_argument", "nodes must be an array of 0..64 test nodes")
	}
	if s.cfg.VirtualBox.Router.ISO == "" {
		return nil, wire.Fail("router_unavailable", "Configure router.iso before creating a lab")
	}
	if a.LAN == "" {
		a.LAN = s.cfg.VirtualBox.Network.LAN
	}
	lan, e := netip.ParsePrefix(a.LAN)
	if e != nil {
		return nil, wire.Fail("invalid_argument", "Invalid LAN")
	}
	t := newTask("lab", duration)
	t.ID = op.TaskID
	t.lab().LAN = a.LAN
	t.lab().DHCP = a.DHCP == nil || *a.DHCP
	t.lab().DNS = append([]string{}, a.DNS...)
	if a.DNS == nil {
		t.lab().DNS = append([]string{}, s.cfg.VirtualBox.Network.DNS...)
	}
	t.lab().RouterISO = s.cfg.VirtualBox.Router.ISO
	t.lab().Internet = a.Internet == nil || *a.Internet
	t.lab().Network = "pending"
	t.lab().RouterPassword = wire.ID() + wire.ID()
	policy := network.Policy{LAN: a.LAN, WAN: s.cfg.VirtualBox.Network.WAN, DHCP: t.lab().DHCP, Internet: t.lab().Internet, DNS: t.lab().DNS, LeaseSeconds: s.cfg.VirtualBox.Network.LeaseSeconds}
	used := map[string]bool{}
	for _, n := range a.Nodes {
		if n.IPv4 != "" {
			if used[n.IPv4] {
				return nil, wire.Fail("invalid_argument", "Duplicate IPv4")
			}
			used[n.IPv4] = true
		}
	}
	next := lan.Addr().Next().Next()
	routerMAC := "08:00:27:" + t.ID[:2] + ":" + t.ID[2:4] + ":" + t.ID[4:6]
	macs := map[string]bool{routerMAC: true}
	for _, v := range a.Nodes {
		template, ok := s.cfg.VirtualBox.Templates[v.Template]
		if !ok {
			return nil, wire.Fail("invalid_argument", "Unknown template")
		}
		if v.CPUs == 0 {
			v.CPUs = template.CPUs
		}
		if v.MemoryMiB == 0 {
			v.MemoryMiB = template.MemoryMiB
		}
		if v.CPUs < 1 || v.CPUs > template.MaxCPUs || v.MemoryMiB < 128 || v.MemoryMiB > template.MaxMemoryMiB || !template.Media.Allows(v.Media) {
			return nil, wire.Fail("policy_denied", "Node exceeds template resource or media policy")
		}
		if v.Media.Camera {
			if _, ok := template.Cameras[v.Camera]; !ok {
				return nil, wire.Fail("policy_denied", "An allowed camera alias is required")
			}
		} else if v.Camera != "" {
			return nil, wire.Fail("invalid_argument", "Camera alias requires media.camera")
		}
		if v.IPv4 == "" {
			for used[next.String()] {
				next = next.Next()
			}
			v.IPv4 = next.String()
			used[v.IPv4] = true
			next = next.Next()
		}
		x := wire.ID()
		mac := "08:00:27:" + x[:2] + ":" + x[2:4] + ":" + x[4:6]
		for macs[mac] {
			x = wire.ID()
			mac = "08:00:27:" + x[:2] + ":" + x[2:4] + ":" + x[4:6]
		}
		macs[mac] = true
		n := &Node{ID: wire.ID(), NodeRequest: v, UUID: wire.UUID(), MAC: mac, State: "pending", Preparation: Preparation{Stage: "pending", Updated: op.Created}, Link: true}
		t.lab().Nodes = append(t.lab().Nodes, n)
		policy.Nodes = append(policy.Nodes, network.Lease{Name: v.Name, MAC: mac, IPv4: v.IPv4})
	}
	if e = policy.Validate(); e != nil {
		return nil, e
	}
	t.lab().Router = &Node{ID: wire.ID(), UUID: wire.UUID(), NodeRequest: NodeRequest{Name: "router", CPUs: 1, MemoryMiB: 256}, MAC: routerMAC, State: "pending", Preparation: Preparation{Stage: "pending", Updated: op.Created}}

	t.Created = op.Created
	return t, nil
}
func (s *Server) buildNix(a NixRequest, op *Operation) (*Task, *Transfer, error) {
	seconds, err := s.duration(a.TimeoutSeconds)
	if err != nil {
		return nil, nil, err
	}
	cache, err := s.transfer(a.CacheID)
	if err != nil || cache.Kind != "cache" {
		return nil, nil, wire.Fail("not_found", "Nix input not found")
	}
	if cache.TaskID != "" {
		return nil, nil, wire.Fail("conflict", "Nix input already belongs to a job")
	}
	if cache.State != "ready" {
		return nil, nil, wire.Fail("input_not_ready", "Nix input must be fully uploaded and verified")
	}
	t := newTask("nixos", seconds)
	t.Created, t.ID, t.nix().WorkerInstance = op.Created, op.TaskID, op.WorkerInstance
	t.nix().CacheID, t.nix().Derivation, t.nix().System = a.CacheID, cache.Derivation, cache.System
	attached := *cache
	attached.TaskID = t.ID
	return t, &attached, nil
}

func (s *Server) nodeAccess(id NodeID, action string) (*Task, *Runtime, *Node, error) {
	t, err := s.task(id.TaskID.TaskID)
	if err != nil {
		return nil, nil, nil, err
	}
	n, err := t.Node(id.NodeID)
	if err != nil {
		return nil, nil, nil, err
	}
	if t.Kind != "lab" || !n.Intent || n.Cleaned || t.Cleanup == "running" {
		return nil, nil, nil, wire.Fail("invalid_state", "Node resource is not available for this operation")
	}
	diagnostic := oneOf(action, "node_exec", "screenshot", "diagnostics", "vm_info")
	if t.Terminal() && !diagnostic {
		return nil, nil, nil, wire.Fail("invalid_state", "Task execution has ended")
	}
	if strings.HasPrefix(action, "command_") && action != "command_cancel" && !n.capabilities()["commands"] {
		return nil, nil, nil, wire.Fail("guest_not_ready", "Guest command capability is unavailable; inspect node_get or lab_diagnose")
	}
	r := s.runtime[t.ID]
	if r == nil {
		ctx, cancel := context.WithCancel(s.ctx)
		r = &Runtime{ctx: ctx, cancel: cancel, relays: map[string]*Relay{}}
		s.runtime[t.ID] = r
	}
	return t, r, n, nil
}
func (s *Server) action(id NodeID, a NodeAction) (any, error) {
	s.mu.Lock()
	t, r, _, err := s.nodeAccess(id, a.Action)
	var parent context.Context
	if err == nil {
		parent = r.ctx
		if t.Terminal() {
			parent = s.ctx
		}
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	budget := 120 * time.Second
	if a.Action == "shutdown" {
		budget = s.cfg.VirtualBox.ShutdownTimeout.Value()
	}
	if a.Action == "node_exec" {
		budget = time.Duration(a.TimeoutSeconds) * time.Second
	}
	if a.Action == "screenshot" || a.Action == "command_cancel" {
		budget = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	if err = readGate(ctx, &r.gate); err != nil {
		return nil, err
	}
	defer r.gate.RUnlock()
	if !oneOf(a.Action, "node_exec", "screenshot", "command_cancel") {
		lock := s.nodeLock(r, id.NodeID)
		if err = lockContext(ctx, lock); err != nil {
			return nil, wire.Fail("resource_busy", "Node operation did not acquire its execution slot before the deadline")
		}
		defer lock.Unlock()
	}
	s.mu.Lock()
	t, _, n, err := s.nodeAccess(id, a.Action)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if oneOf(a.Action, "start", "shutdown", "reboot", "poweroff", "pause", "resume", "reset") {
		power := "running"
		if a.Action == "shutdown" || a.Action == "poweroff" {
			power = "poweroff"
		}
		if a.Action == "pause" {
			power = "paused"
		}
		deadline := s.cfg.VirtualBox.StartupTimeout.Value()
		if a.Action == "shutdown" {
			deadline = s.cfg.VirtualBox.ShutdownTimeout.Value()
		}
		until := time.Now().UTC().Add(deadline)
		n.Expected = &ExpectedState{RequestID: a.RequestID, BeforeBoot: bootOf(n.Guest), Action: a.Action, Power: power, Requested: time.Now().UTC(), Until: &until}
		n.Health.GuestControl = Observation{State: "expected"}
		n.Health.Commands = Observation{State: "expected"}
		s.save(t)
	}
	tc, nc := t.executionSnapshot(), clone(n)
	s.mu.Unlock()
	if strings.HasPrefix(a.Action, "input_") {
		interval := s.cfg.VirtualBox.Templates[nc.Template].InputInterval
		if interval != nil {
			remaining := time.Until(nc.LastInput.Add(time.Duration(*interval * float64(time.Second))))
			if remaining > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(remaining):
				}
			}
		}
		s.mu.Lock()
		n.LastInput = time.Now().UTC()
		s.save(t)
		s.mu.Unlock()
	}
	raw, err := s.backend.Action(ctx, tc, nc, a)
	if err != nil {
		return nil, err
	}
	var value any
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	if n.Expected != nil && n.Expected.RequestID == a.RequestID && a.RequestID != "" {
		now := time.Now().UTC()
		n.Expected.Dispatched = &now
	}
	if a.Action == "network_link" {
		n.Link = a.Enabled
	}
	if a.Action == "display" && a.AutoResize != nil {
		n.AutoResize = *a.AutoResize
	}
	s.save(t)
	s.mu.Unlock()
	return value, nil
}

func (s *Server) startCommand(a StartRequest, op *Operation) (any, error) {
	id := op.TargetID
	spec := wire.CommandSpec{ID: id, Argv: a.Argv, Context: a.Context, Cwd: a.Cwd, Env: a.Env, TimeoutSeconds: a.TimeoutSeconds}
	if e := spec.Validate(); e != nil {
		return nil, e
	}
	s.mu.Lock()
	t, _, _, e := s.nodeAccess(a.NodeID, "command_start")
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	n, e := t.Node(a.NodeID.NodeID)
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	if a.Context == "desktop" && s.cfg.VirtualBox.Templates[n.Template].Kind != "windows" {
		s.mu.Unlock()
		return nil, wire.Fail("unsupported_context", "desktop requires a Windows template; select the Linux user/session in the guest fixture")
	}
	now := time.Now().UTC()
	statusDeadline := now.Add(120*time.Second + commandStatusGrace)
	c := &Command{ID: id, NodeID: a.NodeID.NodeID, CommandStatus: wire.CommandStatus{State: "starting", Stage: "submitting"}, StatusDeadline: &statusDeadline, Retained: RetainedOutput{Stdout: RetainedStream{State: "pending"}, Stderr: RetainedStream{State: "pending"}}, Created: now}
	t.lab().Commands = append(t.lab().Commands, c)
	e = s.db.CreateCommandReference(context.Background(), t, id, op.Intent)
	if e != nil {
		t.lab().Commands = t.lab().Commands[:len(t.lab().Commands)-1]
	}
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	_, e = s.action(a.NodeID, NodeAction{Action: "command_start", Spec: &spec})
	s.mu.Lock()
	defer s.mu.Unlock()
	if !c.Final && c.Observation.LastSuccess == nil {
		c.State, c.Stage = "starting", "awaiting_status"
		end := time.Now().UTC().Add(commandStatusGrace)
		if end.Before(*c.StatusDeadline) {
			c.StatusDeadline = &end
		}
		if e != nil {
			c.State = "uncertain"
			c.Observation.update(time.Now().UTC(), wire.Safe(e), s.cfg.VirtualBox.UnavailableAfter.Value(), false)
		}
	}
	s.save(t)
	return clone(c), e
}
func (s *Server) commandInput(a InputRequest, payload BodyRef) (any, error) {
	if a.Sequence < 0 || len(a.Data) > wire.InputPacketLimit {
		return nil, wire.Fail("invalid_argument", "Invalid input packet")
	}
	s.mu.Lock()
	t, e := s.task(a.TaskID.TaskID)
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	if t.Kind != "lab" || !oneOf(t.State, "preparing", "running") {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "An executing lab is required")
	}
	c, e := t.Command(a.CommandID)
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	if a.Sequence != c.Stdin.NextSequence {
		if a.Sequence < c.Stdin.NextSequence {
			packets, err := s.db.Inputs(s.ctx, t.ID, c.ID, a.Sequence, 1)
			if err == nil && len(packets) == 1 && packets[0].Data == a.Data && packets[0].EOF == a.EOF {
				v := map[string]any{"accepted": true, "sequence": a.Sequence, "delivered": a.Sequence < c.Stdin.Delivered, "written": a.Sequence < c.WrittenInput}
				s.mu.Unlock()
				return v, nil
			}
		}
		s.mu.Unlock()
		return nil, wire.Fail("input_order", "sequence must equal the next persisted input sequence")
	}
	if !c.Observing() {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "Command cannot accept more input")
	}
	if c.Stdin.Closed {
		s.mu.Unlock()
		return nil, wire.Fail("input_closed", "stdin already ended")
	}
	if c.Stdin.NextSequence >= wire.InputPacketCount || c.Stdin.Bytes+int64(len(a.Data)) > wire.CommandInputLimit {
		s.mu.Unlock()
		return nil, wire.Fail("input_limit", "stdin accepts at most 4096 packets and 64MiB; upload larger inputs as files")
	}
	packet := wire.Input{Sequence: a.Sequence, Data: a.Data, EOF: a.EOF}
	previous := c.Stdin
	c.Stdin.Accept(packet)
	e = s.db.AppendInputReference(context.Background(), t, c.ID, packet, payload)
	if e != nil {
		c.Stdin = previous
	}
	nodeID := c.NodeID
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	_, e = s.action(NodeID{TaskID: a.TaskID, NodeID: nodeID}, NodeAction{Action: "command_input", CommandID: a.CommandID, Input: &packet})
	delivery := "submitted"
	if e != nil {
		delivery = "pending"
	} else {
		s.mu.Lock()
		if next, err := s.db.DeliveredInput(s.ctx, t, c, packet.Sequence); err == nil {
			c.Stdin.Delivered = next
			s.save(t)
		}
		s.mu.Unlock()
	}
	return map[string]any{"accepted": true, "persisted": true, "sequence": packet.Sequence, "delivery": delivery}, nil
}
func (s *Server) changeNetwork(id string, a NetworkRequest, recover bool) (any, error) {
	s.mu.Lock()
	t, r, e := s.running(id)
	if e == nil && !recover && t.lab().Network != "ready" {
		e = wire.Fail("network_degraded", "Explicit network_recover is required")
	}
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	if e = readGate(ctx, &r.gate); e != nil {
		return nil, e
	}
	defer r.gate.RUnlock()
	lock := s.nodeLock(r, "network")
	if e = lockContext(ctx, lock); e != nil {
		return nil, e
	}
	defer lock.Unlock()
	s.mu.Lock()
	if t.State != "running" || (!recover && t.lab().Network != "ready") {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "Lab or network state changed")
	}
	proposal := clone(t)
	if a.Internet != nil {
		proposal.lab().Internet = *a.Internet
	}
	if a.DHCP != nil {
		proposal.lab().DHCP = *a.DHCP
	}
	if a.DNS != nil {
		proposal.lab().DNS = append([]string{}, a.DNS...)
	}
	seen := map[string]bool{}
	for _, addr := range a.Nodes {
		n, err := proposal.Node(addr.NodeID)
		if err != nil || seen[addr.NodeID] {
			s.mu.Unlock()
			return nil, wire.Fail("invalid_argument", "Invalid or duplicate node address registration")
		}
		seen[addr.NodeID] = true
		n.IPv4 = addr.IPv4
	}
	policy := taskPolicy(proposal, s.cfg.VirtualBox.Network.WAN, s.cfg.VirtualBox.Network.LeaseSeconds, nil)
	if err := policy.Validate(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	t.lab().Internet, t.lab().DHCP, t.lab().DNS = proposal.lab().Internet, proposal.lab().DHCP, proposal.lab().DNS
	for i, n := range proposal.lab().Nodes {
		t.lab().Nodes[i].IPv4 = n.IPv4
	}
	s.save(t)
	s.mu.Unlock()
	if recover {
		s.closeRelays(t, r)
	}
	if e = s.refreshNetworkContext(ctx, t, r, recover); e != nil {
		s.degrade(t, r, e)
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"task": t.Public(), "guest_network_refresh_required": a.DHCP != nil || a.DNS != nil || len(a.Nodes) > 0}, nil
}
func (s *Server) createEndpoint(a EndpointRequest, id string) (any, error) {
	if !oneOf(a.Protocol, "tcp", "udp") || a.Port < 1 || a.Port > 65535 {
		return nil, wire.Fail("invalid_argument", "Invalid endpoint")
	}
	s.mu.Lock()
	t, r, e := s.running(a.TaskID.TaskID)
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	if _, e = t.Node(a.NodeID.NodeID); e != nil {
		s.mu.Unlock()
		return nil, e
	}
	if e = endpointAdmission(t); e != nil {
		s.mu.Unlock()
		return nil, e
	}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	if e = readGate(ctx, &r.gate); e != nil {
		return nil, e
	}
	defer r.gate.RUnlock()
	lock := s.nodeLock(r, "network")
	if e = lockContext(ctx, lock); e != nil {
		return nil, e
	}
	defer lock.Unlock()
	s.mu.Lock()
	if e = endpointAdmission(t); e != nil {
		s.mu.Unlock()
		return nil, e
	}
	used := map[int]bool{}
	for _, v := range t.lab().Endpoints {
		if v.State != "closed" {
			used[v.HostPort] = true
			used[v.LoopbackPort] = true
		}
	}
	ports := []int{}
	for p := s.cfg.VirtualBox.Network.PortMin; p <= s.cfg.VirtualBox.Network.PortMax && len(ports) < 2; p++ {
		if used[p] {
			continue
		}
		addr := fmt.Sprintf("%s:%d", s.cfg.VirtualBox.Network.Listen, p)
		if len(ports) == 1 {
			addr = fmt.Sprintf("127.0.0.1:%d", p)
		}
		if a.Protocol == "tcp" {
			l, e := net.Listen("tcp4", addr)
			if e != nil {
				continue
			}
			l.Close()
		} else {
			l, e := net.ListenPacket("udp4", addr)
			if e != nil {
				continue
			}
			l.Close()
		}
		ports = append(ports, p)
	}
	if len(ports) != 2 {
		s.mu.Unlock()
		return nil, wire.Fail("resource_busy", "No free endpoint ports")
	}
	entry := &Endpoint{ID: id, NodeID: a.NodeID.NodeID, Protocol: a.Protocol, GuestPort: a.Port, HostPort: ports[0], LoopbackPort: ports[1], RouterPort: ports[1], State: "opening"}
	t.lab().Endpoints = append(t.lab().Endpoints, entry)
	e = s.db.Save(context.Background(), t)
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	if e = s.refreshNetworkContext(ctx, t, r, false); e == nil {
		e = s.openRelay(ctx, t, r, entry)
	}
	s.mu.Lock()
	if e == nil {
		entry.State = "open"
	} else {
		entry.State = "suspended"
	}
	s.save(t)
	result := clone(entry)
	s.mu.Unlock()
	if e != nil {
		s.degrade(t, r, e)
	}
	return result, e
}
func endpointAdmission(t *Task) error {
	if t.State != "running" {
		return &wire.Error{Code: "invalid_state", Message: "Endpoint publication requires a running lab", Details: map[string]any{"phase": t.phase(), "required_phase": "running", "next_action": "Inspect lab_get; wait for preparation or create another batch after cleanup"}}
	}
	if t.lab().Network != "ready" {
		return &wire.Error{Code: "network_not_ready", Message: "Endpoint publication requires a ready router policy and protection", Details: map[string]any{"network": t.lab().Network, "observation": t.lab().NetworkObservation, "next_action": "Inspect lab_get network observation; use network_recover after resolving a degraded network"}}
	}
	active := 0
	for _, e := range t.lab().Endpoints {
		if e.State != "closed" {
			active++
		}
	}
	if active >= 128 {
		return &wire.Error{Code: "endpoint_limit", Message: "Lab has reached its active endpoint limit", Details: map[string]any{"limit": 128, "active": active}}
	}
	return nil
}
func (s *Server) closeEndpoint(a CloseEndpointRequest) (any, error) {
	s.mu.Lock()
	t, r, e := s.running(a.TaskID.TaskID)
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	if e = readGate(ctx, &r.gate); e != nil {
		return nil, e
	}
	defer r.gate.RUnlock()
	lock := s.nodeLock(r, "network")
	if e = lockContext(ctx, lock); e != nil {
		return nil, e
	}
	defer lock.Unlock()
	s.mu.Lock()
	if t.State != "running" {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "Lab state changed")
	}
	var entry *Endpoint
	for _, v := range t.lab().Endpoints {
		if v.ID == a.EndpointID {
			entry = v
		}
	}
	if entry == nil {
		var old Endpoint
		err := s.db.child(s.ctx, "endpoints", a.EndpointID, &old)
		s.mu.Unlock()
		return &old, err
	}
	entry.State = "closing"
	relay := r.relays[entry.ID]
	delete(r.relays, entry.ID)
	s.save(t)
	tc, ec := clone(t), clone(entry)
	s.mu.Unlock()
	if relay != nil {
		relay.Close()
	}
	if e = s.backend.Endpoint(ctx, tc, ec, false); e == nil && tc.lab().Network == "ready" {
		e = s.refreshNetworkContext(ctx, t, r, false)
	}
	s.mu.Lock()
	entry.State = "closed"
	if e != nil {
		entry.State = "release_failed"
	}
	s.save(t)
	s.mu.Unlock()
	if e != nil {
		s.degrade(t, r, e)
	}
	ec.State = entry.State
	return ec, e
}
func (s *Server) media(a MediaRequest) (any, error) {
	s.mu.Lock()
	t, _, e := s.running(a.TaskID.TaskID)
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	n, e := t.Node(a.NodeID.NodeID)
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	p := s.cfg.VirtualBox.Templates[n.Template]
	s.mu.Unlock()
	allow := false
	device := a.Camera
	switch a.Device {
	case "camera":
		_, allow = p.Cameras[a.Camera]
		allow = allow && p.Media.Camera
	case "audio_input":
		allow = p.Media.AudioInput
	case "audio_output":
		allow = p.Media.AudioOutput
	default:
		return nil, wire.Fail("invalid_argument", "Runtime media supports camera, audio_input and audio_output")
	}
	if !allow {
		return nil, wire.Fail("policy_denied", "Media device is not allowed")
	}
	result, err := s.action(a.NodeID, NodeAction{Action: "media_" + a.Device, Enabled: a.Enabled, Device: device})
	if err == nil {
		s.mu.Lock()
		switch a.Device {
		case "camera":
			n.Media.Camera = a.Enabled
			n.Camera = a.Camera
		case "audio_input":
			n.Media.AudioInput = a.Enabled
		case "audio_output":
			n.Media.AudioOutput = a.Enabled
		}
		s.save(t)
		s.mu.Unlock()
	}
	return result, err
}
func (s *Server) purge(t *Task) error {
	if !t.Terminal() || t.Cleanup != "complete" {
		return wire.Fail("invalid_state", "Execution and resource cleanup must be complete")
	}
	delete(s.performancePending, t.ID)
	for id, x := range s.transfers {
		if x.TaskID == t.ID && s.uploading[id] != nil {
			return wire.Fail("conflict", "An upload is still settling")
		}
	}
	for _, o := range s.operations {
		if o.TaskID == t.ID && o.State == "pending" && o.Tool != "execution_delete" {
			return wire.Fail("conflict", "An operation is still settling")
		}
	}
	s.mu.Unlock()
	s.evidenceMu.Lock()
	defer s.evidenceMu.Unlock()
	s.mu.Lock()
	if err := loadChildren(s.ctx, s.db, "evidence", t.ID, &t.Artifacts); err != nil {
		return err
	}
	s.mu.Unlock()
	locked := false
	defer func() {
		if !locked {
			s.mu.Lock()
		}
	}()
	pinned := []*Artifact{}
	for _, a := range t.Artifacts {
		if a.Pinned {
			pinned = append(pinned, a)
		} else if err := s.root.Remove(a.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	dir := path.Join("tasks", t.ID)
	if len(pinned) == 0 {
		if err := s.root.RemoveAll(dir); err != nil {
			return err
		}
	} else {
		f, err := s.root.Open(dir)
		if err == nil {
			entries, e := f.ReadDir(-1)
			f.Close()
			if e != nil {
				return e
			}
			for _, entry := range entries {
				if entry.Name() != "artifacts" {
					if err = s.root.RemoveAll(path.Join(dir, entry.Name())); err != nil {
						return err
					}
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if len(pinned) > 0 {
		keep := map[string]bool{}
		for _, a := range pinned {
			keep[a.Path] = true
		}
		directory := path.Join(dir, "artifacts")
		f, err := s.root.Open(directory)
		if err == nil {
			entries, e := f.ReadDir(-1)
			f.Close()
			if e != nil {
				return e
			}
			for _, entry := range entries {
				p := path.Join(directory, entry.Name())
				if !keep[p] {
					if err = s.root.RemoveAll(p); err != nil {
						return err
					}
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	// Retained input headers are loaded lazily after restart. Discover their
	// durable references, not just entries still in the active transfer cache.
	rows, err := s.db.QueryContext(s.ctx, "SELECT id FROM transfers WHERE task_id=?", t.ID)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	s.mu.Lock()
	locked = true
	for _, id := range ids {
		input, err := s.transfer(id)
		if err != nil {
			return err
		}
		if err = s.removeTransfer(input); err != nil {
			return err
		}
	}
	t.Purged = true
	t.Artifacts = pinned
	if t.Kind == "lab" {
		t.lab().Commands = nil
		t.lab().Nodes = nil
		t.lab().Router = nil
		t.lab().Captures = nil
		t.lab().Endpoints = nil
		t.lab().RouterPassword = ""
		t.lab().RouterISO = ""
		t.lab().GUI = nil
		t.lab().NetworkObservation = nil
	}
	if err := s.db.PruneDetails(s.ctx, t.ID); err != nil {
		return err
	}
	return s.db.Save(s.ctx, t)
}

func (s *Server) collect(id NodeID, name string, a NodeAction, artifactID string) (any, error) {
	s.mu.Lock()
	t, _, _, err := s.nodeAccess(id, a.Action)
	if err == nil {
		_, err = t.Node(id.NodeID)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	artifact := &Artifact{ID: artifactID, Name: name, Created: time.Now().UTC()}
	artifact.Path = path.Join("tasks", id.TaskID.TaskID, "artifacts", artifact.ID)
	if e := s.root.MkdirAll(path.Dir(artifact.Path), 0700); e != nil {
		return nil, e
	}
	a.Local = path.Join(s.root.Path, artifact.Path)
	if _, e := s.action(id, a); e != nil {
		return nil, e
	}
	f, e := s.root.Regular(artifact.Path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	size, hash, e := hashFile(f, s.cfg.Storage.MaxUploadBytes)
	if e != nil {
		return nil, e
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	artifact.Size = size
	artifact.SHA256 = hash
	s.mu.Lock()
	defer s.mu.Unlock()
	t, e = s.task(id.TaskID.TaskID)
	if e != nil {
		return nil, e
	}
	t.Artifacts = append(t.Artifacts, artifact)
	s.save(t)
	return artifactView(artifact), nil
}
func (s *Server) startCapture(a CaptureRequest, id string) (any, error) {
	if !oneOf(a.Kind, "video", "nic", "metrics") {
		return nil, wire.Fail("invalid_argument", "Invalid capture kind")
	}
	s.mu.Lock()
	t, _, e := s.running(a.TaskID.TaskID)
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	if _, e = t.Node(a.NodeID.NodeID); e != nil {
		s.mu.Unlock()
		return nil, e
	}
	for _, x := range t.lab().Captures {
		if x.NodeID == a.NodeID.NodeID && x.Kind == a.Kind && x.State != "stopped" {
			s.mu.Unlock()
			return nil, wire.Fail("conflict", "Capture already running")
		}
	}
	c := &Capture{ID: id, NodeID: a.NodeID.NodeID, Kind: a.Kind, State: "running"}
	t.lab().Captures = append(t.lab().Captures, c)
	e = s.db.Save(context.Background(), t)
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	_, e = s.action(a.NodeID, NodeAction{Action: "capture_start", Capture: clone(c)})
	s.mu.Lock()
	defer s.mu.Unlock()
	if e != nil {
		c.State = "start_uncertain"
	}
	s.save(t)
	return clone(c), e
}
func (s *Server) stopCapture(ctx context.Context, t *Task, r *Runtime, id string, cleaning bool) error {
	s.mu.Lock()
	var c *Capture
	for _, x := range t.lab().Captures {
		if x.ID == id {
			c = x
		}
	}
	if c == nil {
		var old Capture
		err := s.db.child(s.ctx, "captures", id, &old)
		s.mu.Unlock()
		return err
	}
	if c.State == "stopped" {
		s.mu.Unlock()
		return nil
	}
	n, _ := t.Node(c.NodeID)
	snapshot, nc, cc := clone(t), clone(n), clone(c)
	s.mu.Unlock()
	lock := s.nodeLock(r, n.ID)
	if err := lockContext(ctx, lock); err != nil {
		return err
	}
	defer lock.Unlock()
	a := &Artifact{ID: wire.ID(), Name: "capture-" + c.Kind, Created: time.Now().UTC()}
	a.Path = path.Join("tasks", t.ID, "artifacts", a.ID)
	_ = s.root.MkdirAll(path.Dir(a.Path), 0700)
	_, e := s.backend.Action(ctx, snapshot, nc, NodeAction{Action: "capture_stop", Capture: cc, Local: path.Join(s.root.Path, a.Path)})
	if e == nil {
		f, err := s.root.Regular(a.Path)
		e = err
		if e == nil {
			a.Size, a.SHA256, e = hashFile(f, s.cfg.Storage.MaxUploadBytes)
			f.Close()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.State = "stopped"
	if e == nil {
		c.ArtifactID = a.ID
		t.Artifacts = append(t.Artifacts, a)
	} else {
		c.State = "stop_failed"
	}
	s.save(t)
	return e
}

// Reconcile is a local administrator command, never an MCP tool. It only retries
// registered cleanup; it cannot waive ownership or claim cleanup on request.
func (s *Server) Reconcile(id string) error {
	s.mu.Lock()
	t, e := s.task(id)
	if e != nil {
		s.mu.Unlock()
		return e
	}
	if !t.Terminal() {
		s.mu.Unlock()
		return wire.Fail("invalid_state", "Task must be terminal")
	}
	s.mu.Unlock()
	s.cleanup(t)
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Cleanup != "complete" {
		return wire.Fail("cleanup_required", "Resources are still unresolved")
	}
	s.releaseScheduler(t)
	return nil
}
