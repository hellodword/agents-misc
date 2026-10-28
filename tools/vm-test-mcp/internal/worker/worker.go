// Package worker implements the UID-authenticated, ordinary Nix client boundary.
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/unix"
)

type Request struct {
	ManifestDigest string     `json:"manifest_digest,omitempty"`
	Cursor         int        `json:"cursor,omitempty"`
	TaskID         string     `json:"task_id"`
	Derivation     string     `json:"derivation,omitempty"`
	System         string     `json:"system,omitempty"`
	Cache          string     `json:"cache,omitempty"`
	TimeoutSeconds int        `json:"timeout_seconds,omitempty"`
	ArtifactID     string     `json:"artifact_id,omitempty"`
	Offset         int64      `json:"offset,omitempty"`
	Deadline       *time.Time `json:"deadline,omitempty"`
}
type File struct {
	SHA256   string `json:"sha256"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Root     string `json:"root"`
	Relative string `json:"relative"`
	Size     int64  `json:"size"`
}
type Job struct {
	CopiesReleased  bool              `json:"copies_released"`
	HandoffComplete bool              `json:"handoff_complete"`
	ManifestDigest  string            `json:"manifest_digest,omitempty"`
	Request         Request           `json:"request"`
	State           string            `json:"state"`
	Cleanup         string            `json:"cleanup"`
	Outputs         map[string]string `json:"outputs"`
	Files           []File            `json:"files"`
	// Retain the historical on-disk field; it has no observed-exit provenance.
	ExitCode  int                `json:"exit_code"`
	Execution *wire.NixExecution `json:"execution,omitempty"`
	Started   time.Time          `json:"started"`
	Deadline  time.Time          `json:"deadline"`
}
type Worker struct {
	lastRetention time.Time
	mu            sync.Mutex
	cfg           config.Worker
	root          *safefs.Root
	db            *state.DB
	jobs          map[string]*Job
	cancel        map[string]context.CancelFunc
	wg            sync.WaitGroup
	active        string
}

func New(c config.Worker) (*Worker, error) {
	root, e := safefs.Private(c.StateDir, true)
	if e != nil {
		return nil, e
	}
	db, e := state.Open(root.Path)
	if e != nil {
		root.Close()
		return nil, e
	}
	w := &Worker{cfg: c, root: root, db: db, jobs: map[string]*Job{}, cancel: map[string]context.CancelFunc{}}
	rows, e := db.Active(context.Background(), "job")
	if e != nil {
		w.Close()
		return nil, e
	}
	for _, b := range rows {
		var j Job
		if e = json.Unmarshal(b, &j); e != nil {
			w.Close()
			return nil, e
		}
		if j.State == "building" || j.State == "cleanup_required" {
			j.State = "cleanup_required"
			j.Cleanup = "required"
			w.active = j.Request.TaskID
		} else if j.State == "importing" {
			j.State = "interrupted"
			j.Cleanup = "complete"
		}
		if (j.State == "interrupted" || j.State == "cleanup_required") && j.Execution != nil && j.Execution.Error == nil {
			j.Execution.Error = wire.Safe(wire.Fail("worker_restarted", "Worker restarted before the execution result was durable"))
		}
		w.jobs[j.Request.TaskID] = &j
		if e = w.save(&j); e != nil {
			w.Close()
			return nil, e
		}
	}
	return w, nil
}

func (w *Worker) save(j *Job) error {
	return w.db.Save(context.Background(), state.Document{Kind: "job", ID: j.Request.TaskID, Value: j})
}
func (w *Worker) Close() {
	w.mu.Lock()
	for _, cancel := range w.cancel {
		cancel()
	}
	w.mu.Unlock()
	w.wg.Wait()
	w.db.Close()
	w.root.Close()
}
func (w *Worker) command(ctx context.Context, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, w.cfg.Nix, args...)
	c.Env = []string{"HOME=" + os.Getenv("HOME"), "USER=" + os.Getenv("USER"), "PATH=" + filepath.Dir(w.cfg.Nix), "NIX_CONFIG=experimental-features = nix-command flakes\n", "LC_ALL=C"}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.WaitDelay = 5 * time.Second
	return c
}
func (w *Worker) Preflight(ctx context.Context) error {
	if os.Getuid() == 0 {
		return fmt.Errorf("worker requires a non-root identity")
	}
	raw, e := w.command(ctx, "config", "show", "--json").Output()
	if e != nil {
		return fmt.Errorf("inspect Nix client configuration: %w", e)
	}
	var cfg map[string]struct {
		Value any `json:"value"`
	}
	if e = json.Unmarshal(raw, &cfg); e != nil {
		return fmt.Errorf("decode Nix client configuration: %w", e)
	}
	if cfg["sandbox"].Value != true && cfg["sandbox"].Value != "true" {
		return fmt.Errorf("client sandbox must be true")
	}
	if cfg["sandbox-fallback"].Value != false {
		return fmt.Errorf("client sandbox fallback must be disabled")
	}
	// Client configuration is not daemon policy. In particular, Nix defaults
	// build-users-group to empty for this ordinary user and to nixbld for root.
	// Check connectivity and client trust with the daemon used for builds;
	// actual sandbox/build-user/KVM policy remains an administrator acceptance check.
	raw, e = w.command(ctx, "store", "info", "--store", "daemon", "--json").Output()
	if e != nil {
		return fmt.Errorf("inspect Nix daemon (nix store info --store daemon --json): %w", e)
	}
	var daemon struct {
		Trusted *bool `json:"trusted"`
	}
	if e = json.Unmarshal(raw, &daemon); e != nil {
		return fmt.Errorf("decode Nix daemon information: %w", e)
	}
	if daemon.Trusted == nil {
		return fmt.Errorf("Nix daemon did not report client trust")
	}
	if *daemon.Trusted {
		return fmt.Errorf("worker must not be a trusted Nix daemon client")
	}
	return nil
}
func (w *Worker) Call(method string, a Request) (any, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.expireCompleted(time.Now().UTC()); err != nil {
		return nil, err
	}
	allowed := Request{TaskID: a.TaskID}
	switch method {
	case "describe":
		allowed = Request{}
	case "start":
		allowed.Derivation, allowed.System, allowed.Cache, allowed.TimeoutSeconds = a.Derivation, a.System, a.Cache, a.TimeoutSeconds
	case "extend":
		allowed.Deadline = a.Deadline
	case "log":
		allowed.Offset = a.Offset
	case "file":
		allowed.Offset, allowed.ArtifactID = a.Offset, a.ArtifactID
	case "handoff":
		allowed.ManifestDigest = a.ManifestDigest
	case "files":
		allowed.Cursor = a.Cursor
	case "status", "cleanup", "cancel", "purge", "forget":
	default:
		return nil, wire.Fail("invalid_argument", "Unknown worker operation")
	}
	if string(wire.JSON(a)) != string(wire.JSON(allowed)) {
		return nil, wire.Fail("invalid_argument", "Unexpected worker operation parameter")
	}
	if method == "describe" {
		return map[string]any{"version": wire.Version, "build": wire.Identity(), "busy": w.active != "", "active_task_id": w.active}, nil
	}
	if !wire.IDPattern.MatchString(a.TaskID) {
		return nil, wire.Fail("invalid_argument", "Invalid task ID")
	}
	if method == "start" {
		if w.jobs[a.TaskID] == nil {
			var old Job
			if w.db.Load(context.Background(), "job", a.TaskID, &old) == nil {
				w.jobs[a.TaskID] = &old
			}
		}
		if old := w.jobs[a.TaskID]; old != nil {
			if string(wire.JSON(old.Request)) != string(wire.JSON(a)) {
				return nil, wire.Fail("request_conflict", "Worker task parameters differ")
			}
			return public(old), nil
		}
		if w.active != "" {
			return nil, wire.Fail("resource_busy", "Worker requires cleanup or is executing")
		}
		system := runtime.GOARCH + "-linux"
		if runtime.GOARCH == "amd64" {
			system = "x86_64-linux"
		} else if runtime.GOARCH == "arm64" {
			system = "aarch64-linux"
		}
		if a.System != system || !wire.StorePattern.MatchString(a.Derivation) || !strings.HasSuffix(a.Derivation, ".drv") || a.TimeoutSeconds < 1 || a.TimeoutSeconds > 86400 {
			return nil, wire.Fail("invalid_argument", "Invalid Nix request")
		}
		u, e := url.Parse(a.Cache)
		if e != nil || u.Scheme != "" || u.Host != "" || !strings.HasPrefix(u.Path, "/files/") || strings.Contains(u.Path, "..") || strings.Count(u.Path, "/") != 2 || u.RawQuery != "" || u.Fragment != "" {
			return nil, wire.Fail("invalid_argument", "Expected controller-issued cache path")
		}
		now := time.Now().UTC()
		j := &Job{Request: a, State: "importing", Cleanup: "pending", Execution: &wire.NixExecution{Stage: "import"}, Outputs: map[string]string{}, Files: []File{}, Started: now, Deadline: now.Add(time.Duration(a.TimeoutSeconds) * time.Second)}
		if e = w.root.MkdirAll(a.TaskID, 0700); e != nil {
			return nil, e
		}
		if e = w.save(j); e != nil {
			return nil, e
		}
		w.jobs[a.TaskID] = j
		w.active = a.TaskID
		ctx, cancel := context.WithCancel(context.Background())
		w.cancel[a.TaskID] = cancel
		w.wg.Add(1)
		go w.run(ctx, j)
		return public(j), nil
	}
	j := w.jobs[a.TaskID]
	if j == nil {
		var stored Job
		if w.db.Load(context.Background(), "job", a.TaskID, &stored) == nil {
			j = &stored
			w.jobs[a.TaskID] = j
		}
	}
	if j == nil {
		if method == "forget" {
			return map[string]any{"deleted": true}, nil
		}
		if method == "cleanup" {
			return map[string]any{"cleanup": "complete", "never_accepted": true}, nil
		}
		return nil, wire.Fail("not_found", "Worker task not found")
	}
	switch method {
	case "forget":
		if !j.HandoffComplete {
			return nil, wire.Fail("protected", "Evidence handoff is not confirmed")
		}
		if _, err := w.db.ExecContext(context.Background(), "DELETE FROM worker_jobs WHERE id=?", a.TaskID); err != nil {
			return nil, err
		}
		delete(w.jobs, a.TaskID)
		return map[string]any{"deleted": true}, w.db.Compact(context.Background())
	case "files":
		if a.Cursor < 0 || a.Cursor > len(j.Files) {
			return nil, wire.Fail("invalid_argument", "Invalid file cursor")
		}
		end := min(a.Cursor+100, len(j.Files))
		files := []any{}
		for _, f := range j.Files[a.Cursor:end] {
			files = append(files, map[string]any{"id": f.ID, "name": f.Name, "size": f.Size, "sha256": f.SHA256})
		}
		return map[string]any{"items": files, "next_cursor": end, "eof": end == len(j.Files)}, nil
	case "handoff":
		return w.handoff(j, a.ManifestDigest)
	case "extend":
		if (j.State != "importing" && j.State != "building") || a.Deadline == nil || a.Deadline.Before(j.Deadline) || a.Deadline.After(j.Started.Add(24*time.Hour)) {
			return nil, wire.Fail("invalid_state", "Worker deadline cannot be extended")
		}
		j.Deadline = a.Deadline.UTC()
		return public(j), w.save(j)
	case "status":
		return public(j), nil
	case "cleanup", "cancel":
		if cancel := w.cancel[a.TaskID]; cancel != nil {
			cancel()
		}
		return public(j), nil
	case "log":
		return w.readFile(filepath.Join(w.root.Path, a.TaskID), "task.log", a.Offset)
	case "file":
		for _, f := range j.Files {
			if f.ID == a.ArtifactID {
				return w.readFile(f.Root, f.Relative, a.Offset)
			}
		}
		return nil, wire.Fail("not_found", "Artifact does not belong to task")
	case "purge":
		if j.Cleanup != "complete" {
			return nil, wire.Fail("cleanup_required", "Worker cleanup is unresolved")
		}
		if e := w.root.RemoveAll(a.TaskID); e != nil {
			return nil, e
		}
		j.Files = nil
		j.Outputs = nil
		return public(j), w.save(j)
	default:
		return nil, wire.Fail("invalid_argument", "Unknown worker operation")
	}
}
func public(j *Job) map[string]any {
	v := map[string]any{"state": j.State, "cleanup": j.Cleanup, "artifact_count": len(j.Files), "handoff_complete": j.HandoffComplete}
	if j.Execution != nil {
		// Copy under the worker mutex; JSON encoding happens after Call returns.
		v["execution"] = *j.Execution
	}
	return v
}
func (w *Worker) readFile(root, relative string, offset int64) (any, error) {
	if offset < 0 {
		return nil, wire.Fail("invalid_argument", "Invalid offset")
	}
	r, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	safe := &safefs.Root{Root: r}
	f, e := safe.Regular(relative)
	if os.IsNotExist(e) {
		return map[string]any{"data": "", "next_offset": offset, "eof": true}, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	data := make([]byte, 65536)
	n, e := f.ReadAt(data, offset)
	if e != nil && e != io.EOF {
		return nil, e
	}
	return map[string]any{"data": base64.StdEncoding.EncodeToString(data[:n]), "next_offset": offset + int64(n), "eof": e == io.EOF}, nil
}

type capped struct {
	w   io.Writer
	n   int64
	max int64
}

func (c *capped) Write(b []byte) (int, error) {
	n := len(b)
	if c.n < c.max {
		p := b[:min(int64(n), c.max-c.n)]
		written, e := c.w.Write(p)
		c.n += int64(written)
		if e != nil {
			return written, e
		}
	}
	return n, nil
}

// Nix prints the source URL during import. Keep that scoped credential out of
// retained evidence, including when os/exec splits it across output writes.
type importLog struct {
	w               io.Writer
	secret, pending string
}

func (l *importLog) Write(p []byte) (int, error) {
	text := strings.ReplaceAll(l.pending+string(p), l.secret, "[redacted cache]")
	keep := min(len(text), len(l.secret)-1)
	for keep > 0 && !strings.HasSuffix(text, l.secret[:keep]) {
		keep--
	}
	end := len(text) - keep
	if _, err := io.WriteString(l.w, text[:end]); err != nil {
		return 0, err
	}
	l.pending = text[end:]
	return len(p), nil
}

func (l *importLog) Flush() error {
	if l.pending == "" {
		return nil
	}
	l.pending = ""
	_, err := io.WriteString(l.w, "[redacted cache]")
	return err
}

func (w *Worker) run(ctx context.Context, j *Job) {
	defer w.wg.Done()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				w.mu.Lock()
				if !time.Now().Before(j.Deadline) {
					if cancel := w.cancel[j.Request.TaskID]; cancel != nil {
						cancel()
					}
				}
				w.mu.Unlock()
			}
		}
	}()
	defer func() {
		w.mu.Lock()
		if cancel := w.cancel[j.Request.TaskID]; cancel != nil {
			cancel()
		}
		w.mu.Unlock()
		<-watchDone
	}()
	task := j.Request.TaskID
	log, e := w.root.OpenFile(path.Join(task, "task.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		w.complete(j, e, false)
		return
	}
	defer log.Close()
	logs := &capped{w: log, max: 64 << 20}
	cacheURL := strings.TrimRight(w.cfg.CacheURL, "/") + j.Request.Cache
	importLogs := &importLog{w: logs, secret: cacheURL}
	copy := w.command(ctx, "copy", "--from", cacheURL, "--to", "daemon", "--derivation", j.Request.Derivation)
	copy.Stdout = importLogs
	copy.Stderr = importLogs
	e = copy.Run()
	if err := importLogs.Flush(); e == nil {
		e = err
	}
	w.processResult(j, "import", copy)
	if e != nil {
		w.complete(j, e, false)
		return
	}
	if e = w.stage(j, "validate"); e == nil {
		e = w.validateDerivation(ctx, j)
	}
	if e != nil {
		fmt.Fprintln(log, "Derivation policy rejected")
		w.complete(j, e, false)
		return
	}
	w.mu.Lock()
	j.State = "building"
	j.Execution.Stage, j.Execution.Process = "build", nil
	e = w.save(j)
	w.mu.Unlock()
	if e != nil {
		w.complete(j, e, false)
		return
	}
	out := &strings.Builder{}
	build := w.command(ctx, "build", "--store", "daemon", "--builders", "", "--option", "sandbox", "true", "--option", "sandbox-fallback", "false", "--out-link", filepath.Join(w.root.Path, task, "result"), "--json", "--timeout", strconv.Itoa(86400), "-L", j.Request.Derivation+"^*")
	if w.cfg.KeepFailed {
		build.Args = append(build.Args, "--keep-failed")
	}
	build.Stdout = &capped{w: out, max: 4 << 20}
	build.Stderr = logs
	e = build.Run()
	w.processResult(j, "build", build)
	interrupted := ctx.Err() != nil || (build.ProcessState != nil && build.ProcessState.ExitCode() < 0)
	result := &Job{Outputs: map[string]string{}, Files: []File{}}
	if e == nil {
		w.mu.Lock()
		j.Execution.Stage = "collect"
		w.mu.Unlock()
		var results []struct {
			Outputs map[string]string `json:"outputs"`
		}
		e = json.Unmarshal([]byte(out.String()), &results)
		if e == nil {
			for i, r := range results {
				for name, p := range r.Outputs {
					if !wire.StorePattern.MatchString(p) {
						e = fmt.Errorf("invalid output root")
						break
					}
					result.Outputs[fmt.Sprintf("%d-%s", i, name)] = p
				}
			}
		}
		if e == nil {
			e = w.indexFiles(result)
		}
	}
	w.mu.Lock()
	j.Outputs, j.Files = result.Outputs, result.Files
	if build.ProcessState != nil {
		j.ExitCode = build.ProcessState.ExitCode()
	}
	w.mu.Unlock()
	if err := log.Sync(); e == nil {
		e = err
	}
	w.complete(j, e, interrupted)
}
func (w *Worker) validateDerivation(ctx context.Context, j *Job) error {
	c := w.command(ctx, "derivation", "show", j.Request.Derivation)
	var output strings.Builder
	c.Stdout = &capped{w: &output, max: (4 << 20) + 1}
	e := c.Run()
	w.processResult(j, "validate", c)
	if e != nil {
		return e
	}
	if output.Len() > 4<<20 {
		return wire.Fail("limit_exceeded", "Derivation metadata exceeds 4 MiB")
	}
	return derivationPolicy([]byte(output.String()), j.Request)
}

func (w *Worker) stage(j *Job, stage string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	j.Execution.Stage, j.Execution.Process = stage, nil
	if stage == "validate" {
		j.Execution.Imported = true
	}
	return w.save(j)
}

func (w *Worker) processResult(j *Job, stage string, c *exec.Cmd) {
	v := &wire.NixProcessResult{Stage: stage, Source: "unconfirmed"}
	if c.ProcessState != nil {
		if status, ok := c.ProcessState.Sys().(syscall.WaitStatus); ok {
			v.Source = "nix_client_wait"
			if status.Exited() {
				v.Exited = true
				v.ExitCode = new(status.ExitStatus())
			}
			if status.Signaled() {
				v.Exited = true
				v.ExitSignal = unix.SignalName(status.Signal())
				if v.ExitSignal == "" {
					v.ExitSignal = fmt.Sprintf("SIG%d", status.Signal())
				}
				v.CoreDumped = status.CoreDump()
			}
		}
	}
	w.mu.Lock()
	j.Execution.Process = v
	w.mu.Unlock()
}
func derivationPolicy(b []byte, a Request) error {
	type metadata struct {
		System string            `json:"system"`
		Env    map[string]string `json:"env"`
	}
	var envelope struct {
		Version     int                 `json:"version"`
		Derivations map[string]metadata `json:"derivations"`
	}
	if e := json.Unmarshal(b, &envelope); e != nil {
		return e
	}
	var all map[string]metadata
	key := a.Derivation
	if envelope.Version != 0 {
		if envelope.Version != 4 {
			return wire.Fail("policy_denied", "Unsupported derivation JSON version")
		}
		all = envelope.Derivations
		key = filepath.Base(key)
	} else if e := json.Unmarshal(b, &all); e != nil {
		return e
	}
	v, ok := all[key]
	features := strings.Fields(v.Env["requiredSystemFeatures"])
	noChroot := v.Env["__noChroot"] != "" && v.Env["__noChroot"] != "0"
	if raw := v.Env["__json"]; raw != "" {
		var attrs struct {
			NoChroot bool     `json:"__noChroot"`
			Features []string `json:"requiredSystemFeatures"`
		}
		if e := json.Unmarshal([]byte(raw), &attrs); e != nil {
			return e
		}
		noChroot = noChroot || attrs.NoChroot
		features = append(features, attrs.Features...)
	}
	if !ok || v.System != a.System || noChroot || !slices.Contains(features, "kvm") {
		return wire.Fail("policy_denied", "Expected a sandboxed KVM test derivation")
	}
	return nil
}
func (w *Worker) indexFiles(j *Job) error {
	for name, root := range j.Outputs {
		fi, e := os.Lstat(root)
		if e != nil {
			return e
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("output root is a symlink")
		}
		if fi.Mode().IsRegular() {
			j.Files = append(j.Files, File{ID: wire.ID(), Name: name, Root: filepath.Dir(root), Relative: filepath.Base(root), Size: fi.Size()})
			continue
		}
		if !fi.IsDir() {
			continue
		}
		e = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if len(j.Files) > 10000 {
				return fmt.Errorf("artifact count exceeds limit")
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			fi, e := d.Info()
			if e != nil {
				return e
			}
			if !fi.Mode().IsRegular() {
				return nil
			}
			rel, e := filepath.Rel(root, p)
			if e != nil {
				return e
			}
			j.Files = append(j.Files, File{ID: wire.ID(), Name: name + "/" + filepath.ToSlash(rel), Root: root, Relative: filepath.ToSlash(rel), Size: fi.Size()})
			return nil
		})
		if e != nil {
			return e
		}
	}
	for i := range j.Files {
		f := &j.Files[i]
		root, err := os.OpenRoot(f.Root)
		if err != nil {
			return err
		}
		file, err := root.Open(f.Relative)
		if err != nil {
			root.Close()
			return err
		}
		hash := sha256.New()
		n, err := io.Copy(hash, file)
		file.Close()
		root.Close()
		if err != nil {
			return err
		}
		if n != f.Size {
			return fmt.Errorf("output changed during indexing")
		}
		f.SHA256 = hex.EncodeToString(hash.Sum(nil))
	}
	return nil
}
func (w *Worker) complete(j *Job, e error, uncertain bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if file, err := w.root.OpenFile(path.Join(j.Request.TaskID, "task.log"), os.O_RDWR, 0600); err == nil {
		if syncErr := file.Sync(); syncErr != nil && e == nil {
			e = syncErr
		}
		info, statErr := file.Stat()
		file.Close()
		if statErr == nil {
			logJob := &Job{Files: []File{{ID: wire.ID(), Name: "task.log", Root: filepath.Join(w.root.Path, j.Request.TaskID), Relative: "task.log", Size: info.Size()}}}
			if err = w.indexFiles(logJob); err == nil {
				j.Files = append(j.Files, logJob.Files...)
			} else if e == nil {
				e = err
			}
		}
	}
	j.State = "succeeded"
	j.Cleanup = "complete"
	if j.Execution == nil {
		j.Execution = &wire.NixExecution{Stage: "unknown"}
	}
	now := time.Now().UTC()
	j.Execution.Finished = &now
	if e != nil {
		j.State = "failed"
		j.Execution.Error = wire.Safe(e)
	}
	if uncertain {
		j.State = "cleanup_required"
		j.Cleanup = "required"
	}
	j.Execution.ResourcesReleased = !uncertain
	if !uncertain {
		j.Execution.ResourceConfirmation = "nix_client_returned"
	}
	if cancel := w.cancel[j.Request.TaskID]; cancel != nil {
		cancel()
		delete(w.cancel, j.Request.TaskID)
	}
	if err := w.save(j); err != nil {
		// Keep the scheduler occupied when the terminal record is not durable.
		// A previous building record also requires reconciliation after restart.
		j.State, j.Cleanup = "cleanup_required", "required"
		j.Execution.PersistenceError = wire.Safe(wire.Fail("terminal_write", "Nix execution result could not be persisted"))
		w.active = j.Request.TaskID
	} else if !uncertain {
		w.active = ""
	}
}
func (w *Worker) Reconcile(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	j := w.jobs[id]
	if j == nil {
		return wire.Fail("not_found", "Task not found")
	}
	if j.State != "cleanup_required" {
		return wire.Fail("invalid_state", "Only unresolved daemon cleanup can be reconciled")
	}
	next := *j
	next.State, next.Cleanup = "interrupted", "complete"
	if next.Execution != nil {
		v := *next.Execution
		v.ResourcesReleased = true
		v.ResourceConfirmation = "administrator_verified"
		next.Execution = &v
	}
	if err := w.save(&next); err != nil {
		return err
	}
	*j = next
	w.active = ""
	return nil
}

type peerKey struct{}

func (w *Worker) HTTPServer() *http.Server {
	return &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192, ConnContext: func(ctx context.Context, c net.Conn) context.Context {
		uid := -1
		if unixConn, ok := c.(*net.UnixConn); ok {
			raw, e := unixConn.SyscallConn()
			if e == nil {
				_ = raw.Control(func(fd uintptr) {
					cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
					if e == nil {
						uid = int(cred.Uid)
					}
				})
			}
		}
		return context.WithValue(ctx, peerKey{}, uid)
	}, Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if uid, _ := r.Context().Value(peerKey{}).(int); uid != w.cfg.ControllerUID {
			rw.WriteHeader(403)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/call" {
			rw.WriteHeader(404)
			return
		}
		var envelope struct {
			Method string  `json:"method"`
			Params Request `json:"params"`
		}
		data, e := io.ReadAll(io.LimitReader(r.Body, wire.MaxJSON+1))
		if e == nil {
			e = wire.Decode(data, &envelope)
		}
		var result any
		if e == nil {
			result, e = w.Call(envelope.Method, envelope.Params)
		}
		rw.Header().Set("Content-Type", "application/json")
		if e != nil {
			rw.WriteHeader(400)
			result = map[string]any{"error": wire.Safe(e)}
		}
		_ = json.NewEncoder(rw).Encode(result)
	})}
}
func (w *Worker) Listen() (net.Listener, error) {
	if fi, e := os.Lstat(w.cfg.Socket); e == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("socket path is not a socket")
		}
		c, e := net.DialTimeout("unix", w.cfg.Socket, time.Second)
		if e == nil {
			c.Close()
			return nil, fmt.Errorf("worker socket already active")
		}
		if e = os.Remove(w.cfg.Socket); e != nil {
			return nil, e
		}
	}
	l, e := net.Listen("unix", w.cfg.Socket)
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(w.cfg.Socket, 0660); e != nil {
		l.Close()
		return nil, e
	}
	return l, nil
}
