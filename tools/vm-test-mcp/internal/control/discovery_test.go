package control

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func taskView(t *testing.T, s *Server, id string) map[string]any {
	t.Helper()
	v, err := s.accept(context.Background(), "lab_inspect", wire.JSON(TaskID{id}))
	if err != nil {
		t.Fatal(err)
	}
	return v.(map[string]any)
}

func TestBatchReleaseQueueAndReconnect(t *testing.T) {
	s, _ := testServer(t)
	lab := createLab(t, s, "batch-one")
	request := LabRequest{RequestID: "batch-two", Nodes: []NodeRequest{}}
	op := successful(t, s, "lab_create", request)
	next := op.Result.(map[string]any)["id"].(string)
	queued := taskView(t, s, next)
	q := queued["queue"].(map[string]any)
	if queued["phase"] != "queued" || q["position"] != float64(1) || q["reason"] != "executor_occupied" || q["blocking_task_id"] != lab.ID {
		t.Fatal(queued)
	}
	// No command or HTTP connection is necessary to keep a service fixture alive.
	if view := taskView(t, s, lab.ID); view["phase"] != "running" || view["activity"].(map[string]any)["commands_active"] != float64(0) {
		t.Fatal(view)
	}
	retry := successful(t, s, "lab_create", request)
	if retry.ID != op.ID || taskView(t, s, next)["phase"] != "queued" {
		t.Fatal("reconnect duplicated or started a blocked batch")
	}
	successful(t, s, "lab_finish", FinishRequest{TaskChange: TaskChange{Change: Change{"release-one"}, TaskID: TaskID{lab.ID}}, Conclusion: "inconclusive", Summary: "Evidence retained; release before local changes"})
	waitFor(t, func() bool { return taskView(t, s, next)["phase"] == "running" })
	old := taskView(t, s, lab.ID)
	cleanup := old["cleanup"].(map[string]any)
	if cleanup["state"] != "complete" {
		t.Fatal("admitted next batch before cleanup", old)
	}
	for _, r := range cleanup["resources"].([]any) {
		if r.(map[string]any)["state"] != "released" {
			t.Fatal(cleanup)
		}
	}
	successful(t, s, "lab_cancel", TaskChange{Change: Change{"release-two"}, TaskID: TaskID{next}})
	waitFor(t, func() bool { return taskView(t, s, next)["cleanup"].(map[string]any)["state"] == "complete" })
}

func TestQueuedCancellationHasNoAllocatedResources(t *testing.T) {
	s, f := testServer(t)
	lab := createLab(t, s, "blocker")
	op := successful(t, s, "lab_create", LabRequest{RequestID: "abandoned", Nodes: []NodeRequest{{Name: "one", Template: "windows"}}})
	id := op.Result.(map[string]any)["id"].(string)
	successful(t, s, "lab_cancel", TaskChange{Change: Change{"abandon"}, TaskID: TaskID{id}})
	view := taskView(t, s, id)
	cleanup := view["cleanup"].(map[string]any)
	if view["phase"] != "ended" || cleanup["state"] != "complete" || view["started"] != nil {
		t.Fatal(view)
	}
	for _, r := range cleanup["resources"].([]any) {
		if r.(map[string]any)["state"] != "never_allocated" {
			t.Fatal(cleanup)
		}
	}
	f.mu.Lock()
	created, deleted := len(f.created), len(f.deleted)
	f.mu.Unlock()
	if created != 3 || deleted != 0 || taskView(t, s, lab.ID)["phase"] != "running" {
		t.Fatal("queued cancellation touched live resources", created, deleted)
	}
}

