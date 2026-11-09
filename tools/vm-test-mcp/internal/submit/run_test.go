package submit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	manifest         wire.UploadManifest
	objects          map[string][]byte
	ranges           []string
	failDownload     bool
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
		value = map[string]any{"protocol": wire.CurrentProtocol(), "backends": map[string]any{"nixos": map[string]any{"available": f.available, "waiting_reason": f.waitingReason}}}
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
		value = map[string]any{"id": strings.Repeat("b", 32), "phase": map[bool]string{true: "queued", false: "ended"}[state == "queued"], "result": map[string]any{"termination": state, "conclusion": "passed"}, "resources": map[string]any{"state": map[string]string{"complete": "released", "blocked": "blocked", "running": "releasing", "pending": "unallocated"}[cleanup]}, "evidence": map[string]any{"state": map[string]string{"complete": "complete", "blocked": "pending", "running": "pending", "pending": "pending"}[cleanup]}, "artifacts": []any{map[string]any{"id": strings.Repeat("e", 32), "name": "test.log"}}}
	case "evidence_list":
		items := []any{}
		if args["cursor"].(float64) == 0 {
			hash := sha256.Sum256(f.log)
			items = append(items, map[string]any{"id": strings.Repeat("e", 32), "name": "test.log", "size": len(f.log), "sha256": hex.EncodeToString(hash[:])})
		}
		value = map[string]any{"items": items, "next_cursor": 1}
	case "evidence_get":
		hash := sha256.Sum256(f.log)
		value = map[string]any{"url": f.url + "/files/evidence-secret", "size": len(f.log), "sha256": hex.EncodeToString(hash[:])}
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
	for _, name := range []string{"service_get", "upload_create", "upload_get", "operation_get", "nix_job_create", "nix_job_get", "wait", "execution_log", "evidence_list", "evidence_get"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, f.tool)
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/files/") {
			if r.Header.Get("Authorization") != "" {
				t.Error("bearer leaked to scoped file URL")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if r.URL.Path == "/files/evidence-secret" {
				f.ranges = append(f.ranges, r.Header.Get("Range"))
				if f.failDownload {
					w.WriteHeader(503)
					return
				}
				http.ServeContent(w, r, "evidence", time.Time{}, bytes.NewReader(f.log))
				return
			}
			if strings.HasSuffix(r.URL.Path, "/missing") {
				items := []wire.UploadObject{}
				for _, obj := range f.manifest.Objects {
					if _, ok := f.objects[obj.SHA256]; !ok && obj.Name > r.URL.Query().Get("cursor") {
						items = append(items, obj)
					}
				}
				slices.SortFunc(items, func(a, b wire.UploadObject) int { return strings.Compare(a.Name, b.Name) })
				cursor := r.URL.Query().Get("cursor")
				if len(items) > 0 {
					cursor = items[len(items)-1].Name
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "next_cursor": cursor})
				return
			}
			if r.Method != "PUT" {
				t.Error("unexpected file method", r.Method)
			}
			f.uploads++
			if f.failUpload {
				w.WriteHeader(503)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/manifest") {
				if err := json.NewDecoder(r.Body).Decode(&f.manifest); err != nil {
					t.Error(err)
					w.WriteHeader(400)
				}
				return
			}
			data, _ := io.ReadAll(r.Body)
			hash := sha256.Sum256(data)
			if f.objects == nil {
				f.objects = map[string][]byte{}
			}
			f.objects[hex.EncodeToString(hash[:])] = data
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
	meta := Metadata{Derivation: "/nix/store/" + strings.Repeat("0", 32) + "-smoke.drv", System: "x86_64-linux", ForceAccel: true, Nodes: []string{"first", "second", "third"}, Containers: []string{}}
	exe, trace := fakeNix(t, dir, meta)
	return f, RunOptions{Options: Options{Nix: exe, Test: ".#checks.x86_64-linux.smoke", WorkDir: filepath.Join(dir, "work"), CacheDir: filepath.Join(dir, "export-cache")}, Endpoint: f.url + "/mcp", Token: "unit-token"}, trace
}

