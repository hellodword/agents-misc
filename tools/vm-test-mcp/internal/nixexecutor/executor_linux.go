package nixexecutor

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

type runtimeSession struct {
	killAttempted bool
	session       *Session
	group         *group
	listener      *net.UnixListener
	children      map[*exec.Cmd]bool
	monitoring    bool
}

type Executor struct {
	mu                       sync.Mutex
	cfg                      config.Executor
	root                     *safefs.Root
	db                       *state.DB
	groups, boot, invocation string
	sessions                 map[string]*runtimeSession
	closing                  bool
	ctx                      context.Context
	cancel                   context.CancelFunc
	wg                       sync.WaitGroup
}

func New(cfg config.Executor) (*Executor, error) {
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("executor requires root; use just nix-executor")
	}
	for _, dir := range []string{cfg.StateDir, filepath.Dir(cfg.Socket)} {
		if err := executorDirectory(dir); err != nil {
			return nil, err
		}
	}
	groups, err := delegatedRoot()
	if err != nil {
		return nil, err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, err
	}
	invocation := os.Getenv("INVOCATION_ID")
	if !wire.IDPattern.MatchString(invocation) {
		return nil, fmt.Errorf("executor requires a systemd service invocation identity")
	}
	r, err := safefs.Private(cfg.StateDir, true)
	if err != nil {
		return nil, err
	}
	db, err := state.OpenExecutor(r.Path)
	if err != nil {
		r.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &Executor{cfg: cfg, root: r, db: db, groups: groups, boot: strings.TrimSpace(string(boot)), invocation: invocation, sessions: map[string]*runtimeSession{}, ctx: ctx, cancel: cancel}
	e.mu.Lock()
	err = e.recover()
	e.mu.Unlock()
	if err != nil {
		e.Close()
		return nil, err
	}
	e.wg.Add(1)
	go e.deadlines()
	return e, nil
}

// Check the executor host before creating state or sockets. Every existing
// ancestor must keep those resources under root control.
func executorDirectory(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			owner, ok := st.Sys().(*syscall.Stat_t)
			if !ok || !st.IsDir() || owner.Uid != 0 || st.Mode().Perm()&0022 != 0 {
				return fmt.Errorf("executor directory %s must be root-owned and not writable by other users", p)
			}
		}
		if p == "/" {
			return nil
		}
	}
}

func (e *Executor) save(s *Session) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := e.db.ExecContext(ctx, `INSERT INTO executor_sessions(id,worker_instance,task_id,state,body) VALUES(?,?,?,?,?)
 ON CONFLICT(id) DO UPDATE SET state=excluded.state,body=excluded.body`, s.ID, s.WorkerInstance, s.TaskID, s.Resources.State, wire.JSON(s))
	return err
}

