package guest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// Rejection is known only when the service did not start a supervisor. It has
// no process exit code and cannot replace an existing command record.
func rejectSubmission(root, id, executionContext string, cause error) error {
	if executionContext == "" {
		executionContext = "service"
	}
	if !wire.IDPattern.MatchString(id) {
		return wire.Fail("invalid_argument", "Invalid command ID")
	}
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	now := time.Now().UTC()
	empty := sha256.Sum256(nil)
	hash := hex.EncodeToString(empty[:])
	output := &wire.CommandOutput{State: "complete", StdoutSHA256: hash, StderrSHA256: hash}
	for _, name := range []string{"stdout", "stderr"} {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			err = errors.Join(f.Sync(), f.Close())
		}
		if err != nil {
			output.State = "incomplete"
			output.Error = phaseFailure("output_init", err)
		}
	}
	status := wire.CommandStatus{State: "failed", Stage: "finished", Final: true, Updated: &now, Finished: &now, Error: "Command submission was rejected before supervisor start", Execution: &wire.CommandExecution{Context: executionContext}, Termination: &wire.Termination{Reason: "start_failed", Requested: now, Source: "guest_command_service", Error: wire.Safe(cause)}, Output: output}
	return atomic(filepath.Join(dir, "status.json"), status)
}

// Reconcile is called only after an owner has confirmed that this supervisor
// ended. Its exit is not evidence of the test process's exit or output integrity.
func Reconcile(root, id string, cause error) error {
	if !wire.IDPattern.MatchString(id) {
		return wire.Fail("invalid_argument", "Invalid command ID")
	}
	dir := filepath.Join(root, id)
	file := filepath.Join(dir, "status.json")
	var status wire.CommandStatus
	if err := readJSON(file, &status); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	if status.Final {
		return nil
	}
	return atomic(file, lostSupervisor(status, cause))
}

func lostSupervisor(status wire.CommandStatus, cause error) wire.CommandStatus {
	now := time.Now().UTC()
	status.State, status.Stage, status.Final = "uncertain", "finished", true
	status.Updated, status.Finished = &now, &now
	status.Error = "Supervisor ended without a final execution record"
	if status.Termination == nil {
		status.Termination = &wire.Termination{Reason: "supervisor_lost", Requested: now, Source: "supervisor_exit"}
	}
	if status.Termination.Error == nil {
		if cause == nil {
			cause = wire.Fail("missing_terminal_status", "Supervisor did not publish a final execution record")
		}
		status.Termination.Error = phaseFailure("supervisor_exit", cause)
	}
	if status.Output == nil {
		status.Output = &wire.CommandOutput{State: "incomplete"}
	} else if status.Output.State == "open" {
		status.Output.State = "incomplete"
	}
	return status
}