func TestAutomaticRunWaitsForCleanupAndResumesLostReply(t *testing.T) {
	f, options, trace := newRunFixture(t)
	f.loseReply = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := Run(ctx, options); err == nil {
		t.Fatal("expected lost submission reply")
	}
	cache := filepath.Join(options.CacheDir, "files")
	if _, err := os.Stat(filepath.Join(cache, "nar", "fixture.nar.xz")); err != nil {
		t.Fatal("uncertain acceptance lost its input", err)
	}
	result, err := Run(ctx, options)
	if err != nil || !bytes.Contains(result, []byte(`"state":"complete"`)) {
		t.Fatal(string(result), err)
	}
	if _, err := os.Lstat(filepath.Join(options.WorkDir, "inputs", "derivation")); !os.IsNotExist(err) {
		t.Fatal("accepted job retained its GC root", err)
	}
	if _, err := os.Stat(filepath.Join(options.WorkDir, "inputs", "submission.json")); err != nil {
		t.Fatal("accepted job lost its evaluated metadata", err)
	}
	f.mu.Lock()
	if f.creates != 1 || f.uploads != 4 || f.submits != 2 || f.polls < 3 {
		t.Fatalf("creates=%d uploads=%d submits=%d polls=%d", f.creates, f.uploads, f.submits, f.polls)
	}
	f.mu.Unlock()
	if data, err := os.ReadFile(trace); err != nil || string(data) != "eval\nbuild\npath-info\ncopy\n" {
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
	if err == nil || !strings.Contains(err.Error(), "resources=blocked") || !bytes.Contains(result, []byte(`"state":"blocked"`)) {
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
			if f.creates != 1 || f.uploads != 4 || f.submits != 1 || f.polls < 3 {
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
	if data, err := os.ReadFile(trace); err != nil || string(data) != "flake\neval\nbuild\npath-info\ncopy\n" {
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
	if err != nil || string(data) != "eval\nbuild\npath-info\ncopy\n" {
		t.Fatal("evaluated before a usable input was reserved", string(data), err)
	}
}

func TestUploadFailureRetainsCacheUntilAccepted(t *testing.T) {
	f, options, _ := newRunFixture(t)
	f.failUpload = true
	if _, err := Run(context.Background(), options); err == nil {
		t.Fatal("upload unexpectedly completed")
	}
	cache := filepath.Join(options.CacheDir, "files")
	if _, err := os.Stat(filepath.Join(cache, "nar", "fixture.nar.xz")); err != nil {
		t.Fatal("interrupted upload lost input", err)
	}
	f.mu.Lock()
	f.failUpload = false
	f.mu.Unlock()
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(options.WorkDir, "inputs", "derivation")); !os.IsNotExist(err) {
		t.Fatal("accepted retry retained its GC root", err)
	}
}

func TestUnacceptedSubmissionKeepsEvaluatedDerivation(t *testing.T) {
	f, options, trace := newRunFixture(t)
	f.rejectSubmission = true
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if f.creates != 2 || f.submits != 2 {
		t.Fatal("unaccepted input did not get a fresh reservation", f.creates, f.submits)
	}
	data, err := os.ReadFile(trace)
	if err != nil || string(data) != "eval\nbuild\npath-info\ncopy\npath-info\ncopy\n" {
		t.Fatal("retry changed the evaluation", string(data), err)
	}
	if f.uploads != 5 {
		t.Fatal("second reservation reuploaded shared bytes", f.uploads)
	}
	if _, err := os.Stat(filepath.Join(options.WorkDir, "inputs", "submission.json")); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptedRunRetriesLocalCleanupAndDownloads(t *testing.T) {
	f, options, _ := newRunFixture(t)
	f.failDownload = true
	if _, err := Run(context.Background(), options); err == nil {
		t.Fatal("download unexpectedly completed")
	}
	journalPath := filepath.Join(options.WorkDir, "run.json")
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var record runRecord
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record.ReleaseInputs = true
	if err = os.WriteFile(journalPath, wire.JSON(record), 0600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(options.WorkDir, "inputs")
	if err = os.Symlink("/nix/store/"+strings.Repeat("0", 32)+"-smoke.drv", filepath.Join(input, "derivation")); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(input, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(input, 0700) })
	var progress bytes.Buffer
	options.Progress = &progress
	options.Evidence = "manifest"
	if _, err = Run(context.Background(), options); err != nil {
		t.Fatal("root cleanup changed manifest result", err)
	}
	if !strings.Contains(progress.String(), "cleanup pending") {
		t.Fatal("failed local deletion was not reported", progress.String())
	}
	if err = os.Chmod(input, 0700); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.failDownload = false
	f.mu.Unlock()
	// Resume an interrupted artifact body using its manifest identity and HTTP range.
	artifact := filepath.Join(options.WorkDir, "artifacts", strings.Repeat("e", 32))
	if err = os.WriteFile(artifact+".partial", f.log[:1234], 0600); err != nil {
		t.Fatal(err)
	}
	options.Evidence = "all"
	if _, err = Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if data, err = os.ReadFile(artifact); err != nil || !bytes.Equal(data, f.log) {
		t.Fatal("artifact was not completed and verified", err)
	}
	if _, err = os.Lstat(filepath.Join(input, "derivation")); !os.IsNotExist(err) {
		t.Fatal("resumed run retained released root", err)
	}
	if f.submits != 1 || f.ranges[len(f.ranges)-1] != "bytes=1234-" {
		t.Fatal("resume changed remote execution or discarded partial download", f.submits, f.ranges)
	}
	if _, err = Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if len(f.ranges) != 2 {
		t.Fatal("completed artifact was downloaded again", f.ranges)
	}
	data, err = os.ReadFile(journalPath)
	record = runRecord{}
	if err != nil || json.Unmarshal(data, &record) != nil || record.ReleaseInputs {
		t.Fatal("cleanup journal did not settle", err)
	}
}

func fakeNix(t *testing.T, dir string, meta Metadata) (string, string) {
	t.Helper()
	exe, trace := filepath.Join(dir, "fake-nix"), filepath.Join(dir, "trace")
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	info := strings.SplitN(filepath.Base(meta.Derivation), "-", 2)[0] + ".narinfo"
	script := "#!" + shell + "\nprintf '%s\\n' \"$1\" >> '" + trace + "'\ncase \"$1\" in\n" +
		"flake) printf '%s\\n' '{\"resolvedUrl\":\"git+file:///synthetic/project\"}';;\n" +
		"eval) printf '%s\\n' '" + string(wire.JSON(meta)) + "';;\n" +
		"build) ln -sfn \"$5\" \"$4\";;\n" +
		"path-info) printf '%s\\n' '" + meta.Derivation + "';;\n" +
		"copy) vm_test_cache=${4#file://}; vm_test_cache=${vm_test_cache%%\\?*}; mkdir -p \"$vm_test_cache/nar\"; printf synthetic > \"$vm_test_cache/nar/fixture.nar.xz\"; printf 'URL: nar/fixture.nar.xz\\n' > \"$vm_test_cache/" + info + "\";;\n*) exit 1;;\nesac\n"
	if err = os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return exe, trace
}
