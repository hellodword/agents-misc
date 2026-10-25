package control

import (
	"context"
	"hash/fnv"
	"strconv"
	"time"
)

// Schedules are visible but are not inactivity leases. Only task deadlines or
// explicit cancellation release resources; a quiet service remains legitimate.
type PollSchedule struct {
	Next       *time.Time `json:"next,omitempty"`
	IntervalMS int64      `json:"interval_ms"`
	Reason     string     `json:"reason"`
}

func (p PollSchedule) due(now time.Time) bool { return p.Next == nil || !now.Before(*p.Next) }
func (p *PollSchedule) set(now time.Time, delay time.Duration, reason string) {
	next := now.Add(delay)
	p.Next, p.IntervalMS, p.Reason = &next, delay.Milliseconds(), reason
}

func (p PollSchedule) wait(ctx context.Context) error {
	if p.Next == nil {
		return ctx.Err()
	}
	timer := time.NewTimer(time.Until(*p.Next))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

type preparationPolling struct {
	stage   string
	retries int
}

func (p *preparationPolling) next(id, stage string, now time.Time) PollSchedule {
	if p.stage != stage {
		p.stage, p.retries = stage, 0
	} else {
		p.retries = min(p.retries+1, 5)
	}
	var schedule PollSchedule
	reason := "preparation_wait"
	if p.retries > 0 {
		reason = "preparation_backoff"
	}
	schedule.set(now, retryDelay(id, p.retries, 2*time.Second), reason)
	return schedule
}
func retryDelay(id string, failures int, base time.Duration) time.Duration {
	if failures == 0 {
		return base
	}
	delay := min(30*time.Second, base*time.Duration(1<<min(failures, 5)))
	h := fnv.New32a()
	_, _ = h.Write([]byte(id + ":" + strconv.Itoa(failures)))
	// Stable bounded jitter spreads recovery probes across nodes and never
	// extends the maximum interval. Tests can reproduce a given schedule.
	delay = time.Duration(int64(delay) * (800 + int64(h.Sum32()%401)) / 1000)
	return min(delay, 30*time.Second)
}
func (n *Node) nextProbe(now time.Time) {
	failures := max(n.Health.VirtualBox.Failures, n.Health.GuestControl.Failures, n.Health.Commands.Failures)
	reason := "periodic"
	if failures > 0 {
		reason = "backoff"
	}
	n.Polling.set(now, retryDelay(n.ID, failures, probeInterval), reason)
}
func (c *Command) failedPoll(now time.Time) {
	if c.Observation.Error != nil && c.Observation.Error.Code == "resource_busy" {
		c.Schedule.set(now, probeInterval, "capacity_wait")
		return
	}
	c.Schedule.set(now, retryDelay(c.ID, c.Observation.Failures, time.Second), "backoff")
}
func (c *Command) successfulPoll(now time.Time, changed bool) {
	if changed {
		c.IdlePolls = 0
	} else {
		c.IdlePolls = min(c.IdlePolls+1, 4)
	}
	reason := "active"
	if c.IdlePolls > 0 {
		reason = "idle"
	}
	c.Schedule.set(now, time.Duration(1+c.IdlePolls)*time.Second, reason)
}
func (n *Node) blocksCommands() bool {
	return n.State != "running" || n.Health.unavailable() || n.Health.GuestControl.State == "expected" || n.Health.Commands.State == "expected"
}
