package submit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type runFixture struct {
	mu               sync.Mutex
	url              string
	available        bool
	waitingReason    string
	expireInput      bool
	rejectSubmission bool
	failUpload       bool
	loseReply        bool
	cleanup          string
	submits, uploads int
	creates, polls   int
	firstRequest     string
	log              []byte
}

func (f *runFixture) tool(_ context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var args map[string]any
	if err := json.Unmarshal(r.Params.Arguments, &args); err != nil {
		return nil, err
	}
	inputID := strings.Repeat("c", 32)
	if f.creates > 1 {
		inputID = fmt.Sprintf("%032x", f.creates)
	}
	transfer := map[string]any{"id": inputID, "state": "open", "url": f.url + "/files/capability-secret"}
	var value any
	failed := false
	switch r.Params.Name {
	case "service_get":
		value = map[string]any{"backends": map[string]any{"nixos": map[string]any{"available": f.available, "waiting_reason": f.waitingReason}}}
	case "upload_create":
		f.creates++
		value = receipt{ID: strings.Repeat("a", 32), State: "pending"}
	case "operation_get":
		value = receipt{ID: strings.Repeat("a", 32), State: "succeeded", Result: wire.JSON(transfer)}
	case "upload_get":
		if f.expireInput {
			f.expireInput = false
			value = map[string]any{"error": wire.Fail("not_found", "Expired unbound input")}
			failed = true
			break
		}
		if f.submits != 0 {
			transfer["state"] = "ready"
		}
		value = transfer
	case "nix_job_create":
		f.submits++
		if f.rejectSubmission {
			f.rejectSubmission = false
			value, failed = map[string]any{"error": wire.Fail("not_found", "Unbound input expired before admission")}, true
			break
		}
		request := string(wire.JSON(args))
		if f.firstRequest != "" && f.firstRequest != request {
			return nil, fmt.Errorf("retry changed the request")
		}
		f.firstRequest = request
		if f.loseReply && f.submits == 1 {
			return nil, fmt.Errorf("simulated lost submission reply")
		}
		value = receipt{ID: strings.Repeat("d", 32), State: "succeeded", Result: wire.JSON(map[string]any{"id": strings.Repeat("b", 32)})}
	case "nix_job_get", "wait":
		if r.Params.Name == "wait" && args["targets"].([]any)[0].(map[string]any)["kind"] == "operation" {
			value = map[string]any{"reason": "satisfied", "targets": []any{map[string]any{"summary": receipt{ID: strings.Repeat("a", 32), State: "succeeded", Result: wire.JSON(transfer)}}}}
			break
		}
		f.polls++
		state, cleanup := "finished", f.cleanup
		if f.polls == 1 {
			state, cleanup = "queued", "pending"
		} else if f.polls == 2 {
			cleanup = "running"
		}
		value = map[string]any{"id": strings.Repeat("b", 32), "phase": map[bool]string{true: "queued", false: "ended"}[state == "queued"], "result": map[string]any{"termination": state, "conclusion": "passed"}, "cleanup": map[string]any{"state": cleanup}, "artifacts": []any{map[string]any{"id": strings.Repeat("e", 32), "name": "test.log"}}}
	case "execution_log":
		if f.polls == 1 {
			value, failed = map[string]any{"error": wire.Fail("not_found", "Log not created")}, true
			break
		}
		offset := int(args["offset"].(float64))
		if offset < 0 || offset > len(f.log) {
			return nil, fmt.Errorf("invalid requested offset")
		}
		next := min(offset+65536, len(f.log))
		value = map[string]any{"data": base64.StdEncoding.EncodeToString(f.log[offset:next]), "next_offset": next, "eof": next == len(f.log)}
	default:
		return nil, fmt.Errorf("unexpected tool %s", r.Params.Name)
	}
	if op, ok := value.(receipt); ok && r.Params.Name != "operation_get" {
		value = map[string]any{"operation": op, "wait": map[string]any{"reason": "satisfied"}}
	}
	if r.Params.Name == "wait" {
		if task, ok := value.(map[string]any); ok && task["phase"] != nil {
			value = map[string]any{"reason": "satisfied", "targets": []any{map[string]any{"summary": task}}, "continue": args["targets"]}
		}
	}
	return &mcp.CallToolResult{IsError: failed, StructuredContent: value, Content: []mcp.Content{&mcp.TextContent{Text: string(wire.JSON(value))}}}, nil
}

