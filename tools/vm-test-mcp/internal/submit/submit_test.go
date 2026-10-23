package submit

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

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
		if e != nil || result["derivation"] != drv {
			t.Fatal(result, e)
		}
	}
	data, e := os.ReadFile(trace)
	if e != nil || string(data) != "eval\ncopy\ncopy\n" {
		t.Fatal(string(data), e)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != 2 {
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
