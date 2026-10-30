package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/nixexecutor"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/unix"
)

func (w *Worker) checkSession(ctx context.Context, j *Job, diagnostics ...io.Writer) error {
	c := w.command(ctx, "store", "info", "--store", w.store(j), "--json")
	if len(diagnostics) > 0 {
		c.Stderr = diagnostics[0]
	}
	var output strings.Builder
	c.Stdout = &capped{w: &output, max: 1 << 20}
	err := w.runClient(j, c)
	w.processResult(j, "session", c)
	if err != nil {
		return fmt.Errorf("inspect Nix session: %w", err)
	}
	var info struct {
		Trusted *bool `json:"trusted"`
	}
	if err = json.Unmarshal([]byte(output.String()), &info); err != nil {
		return fmt.Errorf("decode Nix session information: %w", err)
	}
	if info.Trusted == nil {
		return fmt.Errorf("Nix session did not report client trust")
	}
	if *info.Trusted {
		return fmt.Errorf("worker must not be a trusted Nix session client")
	}
	return nil
}

type Session struct {
	ClientBoot       string                `json:"client_boot,omitempty"`
	ReleaseStarted   *time.Time            `json:"release_started,omitempty"`
	StopReason       string                `json:"stop_reason,omitempty"`
	Intent           bool                  `json:"intent"`
	ExecutorInstance string                `json:"executor_instance,omitempty"`
	Executor         *nixexecutor.Session  `json:"executor,omitempty"`
	Launching        bool                  `json:"launching"`
	Client           *wire.ProcessIdentity `json:"client,omitempty"`
}

func (w *Worker) sessionRequest(j *Job, reason string) nixexecutor.Request {
	a := nixexecutor.Request{WorkerInstance: w.db.InstanceID, TaskID: j.Request.TaskID, Reason: reason}
	if j.Session.Executor != nil {
		a.SessionID = j.Session.Executor.ID
	}
	return a
}

func (w *Worker) admit(ctx context.Context, j *Job) error {
	var d nixexecutor.Description
	if err := w.executor.Call(ctx, "describe", nixexecutor.Request{}, &d); err != nil {
		return err
	}
	if !d.Ready || d.Build.Version != wire.Version {
		return wire.Fail("executor_unavailable", "Executor is not ready or uses a different protocol")
	}
	w.mu.Lock()
	if ctx.Err() != nil {
		w.mu.Unlock()
		return ctx.Err()
	}
	j.Session.Intent, j.Session.ExecutorInstance = true, d.InstanceID
	j.Execution.Resources.State = "owned"
	a := w.sessionRequest(j, "")
	end := j.Deadline
	a.Deadline = &end
	err := w.save(j)
	w.mu.Unlock()
	if err != nil {
		return err
	}
	var session nixexecutor.Session
	if err = w.executor.Call(ctx, "create", a, &session); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if session.ExecutorInstance != j.Session.ExecutorInstance {
		return wire.Fail("executor_identity", "Executor storage changed during session admission")
	}
	j.Session.Executor = &session
	j.Execution.Resources = session.Resources
	if err = w.save(j); err != nil {
		return err
	}
	if session.Resources.State != "owned" {
		return wire.Fail("invalid_state", "Session admission was closed")
	}
	return nil
}

func (w *Worker) store(j *Job) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return j.Session.Executor.Store
}

// Persist the launch intent before starting a client and its kernel identity
// before waiting. Recovery never signals an unrelated process after PID reuse.
func (w *Worker) runClient(j *Job, c *exec.Cmd) error {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	w.mu.Lock()
	j.Session.ClientBoot = string(boot)
	j.Session.Launching = true
	if err := w.save(j); err != nil {
		j.Session.Launching = false
		w.mu.Unlock()
		return err
	}
	err = c.Start()
	if err == nil {
		id, _, inspect := clientIdentity(c.Process.Pid)
		if inspect == nil {
			j.Session.Client = &id
			j.Session.Launching = false
		}
		if inspect != nil {
			err = inspect
		}
	} else {
		j.Session.Launching = false
	}
	if saved := w.save(j); err == nil {
		err = saved
	}
	w.mu.Unlock()
	if c.Process != nil {
		if err != nil {
			_ = c.Process.Kill()
		}
		wait := c.Wait()
		if err == nil {
			err = wait
		}
	}
	w.mu.Lock()
	j.Session.Client, j.Session.Launching = nil, false
	if saved := w.save(j); err == nil {
		err = saved
	}
	w.mu.Unlock()
	return err
}

func clientIdentity(pid int) (wire.ProcessIdentity, bool, error) {
	var id wire.ProcessIdentity
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return id, false, err
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return id, false, err
	}
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return id, false, fmt.Errorf("process identity is unavailable")
	}
	fields := strings.Fields(string(data[end+2:]))
	if len(fields) < 20 {
		return id, false, fmt.Errorf("process identity is incomplete")
	}
	id = wire.ProcessIdentity{PID: pid, Start: fmt.Sprintf("%x", sha256.Sum256(append(boot, fields[19]...)))}
	return id, fields[0] != "Z" && fields[0] != "X", nil
}