func (e *Executor) recover() error {
	rows, err := e.db.Query("SELECT body FROM executor_sessions WHERE state<>'released'")
	if err != nil {
		return err
	}
	var sessions []*Session
	for rows.Next() {
		var data []byte
		var s Session
		if err = rows.Scan(&data); err == nil {
			err = json.Unmarshal(data, &s)
		}
		if err != nil {
			break
		}
		sessions = append(sessions, &s)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, s := range sessions {
		r := &runtimeSession{session: s, children: map[*exec.Cmd]bool{}}
		e.sessions[s.ID] = r
		if s.ExecutorInstance != e.db.InstanceID || s.Group != filepath.Join(e.groups, "job-"+s.ID) {
			e.block(r, "executor_identity", "Stored session does not match this executor's resource boundary", "administrator")
			continue
		}
		if s.BootID != e.boot {
			if err := e.released(r, "host_restarted"); err != nil {
				return err
			}
			continue
		}
		g, openErr := openGroup(s.Group)
		if os.IsNotExist(openErr) && s.Invocation != e.invocation {
			if err := e.released(r, "previous_invocation_cgroup_removed"); err != nil {
				return err
			}
			continue
		}
		if openErr != nil || s.GroupInode == 0 || g.inode != s.GroupInode {
			if g != nil {
				g.close()
			}
			e.block(r, "executor_identity", "Session cgroup identity cannot be confirmed", "administrator")
			continue
		}
		r.group = g
		if err := e.stopLocked(r, "executor_restarted"); err != nil {
			return err
		}
	}
	return nil
}

func (e *Executor) lookup(a Request) (*runtimeSession, error) {
	if a.SessionID != "" {
		if r := e.sessions[a.SessionID]; r != nil {
			if r.session.WorkerInstance != a.WorkerInstance || r.session.TaskID != a.TaskID {
				return nil, wire.Fail("ownership_mismatch", "Session belongs to a different worker job")
			}
			return r, nil
		}
	}
	var data []byte
	err := e.db.QueryRow("SELECT body FROM executor_sessions WHERE worker_instance=? AND task_id=?", a.WorkerInstance, a.TaskID).Scan(&data)
	if err != nil {
		return nil, err
	}
	var s Session
	if err = json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if a.SessionID != "" && a.SessionID != s.ID {
		return nil, wire.Fail("ownership_mismatch", "Session identity differs")
	}
	if r := e.sessions[s.ID]; r != nil {
		return r, nil
	}
	r := &runtimeSession{session: &s, children: map[*exec.Cmd]bool{}}
	e.sessions[s.ID] = r
	return r, nil
}

func (e *Executor) Call(method string, a Request) (any, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if method == "describe" {
		v := Description{Build: wire.Identity(), InstanceID: e.db.InstanceID, Ready: !e.closing, Active: []Session{}, StopGrace: e.cfg.StopGrace.Seconds(), ReleaseTimeout: e.cfg.ReleaseTimeout.Seconds()}
		for _, r := range e.sessions {
			if !r.session.Resources.Released() {
				v.Active = append(v.Active, cloneSession(r.session))
			}
		}
		return v, nil
	}
	if !wire.IDPattern.MatchString(a.WorkerInstance) || !wire.IDPattern.MatchString(a.TaskID) || a.SessionID != "" && !wire.IDPattern.MatchString(a.SessionID) {
		return nil, wire.Fail("invalid_argument", "Worker, task and session identities are required")
	}
	r, err := e.lookup(a)
	if method == "stop" && errors.Is(err, sql.ErrNoRows) {
		// A stop receipt also seals a task whose admission response was lost.
		// A delayed create with the same durable identity returns this receipt.
		now, id := time.Now().UTC(), wire.ID()
		s := &Session{ID: id, ExecutorInstance: e.db.InstanceID, WorkerInstance: a.WorkerInstance, TaskID: a.TaskID, BootID: e.boot, Invocation: e.invocation, Created: now, Deadline: now, Stopping: &now, Resources: wire.NixResources{State: "released", Confirmation: "never_admitted", Reason: a.Reason, Observed: &now, SessionID: id, ExecutorInstance: e.db.InstanceID}}
		if err := e.save(s); err != nil {
			return nil, err
		}
		e.sessions[id] = &runtimeSession{session: s, children: map[*exec.Cmd]bool{}}
		return cloneSession(s), nil
	}
	if method == "create" && errors.Is(err, sql.ErrNoRows) {
		if e.closing {
			return nil, wire.Fail("executor_stopping", "Executor is closing its sessions")
		}
		if a.Deadline == nil || !a.Deadline.After(time.Now()) || a.Deadline.After(time.Now().Add(24*time.Hour)) {
			return nil, wire.Fail("invalid_argument", "Session deadline must be within 24 hours")
		}
		for _, old := range e.sessions {
			if !old.session.Resources.Released() {
				return nil, wire.Fail("resource_busy", "Executor is still responsible for an existing session")
			}
		}
		id, now := wire.ID(), time.Now().UTC()
		s := &Session{ID: id, ExecutorInstance: e.db.InstanceID, WorkerInstance: a.WorkerInstance, TaskID: a.TaskID, BootID: e.boot, Invocation: e.invocation, Group: filepath.Join(e.groups, "job-"+id), Created: now, Deadline: a.Deadline.UTC(), Resources: wire.NixResources{State: "owned", SessionID: id, ExecutorInstance: e.db.InstanceID, Observed: &now}}
		s.Store = "unix://" + filepath.Join(filepath.Dir(e.cfg.Socket), id+".sock")
		if err = e.save(s); err != nil {
			return nil, err
		}
		r = &runtimeSession{session: s, children: map[*exec.Cmd]bool{}}
		e.sessions[id] = r
		if err = os.Mkdir(s.Group, 0755); err == nil {
			r.group, err = openGroup(s.Group)
		}
		if err == nil {
			s.GroupInode = r.group.inode
			err = e.save(s)
		}
		if err == nil {
			r.listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: strings.TrimPrefix(s.Store, "unix://"), Net: "unix"})
		}
		if err == nil {
			err = os.Chown(strings.TrimPrefix(s.Store, "unix://"), e.cfg.WorkerUID, -1)
		}
		if err == nil {
			err = os.Chmod(strings.TrimPrefix(s.Store, "unix://"), 0600)
		}
		if err != nil {
			// No daemon has been admitted. An absent group or a group we opened
			// can be resolved without claiming ownership of an unexpected group.
			if r.group == nil && os.IsNotExist(err) {
				_ = e.released(r, "session_not_started")
			} else {
				_ = e.stopLocked(r, "session_setup_failed")
			}
			return nil, &wire.Error{Code: "executor_setup", Message: "Could not prepare the task's delegated Nix session", Details: map[string]any{"session_id": id, "required_actor": "administrator", "stage": "session_setup", "cause": wire.Safe(err)}}
		}
		e.wg.Add(1)
		go e.accept(r)
		return cloneSession(s), nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, wire.Fail("session_not_found", "Executor has no session for this worker job")
	}
	if err != nil {
		return nil, err
	}
	switch method {
	case "create", "status":
	case "stop":
		if !oneOf(a.Reason, "completed", "cancelled", "deadline", "worker_stopped", "worker_restarted", "recovery", "failed") {
			return nil, wire.Fail("invalid_argument", "A session stop reason is required")
		}
		if err := e.stopLocked(r, a.Reason); err != nil {
			return nil, err
		}
	case "extend":
		if !time.Now().Before(r.session.Deadline) {
			_ = e.stopLocked(r, "deadline")
		}
		if r.session.Stopping != nil || r.session.Resources.State != "owned" || a.Deadline == nil || a.Deadline.Before(r.session.Deadline) || a.Deadline.After(r.session.Created.Add(24*time.Hour)) {
			return nil, wire.Fail("invalid_state", "Only an owned session can be extended within its lifetime")
		}
		next := cloneSession(r.session)
		next.Deadline = a.Deadline.UTC()
		if err := e.save(&next); err != nil {
			return nil, err
		}
		*r.session = next
	default:
		return nil, wire.Fail("invalid_argument", "Unknown executor operation")
	}
	return cloneSession(r.session), nil
}

