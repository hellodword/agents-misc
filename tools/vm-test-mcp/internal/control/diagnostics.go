package control

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type DiagnosticItem struct {
	NodeID     string      `json:"node_id"`
	Name       string      `json:"name"`
	State      string      `json:"state"`
	Result     any         `json:"result,omitempty"`
	ArtifactID string      `json:"evidence_id,omitempty"`
	Size       int64       `json:"size,omitempty"`
	SHA256     string      `json:"sha256,omitempty"`
	Error      *wire.Error `json:"error,omitempty"`
}
type DiagnosticReport struct {
	EventID     string           `json:"event_id,omitempty"`
	Started     time.Time        `json:"started"`
	Finished    time.Time        `json:"finished"`
	Items       []DiagnosticItem `json:"items"`
	ArtifactID  string           `json:"evidence_id,omitempty"`
	Performance any              `json:"performance"`
}

func (s *Server) newArtifact(t *Task, name string) (*Artifact, string, error) {
	a := &Artifact{ID: wire.ID(), Name: name, Created: time.Now().UTC()}
	a.Path = path.Join("tasks", t.ID, "artifacts", a.ID)
	err := s.root.MkdirAll(path.Dir(a.Path), 0700)
	return a, path.Join(s.root.Path, a.Path), err
}
func (s *Server) retainArtifact(t *Task, a *Artifact) error {
	f, err := s.root.Regular(a.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	a.Size, a.SHA256, err = hashFile(f, s.cfg.Storage.MaxUploadBytes)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Artifacts = append(t.Artifacts, a)
	s.save(t)
	return nil
}
func (s *Server) jsonArtifact(t *Task, name string, value any) (*Artifact, error) {
	a, err := s.writeJSONArtifact(t, name, wire.JSON(value))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Artifacts = append(t.Artifacts, a)
	s.save(t)
	return a, nil
}
func (s *Server) writeJSONArtifact(t *Task, name string, data []byte) (*Artifact, error) {
	a, _, err := s.newArtifact(t, name)
	if err != nil {
		return nil, err
	}
	if a.Size, a.SHA256, err = s.root.Write(a.Path, bytesReader(data), s.cfg.Storage.MaxUploadBytes); err != nil {
		return nil, err
	}
	return a, nil
}
func (s *Server) diagnose(parent context.Context, taskID string, ids []string, automatic bool, eventID ...string) (any, error) {
	ctx, cancel := context.WithTimeout(parent, diagnosticBudget)
	defer cancel()
	s.mu.Lock()
	t, err := s.task(taskID)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	if t.Kind != "lab" || t.Purged || t.Cleanup == "running" || t.Cleanup == "complete" {
		s.mu.Unlock()
		return nil, wire.Fail("invalid_state", "Lab resources are unavailable for collection")
	}
	snapshot := clone(t)
	r := s.runtime[t.ID]
	if r == nil {
		runtimeCtx, stop := context.WithCancel(s.ctx)
		r = &Runtime{ctx: runtimeCtx, cancel: stop, relays: map[string]*Relay{}}
		s.runtime[t.ID] = r
	}
	s.mu.Unlock()
	if !snapshot.Terminal() {
		stop := context.AfterFunc(r.ctx, cancel)
		defer stop()
	}
	nodes := []*Node{}
	if len(ids) == 0 {
		nodes = append(nodes, snapshot.lab().Nodes...)
		if snapshot.lab().Router != nil {
			nodes = append(nodes, snapshot.lab().Router)
		}
	} else {
		seen := map[string]bool{}
		for _, id := range ids {
			n, err := snapshot.Node(id)
			if err != nil {
				return nil, err
			}
			if !seen[id] {
				nodes = append(nodes, n)
				seen[id] = true
			}
		}
	}
	if automatic {
		if !r.diagnostics.TryLock() {
			return nil, wire.Fail("resource_busy", "Evidence collection is already running")
		}
	} else if err = lockContext(ctx, &r.diagnostics); err != nil {
		return nil, err
	}
	defer r.diagnostics.Unlock()
	if err = readGate(ctx, &r.gate); err != nil {
		return nil, err
	}
	defer r.gate.RUnlock()
	s.mu.Lock()
	deleting := t.Cleanup == "running" || t.Cleanup == "complete"
	s.mu.Unlock()
	if deleting {
		return nil, wire.Fail("invalid_state", "Resource cleanup is in progress")
	}
	return s.collectDiagnostics(ctx, t, snapshot, nodes, eventID...)
}

func windowsDiagnostic(script string) NodeAction {
	return NodeAction{Action: "node_exec", TimeoutSeconds: 10, Argv: []string{`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); " + script}}
}

func (s *Server) collectDiagnostics(ctx context.Context, t, snapshot *Task, nodes []*Node, eventID ...string) (any, error) {
	report := DiagnosticReport{Started: time.Now().UTC(), Items: []DiagnosticItem{}}
	if len(eventID) > 0 {
		report.EventID = eventID[0]
		report.ArtifactID = diagnosticArtifactID(t.ID, eventID[0], "report")
		if data, err := s.root.Read(path.Join("tasks", t.ID, "artifacts", report.ArtifactID), 32<<20); err == nil {
			if err = json.Unmarshal(data, &report); err != nil {
				return report, err
			}
			return report, nil
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, n := range nodes {
		if n == nil || !n.Intent || n.Cleaned {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				mu.Lock()
				report.Items = append(report.Items, DiagnosticItem{NodeID: n.ID, Name: "collection", State: "skipped", Error: wire.Safe(ctx.Err())})
				mu.Unlock()
				return
			}
			items := []DiagnosticItem{{NodeID: n.ID, Name: "observation", State: "collected", Result: n.public()}}
			actions := []struct {
				name   string
				action NodeAction
			}{
				{"virtualbox", NodeAction{Action: "vm_info"}},
				{"VBox.log", NodeAction{Action: "diagnostics"}},
				{"screenshot.png", NodeAction{Action: "screenshot"}},
			}
			if n == snapshot.lab().Router {
				actions = append(actions, struct {
					name   string
					action NodeAction
				}{"boot-console.log", NodeAction{Action: "boot_log"}})
			}
			if n != snapshot.lab().Router {
				if s.cfg.VirtualBox.Templates[n.Template].Kind == "linux" {
					actions = append(actions,
						struct {
							name   string
							action NodeAction
						}{"initialization", NodeAction{Action: "node_exec", Argv: []string{"/bin/systemctl", "show", "vm-test-firstboot.service", "--no-pager", "--property=ActiveState", "--property=SubState", "--property=Result"}}},
						struct {
							name   string
							action NodeAction
						}{"services", NodeAction{Action: "node_exec", Argv: []string{"/bin/journalctl", "-b", "--no-pager", "-n", "150", "-u", "vm-test-firstboot", "-u", "vboxadd-service", "-u", "virtualbox-guest-utils"}}},
						struct {
							name   string
							action NodeAction
						}{"kernel", NodeAction{Action: "node_exec", Argv: []string{"/bin/journalctl", "-b", "-k", "--no-pager", "-n", "150"}}})
				} else {
					actions = append(actions,
						struct {
							name   string
							action NodeAction
						}{"services", windowsDiagnostic("Get-Service VBoxService,VMTestGuest | Format-List Name,Status")},
						struct {
							name   string
							action NodeAction
						}{"events", windowsDiagnostic("Get-WinEvent -FilterHashtable @{LogName='System';Level=1,2,3} -MaxEvents 30 -ErrorAction Continue | Format-List TimeCreated,ProviderName,Id,Message")})
				}
			}
			for _, entry := range actions {
				item := DiagnosticItem{NodeID: n.ID, Name: entry.name, State: "collected"}
				if ctx.Err() != nil {
					item.State = "skipped"
					item.Error = wire.Safe(ctx.Err())
					items = append(items, item)
					continue
				}
				action := entry.action
				var artifact *Artifact
				var err error
				if oneOf(action.Action, "diagnostics", "screenshot", "boot_log") {
					artifact, action.Local, err = s.newArtifact(t, n.Name+"-"+entry.name)
					if err == nil && report.EventID != "" {
						artifact.ID = diagnosticArtifactID(t.ID, report.EventID, n.ID+"/"+entry.name)
						artifact.Path = path.Join("tasks", t.ID, "artifacts", artifact.ID)
						action.Local = path.Join(s.root.Path, artifact.Path)
						if retained, lookupErr := s.db.evidence(ctx, artifact.ID); lookupErr == nil {
							item.ArtifactID = retained.ID
							item.Size, item.SHA256 = retained.Size, retained.SHA256
							items = append(items, item)
							continue
						}
					}
				}
				budget := 5 * time.Second
				if action.TimeoutSeconds > 0 {
					budget = time.Duration(action.TimeoutSeconds) * time.Second
				}
				call, stop := context.WithTimeout(ctx, budget)
				var raw json.RawMessage
				if err == nil {
					raw, err = s.backend.Action(call, snapshot, n, action)
				}
				stop()
				if len(raw) > 0 {
					_ = json.Unmarshal(raw, &item.Result)
				}
				if err == nil && artifact != nil {
					if report.EventID == "" {
						err = s.retainArtifact(t, artifact)
					} else {
						var f interface {
							Read([]byte) (int, error)
							Close() error
						}
						f, err = s.root.Regular(artifact.Path)
						if err == nil {
							artifact.Size, artifact.SHA256, err = hashFile(uploadReader{ctx, f}, s.cfg.Storage.MaxUploadBytes)
							f.Close()
						}
					}
					if err == nil {
						item.ArtifactID = artifact.ID
						item.Size, item.SHA256 = artifact.Size, artifact.SHA256
					}
				}
				if result, ok := item.Result.(map[string]any); ok && result["state"] == "uncertain" {
					item.State = "partial"
				}
				if result, ok := item.Result.(map[string]any); ok && action.Action == "boot_log" && (result["truncated"] != false || result["error"] != nil) {
					item.State = "partial"
				}
				if err != nil {
					item.State = "unavailable"
					item.Error = wire.Safe(err)
				}
				items = append(items, item)
			}
			mu.Lock()
			report.Items = append(report.Items, items...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(report.Items, func(i, j int) bool {
		if report.Items[i].NodeID != report.Items[j].NodeID {
			return report.Items[i].NodeID < report.Items[j].NodeID
		}
		return report.Items[i].Name < report.Items[j].Name
	})
	report.Finished = time.Now().UTC()
	report.Performance = s.performance.Window(report.Started.Add(-30*time.Second), report.Finished)
	var a *Artifact
	var err error
	if report.EventID == "" {
		a, err = s.jsonArtifact(t, "diagnostics.json", report)
	} else {
		a = &Artifact{ID: report.ArtifactID, Name: "diagnostics.json", Created: time.Now().UTC()}
		a.Path = path.Join("tasks", t.ID, "artifacts", a.ID)
		a.Size, a.SHA256, err = s.root.Write(a.Path, bytesReader(wire.JSON(report)), 32<<20)
	}
	if err != nil {
		report.ArtifactID = ""
		return report, err
	}
	report.ArtifactID = a.ID
	s.mu.Lock()
	s.event(t, "diagnostics_collected", "Evidence bundle "+a.ID)
	s.save(t)
	s.mu.Unlock()
	return report, nil
}
func (s *Server) execNode(a ExecRequest) (any, error) {
	if a.TimeoutSeconds == 0 {
		a.TimeoutSeconds = 10
	}
	if a.TimeoutSeconds < 1 || a.TimeoutSeconds > 30 || len(a.Argv) == 0 || !guestPath(a.Argv[0]) {
		return nil, wire.Fail("invalid_argument", "node_exec requires an absolute guest executable and a deadline of 1..30 seconds")
	}
	spec := wire.CommandSpec{ID: wire.ID(), Argv: a.Argv, Cwd: a.Cwd, Env: a.Env, TimeoutSeconds: a.TimeoutSeconds}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	value, err := s.action(a.NodeID, NodeAction{Action: "node_exec", Argv: a.Argv, Cwd: a.Cwd, Env: a.Env, TimeoutSeconds: a.TimeoutSeconds})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	t, _ := s.task(a.TaskID.TaskID)
	s.mu.Unlock()
	if result, ok := value.(map[string]any); ok {
		expected := expectedExitCodes(a.ExpectedExitCodes)
		result["expected_exit_codes"] = expected
		result["outcome"] = directOutcome(result, expected)
		if diagnosticOutcome(result["outcome"].(string)) {
			s.mu.Lock()
			queueErr := s.queueDiagnostic(t, "node-exec/"+a.RequestID, "node_exec_"+result["outcome"].(string), []string{a.NodeID.NodeID})
			s.mu.Unlock()
			if queueErr != nil {
				return nil, queueErr
			}
		}
	}
	artifact, err := s.jsonArtifact(t, "node-exec.json", value)
	if err != nil {
		return nil, err
	}
	if result, ok := value.(map[string]any); ok {
		result["evidence_id"] = artifact.ID
	}
	return value, nil
}