func releaseClient(id *wire.ProcessIdentity) (bool, error) {
	if id == nil {
		return true, nil
	}
	fd, err := unix.PidfdOpen(id.PID, 0)
	if errors.Is(err, unix.ESRCH) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer unix.Close(fd)
	current, alive, err := clientIdentity(id.PID)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if current != *id || !alive {
		return true, nil
	}
	return false, unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
}

func (w *Worker) observeLocked(j *Job) {
	if w.closing || j.observing || j.Cleanup == "complete" {
		return
	}
	j.observing = true
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.reconcileResources(j)
		w.mu.Lock()
		j.observing = false
		w.mu.Unlock()
	}()
}

func (w *Worker) reconcileResources(j *Job) {
	w.mu.Lock()
	// Live clients are cancelled by run's context and reaped by runClient.
	finished := j.Execution.Finished != nil
	if !finished && j.Session.StopReason == "" {
		w.mu.Unlock()
		return
	}
	if j.Session.ReleaseStarted == nil {
		now := time.Now().UTC()
		j.Session.ReleaseStarted = &now
	}
	releaseDeadline := j.Session.ReleaseStarted.Add(5 * time.Minute)
	a := w.sessionRequest(j, "completed")
	if j.State != "succeeded" {
		a.Reason = "failed"
	}
	if j.State == "interrupted" {
		a.Reason = "worker_restarted"
	}
	if j.Session.StopReason != "" {
		a.Reason = j.Session.StopReason
	}
	if j.recoverResources {
		a.Reason = "recovery"
		j.recoverResources = false
	}
	intent, expected := j.Session.Intent, j.Session.ExecutorInstance
	client, launching, clientBoot := j.Session.Client, j.Session.Launching, j.Session.ClientBoot
	w.mu.Unlock()
	if launching && clientBoot != "" {
		if boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id"); err == nil && string(boot) != clientBoot {
			launching = false
		}
	}
	var session nixexecutor.Session
	var err error
	if intent {
		err = w.executor.Call(context.Background(), "stop", a, &session)
		if err == nil && session.ExecutorInstance != expected {
			err = wire.Fail("executor_identity", "Executor storage identity differs from the admitted session")
		}
	} else {
		session.Resources = wire.NixResources{State: "unallocated", Confirmation: "never_admitted"}
	}
	if session.Resources.ReleaseDeadline != nil {
		releaseDeadline = *session.Resources.ReleaseDeadline
	}
	clientGone := finished && !launching
	if err == nil && clientGone {
		clientGone, err = releaseClient(client)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err == nil {
		j.Execution.Resources = session.Resources
		if session.DaemonError != nil && j.Execution.Error != nil {
			if j.Execution.Error.Details == nil {
				j.Execution.Error.Details = map[string]any{}
			}
			j.Execution.Error.Details["executor"] = session.DaemonError
		}
	}
	if err != nil || (launching && finished) {
		if err == nil {
			err = wire.Fail("client_identity", "Worker stopped before persisting the Nix client identity")
		}
		j.Execution.Resources.State = "blocked"
		j.Execution.Resources.Error = lifecycleError(j, "resources", err, "administrator")
	} else if !clientGone {
		j.Execution.Resources.State = "releasing"
		j.Execution.Resources.Confirmation = ""
		if !time.Now().Before(releaseDeadline) {
			j.Execution.Resources.State = "blocked"
			j.Execution.Resources.Error = lifecycleError(j, "resources", wire.Fail("termination_unconfirmed", "Nix client exit was not confirmed within the release budget"), "administrator")
		}
	}
	j.Cleanup = "pending"
	if j.Execution.Resources.State == "blocked" {
		j.Cleanup = "required"
	}
	if j.Execution.Resources.Released() && clientGone {
		j.Cleanup = "complete"
		j.Session.Client = nil
		j.Session.Launching = false
	}
	if err = w.save(j); err != nil {
		j.Cleanup = "required"
		j.Execution.Resources.State = "blocked"
		j.Execution.Resources.Error = lifecycleError(j, "resources", err, "administrator")
		return
	}
	if j.Cleanup == "complete" {
		if w.active == j.Request.TaskID {
			w.active = ""
		}
		w.collectLocked(j)
	}
}

func lifecycleError(j *Job, stage string, err error, actor string) *wire.Error {
	return &wire.Error{Code: stage + "_blocked", Category: "execution", Message: "Nix " + stage + " requires recovery", Retry: "reconcile", NextAction: "Inspect the recorded cause and recover this job after resolving it", Details: map[string]any{"stage": stage, "task_id": j.Request.TaskID, "session_id": j.Execution.Resources.SessionID, "cause": wire.LifecycleCause(err), "required_actor": actor, "observed": time.Now().UTC()}}
}
