package submit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestGeneratedDriverLifecycle(t *testing.T) {
	if os.Getenv("VM_TEST_DRIVER_INTEGRATION") != "1" {
		t.Skip("enabled by the vm-test-submit package check with Nix and the locked driver")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nix", "--extra-experimental-features", "nix-command", "eval", "--store", "dummy://", "--raw", "--expr", nixString(driverObserver))
	generated, err := cmd.Output()
	if err != nil {
		t.Fatal("evaluate generated driver observer", err)
	}
	observer := t.TempDir()
	if err := os.WriteFile(filepath.Join(observer, "sitecustomize.py"), generated, 0600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(ctx, "python3", "-B", "../../tests/test_driver_observer.py")
	cmd.Env = append(os.Environ(), "VM_TEST_OBSERVER="+observer)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated observer lifecycle: %v\n%s", err, output)
	}
}

func TestCacheUploadReportsServerRejectionAndBoundsResponse(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input.nar")
	if err := os.WriteFile(file, []byte("cache input"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, response string
		structured     bool
	}{
		{"structured", `{"error":{"code":"invalid_path","message":"Publication requires the same directory","details":{"stage":"publication"}}}`, true},
		{"malformed", "upstream error: synthetic-capability", false},
		{"oversized", strings.Repeat("x", 65537), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method != "PUT" || string(body) != "cache input" {
					t.Error("upload bytes changed", r.Method, string(body), err)
				}
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, tc.response)
			}))
			defer server.Close()
			err := uploadCacheObject(context.Background(), server.Client(), server.URL+"/files/synthetic-capability", "nar/input.nar", file)
			if err == nil || !strings.Contains(err.Error(), "nar/input.nar") || !strings.Contains(err.Error(), "409") || strings.Contains(err.Error(), "synthetic-capability") {
				t.Fatal("missing object/status or leaked capability", err)
			}
			var failure *wire.Error
			if errors.As(err, &failure) != tc.structured {
				t.Fatal("invalid response accepted as a server error", err)
			}
			if tc.structured && (failure.Code != "invalid_path" || failure.Details["stage"] != "publication" || failure.Details["boundary"] != "cache_upload" || failure.Details["object"] != "nar/input.nar" || failure.Details["http_status"] != 409) {
				t.Fatal("server rejection lost its cause or boundary", failure)
			}
		})
	}
}

type uploadFailureTransport struct{}

func (uploadFailureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, &url.Error{Op: "Put", URL: r.URL.String(), Err: context.DeadlineExceeded}
}

func TestCacheUploadTimeoutOmitsCapability(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input.nar")
	if err := os.WriteFile(file, []byte("cache input"), 0600); err != nil {
		t.Fatal(err)
	}
	err := uploadCacheObject(context.Background(), &http.Client{Transport: uploadFailureTransport{}}, "https://fixture.invalid/files/synthetic-capability", "nar/input.nar", file)
	var failure *wire.Error
	if !errors.As(err, &failure) || failure.Code != "backend_timeout" || strings.Contains(err.Error(), "synthetic-capability") || !strings.Contains(err.Error(), "nar/input.nar") {
		t.Fatal("upload timeout lost its classification or disclosed the URL", err)
	}
}

func TestRetryReusesEvaluationAndUploadsStandardCache(t *testing.T) {
	f, run, trace := newRunFixture(t)
	o := run.Options
	o.RequestID = "manual-run"
	o.CacheID = wire.ID()
	o.CacheURL = f.url + "/files/capability-secret"
	for range 2 {
		result, err := Prepare(context.Background(), o)
		if err != nil || result["upload_id"] != o.CacheID {
			t.Fatal(result, err)
		}
		o.CacheID = wire.ID()
	}
	data, err := os.ReadFile(trace)
	if err != nil || string(data) != "eval\nbuild\npath-info\ncopy\npath-info\ncopy\n" {
		t.Fatal(string(data), err)
	}
	f.mu.Lock()
	uploads := f.uploads
	f.mu.Unlock()
	if uploads != 5 {
		t.Fatal("second manifest did not reuse verified objects", uploads)
	}
	if _, err = os.Lstat(filepath.Join(o.WorkDir, "derivation")); err != nil {
		t.Fatal("manual preparation lost its GC root", err)
	}
	o.RequestID = "other-run"
	if _, err = Prepare(context.Background(), o); err == nil {
		t.Fatal("submission identity replaced")
	}
}