func TestDiscoveryProjectsHistoryAndKeepsStableCursor(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	marker := strings.Repeat("synthetic-input-", 8192)
	history := newTask("lab", 60)
	history.State, history.Cleanup = "finished", "complete"
	history.Summary = marker
	history.Commands = []*Command{{ID: wire.ID(), Stdin: state.InputQueue{NextSequence: 1, Bytes: int64(len(marker))}, CommandStatus: wire.CommandStatus{State: "exited"}}}
	doc := state.Document{Kind: "task", ID: history.ID, Value: history}
	if err = s.db.CreateCommand(context.Background(), doc, history.Commands[0].ID, wire.CommandSpec{Argv: []string{"example", marker}, Env: map[string]string{"FIXTURE": marker}}); err != nil {
		t.Fatal(err)
	}
	if err = s.db.AppendInput(context.Background(), doc, history.Commands[0].ID, wire.Input{Data: marker}); err != nil {
		t.Fatal(err)
	}
	next := newTask("lab", 60)
	s.tasks = []*Task{history, next}
	s.save(next)
	list := func(request TaskListRequest) map[string]any {
		t.Helper()
		v, err := s.accept(context.Background(), "lab_list", wire.JSON(request))
		if err != nil {
			t.Fatal(err)
		}
		return v.(map[string]any)
	}
	active := list(TaskListRequest{})
	if items := active["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != next.ID {
		t.Fatal(active)
	}
	first := list(TaskListRequest{Phase: "all", Limit: 1})
	if first["next_cursor"] != history.ID || len(wire.JSON(first)) > 2048 {
		t.Fatal("history payload was not bounded", len(wire.JSON(first)))
	}
	item := first["items"].([]any)[0].(map[string]any)
	if item["phase"] != "ended" || item["activity"].(map[string]any)["commands_active"] != float64(0) {
		t.Fatal(item)
	}
	// Newer submissions do not shift a cursor anchored to a retained task ID.
	newer := newTask("lab", 60)
	s.tasks = append(s.tasks, newer)
	s.save(newer)
	page := list(TaskListRequest{Phase: "all", Limit: 1, Cursor: first["next_cursor"].(string)})
	if page["items"].([]any)[0].(map[string]any)["id"] != next.ID {
		t.Fatal(page)
	}
	view := taskView(t, s, history.ID)
	command := view["commands"].([]any)[0].(map[string]any)
	if command["state"] != "exited" || command["spec"] != nil || command["input"] != nil {
		t.Fatal("task status did not project command status")
	}
	full, err := s.accept(context.Background(), "command_details", wire.JSON(CommandRequest{TaskID: TaskID{history.ID}, CommandID: history.Commands[0].ID}))
	if err != nil || full.(*CommandDetails).Spec.Env["FIXTURE"] != marker || full.(*CommandDetails).Stdin.NextSequence != 1 {
		t.Fatal("explicit command detail lost spec or input progress", err)
	}
	from := 0
	full, err = s.accept(context.Background(), "command_details", wire.JSON(CommandGetRequest{CommandRequest: CommandRequest{TaskID: TaskID{history.ID}, CommandID: history.Commands[0].ID}, InputFrom: &from}))
	if err != nil || full.(*CommandDetails).Input[0].Data != marker || *full.(*CommandDetails).NextSequence != 1 {
		t.Fatal("explicit command detail lost retained inputs", err)
	}
	next.State, next.Cleanup = "cancelled", "required"
	s.active = next.ID
	q := taskView(t, s, newer.ID)["queue"].(map[string]any)
	if q["reason"] != "cleanup_blocked" || q["blocking_task_id"] != next.ID {
		t.Fatal(q)
	}
	if len(list(TaskListRequest{})["items"].([]any)) != 2 {
		t.Fatal("blocked cleanup disappeared from active discovery")
	}
}

func TestExtensionRequiresBoundedReasonAndPreservesDeadlineOnRejection(t *testing.T) {
	s, _ := testServer(t)
	lab := createLab(t, s, "hold")
	before := taskView(t, s, lab.ID)["deadline"]
	_, bad := s.accept(context.Background(), "lab_extend", wire.JSON(ExtendRequest{TaskChange: TaskChange{Change: Change{"empty-hold"}, TaskID: TaskID{lab.ID}}, Seconds: 5}))
	if bad == nil || wire.Safe(bad).Code != "invalid_argument" || taskView(t, s, lab.ID)["deadline"] != before {
		t.Fatal("invalid hold changed execution deadline", bad)
	}
	s.mu.Lock()
	rejectedReceipt := s.requests["empty-hold"]
	s.mu.Unlock()
	if rejectedReceipt != nil {
		t.Fatal("schema rejection allocated receipt")
	}
	successful(t, s, "lab_extend", ExtendRequest{TaskChange: TaskChange{Change: Change{"valid-hold"}, TaskID: TaskID{lab.ID}}, Seconds: 5, Reason: "Collect pending service response before release"})
	view := taskView(t, s, lab.ID)
	hold := view["hold"].(map[string]any)
	if hold["until"] != view["deadline"] || hold["reason"] != "Collect pending service response before release" {
		t.Fatal(view)
	}
	until, err := time.Parse(time.RFC3339Nano, hold["until"].(string))
	if err != nil || until.Sub(*lab.Started) > s.cfg.Tasks.MaxDuration.Value() {
		t.Fatal("hold exceeds total duration", hold, err)
	}
}
