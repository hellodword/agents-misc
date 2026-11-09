package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/nixexecutor"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Indexing owns only this job's I/O mutex. Admission, resource observations and
// other jobs stay responsive while retained output is read and hashed.
func (w *Worker) collectLocked(j *Job) {
	e := &j.Execution.Evidence
	if w.closing || j.collecting || j.Cleanup != "complete" || e.State != "pending" || e.NextAttempt != nil && time.Now().Before(*e.NextAttempt) {
		return
	}
	if j.ManifestDigest != "" {
		e.State, e.Error = "transferring", nil
		_ = w.save(j)
		return
	}
	j.collecting = true
	e.State, e.Attempts, e.Error = "collecting", e.Attempts+1, nil
	now := time.Now().UTC()
	e.Updated, e.NextAttempt = &now, nil
	if err := w.save(j); err != nil {
		e.State = "blocked"
		e.Error = lifecycleError(j, "evidence", err, "administrator")
		j.collecting = false
		return
	}
	copy := &Job{Outputs: map[string]string{}}
	request := w.sessionRequest(j, "")
	runID := j.Request.RunID
	for name, value := range j.Outputs {
		copy.Outputs[name] = value
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		j.io.Lock()
		defer j.io.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), w.cfg.EvidenceTimeout.Value())
		defer cancel()
		logRoot := filepath.Join(w.root.Path, j.Request.TaskID)
		var err error
		if info, statErr := os.Stat(filepath.Join(logRoot, "task.log")); statErr == nil {
			copy.Files = append(copy.Files, File{ID: wire.ID(), Name: "task.log", Root: logRoot, Relative: "task.log", Size: info.Size()})
			file, openErr := os.OpenFile(filepath.Join(logRoot, "task.log"), os.O_RDWR, 0)
			err = openErr
			if err == nil {
				err = file.Sync()
				file.Close()
			}
		} else if !os.IsNotExist(statErr) {
			err = statErr
		}
		if err == nil {
			err = w.recoverOutputLinks(request.TaskID, copy)
		}
		if err == nil {
			err = w.collectExecutorArtifacts(ctx, request, copy)
		}
		if err == nil {
			err = w.indexFilesContext(ctx, copy)
		}
		var test *wire.NixTestResult
		if err == nil {
			test, err = testReceipt(copy.Files, runID)
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		j.collecting = false
		e := &j.Execution.Evidence
		now := time.Now().UTC()
		e.Updated = &now
		if err == nil {
			w.applyTestReceipt(j, test)
			j.Outputs = copy.Outputs
			j.Files = copy.Files
			files := []wire.HandoffFile{}
			for _, file := range j.Files {
				files = append(files, wire.HandoffFile{ID: file.ID, Size: file.Size, SHA256: file.SHA256})
			}
			j.ManifestDigest = wire.ManifestDigest(files)
			e.State = "transferring"
			err = w.save(j)
		}
		if err != nil {
			e.State, e.Error = "blocked", lifecycleError(j, "evidence", err, "administrator")
			if wire.TransientEvidence(err) && e.Attempts < 3 {
				delay := time.Second
				if e.Attempts == 2 {
					delay = 5 * time.Second
				}
				next := now.Add(delay)
				e.State, e.NextAttempt = "pending", &next
			}
			_ = w.save(j)
		}
	}()
}