func newRunFixture(t *testing.T) (*runFixture, RunOptions, string) {
	t.Helper()
	f := &runFixture{available: true, cleanup: "complete", log: append(bytes.Repeat([]byte("log line\n"), 9000), []byte("assertions passed\n")...)}
	server := mcp.NewServer(&mcp.Implementation{Name: "synthetic", Version: "1"}, nil)
	for _, name := range []string{"service_get", "upload_create", "upload_get", "operation_get", "nix_job_create", "nix_job_get", "wait", "execution_log"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, f.tool)
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/files/") {
			if r.Method != "PUT" || r.Header.Get("Authorization") != "" {
				t.Error("unexpected upload method or bearer leakage")
			}
			_, _ = io.Copy(io.Discard, r.Body)
			f.mu.Lock()
			f.uploads++
			fail := f.failUpload
			f.mu.Unlock()
			if fail {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}
		if r.Header.Get("Authorization") != "Bearer unit-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	f.url = httpServer.URL
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake-nix")
	trace := filepath.Join(dir, "trace")
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	meta := Metadata{Derivation: "/nix/store/" + strings.Repeat("0", 32) + "-smoke.drv", System: "x86_64-linux", ForceAccel: true, Nodes: []string{"first", "second", "third"}, Containers: []string{}}
	script := "#!" + shell + "\nprintf '%s\\n' \"$1\" >> '" + trace + "'\ncase \"$1\" in\nflake) printf '%s\\n' '{\"resolvedUrl\":\"git+file:///synthetic/project\"}';;\neval) printf '%s\\n' '" + string(wire.JSON(meta)) + "';;\ncopy) vm_test_cache=${4#file://}; vm_test_cache=${vm_test_cache%%\\?*}; mkdir -p \"$vm_test_cache/nar\"; printf synthetic > \"$vm_test_cache/nar/fixture.nar.xz\";;\n*) exit 1;;\nesac\n"
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(filepath.Join(work, "inputs", strings.Repeat("c", 32), "cache", "nar"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "inputs", strings.Repeat("c", 32), "cache", "nar", "fixture.nar.xz"), []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	return f, RunOptions{Options: Options{Nix: exe, Test: ".#checks.x86_64-linux.smoke", WorkDir: work}, Endpoint: f.url + "/mcp", Token: "unit-token"}, trace
}

func TestAutomaticRunWaitsForCleanupAndResumesLostReply(t *testing.T) {
	f, options, trace := newRunFixture(t)
	f.loseReply = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := Run(ctx, options); err == nil {
		t.Fatal("expected lost submission reply")
	}
	cache := filepath.Join(options.WorkDir, "inputs", strings.Repeat("c", 32), "cache")
	if _, err := os.Stat(filepath.Join(cache, "nar", "fixture.nar.xz")); err != nil {
		t.Fatal("uncertain acceptance lost its input", err)
	}
	result, err := Run(ctx, options)
	if err != nil || !bytes.Contains(result, []byte(`"state":"complete"`)) {
		t.Fatal(string(result), err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("accepted job retained its local cache", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(cache), "submission.json")); err != nil {
		t.Fatal("accepted job lost its evaluated metadata", err)
	}
	f.mu.Lock()
	if f.creates != 1 || f.uploads != 3 || f.submits != 2 || f.polls < 3 {
		t.Fatalf("creates=%d uploads=%d submits=%d polls=%d", f.creates, f.uploads, f.submits, f.polls)
	}
	f.mu.Unlock()
	if data, err := os.ReadFile(trace); err != nil || string(data) != "eval\ncopy\n" {
		t.Fatal(string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(options.WorkDir, "task.log")); err != nil || !bytes.Equal(data, f.log) {
		t.Fatal("log offset handling lost or duplicated bytes", err)
	}
	journal, err := os.ReadFile(filepath.Join(options.WorkDir, "run.json"))
	if err != nil || bytes.Contains(journal, []byte("unit-token")) || bytes.Contains(journal, []byte("capability-secret")) {
		t.Fatal("credentials in journal", err)
	}
	// Observing an already completed run neither resubmits nor repeats log bytes.
	if _, err := Run(ctx, options); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(options.WorkDir, "task.log")); err != nil || !bytes.Equal(data, f.log) {
		t.Fatal("completed retry duplicated logs", err)
	}
	options.Test = ".#different-test"
	if _, err := Run(ctx, options); err == nil {
		t.Fatal("changed test reused a submission")
	}
}

func TestAutomaticRunReportsRequiredCleanup(t *testing.T) {
	f, options, _ := newRunFixture(t)
	f.cleanup = "blocked"
	result, err := Run(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "cleanup=blocked") || !bytes.Contains(result, []byte(`"state":"blocked"`)) {
		t.Fatal(string(result), err)
	}
	if _, err := os.Stat(filepath.Join(options.WorkDir, "result.json")); err != nil {
		t.Fatal(err)
	}
}

func TestUnavailableWorkerDoesNotAllocateOrEvaluate(t *testing.T) {
	f, options, trace := newRunFixture(t)
	f.available = false
	f.waitingReason = "worker_unavailable"
	if _, err := Run(context.Background(), options); err == nil || !strings.Contains(err.Error(), "worker_unavailable") {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.creates != 0 || f.submits != 0 || f.uploads != 0 {
		t.Fatal("unavailable worker allocated work")
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatal("unavailable worker evaluated Nix")
	}
}

func TestAutomaticRunQueuesWhileBackendBusy(t *testing.T) {
	for _, reason := range []string{"virtualbox_running", "worker_busy"} {
		t.Run(reason, func(t *testing.T) {
			f, options, _ := newRunFixture(t)
			f.available, f.waitingReason = false, reason
			result, err := Run(context.Background(), options)
			if err != nil || !bytes.Contains(result, []byte(`"state":"complete"`)) {
				t.Fatal(string(result), err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.creates != 1 || f.uploads != 3 || f.submits != 1 || f.polls < 3 {
				t.Fatalf("busy backend did not queue and finish: creates=%d uploads=%d submits=%d polls=%d", f.creates, f.uploads, f.submits, f.polls)
			}
		})
	}
}

func TestSmokeUsesNixProjectResolution(t *testing.T) {
	_, options, trace := newRunFixture(t)
	options.Test = ""
	options.SmokeProject = filepath.Join(t.TempDir(), "project")
	options.SmokeFile = filepath.Join(t.TempDir(), "vm-smoke.nix")
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(trace); err != nil || string(data) != "flake\neval\ncopy\n" {
		t.Fatal(string(data), err)
	}
}

func TestExpiredUnsubmittedInputGetsFreshReservation(t *testing.T) {
	f, options, trace := newRunFixture(t)
	f.expireInput = true
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if f.creates != 2 || f.submits != 1 {
		t.Fatal("expired reservation was submitted", f.creates, f.submits)
	}
	data, err := os.ReadFile(trace)
	if err != nil || string(data) != "eval\ncopy\n" {
		t.Fatal("evaluated before a usable input was reserved", string(data), err)
	}
}

func TestUploadFailureRetainsCacheUntilAccepted(t *testing.T) {
	f, options, _ := newRunFixture(t)
	f.failUpload = true
	if _, err := Run(context.Background(), options); err == nil {
		t.Fatal("upload unexpectedly completed")
	}
	cache := filepath.Join(options.WorkDir, "inputs", strings.Repeat("c", 32), "cache")
	if _, err := os.Stat(filepath.Join(cache, "nar", "fixture.nar.xz")); err != nil {
		t.Fatal("interrupted upload lost input", err)
	}
	f.mu.Lock()
	f.failUpload = false
	f.mu.Unlock()
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("accepted retry retained its input cache", err)
	}
}

func TestUnacceptedSubmissionReleasesCacheBeforeNewAttempt(t *testing.T) {
	f, options, trace := newRunFixture(t)
	f.rejectSubmission = true
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if f.creates != 2 || f.submits != 2 {
		t.Fatal("unaccepted input did not get a fresh attempt", f.creates, f.submits)
	}
	data, err := os.ReadFile(trace)
	if err != nil || string(data) != "eval\ncopy\neval\ncopy\n" {
		t.Fatal("retry did not regenerate its released cache", string(data), err)
	}
	for _, id := range []string{strings.Repeat("c", 32), fmt.Sprintf("%032x", 2)} {
		if _, err := os.Stat(filepath.Join(options.WorkDir, "inputs", id, "cache")); !os.IsNotExist(err) {
			t.Fatal("released attempt retained input cache", id, err)
		}
		if _, err := os.Stat(filepath.Join(options.WorkDir, "inputs", id, "submission.json")); err != nil {
			t.Fatal("attempt lost evaluated metadata", id, err)
		}
	}
}

func TestAcceptedRunRetriesLocalCleanupWithoutResubmitting(t *testing.T) {
	f, options, _ := newRunFixture(t)
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(options.WorkDir, "run.json")
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var record runRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	// Model interruption after the accepted identity and cleanup intent were synced.
	record.CleanupInputs = []string{record.CacheID}
	if err := os.WriteFile(journalPath, wire.JSON(record), 0600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(options.WorkDir, "inputs", record.CacheID, "cache")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "remaining"), []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(cache)
	if err := os.Chmod(parent, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0700) })
	var progress bytes.Buffer
	options.Progress = &progress
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal("cache deletion changed job result", err)
	}
	if !strings.Contains(progress.String(), "cleanup pending") {
		t.Fatal("failed local deletion was not reported", progress.String())
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("resumed run retained released input", err)
	}
	if f.submits != 1 {
		t.Fatal("cleanup retry resubmitted execution", f.submits)
	}
	data, err = os.ReadFile(journalPath)
	if err != nil || json.Unmarshal(data, &record) != nil {
		t.Fatal(err)
	}
	// The journal contains only unfinished cleanup work.
	var contents map[string]any
	if err := json.Unmarshal(data, &contents); err != nil || contents["cleanup_inputs"] != nil {
		t.Fatal("completed cache cleanup kept accumulating records", string(data), err)
	}
}
