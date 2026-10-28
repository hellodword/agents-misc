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

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func TestLargeUploadRenewalRetainsCapabilityWithoutEmbeddingInventory(t *testing.T) {
	s, _ := testServer(t)
	created := successful(t, s, "upload_create", TransferRequest{Change: Change{"large-cache"}, Kind: "cache"})
	id := created.Result.(map[string]any)["id"].(string)
	s.mu.Lock()
	for i := range 12000 {
		s.transfers[id].Objects[fmt.Sprintf("%032x.narinfo", i)] = state.TransferObject{Size: 42, SHA256: strings.Repeat("a", 64)}
	}
	s.mu.Unlock()
	renewed := successful(t, s, "upload_renew", RenewRequest{Change: Change{"renew-large-cache"}, TransferGetRequest: TransferGetRequest{TransferID: id}})
	value := renewed.Result.(map[string]any)
	if value["url"] == "" || value["url"] == nil || value["objects"] != nil || len(wire.JSON(value)) > 4096 {
		t.Fatal("renewal receipt lost its capability or embedded inventory")
	}
	page, err := s.Call(context.Background(), "upload_objects", wire.JSON(UploadObjectsRequest{TransferGetRequest: TransferGetRequest{TransferID: id}, ListRequest: ListRequest{Limit: 2}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(page.(map[string]any)["items"].([]any)) != 2 {
		t.Fatal("inventory did not respect its explicit page boundary")
	}
}

func TestCacheUploadPublishesNestedObjectsAndSeals(t *testing.T) {
	s, _ := testServer(t)
	v := successful(t, s, "upload_create", TransferRequest{Change: Change{"nested-cache"}, Kind: "cache"}).Result.(map[string]any)
	id, url := v["id"].(string), v["url"].(string)
	nar := "opaque NAR bytes; the Nix daemon validates archive structure"
	digest := sha256.Sum256([]byte(nar))
	hash := hex.EncodeToString(digest[:])
	name := strings.Repeat("0", 32) + ".narinfo"
	info := fmt.Sprintf("StorePath: /nix/store/%s-fixture.drv\nURL: nar/fixture.nar\nCompression: none\nFileHash: sha256:%s\nFileSize: %d\nNarHash: sha256:%s\nNarSize: %d\nReferences: \n", strings.Repeat("0", 32), nixHash(hash), len(nar), nixHash(hash), len(nar))
	for _, object := range []struct{ name, data string }{{name, info}, {"nar/fixture.nar", nar}} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", url+"/"+object.name, strings.NewReader(object.data)))
		if w.Code != 200 {
			t.Fatal(object.name, w.Code, w.Body.String())
		}
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", url+"/"+object.name, nil))
		if w.Code != 200 || w.Body.String() != object.data {
			t.Fatal("published bytes differ", object.name, w.Code, w.Body.String())
		}
	}
	// Sealing and attachment are separate durable steps. Failure to create a
	// task must leave the sealed input available to a subsequent submission.
	requestID := "cache-attachment-write-failure"
	s.mu.Lock()
	s.requests[requestID] = &Operation{Created: time.Now().UTC(), TaskID: wire.ID()}
	s.mu.Unlock()
	if _, err := s.db.Exec(`CREATE TRIGGER reject_task BEFORE INSERT ON executions BEGIN SELECT RAISE(ABORT,'synthetic task write failure'); END;`); err != nil {
		t.Fatal(err)
	}
	_, err := s.createNix(NixRequest{Change: Change{requestID}, CacheID: id, Derivation: "/nix/store/" + strings.Repeat("0", 32) + "-fixture.drv", System: "x86_64-linux"})
	if err == nil {
		t.Fatal("task write unexpectedly succeeded")
	}
	s.mu.Lock()
	attached := s.transfers[id].TaskID
	s.mu.Unlock()
	var retained Transfer
	if err := s.db.Load(context.Background(), "transfer", id, &retained); err != nil || attached != "" || retained.TaskID != "" || retained.State != "sealed" {
		t.Fatal("failed submission consumed the cache", attached, retained, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_task"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	object := s.transfers[id].Objects["nar/fixture.nar"]
	err = s.seal(s.transfers[id])
	sealed := s.transfers[id].State
	s.mu.Unlock()
	if err != nil || sealed != "sealed" || object.SHA256 != hash || object.Size != int64(len(nar)) {
		t.Fatal("cache integrity/sealing failed", object, sealed, err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", url+"/nar/fixture.nar", strings.NewReader("late replacement")))
	if w.Code != 409 {
		t.Fatal("sealed cache changed", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", url+"/nar/fixture.nar", nil))
	if w.Code != 200 || w.Body.String() != nar {
		t.Fatal("sealed cache lost its original bytes", w.Code, w.Body.String())
	}
}

func queuedUpload(t *testing.T, s *Server) (string, string, string, string) {
	t.Helper()
	op := successful(t, s, "lab_create", LabRequest{RequestID: "upload-queued", Nodes: []NodeRequest{{Name: "queued", Template: "windows"}}})
	v := op.Result.(map[string]any)
	id := v["id"].(string)
	node := taskView(t, s, id)["nodes"].([]any)[0].(map[string]any)["id"].(string)
	op = successful(t, s, "upload_create", TransferRequest{Change: Change{"prepare-upload"}, Kind: "file", TaskID: id})
	v = op.Result.(map[string]any)
	return id, node, v["id"].(string), v["url"].(string)
}

func TestQueuedFilePreparationAndRetainedInputs(t *testing.T) {
	s, f := testServer(t)
	active := createLab(t, s, "active-upload")
	id, node, transfer, url := queuedUpload(t, s)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", url, strings.NewReader("prepared input")))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	op := operation(t, s, "guest_file_put", PutRequest{NodeChange: NodeChange{Change: Change{"too-early"}, NodeID: NodeID{TaskID: TaskID{id}, NodeID: node}}, TransferID: transfer, Path: `C:\input`})
	if op.Error == nil || op.Error.Code != "invalid_state" {
		t.Fatal(op)
	}
	s.mu.Lock()
	if s.transfers[transfer].State != "open" {
		t.Fatal("premature delivery sealed input")
	}
	hash := s.transfers[transfer].Objects["blob"].SHA256
	s.mu.Unlock()
	successful(t, s, "lab_cancel", TaskChange{Change: Change{"cancel-upload-queue"}, TaskID: TaskID{id}})
	if taskView(t, s, id)["cleanup"].(map[string]any)["state"] != "complete" {
		t.Fatal("queued cancellation failed")
	}
	metadata, e := s.accept(context.Background(), "upload_get", wire.JSON(TransferGetRequest{TransferID: transfer}))
	if e != nil || metadata.(map[string]any)["can_upload"] != false || metadata.(map[string]any)["state"] != "retained" {
		t.Fatal(metadata, e)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", url, strings.NewReader("late write")))
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	renewed := successful(t, s, "upload_renew", RenewRequest{Change: Change{"renew-retained"}, TransferGetRequest: TransferGetRequest{TransferID: transfer}})
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", renewed.Result.(map[string]any)["url"].(string), nil))
	if w.Code != 200 || w.Body.String() != "prepared input" {
		t.Fatal(w.Code, w.Body.String())
	}
	s.mu.Lock()
	if s.transfers[transfer].Objects["blob"].SHA256 != hash {
		t.Fatal("retained identity changed")
	}
	s.mu.Unlock()
	f.mu.Lock()
	created, deleted := len(f.created), len(f.deleted)
	f.mu.Unlock()
	if created != 3 || deleted != 0 || taskView(t, s, active.ID)["phase"] != "running" {
		t.Fatal("upload/cancel touched occupied executor", created, deleted)
	}
	successful(t, s, "execution_delete", TaskChange{Change: Change{"purge-upload-queue"}, TaskID: TaskID{id}})
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	if w.Code != 404 {
		t.Fatal("purged input still available", w.Code)
	}
}

func TestQueuedCancellationInterruptsUploadAndKeepsPreviousPublication(t *testing.T) {
	s, _ := testServer(t)
	_ = createLab(t, s, "occupied-upload")
	id, _, transfer, url := queuedUpload(t, s)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", url, strings.NewReader("complete original")))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	rd, wr := io.Pipe()
	defer wr.Close()
	defer rd.Close()
	w = httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", url, rd)) }()
	if _, err := wr.Write([]byte("partial replacement")); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	successful(t, s, "lab_cancel", TaskChange{Change: Change{"interrupt-upload"}, TaskID: TaskID{id}})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel left body read blocked")
	}
	if w.Code == 200 {
		t.Fatal("interrupted replacement published")
	}
	waitFor(t, func() bool { return taskView(t, s, id)["cleanup"].(map[string]any)["state"] == "complete" })
	if time.Since(started) > time.Second {
		t.Fatal("cleanup failed to converge")
	}
	s.mu.Lock()
	object := s.transfers[transfer].Objects["blob"]
	s.mu.Unlock()
	raw, err := os.ReadFile(filepath.Join(s.root.Path, "transfers", transfer, object.File))
	if err != nil || string(raw) != "complete original" {
		t.Fatal("cancellation corrupted prior bytes", string(raw), err)
	}
	files, err := os.ReadDir(filepath.Join(s.root.Path, "transfers", transfer))
	if err != nil || len(files) != 1 || files[0].Name() != object.File {
		t.Fatal("unfinished upload files retained", files, err)
	}
	successful(t, s, "execution_delete", TaskChange{Change: Change{"purge-interrupted-upload"}, TaskID: TaskID{id}})
}

func TestUploadLockWaitHonorsRequestCancellation(t *testing.T) {
	s, _ := testServer(t)
	_ = createLab(t, s, "occupied-upload")
	_, _, _, url := queuedUpload(t, s)
	rd, wr := io.Pipe()
	defer rd.Close()
	defer wr.Close()
	firstCtx, firstCancel := context.WithCancel(context.Background())
	defer firstCancel()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", url, rd).WithContext(firstCtx))
	}()
	if _, err := wr.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	started := time.Now()
	s.Handler().ServeHTTP(w, httptest.NewRequest("PUT", url, strings.NewReader("second")).WithContext(ctx))
	if w.Code != 408 || time.Since(started) > time.Second {
		t.Fatal("upload lock ignored deadline", w.Code)
	}
	firstCancel()
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not release upload")
	}
}

func transferHTTP(t *testing.T, s *Server, method, url, data string, status int) string {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(method, url, strings.NewReader(data)))
	if w.Code != status {
		t.Fatal(method, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestTransferPublicationPersistenceFailure(t *testing.T) {
	for _, boundary := range []string{"statement", "commit"} {
		t.Run(boundary, func(t *testing.T) {
			s, _ := testServer(t)
			v, err := s.createTransfer(TransferRequest{Kind: "cache"})
			if err != nil {
				t.Fatal(err)
			}
			view := v.(map[string]any)
			id, url := view["id"].(string), view["url"].(string)+"/nar/fixture.nar"
			transferHTTP(t, s, "PUT", url, "original", 200)
			s.mu.Lock()
			original, bytes := s.transfers[id].Objects["nar/fixture.nar"], s.transfers[id].Bytes
			s.mu.Unlock()
			var query string
			if boundary == "statement" {
				query = `CREATE TRIGGER reject_object BEFORE INSERT ON transfer_objects BEGIN SELECT RAISE(ABORT,'synthetic write failure'); END;`
			} else {
				query = `CREATE TABLE parent_fixture(id INTEGER PRIMARY KEY);
CREATE TABLE child_fixture(parent INTEGER REFERENCES parent_fixture(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER reject_object AFTER UPDATE ON transfer_objects BEGIN INSERT INTO child_fixture VALUES(1); END;`
			}
			if _, err := s.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			transferHTTP(t, s, "PUT", url, "replacement", 409)
			if got := transferHTTP(t, s, "GET", url, "", 200); got != "original" {
				t.Fatal(got)
			}
			objects, total, err := s.db.TransferObjects(context.Background(), id)
			if err != nil || objects["nar/fixture.nar"] != original || total != bytes {
				t.Fatal(objects, total, err)
			}
			s.mu.Lock()
			current, used := s.transfers[id].Objects["nar/fixture.nar"], s.transfers[id].Bytes
			s.mu.Unlock()
			if current != original || used != bytes {
				t.Fatal(current, used)
			}
			files, err := os.ReadDir(filepath.Join(s.root.Path, "transfers", id))
			if err != nil || len(files) != 2 {
				t.Fatal("unpublished revision was not removed", files, err)
			}
			if _, err := s.db.Exec("DROP TRIGGER reject_object"); err != nil {
				t.Fatal(err)
			}
			transferHTTP(t, s, "PUT", url, "accepted", 200)
			if got := transferHTTP(t, s, "GET", url, "", 200); got != "accepted" {
				t.Fatal(got)
			}
		})
	}
}

func TestTransferRevisionRecoveryAtCommitBoundary(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("committed=%t", committed), func(t *testing.T) {
			cfg := testConfig(t)
			s, err := New(cfg, &fakeBackend{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { s.Close() }()
			v, err := s.createTransfer(TransferRequest{Kind: "cache"})
			if err != nil {
				t.Fatal(err)
			}
			view := v.(map[string]any)
			id, url := view["id"].(string), view["url"].(string)+"/nar/fixture.nar"
			transferHTTP(t, s, "PUT", url, "original", 200)
			name := ".object-" + wire.ID()
			dir := filepath.Join("transfers", id)
			n, h, err := s.root.Write(filepath.Join(dir, name), strings.NewReader("replacement"), 100)
			if err != nil {
				t.Fatal(err)
			}
			// Model process loss at either side of the SQLite commit. No in-memory
			// update or superseded-file deletion runs before the controller reopens.
			if committed {
				if err := s.db.StoreTransferObject(context.Background(), id, "nar/fixture.nar", Object{Size: n, SHA256: h, File: name}); err != nil {
					t.Fatal(err)
				}
			}
			for _, extra := range []string{".object-" + wire.ID() + "." + wire.ID() + ".partial", "review-note"} {
				if _, _, err := s.root.Write(filepath.Join(dir, extra), strings.NewReader("retained fixture"), 100); err != nil {
					t.Fatal(err)
				}
			}
			s.Close()
			s, err = New(cfg, &fakeBackend{})
			if err != nil {
				t.Fatal(err)
			}
			want := "original"
			if committed {
				want = "replacement"
			}
			if got := transferHTTP(t, s, "GET", url, "", 200); got != want {
				t.Fatal(got)
			}
			got := s.transfers[id]
			if got.Bytes != got.Objects["nix-cache-info"].Size+int64(len(want)) {
				t.Fatal(got.Bytes)
			}
			files, err := os.ReadDir(filepath.Join(s.root.Path, dir))
			if err != nil || len(files) != 3 {
				t.Fatal("recovery lost indexed bytes or retained orphan revisions", files, err)
			}
			if data, err := s.root.Read(filepath.Join(dir, "review-note"), 100); err != nil || string(data) != "retained fixture" {
				t.Fatal(string(data), err)
			}
		})
	}
}

func TestTransferReplacementQuotaAndSealPersistence(t *testing.T) {
	s, _ := testServer(t)
	v, err := s.createTransfer(TransferRequest{Kind: "cache"})
	if err != nil {
		t.Fatal(err)
	}
	view := v.(map[string]any)
	id, url := view["id"].(string), view["url"].(string)
	s.mu.Lock()
	s.cfg.Storage.MaxUploadBytes = s.transfers[id].Bytes + 8
	s.mu.Unlock()
	transferHTTP(t, s, "PUT", url+"/nar/one.nar", "12345678", 200)
	transferHTTP(t, s, "PUT", url+"/nar/two.nar", "x", 413)
	transferHTTP(t, s, "PUT", url+"/nar/one.nar", "1234", 200)
	transferHTTP(t, s, "PUT", url+"/nar/two.nar", "5678", 200)
	s.mu.Lock()
	if s.transfers[id].Bytes != s.cfg.Storage.MaxUploadBytes {
		t.Fatal(s.transfers[id].Bytes)
	}
	s.mu.Unlock()
	// A failed header commit cannot make an input read-only only in memory.
	file := &Transfer{ID: wire.ID(), Kind: "file", State: "open", Objects: map[string]Object{"blob": {Size: 1, File: "blob"}}}
	if err := s.db.CreateTransfer(context.Background(), state.Document{Kind: "transfer", ID: file.ID, Value: file}, file.Objects); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_seal BEFORE UPDATE ON transfers BEGIN SELECT RAISE(ABORT,'synthetic seal failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := s.seal(file); err == nil || file.State != "open" {
		t.Fatal(file.State, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_seal"); err != nil {
		t.Fatal(err)
	}
	if err := s.seal(file); err != nil || file.State != "sealed" {
		t.Fatal(file.State, err)
	}
}