// Nix publishes these GC roots before returning JSON. They survive a lost
// response or worker restart and keep successful evidence reachable.
func (w *Worker) recoverOutputLinks(taskID string, result *Job) error {
	dir, err := w.root.Open(taskID)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, target := range result.Outputs {
		known[target] = true
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != "result" && !strings.HasPrefix(name, "result-") {
			continue
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return wire.Fail("evidence_integrity", "Nix output root is not a symbolic link")
		}
		target, err := w.root.Readlink(path.Join(taskID, name))
		if err != nil {
			return err
		}
		if !wire.StorePattern.MatchString(target) {
			return wire.Fail("evidence_integrity", "Nix output root does not identify a store output")
		}
		if !known[target] {
			result.Outputs[name] = target
			known[target] = true
		}
	}
	return nil
}
func (w *Worker) collectExecutorArtifacts(ctx context.Context, request nixexecutor.Request, result *Job) error {
	if request.SessionID == "" {
		return nil
	}
	var total int64
	count := 0
	for {
		var page nixexecutor.ArtifactPage
		if err := w.executor.Call(ctx, "evidence_list", request, &page); err != nil {
			return err
		}
		for _, item := range page.Files {
			count++
			if !wire.IDPattern.MatchString(item.ID) || safefs.Name(item.Name) != nil || item.Size < 0 || item.Size > w.cfg.MaxEvidenceBytes-total || count > 10000 {
				return wire.Fail("quota_exceeded", "Executor evidence exceeds the worker retention limit")
			}
			total += item.Size
			reader, ok := w.executor.(nixexecutor.ArtifactReader)
			if !ok {
				return fmt.Errorf("executor cannot stream retained evidence")
			}
			request.ArtifactID = item.ID
			body, err := reader.ReadArtifact(ctx, request)
			if err != nil {
				return err
			}
			name := path.Join(request.TaskID, "retained", item.ID)
			size, digest, err := w.root.Write(name, contextReader{ctx, body}, item.Size)
			body.Close()
			if err != nil {
				return err
			}
			if size != item.Size {
				return wire.Fail("evidence_integrity", "Executor evidence size differs from its sealed inventory")
			}
			result.Files = append(result.Files, File{ID: item.ID, Name: "retained/" + item.Name, Root: w.root.Path, Relative: name, Size: size, SHA256: digest})
		}
		if page.NextCursor == 0 {
			return nil
		}
		if page.NextCursor <= request.Cursor {
			return wire.Fail("worker_protocol_error", "Executor evidence pagination did not advance")
		}
		request.Cursor, request.ArtifactID = page.NextCursor, ""
	}
}

func testReceipt(files []File, runID string) (*wire.NixTestResult, error) {
	var receipt *wire.NixTestResult
	for _, item := range files {
		if !strings.HasSuffix(item.Name, "/driver-result.json") {
			continue
		}
		root, err := os.OpenRoot(item.Root)
		if err != nil {
			return nil, err
		}
		data, err := (&safefs.Root{Root: root}).Read(item.Relative, wire.MaxJSON)
		root.Close()
		if err != nil {
			return nil, err
		}
		var current wire.NixTestResult
		if err := json.Unmarshal(data, &current); err != nil {
			return nil, err
		}
		if current.RunID != runID {
			continue
		}
		if current.Version != 1 || !oneOfOutcome(current.Outcome) {
			return nil, wire.Fail("driver_protocol_error", "Driver result uses an unsupported format")
		}
		if receipt != nil && string(wire.JSON(receipt)) != string(wire.JSON(current)) {
			return nil, wire.Fail("evidence_integrity", "Driver result receipts disagree")
		}
		receipt = &current
	}
	return receipt, nil
}

func oneOfOutcome(outcome string) bool {
	switch outcome {
	case "passed", "failed", "timed_out", "driver_failed", "unconfirmed":
		return true
	}
	return false
}

func (w *Worker) applyTestReceipt(j *Job, test *wire.NixTestResult) {
	if test == nil {
		test = &wire.NixTestResult{Version: 1, RunID: j.Request.RunID, Phase: "unobserved", Outcome: "unconfirmed", Collection: wire.CollectionStatus{State: "unavailable"}}
		switch j.Execution.Stage {
		case "session", "import", "validate":
			test.Started = new(false)
			test.Phase, test.Outcome = "not_started", "not_started"
		}
		process := j.Execution.Process
		if process != nil && process.Exited && process.ExitCode != nil && *process.ExitCode != 0 {
			test.Phase, test.Outcome = "not_started", "not_started"
			code, message := "input_failed", "Test driver did not start because input preparation failed"
			if process.Stage == "build" {
				test.Phase, test.Outcome = "unobserved", "unconfirmed"
				code, message = "build_failed", "Nix build failed; no driver result was recovered"
			} else {
				test.Started = new(false)
			}
			j.Execution.Error = wire.Safe(wire.Fail(code, message))
		}
	} else {
		// Collection is observed after resource release. A receipt interrupted
		// during capture records its last driver phase, but capture cannot resume.
		if test.Collection.State != "complete" && test.Collection.State != "partial" && test.Collection.State != "unavailable" {
			test.Collection.State = "unavailable"
		}
		switch test.Outcome {
		case "failed":
			j.Execution.Error = wire.Safe(wire.Fail("test_failed", "Test driver reported a script failure"))
		case "timed_out":
			j.Execution.Error = wire.Safe(wire.Fail("test_timeout", "Test driver reached its global deadline"))
		case "driver_failed":
			j.Execution.Error = wire.Safe(wire.Fail("driver_failed", "Test driver initialization or completion failed"))
		}
	}
	j.Execution.Test, j.Execution.Collection = test, test.Collection
}
