package control

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func retainedFixture(t *testing.T, s *Server, when time.Time, success, pin bool) *Task {
	t.Helper()
	task := newTask("lab", 60)
	task.State = "finished"
	task.Conclusion = "passed"
	if !success {
		task.State = "failed"
		task.Conclusion = "failed"
	}
	task.Cleanup = "complete"
	task.Ended = &when
	artifact := &Artifact{ID: wire.ID(), Name: "fixture", Pinned: pin, Created: when}
	artifact.Path = "tasks/" + task.ID + "/artifacts/" + artifact.ID
	n, h, err := s.root.Write(artifact.Path, strings.NewReader(strings.Repeat("e", 1024)), 2048)
	if err != nil {
		t.Fatal(err)
	}
	artifact.Size = n
	artifact.SHA256 = h
	task.Artifacts = []*Artifact{artifact}
	s.tasks = append(s.tasks, task)
	s.save(task)
	return task
}
func TestRetentionPhysicalTTLAndPin(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	success := retainedFixture(t, s, now.Add(-25*time.Hour), true, false)
	failure := retainedFixture(t, s, now.Add(-6*24*time.Hour), false, false)
	pinned := retainedFixture(t, s, now.Add(-40*24*time.Hour), true, true)
	expired := retainedFixture(t, s, now.Add(-31*24*time.Hour), false, false)
	active := retainedFixture(t, s, now.Add(-31*24*time.Hour), false, false)
	active.State = "interrupted"
	active.Cleanup = "required"
	s.save(active)
	paths := map[string]string{}
	for _, task := range []*Task{success, failure, pinned, expired, active} {
		paths[task.ID] = evidenceForTest(t, s, task.ID)[0].Path
	}
	if err = s.reclaim(now, false); err != nil {
		t.Fatal(err)
	}
	for _, task := range []*Task{success, expired} {
		if _, err = os.Stat(filepath.Join(s.root.Path, paths[task.ID])); !os.IsNotExist(err) {
			t.Fatal("expired file remains", task.ID, err)
		}
	}
	for _, task := range []*Task{failure, pinned, active} {
		if _, err = os.Stat(filepath.Join(s.root.Path, paths[task.ID])); err != nil {
			t.Fatal("protected file lost", task.ID, err)
		}
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM executions WHERE id=?", expired.ID).Scan(&count)
	if count != 0 {
		t.Fatal("summary index survived 30 days")
	}
	s.db.QueryRow("SELECT count(*) FROM executions WHERE id=?", success.ID).Scan(&count)
	if count != 1 {
		t.Fatal("summary expired too early")
	}
	if _, err = s.changeEvidence("evidence_unpin", EvidenceChange{ArtifactGetRequest: ArtifactGetRequest{TaskID: TaskID{pinned.ID}, ArtifactID: evidenceForTest(t, s, pinned.ID)[0].ID}}); err != nil {
		t.Fatal(err)
	}
	if err = s.reclaim(now.Add(2*24*time.Hour), false); err != nil {
		t.Fatal(err)
	}
	s.db.QueryRow("SELECT count(*) FROM executions WHERE id=?", pinned.ID).Scan(&count)
	if count != 0 {
		t.Fatal("unpin did not release expired index")
	}
	if _, err = os.Stat(filepath.Join(s.root.Path, paths[failure.ID])); !os.IsNotExist(err) {
		t.Fatal("failure evidence survived seven days", err)
	}
}
func TestPressureCountsPinsProtectsCleanupAndRecovers(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	success := retainedFixture(t, s, now, true, false)
	failure := retainedFixture(t, s, now.Add(-time.Hour), false, false)
	pinned := retainedFixture(t, s, now, true, true)
	s.cfg.Storage.EvidenceBytes = 2500
	s.cfg.Storage.LowWaterBytes = 2100
	s.refreshStorage(true)
	if err = s.admission(); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM evidence WHERE task_id=?", success.ID).Scan(&n)
	if n != 0 {
		t.Fatal("pressure did not prefer successful evidence")
	}
	s.db.QueryRow("SELECT count(*) FROM evidence WHERE task_id=?", failure.ID).Scan(&n)
	if n != 1 {
		t.Fatal("failure reclaimed before newer success")
	}
	s.cfg.Storage.EvidenceBytes = 512
	s.cfg.Storage.LowWaterBytes = 256
	s.refreshStorage(true)
	if err = s.admission(); wire.Safe(err).Code != "storage_pressure" {
		t.Fatal(err)
	}
	if _, err = s.changeEvidence("evidence_unpin", EvidenceChange{ArtifactGetRequest: ArtifactGetRequest{TaskID: TaskID{pinned.ID}, ArtifactID: evidenceForTest(t, s, pinned.ID)[0].ID}}); err != nil {
		t.Fatal(err)
	}
	s.refreshStorage(true)
	if err = s.admission(); err != nil {
		t.Fatal("admissions did not reopen", err)
	}
}
func TestInactiveUploadAndBodyCollection(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := time.Now().Add(-2 * time.Hour)
	transfer := &Transfer{ID: wire.ID(), Kind: "file", State: "open", Created: old, Updated: old, Objects: map[string]state.TransferObject{}}
	s.transfers[transfer.ID] = transfer
	if err = s.db.CreateTransfer(context.Background(), transfer, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.root.Write("transfers/"+transfer.ID+"/abandoned", strings.NewReader("partial"), 64); err != nil {
		t.Fatal(err)
	}
	if err = s.reclaim(time.Now(), false); err != nil {
		t.Fatal(err)
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM transfers").Scan(&count)
	if count != 0 {
		t.Fatal("idle transfer index retained")
	}
	if _, err = os.Stat(filepath.Join(s.root.Path, "transfers", transfer.ID)); !os.IsNotExist(err) {
		t.Fatal("idle files retained")
	}
}

func TestProtectedRunningCapturesCountButCloneDisksDoNot(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := newTask("lab", 60)
	task.State = "running"
	s.tasks = []*Task{task}
	s.save(task)
	for name, body := range map[string]string{"runtime/vm-owned/disk.vdi": strings.Repeat("d", 8192), "runtime/capture-owned.webm": strings.Repeat("v", 1024), "runtime/router-console/console.log": "boot"} {
		if _, _, err = s.root.Write("tasks/"+task.ID+"/"+name, strings.NewReader(body), 10000); err != nil {
			t.Fatal(err)
		}
	}
	size, err := s.evidenceUsage()
	if err != nil || size != 1028 {
		t.Fatal("running evidence accounting", size, err)
	}
	s.cfg.Storage.EvidenceBytes = 512
	s.cfg.Storage.LowWaterBytes = 256
	s.refreshStorage(true)
	if err = s.admission(); wire.Safe(err).Code != "storage_pressure" {
		t.Fatal(err)
	}
	if size, err = s.evidenceUsage(); err != nil || size != 1028 {
		t.Fatal("pressure deleted active evidence", size, err)
	}
}

func TestRetainedInputsStayReadableAndExpireAfterRestart(t *testing.T) {
	cfg := testConfig(t)
	s, err := New(cfg, &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	task := retainedFixture(t, s, time.Now().UTC().Add(-2*24*time.Hour), true, false)
	input := &Transfer{ID: wire.ID(), TaskID: task.ID, Kind: "file", State: "retained", Created: time.Now().UTC(), Updated: time.Now().UTC(), Objects: map[string]state.TransferObject{}}
	relative := "transfers/" + input.ID + "/blob"
	size, hash, err := s.root.Write(relative, strings.NewReader("reusable input"), 100)
	if err != nil {
		t.Fatal(err)
	}
	input.Objects["blob"] = state.TransferObject{File: "blob", Size: size, SHA256: hash}
	if err = s.db.CreateTransfer(context.Background(), input, input.Objects); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = New(cfg, &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.transfers[input.ID] != nil {
		t.Fatal("startup loaded historical input")
	}
	renewed := readUpload(t, s, input.ID)
	url := renewed["url"].(string)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest("GET", url+"/blob", nil))
	if response.Code != 200 || response.Body.String() != "reusable input" {
		t.Fatal("historical input unavailable", response.Code, response.Body.String())
	}
	err = s.reclaim(time.Now().UTC(), false)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	s.db.QueryRow("SELECT count(*) FROM transfers WHERE id=?", input.ID).Scan(&count)
	if count != 0 {
		t.Fatal("retained input index survived TTL")
	}
	if _, err = os.Stat(filepath.Join(cfg.ControllerDir(), relative)); !os.IsNotExist(err) {
		t.Fatal("retained input survived TTL", err)
	}
}

func TestIndependentPinReceiptAndOrphanLifetimes(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC()
	task := retainedFixture(t, s, now.Add(-40*24*time.Hour), true, true)
	artifact := evidenceForTest(t, s, task.ID)[0]
	op := &Operation{ID: wire.ID(), RequestID: "retained-receipt", Tool: "lab_finish", TaskID: task.ID, State: "succeeded", Created: now.Add(-time.Hour), Ended: &now}
	if err = s.db.Save(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	orphan := "tasks/" + task.ID + "/artifacts/" + wire.ID() + ".partial"
	if _, _, err = s.root.Write(orphan, strings.NewReader("unindexed"), 64); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-2 * time.Hour)
	if err = os.Chtimes(filepath.Join(s.root.Path, orphan), old, old); err != nil {
		t.Fatal(err)
	}
	if err = s.reclaim(now, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.task(task.ID); wire.Safe(err).Code != "not_found" {
		t.Fatal("summary survived its lifetime", err)
	}
	if _, err = s.operation(op.ID); err != nil {
		t.Fatal("recent receipt depended on old summary", err)
	}
	result, err := s.Call(context.Background(), "evidence_get", wire.JSON(ArtifactGetRequest{ArtifactID: artifact.ID}))
	if err != nil || result.(map[string]any)["execution_id"] != task.ID {
		t.Fatal("pin lost provenance or readability", result, err)
	}
	if _, err = os.Stat(filepath.Join(s.root.Path, orphan)); !os.IsNotExist(err) {
		t.Fatal("unindexed artifact retained", err)
	}
	if err = s.reclaim(now.Add(31*24*time.Hour), false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.operation(op.ID); wire.Safe(err).Code != "not_found" {
		t.Fatal("receipt did not physically expire", err)
	}
	if _, err = s.db.evidence(context.Background(), artifact.ID); err != nil {
		t.Fatal("receipt TTL removed pin", err)
	}
}
