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
	"sync"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

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
	dir := t.TempDir()
	exe := filepath.Join(dir, "fake-nix")
	trace := filepath.Join(dir, "trace")
	shell, e := exec.LookPath("sh")
	if e != nil {
		t.Fatal(e)
	}
	drv := "/nix/store/" + strings.Repeat("0", 32) + "-test-run.drv"
	meta := Metadata{Derivation: drv, System: "x86_64-linux", ForceAccel: true, Nodes: []string{"one", "two"}, Containers: []string{}}
	script := "#!" + shell + "\nprintf '%s\\n' \"$1\" >> '" + trace + "'\ncase \"$1\" in\neval) printf '%s\\n' '" + string(wire.JSON(meta)) + "';;\ncopy) exit 0;;\n*) exit 1;;\nesac\n"
	if e = os.WriteFile(exe, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	cache := filepath.Join(dir, "cache")
	if e = os.MkdirAll(filepath.Join(cache, "nar"), 0700); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(cache, "nix-cache-info"), []byte("StoreDir: /nix/store\n"), 0600)
	os.WriteFile(filepath.Join(cache, strings.Repeat("0", 32)+".narinfo"), []byte("synthetic narinfo"), 0600)
	os.WriteFile(filepath.Join(cache, "nar", "test.nar.xz"), []byte("synthetic NAR"), 0600)
	var mu sync.Mutex
	uploads := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Error(r.Method)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		uploads[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer server.Close()
	o := Options{Nix: exe, Test: ".#checks.x86_64-linux.test", RequestID: "same-run", CacheID: wire.ID(), CacheURL: server.URL + "/files/synthetic-token", WorkDir: dir}
	for range 2 {
		result, e := Prepare(context.Background(), o)
		if e != nil || result["upload_id"] != o.CacheID {
			t.Fatal(result, e)
		}
	}
	data, e := os.ReadFile(trace)
	if e != nil || string(data) != "eval\ncopy\ncopy\n" {
		t.Fatal(string(data), e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != 4 {
		t.Fatal(uploads)
	}
	for _, count := range uploads {
		if count != 2 {
			t.Fatal(uploads)
		}
	}
	o.RequestID = "other-run"
	if _, e = Prepare(context.Background(), o); e == nil {
		t.Fatal("submission identity replaced")
	}
}
