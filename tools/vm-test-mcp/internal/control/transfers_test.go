package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func transferHTTP(t *testing.T, s *Server, method, url, data string, status int) string {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(method, url, strings.NewReader(data)))
	if w.Code != status {
		t.Fatalf("%s status=%d: %s", method, w.Code, w.Body.String())
	}
	return w.Body.String()
}
func readUpload(t *testing.T, s *Server, id string) map[string]any {
	t.Helper()
	v, e := s.Call(context.Background(), "upload_get", wire.JSON(TransferGetRequest{id}))
	if e != nil {
		t.Fatal(e)
	}
	return v.(map[string]any)
}
func newUpload(t *testing.T, s *Server, kind string) (string, string) {
	t.Helper()
	v := successful(t, s, "upload_create", TransferRequest{Change: Change{wire.ID()}, Kind: kind}).Result.(map[string]any)
	return v["id"].(string), v["url"].(string)
}
func declareUpload(t *testing.T, s *Server, id, url, drv string, objects map[string]string) {
	t.Helper()
	m := wire.UploadManifest{UploadID: id, Derivation: drv, Objects: []wire.UploadObject{}}
	if drv != "" {
		m.System = "x86_64-linux"
	}
	for name, data := range objects {
		hash := sha256.Sum256([]byte(data))
		m.Objects = append(m.Objects, wire.UploadObject{Name: name, Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])})
	}
	transferHTTP(t, s, "PUT", url+"/manifest", string(wire.JSON(m)), 200)
}
func fileUpload(t *testing.T, s *Server, body string) (string, string) {
	t.Helper()
	id, url := newUpload(t, s, "file")
	declareUpload(t, s, id, url, "", map[string]string{"blob": body})
	transferHTTP(t, s, "PUT", url+"/blob", body, 200)
	return id, url
}
func cacheObjects() (string, map[string]string) {
	drv := "/nix/store/" + strings.Repeat("0", 32) + "-fixture.drv"
	nar := "opaque NAR bytes validated by the importing Nix daemon"
	digest := sha256.Sum256([]byte(nar))
	hash := nixHash(hex.EncodeToString(digest[:]))
	info := fmt.Sprintf("StorePath: %s\nURL: nar/fixture.nar\nCompression: none\nFileHash: sha256:%s\nFileSize: %d\nNarHash: sha256:%s\nNarSize: %d\nReferences: \n", drv, hash, len(nar), hash, len(nar))
	return drv, map[string]string{"nix-cache-info": "StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 30\n", strings.Repeat("0", 32) + ".narinfo": info, "nar/fixture.nar": nar}
}
func TestManifestPublicationAutomaticReadinessAndRetry(t *testing.T) {
	s, _ := testServer(t)
	for _, body := range []string{"", "immutable input"} {
		id, url := fileUpload(t, s, body)
		if readUpload(t, s, id)["state"] != "ready" {
			t.Fatal("all manifest objects did not publish readiness")
		}
		declareUpload(t, s, id, url, "", map[string]string{"blob": body})
		transferHTTP(t, s, "PUT", url+"/blob", body, 200)
		transferHTTP(t, s, "PUT", url+"/blob", "changed", 409)
		if got := transferHTTP(t, s, "GET", url+"/blob", "", 200); got != body {
			t.Fatal("retry changed immutable input")
		}
	}
}
func TestCacheAdmissionRollbackKeepsVerifiedInput(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, url := newUpload(t, s, "cache")
	drv, objects := cacheObjects()
	declareUpload(t, s, id, url, drv, objects)
	for name, data := range objects {
		transferHTTP(t, s, "PUT", url+"/"+name, data, 200)
	}
	if readUpload(t, s, id)["state"] != "ready" {
		t.Fatal("cache was not verified")
	}
	if _, err = s.db.Exec(`CREATE TRIGGER reject_execution BEFORE INSERT ON executions BEGIN SELECT RAISE(ABORT,'synthetic admission failure'); END`); err != nil {
		t.Fatal(err)
	}
	args := NixRequest{Change: Change{"attach"}, CacheID: id}
	if _, err = s.accept(context.Background(), "nix_job_create", wire.JSON(args)); err == nil {
		t.Fatal("admission unexpectedly succeeded")
	}
	view := readUpload(t, s, id)
	if view["state"] != "ready" || view["execution_id"] != "" {
		t.Fatal("rollback lost reusable input", view)
	}
	if _, err = s.db.Exec("DROP TRIGGER reject_execution"); err != nil {
		t.Fatal(err)
	}
	accepted := successful(t, s, "nix_job_create", args)
	if accepted.TaskID == "" {
		t.Fatal("job identity not committed")
	}
	again := successful(t, s, "nix_job_create", args)
	if again.ID != accepted.ID {
		t.Fatal("duplicate admission")
	}
}
func TestQueuedInputsReuseAndRetention(t *testing.T) {
	s, f := testServer(t)
	active := createLab(t, s, "occupied")
	id, url := fileUpload(t, s, "complete original")
	op := successful(t, s, "lab_create", LabRequest{RequestID: "queued-input", Nodes: []NodeRequest{{Name: "one", Template: "windows"}}, UploadIDs: []string{id}})
	labID := op.TaskID
	if readUpload(t, s, id)["execution_id"] != labID {
		t.Fatal("admission did not bind input")
	}
	// A repeated immutable PUT is still a writer that cleanup must settle.
	rd, wr := io.Pipe()
	defer rd.Close()
	defer wr.Close()
	done := make(chan struct{})
	response := httptest.NewRecorder()
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(response, httptest.NewRequest("PUT", url+"/blob", rd))
	}()
	if _, err := wr.Write([]byte("complete")); err != nil {
		t.Fatal(err)
	}
	successful(t, s, "lab_cancel", TaskChange{Change: Change{"cancel-input"}, TaskID: TaskID{labID}})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup left duplicate upload blocked")
	}
	waitFor(t, func() bool { return taskView(t, s, labID)["cleanup"].(map[string]any)["state"] == "complete" })
	if got := transferHTTP(t, s, "GET", url+"/blob", "", 200); got != "complete original" {
		t.Fatal(got)
	}
	if readUpload(t, s, id)["state"] != "retained" {
		t.Fatal("input category did not follow cleanup")
	}
	f.mu.Lock()
	created, deleted := len(f.created), len(f.deleted)
	f.mu.Unlock()
	if created != 3 || deleted != 0 || taskView(t, s, active.ID)["phase"] != "running" {
		t.Fatal("queued input touched active VMs")
	}
	successful(t, s, "execution_delete", ExecutionChange{Change: Change{"delete-input"}, ExecutionID: labID})
	transferHTTP(t, s, "GET", url+"/blob", "", 404)
}
func TestUploadLockAndDeleteConvergeOnCancellation(t *testing.T) {
	s, _ := testServer(t)
	id, url := newUpload(t, s, "file")
	declareUpload(t, s, id, url, "", map[string]string{"blob": "first payload"})
	rd, wr := io.Pipe()
	defer rd.Close()
	defer wr.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", url+"/blob", rd))
	}()
	if _, err := wr.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest("PUT", url+"/blob", strings.NewReader("first payload")).WithContext(ctx))
	if response.Code != 408 {
		t.Fatal("upload lock ignored observation deadline", response.Code)
	}
	successful(t, s, "upload_delete", UploadDeleteRequest{Change: Change{"delete"}, TransferGetRequest: TransferGetRequest{id}})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("delete did not settle writer")
	}
	if _, err := os.Stat(filepath.Join(s.root.Path, "transfers", id)); !os.IsNotExist(err) {
		t.Fatal("deleted upload still occupies storage", err)
	}
}
func TestManifestReservesTotalBudgetBeforeWriting(t *testing.T) {
	s, _ := testServer(t)
	s.cfg.Storage.InputBytes = 12
	s.cfg.Storage.MaxUploadBytes = 8
	first, url := newUpload(t, s, "file")
	declareUpload(t, s, first, url, "", map[string]string{"blob": "12345678"})
	second, next := newUpload(t, s, "file")
	hash := sha256.Sum256([]byte("12345"))
	m := wire.UploadManifest{UploadID: second, Objects: []wire.UploadObject{{Name: "blob", Size: 5, SHA256: hex.EncodeToString(hash[:])}}}
	transferHTTP(t, s, "PUT", next+"/manifest", string(wire.JSON(m)), 409)
	transferHTTP(t, s, "PUT", url+"/blob", "12345678", 200)
	successful(t, s, "upload_delete", UploadDeleteRequest{Change: Change{"release-reservation"}, TransferGetRequest: TransferGetRequest{first}})
	transferHTTP(t, s, "PUT", next+"/manifest", string(wire.JSON(m)), 200)
}
func TestUploadPublicationFailureAndRestart(t *testing.T) {
	for _, boundary := range []string{"statement", "commit", "ready"} {
		t.Run(boundary, func(t *testing.T) {
			cfg := testConfig(t)
			s, err := New(cfg, &fakeBackend{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			id, url := newUpload(t, s, "file")
			declareUpload(t, s, id, url, "", map[string]string{"blob": "durable bytes"})
			statement := `CREATE TRIGGER reject_publish BEFORE UPDATE ON transfer_objects BEGIN SELECT RAISE(ABORT,'synthetic failure'); END;`
			if boundary == "commit" {
				statement = `CREATE TABLE parent_fixture(id INTEGER PRIMARY KEY); CREATE TABLE child_fixture(parent INTEGER REFERENCES parent_fixture(id) DEFERRABLE INITIALLY DEFERRED); CREATE TRIGGER reject_publish AFTER UPDATE ON transfer_objects BEGIN INSERT INTO child_fixture VALUES(1); END;`
			}
			if boundary == "ready" {
				statement = `CREATE TRIGGER reject_publish BEFORE UPDATE ON transfers WHEN json_extract(new.body,'$.state')='ready' BEGIN SELECT RAISE(ABORT,'synthetic readiness failure'); END;`
			}
			if _, err = s.db.Exec(statement); err != nil {
				t.Fatal(err)
			}
			transferHTTP(t, s, "PUT", url+"/blob", "durable bytes", 409)
			if _, err = s.db.Exec("DROP TRIGGER reject_publish"); err != nil {
				t.Fatal(err)
			}
			// Include a file that reached disk but not its immutable publication index.
			orphan := "transfers/" + id + "/.object-" + wire.ID()
			if _, _, err = s.root.Write(orphan, strings.NewReader("unpublished"), 100); err != nil {
				t.Fatal(err)
			}
			s.Close()
			s, err = New(cfg, &fakeBackend{})
			if err != nil {
				t.Fatal(err)
			}
			transferHTTP(t, s, "PUT", url+"/blob", "durable bytes", 200)
			if readUpload(t, s, id)["state"] != "ready" {
				t.Fatal("recovery did not converge")
			}
			if got := transferHTTP(t, s, "GET", url+"/blob", "", 200); got != "durable bytes" {
				t.Fatal(got)
			}
			if _, err = os.Stat(filepath.Join(s.root.Path, orphan)); !os.IsNotExist(err) {
				t.Fatal("unreferenced publication survived recovery")
			}
		})
	}
}
func TestUploadInventorySQLCursorSurvivesDeletion(t *testing.T) {
	s, _ := testServer(t)
	id, _ := newUpload(t, s, "cache")
	for i := range 12 {
		if _, err := s.db.Exec("INSERT INTO transfer_objects(transfer_id,name,expected_size,expected_sha256) VALUES(?,?,0,?)", id, fmt.Sprintf("%032x.narinfo", i), strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
	}
	v := readUpload(t, s, id)
	if len(wire.JSON(v)) > 4096 || v["objects"] != nil {
		t.Fatal("summary included inventory")
	}
	page, err := s.Call(context.Background(), "upload_objects", wire.JSON(UploadObjectsRequest{TransferGetRequest: TransferGetRequest{id}, Limit: 2}))
	if err != nil {
		t.Fatal(err)
	}
	cursor := page.(map[string]any)["next_cursor"].(string)
	if _, err = s.db.Exec("DELETE FROM transfer_objects WHERE transfer_id=? AND name<=?", id, cursor); err != nil {
		t.Fatal(err)
	}
	page, err = s.Call(context.Background(), "upload_objects", wire.JSON(UploadObjectsRequest{TransferGetRequest: TransferGetRequest{id}, Cursor: cursor, Limit: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.(map[string]any)["items"].([]any)) != 2 {
		t.Fatal("cursor depended on previous rows")
	}
}