func oneOf(value string, values ...string) bool {
	for _, v := range values {
		if value == v {
			return true
		}
	}
	return false
}

func (e *Executor) accept(r *runtimeSession) {
	defer e.wg.Done()
	for {
		conn, err := r.listener.AcceptUnix()
		if err != nil {
			return
		}
		if peerUID(conn) != e.cfg.WorkerUID {
			conn.Close()
			continue
		}
		e.mu.Lock()
		if e.closing || r.session.Stopping != nil || r.session.Resources.State != "owned" {
			e.mu.Unlock()
			conn.Close()
			continue
		}
		file, err := conn.File()
		conn.Close()
		if err == nil {
			log := &daemonLog{}
			c := exec.Command(e.cfg.Nix, "daemon", "--stdio", "--force-untrusted", "--store", "local", "--extra-experimental-features", "nix-command daemon-trust-override", "--option", "sandbox", "true", "--option", "sandbox-fallback", "false", "--option", "builders", "", "--option", "require-sigs", "true")
			c.Env = []string{"HOME=/root", "USER=root", "PATH=" + filepath.Dir(e.cfg.Nix), "LC_ALL=C"}
			c.Stdin, c.Stdout, c.Stderr = file, file, log
			c.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: int(r.group.fd.Fd()), Setsid: true}
			err = c.Start()
			file.Close()
			if err == nil {
				r.children[c] = true
				e.wg.Add(1)
				go func() {
					defer e.wg.Done()
					waitErr := c.Wait()
					e.mu.Lock()
					defer e.mu.Unlock()
					delete(r.children, c)
					if waitErr != nil && r.session.Stopping == nil {
						r.session.DaemonError = &wire.Error{Code: "nix_daemon_exit", Message: "Task Nix daemon exited unexpectedly", Details: map[string]any{"stage": "daemon", "exit_code": c.ProcessState.ExitCode(), "stderr": daemonURL.ReplaceAllString(log.String(), "[redacted URL]")}}
						_ = e.stopLocked(r, "daemon_exit")
					}
				}()
			}
		}
		if err != nil {
			r.session.DaemonError = &wire.Error{Code: "nix_daemon_start", Message: "Task Nix daemon could not start inside its cgroup", Details: map[string]any{"stage": "daemon", "cause": wire.Safe(err)}}
			_ = e.stopLocked(r, "daemon_start_failed")
		}
		e.mu.Unlock()
	}
}

