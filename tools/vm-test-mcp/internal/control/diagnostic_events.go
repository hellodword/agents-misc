package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type DiagnosticEvent struct {
	ID          string      `json:"id"`
	ExecutionID string      `json:"execution_id"`
	Source      string      `json:"source"`
	NodeIDs     []string    `json:"node_ids"`
	State       string      `json:"state"`
	Outcome     string      `json:"outcome,omitempty"`
	Created     time.Time   `json:"created"`
	Started     *time.Time  `json:"started,omitempty"`
	Finished    *time.Time  `json:"finished,omitempty"`
	ArtifactID  string      `json:"evidence_id,omitempty"`
	Error       *wire.Error `json:"error,omitempty"`
}

func diagnosticArtifactID(taskID, eventID, name string) string {
	hash := sha256.Sum256(wire.JSON([]string{taskID, eventID, name}))
	return hex.EncodeToString(hash[:16])
}

// The caller holds s.mu. A source transition has one durable collection intent.
func (s *Server) queueDiagnostic(t *Task, id, source string, nodes []string) error {
	if t.Kind != "lab" || t.Purged {
		return nil
	}
	e := DiagnosticEvent{ID: id, ExecutionID: t.ID, Source: source, NodeIDs: nodes, State: "pending", Created: time.Now().UTC()}
	_, err := s.db.ExecContext(s.ctx, "INSERT INTO diagnostic_events(task_id,event_id,state,body) VALUES(?,?,?,?) ON CONFLICT(task_id,event_id) DO NOTHING", t.ID, id, e.State, wire.JSON(e))
	if err == nil {
		err = s.refreshDiagnosticState(t)
		if err == nil {
			s.diagnosticLifecycleLocked()
		}
	}
	return err
}

func (s *Server) diagnosticEvent(taskID, id string) (DiagnosticEvent, error) {
	var e DiagnosticEvent
	var raw []byte
	err := s.db.QueryRowContext(s.ctx, "SELECT body FROM diagnostic_events WHERE task_id=? AND event_id=?", taskID, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &e)
	}
	return e, err
}
func (s *Server) storeDiagnostic(e DiagnosticEvent) error {
	_, err := s.db.ExecContext(context.Background(), "UPDATE diagnostic_events SET state=?,body=? WHERE task_id=? AND event_id=?", e.State, wire.JSON(e), e.ExecutionID, e.ID)
	return err
}

