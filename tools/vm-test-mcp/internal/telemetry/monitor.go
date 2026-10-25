// Package telemetry keeps bounded, passive controller and host observations.
// It never records command arguments, environment, paths, or process contents.
package telemetry

import (
	"context"
	"sync"
	"time"
)

const Interval = 2 * time.Second
const SampleLimit = 120
const CallLimit = 512

type Metric struct {
	State    string             `json:"state"`
	Reason   string             `json:"reason,omitempty"`
	Counters map[string]uint64  `json:"counters,omitempty"`
	Gauges   map[string]float64 `json:"gauges,omitempty"`
}

type Cgroup struct {
	Depth    int    `json:"depth"`
	CPU      Metric `json:"cpu"`
	Quota    Metric `json:"quota"`
	Pressure Metric `json:"pressure"`
}

type Sample struct {
	Time           time.Time         `json:"time"`
	TickDelayUS    int64             `json:"tick_delay_us"`
	CollectionUS   int64             `json:"collection_us"`
	ReadBytes      int               `json:"read_bytes"`
	CPU            Metric            `json:"cpu"`
	RunQueue       Metric            `json:"run_queue"`
	Pressure       map[string]Metric `json:"pressure"`
	CgroupState    string            `json:"cgroup_state"`
	CgroupReason   string            `json:"cgroup_reason,omitempty"`
	CgroupIdentity string            `json:"cgroup_identity,omitempty"`
	Cgroups        []Cgroup          `json:"cgroups,omitempty"`
}

type Call struct {
	Started     time.Time `json:"started"`
	Finished    time.Time `json:"finished"`
	Scope       string    `json:"scope"`
	Operation   string    `json:"operation"`
	TaskID      string    `json:"task_id,omitempty"`
	NodeID      string    `json:"node_id,omitempty"`
	WaitUS      int64     `json:"wait_us"`
	ExecutionUS int64     `json:"execution_us"`
	UserUS      *int64    `json:"user_us,omitempty"`
	SystemUS    *int64    `json:"system_us,omitempty"`
	ReadBytes   int64     `json:"read_bytes"`
	WriteBytes  int64     `json:"write_bytes"`
	Failed      bool      `json:"failed"`
}

type Window struct {
	From             time.Time `json:"from"`
	Until            time.Time `json:"until"`
	State            string    `json:"state"`
	Samples          []Sample  `json:"samples"`
	Calls            []Call    `json:"calls"`
	SamplesTruncated bool      `json:"samples_truncated"`
	CallsTruncated   bool      `json:"calls_truncated"`
}

type ring[T any] struct {
	data  []T
	next  int
	total uint64
	limit int
}

func (r *ring[T]) add(v T) {
	if len(r.data) < r.limit {
		r.data = append(r.data, v)
	} else {
		r.data[r.next] = v
	}
	r.next = (r.next + 1) % r.limit
	r.total++
}
func (r *ring[T]) each(f func(T)) {
	start := 0
	if len(r.data) == r.limit {
		start = r.next
	}
	for i := range len(r.data) {
		f(r.data[(start+i)%len(r.data)])
	}
}

type Monitor struct {
	mu      sync.Mutex
	samples ring[Sample]
	calls   ring[Call]
}

func New() *Monitor {
	return &Monitor{samples: ring[Sample]{limit: SampleLimit}, calls: ring[Call]{limit: CallLimit}}
}

func (m *Monitor) Run(ctx context.Context) {
	m.collect(time.Now())
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case due := <-tick.C:
			m.collect(due)
		}
	}
}
func (m *Monitor) collect(due time.Time) {
	s := Collect()
	s.TickDelayUS = max(0, s.Time.Sub(due).Microseconds())
	m.mu.Lock()
	m.samples.add(s)
	m.mu.Unlock()
}
func (m *Monitor) Record(c Call) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.calls.add(c)
	m.mu.Unlock()
}
func (m *Monitor) Window(from, until time.Time) Window {
	w := Window{From: from, Until: until, State: "no_data", Samples: []Sample{}, Calls: []Call{}}
	if m == nil {
		return w
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	first := true
	m.samples.each(func(s Sample) {
		if first {
			w.SamplesTruncated = m.samples.total > uint64(m.samples.limit) && s.Time.After(from)
			first = false
		}
		if !s.Time.Before(from) && !s.Time.After(until) {
			w.Samples = append(w.Samples, s)
		}
	})
	first = true
	m.calls.each(func(c Call) {
		if first {
			w.CallsTruncated = m.calls.total > uint64(m.calls.limit) && c.Finished.After(from)
			first = false
		}
		if !c.Finished.Before(from) && !c.Started.After(until) {
			w.Calls = append(w.Calls, c)
		}
	})
	if len(w.Samples) > 0 {
		w.State = "observed"
	}
	return w
}
func (m *Monitor) Latest() any {
	if m == nil {
		return map[string]any{"state": "no_data"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *Sample
	m.samples.each(func(s Sample) { latest = &s })
	state := "no_data"
	if latest != nil {
		state = "observed"
	}
	return map[string]any{"state": state, "interval_ms": Interval.Milliseconds(), "sample_limit": SampleLimit, "call_limit": CallLimit, "latest": latest}
}
