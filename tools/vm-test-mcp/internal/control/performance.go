package control

import (
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/telemetry"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (b *VBoxBackend) recordCall(started time.Time, wait time.Duration, scope, operation, taskID, nodeID string, r hostexec.Result, err error) {
	b.Performance.Record(telemetry.Call{Started: started.UTC(), Finished: time.Now().UTC(), Scope: scope, Operation: operation, TaskID: taskID, NodeID: nodeID, WaitUS: wait.Microseconds(), ExecutionUS: r.ExecutionUS, UserUS: r.UserUS, SystemUS: r.SystemUS, ReadBytes: int64(len(r.Stdout) + len(r.Stderr)), Failed: err != nil})
}

// Called with s.mu held. A fault window never keeps a task's executor alive.
// Coalesce nearby faults per task and retain evidence outside the task lease.
func (s *Server) retainPerformance(t *Task, trigger string) {
	s.retainPerformanceWindow(t, trigger, 30*time.Second)
}
func (s *Server) retainPerformanceWindow(t *Task, trigger string, duration time.Duration) {
	if s.performance == nil || s.closed || t.Purged || !s.performancePending[t.ID].IsZero() {
		return
	}
	at := time.Now().UTC()
	s.performancePending[t.ID] = at.Add(duration)
	before := s.performance.Window(at.Add(-30*time.Second), at)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-s.ctx.Done():
		}
		after := s.performance.Window(at, time.Now().UTC())
		data := wire.JSON(map[string]any{"trigger": trigger, "time": at, "before": before, "after": after})
		s.mu.Lock()
		defer s.mu.Unlock()
		pending := s.performancePending[t.ID]
		delete(s.performancePending, t.ID)
		if t.Purged || pending.IsZero() {
			return
		}
		// Serialize the bounded artifact publication with purge. An explicit
		// purge wins over a pending observation and is never resurrected later.
		a, err := s.writeJSONArtifact(t, "performance.json", data)
		if err == nil {
			t.Artifacts = append(t.Artifacts, a)
			s.eventDetails(t, "performance_collected", "Host and controller observation window retained", map[string]any{"evidence_id": a.ID, "trigger": trigger, "time": at})
		} else {
			s.eventDetails(t, "performance_unavailable", "Host observation window could not be retained", map[string]any{"cause": wire.Safe(err), "trigger": trigger, "time": at})
		}
		s.save(t)
	}()
}
