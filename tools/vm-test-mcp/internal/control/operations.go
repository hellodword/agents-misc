package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/network"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (s *Server) query(ctx context.Context, name string, args any) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch a := args.(type) {
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
			templates[name] = v
		}
		backends := map[string]any{}
		s.mu.Unlock()
		for _, kind := range []string{"lab", "nixos"} {
			c, cancel := context.WithTimeout(ctx, 3*time.Second)
			reason, e := s.backend.Available(c, kind)
			cancel()
			backends[kind] = map[string]any{"available": e == nil && reason == "", "waiting_reason": reason}
		}
		s.mu.Lock()
		return map[string]any{"version": wire.Version, "templates": templates, "backends": backends, "active_task_id": s.active, "limits": map[string]any{"default_seconds": s.cfg.Tasks.DefaultTimeout.Seconds(), "max_seconds": s.cfg.Tasks.MaxDuration.Seconds(), "max_upload_bytes": s.cfg.Storage.MaxUploadBytes, "max_nodes": 64, "startup_timeout": s.cfg.VirtualBox.StartupTimeout.Value().String(), "shutdown_timeout": s.cfg.VirtualBox.ShutdownTimeout.Value().String(), "cleanup_timeout": s.cfg.Tasks.CleanupTimeout.Value().String(), "probe_interval": probeInterval.String(), "probe_timeout": probeTimeout.String(), "unavailable_after": s.cfg.VirtualBox.UnavailableAfter.Value().String(), "failure_threshold": 3, "recovery_successes": 2, "diagnostic_timeout": diagnosticBudget.String(), "node_exec_default_seconds": 10, "node_exec_max_seconds": 30}, "endpoint_address": s.cfg.VirtualBox.Network.Listen}, nil
	case ListRequest:
		start, end, e := page(a.Cursor, a.Limit, len(s.tasks))
		if e != nil {
			return nil, e
		}
		items := []any{}
		for _, t := range s.tasks[start:end] {
			items = append(items, t.Public())
		}
		return map[string]any{"items": items, "next_cursor": end}, nil
	case TaskID:
		t, e := s.task(a.TaskID)
		if e != nil {
			return nil, e
		}
		return t.Public(), nil
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
		o := s.operations[a.OperationID]
		if o == nil {
			return nil, wire.Fail("not_found", "Operation not found")
		}
		return clone(o), nil
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
		t, e := s.task(a.TaskID.TaskID)
		if e != nil {
			return nil, e
		}
		c, e := t.Command(a.CommandID)
		if e != nil {
			return nil, e
		}
		return clone(c), nil
	case ReadRequest:
		t, e := s.task(a.TaskID.TaskID)
		if e != nil {
			return nil, e
		}
		if _, e = t.Command(a.CommandID); e != nil {
			return nil, e
		}
		if a.Stream != "stdout" && a.Stream != "stderr" {
			return nil, wire.Fail("invalid_argument", "stream must be stdout or stderr")
		}
		return s.readLog(path.Join("tasks", t.ID, "commands", a.CommandID, a.Stream), a.Offset, a.Limit)
	case TransferGetRequest:
		t := s.transfers[a.TransferID]
		if t == nil || t.State == "purged" {
			return nil, wire.Fail("not_found", "Transfer not found")
		}
		return s.transferView(t), nil
	case CaptureGetRequest:
		t, e := s.task(a.TaskID.TaskID)
		if e != nil {
			return nil, e
		}
		for _, c := range t.Captures {
			if c.ID == a.CaptureID {
				return clone(c), nil
			}
		}
		return nil, wire.Fail("not_found", "Capture does not belong to task")
	case ArtifactListRequest:
		t, e := s.task(a.TaskID.TaskID)
		if e != nil {
			return nil, e
		}
		start, end, e := page(a.Cursor, a.Limit, len(t.Artifacts))
		if e != nil {
			return nil, e
		}
		items := []any{}
		for _, f := range t.Artifacts[start:end] {
			items = append(items, artifactView(f))
		}
		return map[string]any{"items": items, "next_cursor": end, "purged": t.Purged}, nil
	case ArtifactGetRequest:
		t, e := s.task(a.TaskID.TaskID)
		if e != nil {
			return nil, e
		}
		if t.Purged {
			return nil, wire.Fail("purged", "Task data was purged")
		}
		for _, f := range t.Artifacts {
			if f.ID == a.ArtifactID {
				v := artifactView(f)
				v["url"], v["expires"] = s.capURL("artifact", t.ID+"/"+f.ID, "read")
				return v, nil
			}
		}
		return nil, wire.Fail("not_found", "Artifact does not belong to task")
	}
	return nil, wire.Fail("invalid_argument", "Unsupported query")
}
func artifactView(a *Artifact) map[string]any {
	return map[string]any{"id": a.ID, "name": a.Name, "size": a.Size, "sha256": a.SHA256, "created": a.Created}
}

