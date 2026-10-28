package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestPreparationPollingBoundsTrafficAndResetsAfterProgress(t *testing.T) {
	start := time.Unix(1700000000, 0).UTC()
	for _, id := range []string{"router", "windows", "linux"} {
		now, calls := start, 0
		var polling preparationPolling
		for now.Before(start.Add(10 * time.Minute)) {
			schedule := polling.next(id, "guest_control", now)
			if schedule.Next == nil || schedule.IntervalMS < 2000 || schedule.IntervalMS > 30000 || !schedule.Next.After(now) {
				t.Fatal(schedule)
			}
			now = *schedule.Next
			calls++
		}
		// A continuously unavailable ten-minute boot needs at most thirty
		// probes, rather than a fixed two-second loop's three hundred.
		if calls < 20 || calls > 30 {
			t.Fatalf("unbounded startup polling: %d calls", calls)
		}
		progress := polling.next(id, "initializing", now)
		if progress.IntervalMS != 2000 || !progress.Next.Equal(now.Add(2*time.Second)) {
			t.Fatalf("stage progress did not reset the observation delay: %#v", progress)
		}
	}
}

func TestPreparationBackoffDoesNotExtendCancellationOrDeadline(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		var schedule PollSchedule
		schedule.set(time.Now(), 30*time.Second, "preparation_backoff")
		if cancelled {
			cancel()
		}
		started := time.Now()
		err := schedule.wait(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
			t.Fatal("backoff delayed termination", err, time.Since(started))
		}
	}
}

func TestUnavailableNodeOwnsRecoveryForAllCommands(t *testing.T) {
	b := &readBackend{read: func(a NodeAction) (wire.CommandSnapshot, error) {
		return snapshotFixture(a.CommandID, wire.CommandStatus{State: "running"}, nil, nil, 0, 0, 1024), nil
	}}
	s, task, c := commandFixture(t, b)
	n := task.lab().Nodes[0]
	n.Health.GuestControl = Observation{State: "unavailable", Failures: 5}
	n.nextProbe(time.Now().UTC())
	for range 25 {
		task.lab().Commands = append(task.lab().Commands, &Command{ID: wire.ID(), NodeID: n.ID, CommandStatus: wire.CommandStatus{State: "running"}})
	}
	for range 3 {
		for _, command := range task.lab().Commands {
			s.pollCommand(task, s.runtime[task.ID], command.ID)
		}
	}
	if b.calls != 0 || c.Schedule.Reason != "node_unavailable" || task.State != "running" || c.Final {
		t.Fatal(b.calls, c)
	}
	// One successful node probe is insufficient; a second recovers the shared
	// capability, after which the same command resumes without resubmission.
	good := &NodeStatus{State: "running"}
	s.observeNode(n, good, nil, time.Now().UTC())
	s.pollCommand(task, s.runtime[task.ID], c.ID)
	if b.calls != 0 {
		t.Fatal("polled before recovery", b.calls)
	}
	s.observeNode(n, good, nil, time.Now().UTC())
	s.pollCommand(task, s.runtime[task.ID], c.ID)
	if b.calls != 1 || c.State != "running" || c.Observation.State != "available" {
		t.Fatal(b.calls, c)
	}
}

func TestRetryAndIdleSchedulingAreBoundedAndVisible(t *testing.T) {
	now := time.Now().UTC()
	a, b := wire.ID(), wire.ID()
	different := false
	for failures := 1; failures <= 100; failures++ {
		delay := retryDelay(a, failures, probeInterval)
		if delay < 8*time.Second || delay > 30*time.Second || delay != retryDelay(a, failures, probeInterval) {
			t.Fatal(delay)
		}
		different = different || delay != retryDelay(b, failures, probeInterval)
	}
	if !different {
		t.Fatal("recovery probes were not spread")
	}
	c := &Command{ID: a}
	for range 20 {
		c.successfulPoll(now, false)
	}
	if c.Schedule.IntervalMS != 5000 || c.Schedule.Reason != "idle" || c.Schedule.due(now) || !c.Schedule.due(now.Add(5*time.Second)) {
		t.Fatal(c.Schedule)
	}
	c.successfulPoll(now, true)
	if c.Schedule.IntervalMS != 1000 || c.Schedule.Reason != "active" {
		t.Fatal(c.Schedule)
	}
	c.Observation = Observation{Error: &wire.Error{Code: "resource_busy"}}
	c.failedPoll(now)
	if c.Schedule.IntervalMS != 5000 || c.Schedule.Reason != "capacity_wait" || c.Observation.Failures != 0 {
		t.Fatal(c)
	}
}

func TestOutputDeadlineConvergesWhileNodeRemainsUnavailable(t *testing.T) {
	b := &readBackend{}
	s, task, c := commandFixture(t, b)
	task.lab().Nodes[0].Health.GuestControl.State = "unavailable"
	end, code := time.Now().Add(-time.Second), 19
	c.CommandStatus = wire.CommandStatus{State: "exited", Final: true, ExitCode: &code}
	c.Retained.Deadline = &end
	s.pollCommand(task, s.runtime[task.ID], c.ID)
	if b.calls != 0 || c.Polling() || *c.ExitCode != 19 || c.Retained.Stdout.Error.Code != "output_timeout" {
		t.Fatal(c)
	}
}

func TestQuietCommandPollsBackOffAndOutputResumesQuickly(t *testing.T) {
	b := &readBackend{}
	s, task, c := commandFixture(t, b)
	out := []byte(nil)
	b.read = func(a NodeAction) (wire.CommandSnapshot, error) {
		return snapshotFixture(c.ID, wire.CommandStatus{State: "running"}, out, nil, a.StdoutOffset, a.StderrOffset, 1024), nil
	}
	for range 7 {
		s.pollCommandContext(context.Background(), task, c.ID, false)
	}
	if c.Schedule.IntervalMS != 5000 || c.Final {
		t.Fatal(c)
	}
	out = []byte("new output")
	s.pollCommandContext(context.Background(), task, c.ID, false)
	if c.Schedule.IntervalMS != 1000 || c.Retained.Stdout.Bytes != int64(len(out)) {
		t.Fatal(c)
	}
}
