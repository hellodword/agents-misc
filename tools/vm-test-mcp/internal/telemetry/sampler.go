package telemetry

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type reader struct{ bytes int }
type counted struct {
	io.Reader
	bytes *int
}

func (r counted) Read(p []byte) (int, error) { n, e := r.Reader.Read(p); *r.bytes += n; return n, e }
func (r *reader) read(name string, limit int) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit+1)))
	r.bytes += len(b)
	if len(b) > limit {
		return "", errors.New("sample bound exceeded")
	}
	return string(b), err
}
func unavailable(err error) Metric {
	reason := "read_failed"
	if os.IsNotExist(err) {
		reason = "not_exposed"
	}
	if os.IsPermission(err) {
		reason = "access_denied"
	}
	return Metric{State: "unavailable", Reason: reason}
}
func invalid() Metric { return Metric{State: "unavailable", Reason: "invalid_data"} }
func counters(data string, keys ...string) Metric {
	m := Metric{State: "observed", Counters: map[string]uint64{}}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		for _, key := range keys {
			if f[0] != key {
				continue
			}
			v, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil {
				return invalid()
			}
			m.Counters[key] = v
		}
	}
	if len(m.Counters) == 0 {
		return invalid()
	}
	return m
}
func pressure(data string, cpu bool) Metric {
	m := Metric{State: "observed", Counters: map[string]uint64{}, Gauges: map[string]float64{}}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || (f[0] != "some" && f[0] != "full") || (cpu && f[0] == "full") {
			continue
		}
		for _, field := range f[1:] {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				return invalid()
			}
			if key == "total" {
				v, err := strconv.ParseUint(value, 10, 64)
				if err != nil {
					return invalid()
				}
				m.Counters[f[0]+"_total_usec"] = v
			} else if key == "avg10" || key == "avg60" || key == "avg300" {
				v, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 100 {
					return invalid()
				}
				m.Gauges[f[0]+"_"+key+"_percent"] = v
			}
		}
	}
	if len(m.Counters) == 0 {
		return invalid()
	}
	return m
}
func quota(data string) Metric {
	f := strings.Fields(data)
	if len(f) != 2 {
		return invalid()
	}
	period, err := strconv.ParseUint(f[1], 10, 64)
	if err != nil || period == 0 {
		return invalid()
	}
	m := Metric{State: "observed", Counters: map[string]uint64{"period_usec": period}}
	if f[0] == "max" {
		m.Gauges = map[string]float64{"unlimited": 1}
		return m
	}
	q, err := strconv.ParseUint(f[0], 10, 64)
	if err != nil || q == 0 {
		return invalid()
	}
	m.Counters["quota_usec"] = q
	m.Gauges = map[string]float64{"cpu_limit": float64(q) / float64(period)}
	return m
}
func (r *reader) metric(name string, parse func(string) Metric) Metric {
	data, err := r.read(name, 4096)
	if err != nil {
		return unavailable(err)
	}
	return parse(data)
}