var daemonURL = regexp.MustCompile(`https?://[^\s'"<>]+`)

type daemonLog struct{ buffer bytes.Buffer }

func (l *daemonLog) String() string { return l.buffer.String() }
func (l *daemonLog) Write(p []byte) (int, error) {
	n := len(p)
	if l.buffer.Len() < 32<<10 {
		_, _ = l.buffer.Write(p[:min(n, (32<<10)-l.buffer.Len())])
	}
	return n, nil
}

func (e *Executor) stopLocked(r *runtimeSession, reason string) error {
	if r.session.Resources.Released() {
		return nil
	}
	if r.monitoring {
		if reason == "recovery" && r.session.Resources.State == "blocked" {
			e.releaseAttempt(r)
			r.killAttempted = false
			return e.save(r.session)
		}
		return nil
	}
	if r.session.Stopping == nil {
		now := time.Now().UTC()
		r.session.Stopping = &now
		r.session.Resources.Reason = reason
	}
	// Admission and spawning use the same mutex; once the listener is closed,
	// no accepted-but-not-started connection can create another daemon.
	if r.listener != nil {
		_ = r.listener.Close()
	}
	r.session.Resources.State = "releasing"
	r.session.Resources.Error = nil
	if r.session.Resources.ReleaseDeadline == nil {
		e.releaseAttempt(r)
	}
	err := e.save(r.session)
	for c := range r.children {
		_ = c.Process.Signal(os.Interrupt)
	}
	if !r.monitoring && r.group != nil {
		r.monitoring = true
		e.wg.Add(1)
		go e.observeRelease(r)
	}
	if r.group == nil {
		e.block(r, "executor_identity", "Session group could not be opened; inspect the host before recovery", "administrator")
	}
	return err
}

func (e *Executor) releaseAttempt(r *runtimeSession) {
	now := time.Now().UTC()
	end := now.Add(e.cfg.ReleaseTimeout.Value())
	x := &r.session.Resources
	x.State, x.Error = "releasing", nil
	x.Attempts++
	x.ReleaseStarted, x.ReleaseDeadline = &now, &end
}

func (e *Executor) block(r *runtimeSession, code, message, actor string) {
	now := time.Now().UTC()
	r.session.Resources.State, r.session.Resources.Observed = "blocked", &now
	r.session.Resources.Error = &wire.Error{Code: code, Message: message, Retry: "reconcile", NextAction: "Inspect the executor session and recover the existing job after resolving its resource error", Details: map[string]any{"stage": "resources", "session_id": r.session.ID, "required_actor": actor}}
	_ = e.save(r.session)
}

func (e *Executor) released(r *runtimeSession, confirmation string) error {
	next := cloneSession(r.session)
	now := time.Now().UTC()
	if next.Stopping == nil {
		next.Stopping = &now
		next.Resources.Reason = "executor_restarted"
	}
	next.Resources.State, next.Resources.Confirmation, next.Resources.Observed, next.Resources.Error = "released", confirmation, &now, nil
	if err := e.save(&next); err != nil {
		e.block(r, "executor_persistence", "Resource release could not be persisted", "administrator")
		return err
	}
	*r.session = next
	socket := filepath.Join(filepath.Dir(e.cfg.Socket), next.ID+".sock")
	if next.Store == "unix://"+socket {
		if info, err := os.Lstat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			_ = os.Remove(socket)
		}
	}
	return nil
}