func (s *Server) mutate(name string, args any) (any, error) {
	switch a := args.(type) {
	case DiagnoseRequest:
		return s.diagnose(s.ctx, a.TaskID.TaskID, a.NodeIDs, false)
	case ExecRequest:
		return s.execNode(a)
	case LabRequest:
		return s.createLab(a)
	case NixRequest:
		return s.createNix(a)
	case TransferRequest:
		return s.createTransfer(a)
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
		if name == "task_cleanup" {
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
		if name == "task_purge" {
			err := s.purge(t)
			return t.Public(), err
		}
		if t.Terminal() {
			return t.Public(), nil
		}
		queued := t.State == "queued"
		t.State = "cancelled"
		t.Conclusion = "inconclusive"
		if queued {
			t.Cleanup = "complete"
			now := time.Now().UTC()
			t.Ended = &now
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
		return s.startCommand(a)
	case InputRequest:
		return s.commandInput(a)
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
		return s.collect(a.NodeID, "guest-archive.tar", NodeAction{Action: "file_get", Path: a.Path})
	case PowerRequest:
		if !oneOf(a.Action, "start", "shutdown", "reboot", "poweroff", "pause", "resume", "reset") {
			return nil, wire.Fail("invalid_argument", "Invalid power action")
		}
		return s.action(a.NodeID, NodeAction{Action: a.Action})
	case EndpointRequest:
		return s.createEndpoint(a)
	case CloseEndpointRequest:
		return s.closeEndpoint(a)
	case ScreenshotRequest:
		if a.Screen < 0 || a.Screen > 63 {
			return nil, wire.Fail("invalid_argument", "Invalid display index")
		}
		return s.collect(a.NodeID, "screenshot.png", NodeAction{Action: "screenshot", Screen: a.Screen})
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
		return s.action(a.NodeID, NodeAction{Action: "input_" + a.Action, Screen: a.Screen, X: a.X, Y: a.Y, EndX: a.EndX, EndY: a.EndY, Button: a.Button, DZ: a.DZ, DW: a.DW, Relative: a.Relative, Scancodes: a.Scancodes})
	case MediaRequest:
		return s.media(a)
	case CaptureRequest:
		return s.startCapture(a)
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
	return &Task{ID: wire.ID(), Kind: kind, State: "queued", Created: time.Now().UTC(), TimeoutSeconds: seconds, Cleanup: "pending", Nodes: []*Node{}, Commands: []*Command{}, Endpoints: []*Endpoint{}, Captures: []*Capture{}, Artifacts: []*Artifact{}}
}
func (s *Server) createLab(a LabRequest) (any, error) {
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
	t.LAN = a.LAN
	t.DHCP = a.DHCP == nil || *a.DHCP
	t.DNS = append([]string{}, a.DNS...)
	if a.DNS == nil {
		t.DNS = append([]string{}, s.cfg.VirtualBox.Network.DNS...)
	}
	t.RouterISO = s.cfg.VirtualBox.Router.ISO
	t.Internet = a.Internet == nil || *a.Internet
	t.Network = "pending"
	t.RouterPassword = wire.ID() + wire.ID()
	policy := network.Policy{LAN: a.LAN, WAN: s.cfg.VirtualBox.Network.WAN, DHCP: t.DHCP, Internet: t.Internet, DNS: t.DNS, LeaseSeconds: s.cfg.VirtualBox.Network.LeaseSeconds}
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
		n := &Node{ID: wire.ID(), NodeRequest: v, UUID: wire.UUID(), MAC: mac, State: "pending", Link: true}
		t.Nodes = append(t.Nodes, n)
		policy.Nodes = append(policy.Nodes, network.Lease{Name: v.Name, MAC: mac, IPv4: v.IPv4})
	}
	if e = policy.Validate(); e != nil {
		return nil, e
	}
	t.Router = &Node{ID: wire.ID(), UUID: wire.UUID(), NodeRequest: NodeRequest{Name: "router", CPUs: 1, MemoryMiB: 256}, MAC: routerMAC, State: "pending"}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Created = s.requests[a.RequestID].Created
	if e = s.db.Save(context.Background(), state.Document{Kind: "task", ID: t.ID, Value: t}); e != nil {
		return nil, e
	}
	s.tasks = append(s.tasks, t)
	sort.SliceStable(s.tasks, func(i, j int) bool { return s.tasks[i].Created.Before(s.tasks[j].Created) })
	return t.Public(), nil
}
func (s *Server) createNix(a NixRequest) (any, error) {
	seconds, e := s.duration(a.TimeoutSeconds)
	if e != nil {
		return nil, e
	}
	if !wire.StorePattern.MatchString(a.Derivation) || !strings.HasSuffix(a.Derivation, ".drv") || !oneOf(a.System, "x86_64-linux", "aarch64-linux") {
		return nil, wire.Fail("invalid_argument", "Invalid derivation or system")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cache := s.transfers[a.CacheID]
	if cache == nil || cache.Kind != "cache" || cache.State == "purged" {
		return nil, wire.Fail("not_found", "Cache transfer not found")
	}
	if cache.TaskID != "" {
		return nil, wire.Fail("conflict", "Cache is already attached to a task")
	}
	for _, prior := range s.tasks {
		if prior.Derivation == a.Derivation {
			return nil, wire.Fail("conflict", "A fresh execution requires a fresh derivation identity")
		}
	}
	if _, ok := cache.Objects[strings.SplitN(path.Base(a.Derivation), "-", 2)[0]+".narinfo"]; !ok {
		return nil, wire.Fail("invalid_cache", "Cache does not contain the requested derivation")
	}
	if e = s.seal(cache); e != nil {
		return nil, e
	}
	t := newTask("nixos", seconds)
	t.Created = s.requests[a.RequestID].Created
	t.CacheID = a.CacheID
	t.Derivation = a.Derivation
	t.System = a.System
	cache.TaskID = t.ID
	if e = s.db.Save(context.Background(), state.Document{Kind: "task", ID: t.ID, Value: t}, state.Document{Kind: "transfer", ID: cache.ID, Value: cache}); e != nil {
		return nil, e
	}
	s.tasks = append(s.tasks, t)
	sort.SliceStable(s.tasks, func(i, j int) bool { return s.tasks[i].Created.Before(s.tasks[j].Created) })
	return t.Public(), nil
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
		return nil, nil, nil, wire.Fail("guest_not_ready", "Guest command capability is unavailable; inspect node_get or task_diagnose")
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
		until := time.Now().UTC().Add(s.cfg.VirtualBox.StartupTimeout.Value())
		n.Expected = &ExpectedState{Action: a.Action, Power: power, Requested: time.Now().UTC(), Until: &until}
		s.save(t)
	}
	tc, nc := clone(t), clone(n)
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

func (s *Server) startCommand(a StartRequest) (any, error) {
	id := wire.ID()
	spec := wire.CommandSpec{ID: id, Argv: a.Argv, Cwd: a.Cwd, Env: a.Env, TimeoutSeconds: a.TimeoutSeconds}
	if e := spec.Validate(); e != nil {
		return nil, e
	}
	s.mu.Lock()
	t, _, _, e := s.nodeAccess(a.NodeID, "command_start")
	if e != nil {
		s.mu.Unlock()
		return nil, e
	}
	if _, e = t.Node(a.NodeID.NodeID); e != nil {
		s.mu.Unlock()
		return nil, e
	}
	c := &Command{ID: id, NodeID: a.NodeID.NodeID, Spec: spec, CommandStatus: wire.CommandStatus{State: "starting"}, Input: []wire.Input{}, Created: time.Now().UTC()}
	t.Commands = append(t.Commands, c)
	e = s.db.Save(context.Background(), state.Document{Kind: "task", ID: t.ID, Value: t})
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	_, e = s.action(a.NodeID, NodeAction{Action: "command_start", Spec: &spec})
	s.mu.Lock()
	defer s.mu.Unlock()
	c.State = "running"
	if e != nil {
		c.State = "uncertain"
	}
	s.save(t)
	return clone(c), e
}
func (s *Server) commandInput(a InputRequest) (any, error) {
	if a.Sequence < 0 || len(a.Data) > 65536 {
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
	if a.Sequence != len(c.Input) {
		s.mu.Unlock()
		return nil, wire.Fail("input_order", "sequence must equal the next persisted input sequence")
	}
	if !oneOf(c.State, "running", "starting", "uncertain") || len(c.Input) >= 1000000 {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "Command cannot accept more input")
	}
	if len(c.Input) > 0 && c.Input[len(c.Input)-1].EOF {
		s.mu.Unlock()
		return nil, wire.Fail("input_closed", "stdin already ended")
	}
	packet := wire.Input{Sequence: a.Sequence, Data: a.Data, EOF: a.EOF}
	c.Input = append(c.Input, packet)
	e = s.db.Save(context.Background(), state.Document{Kind: "task", ID: t.ID, Value: t})
	nodeID := c.NodeID
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	_, e = s.action(NodeID{TaskID: a.TaskID, NodeID: nodeID}, NodeAction{Action: "command_input", CommandID: a.CommandID, Input: &packet})
	delivery := "submitted"
	if e != nil {
		delivery = "pending"
	}
	return map[string]any{"persisted": true, "sequence": packet.Sequence, "delivery": delivery}, nil
}
func (s *Server) changeNetwork(id string, a NetworkRequest, recover bool) (any, error) {
	s.mu.Lock()
	t, r, e := s.running(id)
	if e == nil && !recover && t.Network != "ready" {
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
	if t.State != "running" || (!recover && t.Network != "ready") {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "Lab or network state changed")
	}
	proposal := clone(t)
	if a.Internet != nil {
		proposal.Internet = *a.Internet
	}
	if a.DHCP != nil {
		proposal.DHCP = *a.DHCP
	}
	if a.DNS != nil {
		proposal.DNS = append([]string{}, a.DNS...)
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
	t.Internet, t.DHCP, t.DNS = proposal.Internet, proposal.DHCP, proposal.DNS
	for i, n := range proposal.Nodes {
		t.Nodes[i].IPv4 = n.IPv4
	}
	s.save(t)
	s.mu.Unlock()
	if recover {
		s.closeRelays(t, r)
	}
	if e = s.refreshNetworkContext(ctx, t, r, recover); e != nil {
		s.degrade(t, r)
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return map[string]any{"task": t.Public(), "guest_network_refresh_required": a.DHCP != nil || a.DNS != nil || len(a.Nodes) > 0}, nil
}
func (s *Server) createEndpoint(a EndpointRequest) (any, error) {
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
	if t.Network != "ready" || len(t.Endpoints) >= 128 {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "Network unavailable or endpoint limit reached")
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
	if t.State != "running" || t.Network != "ready" || len(t.Endpoints) >= 128 {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "Lab or network state changed")
	}
	used := map[int]bool{}
	for _, v := range t.Endpoints {
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
	entry := &Endpoint{ID: wire.ID(), NodeID: a.NodeID.NodeID, Protocol: a.Protocol, GuestPort: a.Port, HostPort: ports[0], LoopbackPort: ports[1], RouterPort: ports[1], State: "opening"}
	t.Endpoints = append(t.Endpoints, entry)
	e = s.db.Save(context.Background(), state.Document{Kind: "task", ID: t.ID, Value: t})
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
		s.degrade(t, r)
	}
	return result, e
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
	for _, v := range t.Endpoints {
		if v.ID == a.EndpointID {
			entry = v
		}
	}
	if entry == nil {
		s.mu.Unlock()
		return nil, wire.Fail("not_found", "Endpoint does not belong to task")
	}
	entry.State = "closed"
	relay := r.relays[entry.ID]
	delete(r.relays, entry.ID)
	s.save(t)
	tc, ec := clone(t), clone(entry)
	s.mu.Unlock()
	if relay != nil {
		relay.Close()
	}
	if e = s.backend.Endpoint(ctx, tc, ec, false); e == nil && tc.Network == "ready" {
		e = s.refreshNetworkContext(ctx, t, r, false)
	}
	if e != nil {
		s.degrade(t, r)
	}
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
		return wire.Fail("invalid_state", "Only a terminal, fully cleaned task can be purged")
	}
	for _, o := range s.operations {
		if o.TaskID == t.ID && o.State == "pending" && o.Tool != "task_purge" {
			return wire.Fail("conflict", "A task operation is still settling")
		}
	}
	if t.Kind == "nixos" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, e := s.backend.Nix(ctx, "purge", clone(t))
		cancel()
		if e != nil {
			return e
		}
	}
	if e := s.root.RemoveAll(path.Join("tasks", t.ID)); e != nil {
		return e
	}
	for _, x := range s.transfers {
		if x.TaskID == t.ID {
			if e := s.root.RemoveAll(path.Join("transfers", x.ID)); e != nil {
				return e
			}
			x.State = "purged"
			s.persist(state.Document{Kind: "transfer", ID: x.ID, Value: x})
		}
	}
	t.Purged = true
	for _, c := range t.Commands {
		c.Input = nil
		c.Spec = wire.CommandSpec{ID: c.ID}
	}
	// Receipts keep their identity/hash/status for idempotency, but old task or
	// command snapshots must not retain the payload that was explicitly purged.
	for _, o := range s.operations {
		if o.TaskID == t.ID && o.State != "pending" {
			o.Result = map[string]any{"task_id": t.ID, "purged": true}
			s.persist(state.Document{Kind: "operation", ID: o.ID, Value: o})
		}
	}
	s.save(t)
	return nil
}
func (s *Server) expireRetention() {
	if s.cfg.Storage.Days == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-time.Duration(s.cfg.Storage.Days) * 24 * time.Hour)
	for _, t := range s.tasks {
		if !t.Purged && t.Ended != nil && t.Ended.Before(cutoff) && t.Cleanup == "complete" {
			_ = s.purge(t)
		}
	}
}

func (s *Server) collect(id NodeID, name string, a NodeAction) (any, error) {
	s.mu.Lock()
	t, _, _, err := s.nodeAccess(id, a.Action)
	if err == nil {
		_, err = t.Node(id.NodeID)
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	artifact := &Artifact{ID: wire.ID(), Name: name, Created: time.Now().UTC()}
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
func (s *Server) startCapture(a CaptureRequest) (any, error) {
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
	for _, x := range t.Captures {
		if x.NodeID == a.NodeID.NodeID && x.Kind == a.Kind && x.State == "running" {
			s.mu.Unlock()
			return nil, wire.Fail("conflict", "Capture already running")
		}
	}
	c := &Capture{ID: wire.ID(), NodeID: a.NodeID.NodeID, Kind: a.Kind, State: "running"}
	t.Captures = append(t.Captures, c)
	e = s.db.Save(context.Background(), state.Document{Kind: "task", ID: t.ID, Value: t})
	s.mu.Unlock()
	if e != nil {
		return nil, e
	}
	_, e = s.action(a.NodeID, NodeAction{Action: "capture_start", Capture: clone(c)})
	s.mu.Lock()
	defer s.mu.Unlock()
	if e != nil {
		c.State = "failed"
	}
	s.save(t)
	return clone(c), e
}
func (s *Server) stopCapture(ctx context.Context, t *Task, r *Runtime, id string, cleaning bool) error {
	s.mu.Lock()
	var c *Capture
	for _, x := range t.Captures {
		if x.ID == id {
			c = x
		}
	}
	if c == nil {
		s.mu.Unlock()
		return wire.Fail("not_found", "Capture does not belong to task")
	}
	if c.State != "running" {
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
		c.State = "failed"
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