func (s *Server) finishDiagnostic(e DiagnosticEvent, result any, err error) {
	now := time.Now().UTC()
	e.Finished = &now
	e.State = "complete"
	if report, ok := result.(DiagnosticReport); ok {
		e.ArtifactID = report.ArtifactID
		collected, missed := 0, 0
		for _, item := range report.Items {
			if item.State == "collected" {
				collected++
			} else {
				missed++
			}
		}
		if missed > 0 {
			e.State = "partial"
			if collected == 0 {
				e.State = "unavailable"
			}
		}
	}
	if err != nil {
		e.Error = wire.Safe(err)
		e.State = "unavailable"
		if e.ArtifactID != "" {
			e.State = "partial"
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, loadErr := s.task(e.ExecutionID); loadErr == nil && !t.Purged {
		if e.ArtifactID != "" {
			e.Outcome = e.State
			e.State = "captured"
		}
		if saveErr := s.storeDiagnostic(e); saveErr != nil {
			s.closed = true
			s.cancel()
			return
		}
		if err := s.refreshDiagnosticState(t); err != nil {
			s.closed = true
			s.cancel()
			return
		}
		s.event(t, "evidence_captured", e.Source+": "+e.State)
		s.save(t)
		s.diagnosticLifecycleLocked()
	}
}

// Resource cleanup cancels live collectors and owns the final bounded capture.
// Every pending intent is settled even when its VMs have already been released.
func (s *Server) diagnosticLifecycleLocked() {
	if s.closed {
		return
	}
	rows, err := s.db.QueryContext(s.ctx, "SELECT body FROM diagnostic_events WHERE state IN ('pending','captured') ORDER BY task_id,event_id LIMIT 64")
	if err != nil {
		return
	}
	var pending []DiagnosticEvent
	for rows.Next() {
		var raw []byte
		var e DiagnosticEvent
		if rows.Scan(&raw) != nil || json.Unmarshal(raw, &e) != nil {
			continue
		}
		pending = append(pending, e)
	}
	rows.Close()
	for _, e := range pending {
		t, err := s.task(e.ExecutionID)
		if err != nil || t.Purged {
			continue
		}
		if e.State == "captured" {
			if s.diagnosticPublishing >= 2 {
				continue
			}
			if e.ID == "execution-ended" && t.Cleanup == "running" {
				continue
			}
			e.State = "publishing"
			if s.storeDiagnostic(e) != nil {
				continue
			}
			s.wg.Add(1)
			s.diagnosticPublishing++
			go func() {
				defer s.wg.Done()
				s.publishDiagnostics(e)
				s.mu.Lock()
				s.diagnosticPublishing--
				s.diagnosticLifecycleLocked()
				s.mu.Unlock()
			}()
			continue
		}
		if t.Cleanup == "running" {
			continue
		}
		if t.Cleanup == "complete" {
			now := time.Now().UTC()
			e.Finished = &now
			e.State = "unavailable"
			e.Error = wire.Safe(wire.Fail("resources_released", "Resources were released before this collection could run"))
			_ = s.storeDiagnostic(e)
			_ = s.refreshDiagnosticState(t)
			continue
		}
		r := s.runtime[t.ID]
		if r == nil || r.diagnosticActive || t.Terminal() {
			continue
		}
		now := time.Now().UTC()
		e.State, e.Started = "collecting", &now
		if s.storeDiagnostic(e) != nil {
			continue
		}
		_ = s.refreshDiagnosticState(t)
		r.diagnosticActive = true
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			result, err := s.diagnose(s.ctx, t.ID, e.NodeIDs, false, e.ID)
			s.finishDiagnostic(e, result, err)
			s.mu.Lock()
			r.diagnosticActive = false
			s.diagnosticLifecycleLocked()
			s.mu.Unlock()
		}()
	}
}

func (s *Server) collectFinalDiagnostics(ctx context.Context, t, snapshot *Task, nodes []*Node) {
	s.mu.Lock()
	err := s.queueDiagnostic(t, "execution-ended", "execution_end", nil)
	e, loadErr := s.diagnosticEvent(t.ID, "execution-ended")
	if err == nil {
		err = loadErr
	}
	if err == nil && oneOf(e.State, "captured", "publishing", "blocked", "complete", "partial", "unavailable") {
		s.mu.Unlock()
		return
	}
	if err == nil {
		now := time.Now().UTC()
		e.State, e.Started = "collecting", &now
		err = s.storeDiagnostic(e)
	}
	s.mu.Unlock()
	if err != nil {
		return
	}
	result, err := s.collectDiagnostics(ctx, t, snapshot, nodes, e.ID)
	s.finishDiagnostic(e, result, err)
}

func (s *Server) refreshDiagnosticState(t *Task) error {
	rows, err := s.db.QueryContext(context.Background(), `SELECT state,COALESCE(json_extract(body,'$.outcome'),''),COUNT(*) FROM diagnostic_events WHERE task_id=? GROUP BY state,COALESCE(json_extract(body,'$.outcome'),'')`, t.ID)
	if err != nil {
		return err
	}
	status := wire.CollectionStatus{State: "idle"}
	handoff := wire.NixEvidence{State: "complete"}
	for rows.Next() {
		var state, outcome string
		var count int
		if err = rows.Scan(&state, &outcome, &count); err != nil {
			break
		}
		if oneOf(state, "captured", "publishing", "blocked") {
			if handoff.State != "blocked" {
				handoff.State = "transferring"
			}
			if state == "blocked" {
				handoff.State = "blocked"
			}
			state = outcome
		}
		switch state {
		case "pending":
			status.Pending += count
		case "collecting":
			status.Active += count
		case "complete":
			status.Complete += count
		case "partial":
			status.Partial += count
		case "unavailable":
			status.Unavailable += count
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if handoff.State == "blocked" {
		var raw []byte
		if err := s.db.QueryRowContext(context.Background(), "SELECT body FROM diagnostic_events WHERE task_id=? AND state='blocked' ORDER BY event_id LIMIT 1", t.ID).Scan(&raw); err != nil {
			return err
		}
		var event DiagnosticEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return err
		}
		handoff.Error = &wire.Error{Code: "evidence_blocked", Category: "execution", Message: "Captured diagnostics require publication recovery", Retry: "reconcile", NextAction: "Resolve the recorded cause, then call execution_recover with scope evidence", Details: map[string]any{"event_id": event.ID, "cause": event.Error}}
	}
	switch {
	case status.Active > 0:
		status.State = "collecting"
	case status.Pending > 0:
		status.State = "pending"
	case status.Partial+status.Unavailable > 0:
		status.State = "partial"
		if status.Complete+status.Partial == 0 {
			status.State = "unavailable"
		}
	case status.Complete > 0:
		status.State = "complete"
	}
	if status.Pending+status.Active > 0 && handoff.State == "complete" {
		handoff.State = "pending"
	}
	t.lab().Collection, t.lab().Evidence = status, handoff
	return s.db.Save(context.Background(), t)
}

func (s *Server) publishDiagnostics(e DiagnosticEvent) {
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.Tasks.EvidenceTimeout.Value())
	defer cancel()
	file := path.Join("tasks", e.ExecutionID, "artifacts", e.ArtifactID)
	data, err := s.root.Read(file, 32<<20)
	var report DiagnosticReport
	if err == nil {
		err = json.Unmarshal(data, &report)
	}
	if err == nil && (report.EventID != e.ID || report.ArtifactID != e.ArtifactID) {
		err = wire.Fail("integrity_mismatch", "Captured diagnostic report identity differs from its event")
	}
	var artifacts []*Artifact
	if err == nil {
		for _, item := range report.Items {
			if item.ArtifactID == "" {
				continue
			}
			if !wire.IDPattern.MatchString(item.ArtifactID) || !sha256Pattern.MatchString(item.SHA256) {
				err = wire.Fail("integrity_mismatch", "Invalid captured diagnostic object")
				break
			}
			a := &Artifact{ID: item.ArtifactID, ExecutionID: e.ExecutionID, ExecutionKind: "lab", Name: item.NodeID + "-" + item.Name, Size: item.Size, SHA256: item.SHA256, Created: report.Finished}
			a.Path = path.Join("tasks", e.ExecutionID, "artifacts", a.ID)
			f, openErr := s.root.Regular(a.Path)
			if openErr != nil {
				err = openErr
				break
			}
			size, hash, verifyErr := hashFile(uploadReader{ctx, f}, a.Size)
			f.Close()
			if verifyErr != nil {
				err = verifyErr
				break
			}
			if size != a.Size || hash != a.SHA256 {
				err = wire.Fail("integrity_mismatch", "Captured diagnostic bytes differ from their manifest")
				break
			}
			artifacts = append(artifacts, a)
		}
	}
	if err == nil {
		hash := sha256.Sum256(data)
		artifacts = append(artifacts, &Artifact{ID: e.ArtifactID, ExecutionID: e.ExecutionID, ExecutionKind: "lab", Name: "diagnostics.json", Path: file, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:]), Created: report.Finished})
		for _, a := range artifacts {
			if err = s.db.PublishEvidence(ctx, a); err != nil {
				break
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, loadErr := s.task(e.ExecutionID); loadErr == nil && !t.Purged {
		e.State = e.Outcome
		if err != nil {
			e.State = "blocked"
			e.Error = wire.Safe(err)
		} else {
			e.Error = nil
		}
		if s.ctx.Err() != nil {
			e.State = "captured"
		}
		if saveErr := s.storeDiagnostic(e); saveErr == nil {
			_ = s.refreshDiagnosticState(t)
		}
		s.event(t, "evidence_published", e.Source+": "+e.State)
		s.save(t)
	}
}
