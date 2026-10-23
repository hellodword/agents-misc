package submit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type RunOptions struct {
	Options
	Endpoint, Token string
	TimeoutSeconds  int
	Progress        io.Writer
	pollInterval    time.Duration
}

// run.json contains retry identities and receipts, never bearer/capability tokens.
type runRecord struct {
	Endpoint       string         `json:"endpoint"`
	RequestID      string         `json:"request_id"`
	Test           string         `json:"test"`
	SmokeProject   string         `json:"smoke_project"`
	TimeoutSeconds int            `json:"timeout_seconds"`
	CacheID        string         `json:"cache_id,omitempty"`
	Submission     map[string]any `json:"submission,omitempty"`
	TaskID         string         `json:"task_id,omitempty"`
}

type receipt struct {
	ID     string          `json:"id"`
	State  string          `json:"state"`
	Result json.RawMessage `json:"result"`
	Error  *wire.Error     `json:"error"`
}

type bearerTransport struct{ token string }

func (t bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}

func wait(ctx context.Context, interval time.Duration) error {
	t := time.NewTimer(interval)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func call(ctx context.Context, session *mcp.ClientSession, name string, args any, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	raw, err := json.Marshal(r.StructuredContent)
	if err != nil {
		return err
	}
	if r.IsError {
		var failure struct {
			Error *wire.Error `json:"error"`
		}
		if json.Unmarshal(raw, &failure) == nil && failure.Error != nil {
			return failure.Error
		}
		return fmt.Errorf("%s failed", name)
	}
	if string(raw) == "null" {
		return fmt.Errorf("%s returned no structured result", name)
	}
	return json.Unmarshal(raw, result)
}

func mutate(ctx context.Context, session *mcp.ClientSession, name string, args any, interval time.Duration, result any) error {
	var operation receipt
	if err := call(ctx, session, name, args, &operation); err != nil {
		return err
	}
	for operation.State == "pending" {
		if !wire.IDPattern.MatchString(operation.ID) {
			return fmt.Errorf("invalid operation ID")
		}
		if err := wait(ctx, interval); err != nil {
			return err
		}
		if err := call(ctx, session, "operation_get", map[string]any{"operation_id": operation.ID}, &operation); err != nil {
			return err
		}
	}
	if operation.State != "succeeded" {
		if operation.Error != nil {
			return operation.Error
		}
		return fmt.Errorf("operation %s is %s; inspect the original receipt", operation.ID, operation.State)
	}
	return json.Unmarshal(operation.Result, result)
}

// Run submits through MCP and follows the durable task until cleanup finishes.
// Interrupting this client leaves the remote task running; reusing WorkDir resumes.
func Run(ctx context.Context, o RunOptions) (json.RawMessage, error) {
	if o.Progress == nil {
		o.Progress = io.Discard
	}
	if o.pollInterval == 0 {
		o.pollInterval = 2 * time.Second
	}
	if o.TimeoutSeconds < 0 || o.TimeoutSeconds > 30*86400 {
		return nil, fmt.Errorf("timeout-seconds must be between 0 and 2592000")
	}
	if o.WorkDir == "" {
		if o.RequestID == "" {
			o.RequestID = "nixos-" + wire.ID()
		}
		// IDs become a directory component only after validation.
		if !requestIDPattern.MatchString(o.RequestID) {
			return nil, fmt.Errorf("invalid request ID")
		}
		var err error
		o.WorkDir, err = filepath.Abs(filepath.Join("tmp", "agent", "vm-test-runs", o.RequestID))
		if err != nil {
			return nil, err
		}
	}
	root, err := safefs.Private(o.WorkDir, true)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	j := runRecord{Endpoint: o.Endpoint, RequestID: o.RequestID, Test: o.Test, SmokeProject: o.SmokeProject, TimeoutSeconds: o.TimeoutSeconds}
	if old, err := root.Read("run.json", 1<<20); err == nil {
		if err := wire.Decode(old, &j); err != nil {
			return nil, err
		}
		if o.TimeoutSeconds == 0 {
			o.TimeoutSeconds = j.TimeoutSeconds
		}
		if j.Endpoint != o.Endpoint || j.Test != o.Test || j.SmokeProject != o.SmokeProject || j.TimeoutSeconds != o.TimeoutSeconds || o.RequestID != "" && j.RequestID != o.RequestID {
			return nil, fmt.Errorf("work-dir belongs to a different submission; reuse its original arguments")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if j.RequestID == "" {
		j.RequestID = "nixos-" + wire.ID()
	}
	if !requestIDPattern.MatchString(j.RequestID) {
		return nil, fmt.Errorf("invalid request ID")
	}
	o.RequestID = j.RequestID
	save := func() error {
		_, _, err := root.Write("run.json", bytes.NewReader(wire.JSON(j)), 1<<20)
		return err
	}
	if err := save(); err != nil {
		return nil, err
	}
	fmt.Fprintf(o.Progress, "Request: %s\nWorkspace: %s\n", j.RequestID, o.WorkDir)
	client := mcp.NewClient(&mcp.Implementation{Name: "vm-test-submit", Version: wire.Version}, nil)
	httpClient := &http.Client{Transport: bearerTransport{o.Token}, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return fmt.Errorf("MCP redirects are not allowed")
	}}
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: o.Endpoint, HTTPClient: httpClient, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to MCP: %w", err)
	}
	defer session.Close()
	if j.TaskID == "" {
		if j.Submission == nil {
			var description struct {
				Backends map[string]struct {
					Available bool   `json:"available"`
					Reason    string `json:"waiting_reason"`
				} `json:"backends"`
			}
			if err := call(ctx, session, "service_get", map[string]any{}, &description); err != nil {
				return nil, err
			}
			// Busy execution resources can accept queued work. The controller
			// waits for the active backend's cleanup before starting this task.
			if b := description.Backends["nixos"]; !b.Available && b.Reason != "virtualbox_running" && b.Reason != "worker_busy" {
				return nil, fmt.Errorf("NixOS backend unavailable: %s", b.Reason)
			}
			var transfer struct {
				ID    string `json:"id"`
				URL   string `json:"url"`
				State string `json:"state"`
			}
			if j.CacheID == "" {
				if err := mutate(ctx, session, "transfer_create", map[string]any{"request_id": j.RequestID + "-cache", "kind": "cache"}, o.pollInterval, &transfer); err != nil {
					return nil, err
				}
				if !wire.IDPattern.MatchString(transfer.ID) {
					return nil, fmt.Errorf("invalid transfer ID")
				}
				j.CacheID = transfer.ID
				if err := save(); err != nil {
					return nil, err
				}
			}
			// Always renew the URL; transfer_create receipts may contain expired URLs.
			if err := call(ctx, session, "transfer_get", map[string]any{"transfer_id": j.CacheID}, &transfer); err != nil {
				return nil, err
			}
			if transfer.State != "open" {
				return nil, fmt.Errorf("input transfer is already sealed; inspect its task")
			}
			o.CacheID, o.CacheURL = j.CacheID, transfer.URL
			fmt.Fprintln(o.Progress, "Evaluating and uploading the test closure…")
			j.Submission, err = Prepare(ctx, o.Options)
			if err != nil {
				return nil, err
			}
			if o.TimeoutSeconds != 0 {
				j.Submission["timeout_seconds"] = o.TimeoutSeconds
			}
			// Persist the exact request before sending it. A lost reply must not
			// allocate another cache, upload to a sealed cache or start a new run.
			if err := save(); err != nil {
				return nil, err
			}
		}
		var task struct {
			ID string `json:"id"`
		}
		if err := mutate(ctx, session, "nixos_run", j.Submission, o.pollInterval, &task); err != nil {
			return nil, err
		}
		if !wire.IDPattern.MatchString(task.ID) {
			return nil, fmt.Errorf("invalid task ID")
		}
		j.TaskID = task.ID
		if err := save(); err != nil {
			return nil, err
		}
	}
	fmt.Fprintf(o.Progress, "Task: %s\n", j.TaskID)
	return observe(ctx, session, root, j.TaskID, o)
}

func observe(ctx context.Context, session *mcp.ClientSession, root *safefs.Root, id string, o RunOptions) (json.RawMessage, error) {
	log, err := root.OpenFile("task.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	st, err := log.Stat()
	if err != nil {
		return nil, err
	}
	offset := st.Size()
	previous := ""
	for {
		var raw json.RawMessage
		if err := call(ctx, session, "task_get", map[string]any{"task_id": id}, &raw); err != nil {
			return nil, err
		}
		var task struct {
			Phase   string
			Result  struct{ Termination, Conclusion string }
			Cleanup struct{ State string }
		}
		if err := json.Unmarshal(raw, &task); err != nil {
			return nil, err
		}
		if status := task.Phase + "/" + task.Cleanup.State; status != previous {
			fmt.Fprintln(o.Progress, status)
			previous = status
		}
		for {
			var chunk struct {
				Data       string `json:"data"`
				NextOffset int64  `json:"next_offset"`
				EOF        bool   `json:"eof"`
			}
			if err := call(ctx, session, "task_log", map[string]any{"task_id": id, "offset": offset}, &chunk); err != nil {
				if failure, ok := err.(*wire.Error); ok && failure.Code == "not_found" {
					break // Queued tasks may not have a log yet.
				}
				return nil, err
			}
			data, err := base64.StdEncoding.DecodeString(chunk.Data)
			if err != nil || chunk.NextOffset != offset+int64(len(data)) || len(data) == 0 && !chunk.EOF {
				return nil, fmt.Errorf("invalid task log offset or data")
			}
			if _, err := log.Write(data); err != nil {
				return nil, err
			}
			if err := log.Sync(); err != nil {
				return nil, err
			}
			offset = chunk.NextOffset
			_, _ = o.Progress.Write(data)
			if chunk.EOF {
				break
			}
		}
		terminal := task.Phase == "ended"
		if terminal && (task.Cleanup.State == "complete" || task.Cleanup.State == "blocked") {
			if _, _, err := root.Write("result.json", bytes.NewReader(raw), 4<<20); err != nil {
				return nil, err
			}
			if task.Result.Termination != "finished" || task.Result.Conclusion != "passed" || task.Cleanup.State != "complete" {
				return raw, fmt.Errorf("task %s: state=%s conclusion=%s cleanup=%s", id, task.Result.Termination, task.Result.Conclusion, task.Cleanup.State)
			}
			return raw, nil
		}
		if err := wait(ctx, o.pollInterval); err != nil {
			return nil, fmt.Errorf("task %s remains on the server; resume with --work-dir %q: %w", id, o.WorkDir, err)
		}
	}
}
