package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWindowRetainsBoundedChronologicalEvidence(t *testing.T) {
	m := New()
	base := time.Now().UTC().Add(-10 * time.Minute)
	if m.Window(base, time.Now()).State != "no_data" {
		t.Fatal("fabricated samples")
	}
	for i := 0; i < SampleLimit+3; i++ {
		m.samples.add(Sample{Time: base.Add(time.Duration(i) * time.Second)})
	}
	for i := 0; i < CallLimit+7; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond)
		m.Record(Call{Started: at, Finished: at, Operation: "probe"})
	}
	w := m.Window(base, time.Now())
	if len(w.Samples) != SampleLimit || len(w.Calls) != CallLimit || !w.SamplesTruncated || !w.CallsTruncated || !w.Samples[0].Time.Equal(base.Add(3*time.Second)) || !w.Calls[0].Started.Equal(base.Add(7*time.Millisecond)) {
		t.Fatal(w)
	}
	narrow := m.Window(base.Add(100*time.Second), base.Add(103*time.Second))
	if len(narrow.Samples) != 4 || len(narrow.Calls) != 0 || narrow.SamplesTruncated || narrow.CallsTruncated {
		t.Fatal(narrow)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				m.Record(Call{Started: base, Finished: base})
				_ = m.Window(base, time.Now())
				_ = m.Latest()
			}
		}()
	}
	wg.Wait()
}

func TestCgroupNamespaceAncestorsAndMetricAvailability(t *testing.T) {
	root := t.TempDir()
	proc, cg := filepath.Join(root, "proc"), filepath.Join(root, "cgroup")
	write := func(name, data string) {
		t.Helper()
		if e := os.MkdirAll(filepath.Dir(name), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(name, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(filepath.Join(proc, "stat"), "cpu 100 2 3 400 5 6 7 8 9 10\n"+strings.Repeat("cpu0 irrelevant\n", 10000))
	write(filepath.Join(proc, "loadavg"), "0.0 1.0 2.0 3/42 123\n")
	psi := "some avg10=12.50 avg60=2.00 avg300=1.0 total=1000\nfull avg10=0.0 avg60=0.0 avg300=0.0 total=0\n"
	write(filepath.Join(proc, "pressure/cpu"), psi)
	write(filepath.Join(proc, "self/cgroup"), "0::/private/service\n")
	write(filepath.Join(proc, "self/mountinfo"), "1 2 0:1 /private "+cg+" rw - cgroup2 none rw\n")
	write(filepath.Join(cg, "service/cpu.stat"), "usage_usec 3000\nuser_usec 2000\nsystem_usec 1000\nnr_periods 30\nnr_throttled 2\nthrottled_usec 100\n")
	write(filepath.Join(cg, "service/cpu.max"), "50000 100000\n")
	write(filepath.Join(cg, "service/cpu.pressure"), psi)
	write(filepath.Join(cg, "cpu.stat"), "usage_usec 4000\n")
	write(filepath.Join(cg, "cpu.max"), "max 100000\n")
	s := collect(proc, "linux")
	if s.CPU.Counters["user_ticks"] != 100 || s.RunQueue.Counters["runnable"] != 3 || s.Pressure["cpu"].Gauges["some_avg10_percent"] != 12.5 || s.Pressure["memory"].State != "unavailable" {
		t.Fatal(s)
	}
	if _, exists := s.Pressure["cpu"].Counters["full_total_usec"]; exists {
		t.Fatal("system CPU full PSI is undefined")
	}
	if s.CgroupState != "observed" || len(s.Cgroups) != 2 || s.Cgroups[0].Quota.Gauges["cpu_limit"] != 0.5 || s.Cgroups[0].CPU.Counters["throttled_usec"] != 100 || s.Cgroups[1].Quota.Gauges["unlimited"] != 1 {
		t.Fatal(s)
	}
	if _, exists := s.Cgroups[1].CPU.Counters["nr_throttled"]; exists {
		t.Fatal("missing throttling became zero")
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), root) || s.ReadBytes > 8192 {
		t.Fatal("identity leaked or per-CPU table read", string(raw))
	}
	write(filepath.Join(proc, "self/cgroup"), "0::/other/service\n")
	if next := collect(proc, "linux"); next.CgroupState != "unavailable" {
		t.Fatal("read wrong cgroup", next)
	}
	if unsupported := collect(proc, "windows"); unsupported.CPU.State != "unsupported" {
		t.Fatal(unsupported)
	}
}

func TestMalformedAndMissingCountersAreNotZeroEvidence(t *testing.T) {
	for _, m := range []Metric{pressure("some total=bad", true), pressure("some avg10=NaN total=1", true), quota("max 0"), quota("bad 1000"), counters("nr_throttled -1", "nr_throttled"), counters("unrelated 0", "nr_throttled")} {
		if m.State != "unavailable" {
			t.Fatal(m)
		}
	}
	root := t.TempDir()
	if s := collect(root, "linux"); s.CPU.State != "unavailable" || s.CgroupState != "unavailable" {
		t.Fatal(s)
	}
}

func BenchmarkCollect(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		s := Collect()
		b.ReportMetric(float64(s.ReadBytes), "read-bytes/op")
	}
}