func (e *Executor) observeRelease(r *runtimeSession) {
	defer e.wg.Done()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		e.mu.Lock()
		live, err := r.group.populated()
		if err == nil && !live {
			if err = e.released(r, "sealed_cgroup_empty"); err == nil {
				_ = removeGroups(r.session.Group)
				r.group.close()
				r.group = nil
				r.monitoring = false
				e.mu.Unlock()
				return
			}
		}
		elapsed := time.Since(*r.session.Stopping)
		if !r.killAttempted && elapsed >= e.cfg.StopGrace.Value() {
			err = r.group.kill()
			r.killAttempted = true
		}
		if r.session.Resources.State != "blocked" && (err != nil || !time.Now().Before(*r.session.Resources.ReleaseDeadline)) {
			message := "Session processes have not exited within the release budget"
			if err != nil {
				message = "Executor could not terminate or observe the session cgroup: " + err.Error()
			}
			e.block(r, "resource_release_unconfirmed", message, "administrator")
		}
		e.mu.Unlock()
		select {
		case <-e.ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (e *Executor) deadlines() {
	defer e.wg.Done()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	lastRetention := time.Now()
	for {
		select {
		case <-e.ctx.Done():
			return
		case now := <-tick.C:
			e.mu.Lock()
			for _, r := range e.sessions {
				if r.session.Stopping == nil && !now.Before(r.session.Deadline) && !r.session.Resources.Released() {
					_ = e.stopLocked(r, "deadline")
				}
			}
			if now.Sub(lastRetention) >= time.Minute {
				for id, r := range e.sessions {
					if r.session.Resources.Released() && !r.monitoring {
						delete(e.sessions, id)
					}
				}
				_, _ = e.db.Exec(`DELETE FROM executor_sessions WHERE id IN (SELECT id FROM executor_sessions WHERE state='released' AND json_extract(body,'$.resources.observed')<? LIMIT 128)`, now.Add(-30*24*time.Hour).UTC().Format(time.RFC3339Nano))
				lastRetention = now
			}
			e.mu.Unlock()
		}
	}
}

// Shutdown preserves unresolved receipts. The enclosing mixed-kill service
// terminates remaining processes if this bounded graceful shutdown cannot.
func (e *Executor) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	e.closing = true
	for _, r := range e.sessions {
		_ = e.stopLocked(r, "executor_shutdown")
	}
	e.mu.Unlock()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		e.mu.Lock()
		pending := false
		for _, r := range e.sessions {
			pending = pending || !r.session.Resources.Released()
		}
		e.mu.Unlock()
		if !pending {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (e *Executor) Close() {
	e.cancel()
	e.wg.Wait()
	for _, r := range e.sessions {
		if r.group != nil {
			r.group.close()
		}
	}
	e.db.Close()
	e.root.Close()
}

func peerUID(c net.Conn) int {
	uid := -1
	if c, ok := c.(*net.UnixConn); ok {
		raw, err := c.SyscallConn()
		if err == nil {
			_ = raw.Control(func(fd uintptr) {
				if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
					uid = int(cred.Uid)
				}
			})
		}
	}
	return uid
}

type peerKey struct{}

func (e *Executor) HTTPServer() *http.Server {
	return &http.Server{ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, peerKey{}, peerUID(c))
		},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uid, _ := r.Context().Value(peerKey{}).(int)
			if uid != e.cfg.WorkerUID && uid != 0 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if r.Method != http.MethodPost || r.URL.Path != "/call" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var a struct {
				Method string  `json:"method"`
				Params Request `json:"params"`
			}
			data, err := io.ReadAll(io.LimitReader(r.Body, wire.MaxJSON+1))
			if err == nil {
				err = wire.Decode(data, &a)
			}
			var result any
			if err == nil {
				result, err = e.Call(a.Method, a.Params)
			}
			w.Header().Set("Content-Type", "application/json")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				result = map[string]any{"error": wire.Safe(err)}
			}
			_ = json.NewEncoder(w).Encode(result)
		})}
}

func (e *Executor) Listen() (net.Listener, error) {
	dir := filepath.Dir(e.cfg.Socket)
	if err := os.Mkdir(dir, 0711); err == nil {
		// systemd starts us with UMask=0077. Allow the worker to traverse this
		// dedicated directory; each socket itself is restricted to its UID.
		if err = os.Chmod(dir, 0711); err != nil {
			return nil, err
		}
	} else if !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Sys().(*syscall.Stat_t).Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return nil, fmt.Errorf("executor socket directory must be root-owned and not writable by other users")
	}
	if info, err := os.Lstat(e.cfg.Socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("executor socket path is not a socket")
		}
		if err = os.Remove(e.cfg.Socket); err != nil {
			return nil, err
		}
	}
	l, err := net.Listen("unix", e.cfg.Socket)
	if err != nil {
		return nil, err
	}
	if err = os.Chown(e.cfg.Socket, e.cfg.WorkerUID, -1); err == nil {
		err = os.Chmod(e.cfg.Socket, 0600)
	}
	if err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}