// Resolve the process's unified cgroup against its mount, including cgroup
// namespaces. Ancestors outside the visible mount are deliberately unobserved.
func cgroupLocation(membership, mounts string) (string, string, bool) {
	group := ""
	for _, line := range strings.Split(membership, "\n") {
		if strings.HasPrefix(line, "0::/") {
			group = strings.TrimPrefix(line, "0::")
			break
		}
	}
	if group == "" {
		return "", "", false
	}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\134`, `\`)
	for _, line := range strings.Split(mounts, "\n") {
		left, right, ok := strings.Cut(line, " - ")
		fields := strings.Fields(left)
		if !ok || !strings.HasPrefix(right, "cgroup2 ") || len(fields) < 5 {
			continue
		}
		root, mount := unescape.Replace(fields[3]), unescape.Replace(fields[4])
		rel, err := filepath.Rel(root, group)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		return filepath.Join(mount, rel), mount, true
	}
	return "", "", false
}

func Collect() Sample { return collect("/proc", runtime.GOOS) }
func collect(proc, platform string) Sample {
	start := time.Now()
	s := Sample{Time: start.UTC(), Pressure: map[string]Metric{}}
	if platform != "linux" {
		s.CPU = Metric{State: "unsupported", Reason: "linux_proc_required"}
		s.RunQueue = s.CPU
		s.Pressure["cpu"], s.Pressure["memory"], s.Pressure["io"] = s.CPU, s.CPU, s.CPU
		s.CgroupState, s.CgroupReason = "unsupported", "cgroup_v2_required"
		return s
	}
	r := &reader{}
	// Read only the aggregate CPU line; per-CPU and interrupt tables can be large.
	f, err := os.Open(filepath.Join(proc, "stat"))
	if err != nil {
		s.CPU = unavailable(err)
	} else {
		data, e := bufio.NewReader(counted{io.LimitReader(f, 1024), &r.bytes}).ReadString('\n')
		_ = f.Close()
		fields := strings.Fields(data)
		s.CPU = invalid()
		if e == nil && len(fields) >= 9 && fields[0] == "cpu" {
			values := ""
			for i, key := range []string{"user_ticks", "nice_ticks", "system_ticks", "idle_ticks", "iowait_ticks", "irq_ticks", "softirq_ticks", "steal_ticks"} {
				values += key + " " + fields[i+1] + "\n"
			}
			s.CPU = counters(values, "user_ticks", "nice_ticks", "system_ticks", "idle_ticks", "iowait_ticks", "irq_ticks", "softirq_ticks", "steal_ticks")
		}
	}
	s.RunQueue = r.metric(filepath.Join(proc, "loadavg"), func(data string) Metric {
		fields := strings.Fields(data)
		if len(fields) < 4 {
			return invalid()
		}
		running, total, ok := strings.Cut(fields[3], "/")
		if !ok {
			return invalid()
		}
		return counters("runnable "+running+"\nthreads "+total, "runnable", "threads")
	})
	for _, name := range []string{"cpu", "memory", "io"} {
		s.Pressure[name] = r.metric(filepath.Join(proc, "pressure", name), func(data string) Metric { return pressure(data, name == "cpu") })
	}
	membership, e1 := r.read(filepath.Join(proc, "self/cgroup"), 8192)
	mounts, e2 := r.read(filepath.Join(proc, "self/mountinfo"), 65536)
	s.CgroupState, s.CgroupReason = "unavailable", "cgroup_v2_not_visible"
	if e1 == nil && e2 == nil {
		if !strings.Contains(membership, "0::/") {
			s.CgroupState, s.CgroupReason = "unsupported", "cgroup_v2_required"
		}
		if dir, root, ok := cgroupLocation(membership, mounts); ok {
			s.CgroupState, s.CgroupReason = "observed", ""
			identity := sha256.Sum256([]byte(membership + "\n" + root))
			s.CgroupIdentity = hex.EncodeToString(identity[:])
			for depth := 0; depth < 16; depth++ {
				s.Cgroups = append(s.Cgroups, Cgroup{Depth: depth,
					CPU: r.metric(filepath.Join(dir, "cpu.stat"), func(data string) Metric {
						return counters(data, "usage_usec", "user_usec", "system_usec", "nr_periods", "nr_throttled", "throttled_usec")
					}),
					Quota:    r.metric(filepath.Join(dir, "cpu.max"), quota),
					Pressure: r.metric(filepath.Join(dir, "cpu.pressure"), func(data string) Metric { return pressure(data, false) }),
				})
				if dir == root {
					break
				}
				dir = filepath.Dir(dir)
				if depth == 15 {
					s.CgroupState, s.CgroupReason = "partial", "ancestor_limit"
				}
			}
		}
	}
	s.ReadBytes, s.CollectionUS = r.bytes, time.Since(start).Microseconds()
	return s
}
