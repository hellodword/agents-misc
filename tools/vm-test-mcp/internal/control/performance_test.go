package control

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/telemetry"
)

type delayedRunner struct{}

func (delayedRunner) Run(ctx context.Context, _ string, _ []string, _ io.Reader) ([]byte, error) {
	select {
	case <-time.After(20 * time.Millisecond):
		return []byte("output must not enter telemetry"), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestGuestCallSeparatesCapacityWaitFromExecution(t *testing.T) {
	cfg := testConfig(t)
	b := &VBoxBackend{Config: cfg, Runner: delayedRunner{}, Performance: telemetry.New()}
	s, task, _ := fixtureLab(t, b, time.Second)
	b.Runner = delayedRunner{}
	if err := os.MkdirAll(b.runtime(task), 0700); err != nil {
		t.Fatal(err)
	}
	n := task.Nodes[0]
	slots := make(chan struct{}, 4)
	for range 4 {
		slots <- struct{}{}
	}
	b.guestSlots.Store(n.UUID, slots)
	go func() { time.Sleep(50 * time.Millisecond); <-slots }()
	start := time.Now()
	_, err := b.guestCaptured(context.Background(), task, n, "run", "private-argv")
	if err != nil {
		t.Fatal(err)
	}
	w := s.performance.Window(start, time.Now())
	if len(w.Calls) != 1 || w.Calls[0].WaitUS < 40000 || w.Calls[0].ExecutionUS < 15000 || w.Calls[0].ExecutionUS > 250000 || w.Calls[0].UserUS != nil {
		t.Fatal(w)
	}
	raw, _ := json.Marshal(w)
	if strings.Contains(string(raw), "private-argv") || strings.Contains(string(raw), "output must") {
		t.Fatal(string(raw))
	}
	// A timed-out slot never claims that a subprocess executed.
	slots <- struct{}{}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	start = time.Now()
	_, err = b.guestCaptured(ctx, task, n, "run")
	w = s.performance.Window(start, time.Now())
	if err == nil || len(w.Calls) != 1 || w.Calls[0].ExecutionUS != 0 || !w.Calls[0].Failed || w.Calls[0].WaitUS < 15000 {
		t.Fatal(w, err)
	}
}

func TestFaultWindowSurvivesReleaseAndDoesNotResurrectPurgedEvidence(t *testing.T) {
	s, task, r := fixtureLab(t, &fakeBackend{}, time.Second)
	s.mu.Lock()
	s.retainPerformanceWindow(task, "controlled_delay", 40*time.Millisecond)
	task.State = "finished"
	r.cancel()
	s.mu.Unlock()
	s.cleanup(task)
	s.mu.Lock()
	if task.Cleanup != "complete" {
		t.Fatal("evidence retained executor", task.Public())
	}
	s.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		pending := !s.performancePending[task.ID].IsZero()
		s.mu.Unlock()
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("evidence did not settle")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for _, a := range task.Artifacts {
		if a.Name == "performance.json" {
			found = true
			raw, e := os.ReadFile(filepath.Join(s.root.Path, a.Path))
			if e != nil || !strings.Contains(string(raw), "controlled_delay") || !strings.Contains(string(raw), "no_data") {
				t.Fatal(string(raw), e)
			}
		}
	}
	if !found {
		t.Fatal("missing fault window")
	}
	if e := s.purge(task); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(s.root.Path, "tasks", task.ID)); !os.IsNotExist(e) {
		t.Fatal("purge failed", e)
	}
}

func TestRealHostCallReportsElapsedAndCPUWithoutPayload(t *testing.T) {
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	r, e := hostexec.Capture(context.Background(), hostexec.Exec{}, exe, []string{"-test.run=^$"}, nil)
	if e != nil || r.ExecutionUS <= 0 || r.UserUS == nil || r.SystemUS == nil {
		t.Fatal(r, e)
	}
}

func TestPurgeCancelsPendingFaultEvidence(t *testing.T) {
	s, task, r := fixtureLab(t, &fakeBackend{}, time.Second)
	s.mu.Lock()
	task.State = "finished"
	r.cancel()
	s.mu.Unlock()
	s.cleanup(task)
	s.mu.Lock()
	s.retainPerformanceWindow(task, "controlled_delay", 20*time.Millisecond)
	if e := s.purge(task); e != nil {
		t.Fatal(e)
	}
	s.mu.Unlock()
	// Wait for this task's short capture to settle, rather than shutting down
	// the monitor: a later writer must respect the already completed purge.
	s.wg.Wait()
	if _, e := os.Stat(filepath.Join(s.root.Path, "tasks", task.ID)); !os.IsNotExist(e) {
		t.Fatal("pending capture resurrected purged files", e)
	}
}
