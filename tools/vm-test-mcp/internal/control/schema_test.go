package control

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type nativeInputBackend struct {
	fakeBackend
	received chan NodeAction
}

func (b *nativeInputBackend) Action(ctx context.Context, t *Task, n *Node, a NodeAction) (json.RawMessage, error) {
	if oneOf(a.Action, "input_click", "input_drag") {
		b.received <- a
		return wire.JSON(map[string]any{"delivered": true}), nil
	}
	return b.fakeBackend.Action(ctx, t, n, a)
}

func TestNativeClickDefaultsToAButtonTransition(t *testing.T) {
	b := &nativeInputBackend{received: make(chan NodeAction, 2)}
	s, task, _ := fixtureLab(t, b, time.Second)
	for _, action := range []string{"click", "drag"} {
		request := InputNodeRequest{NodeChange: NodeChange{Change: Change{RequestID: "default-" + action}, NodeID: NodeID{TaskID: TaskID{task.ID}, NodeID: task.lab().Nodes[0].ID}}, Action: action, X: 12, Y: 24, EndX: 30, EndY: 40}
		successful(t, s, "node_input", request)
		if got := <-b.received; got.Button != 1 || got.X != 12 || got.Y != 24 || got.Action != "input_"+action {
			t.Fatal(got)
		}
	}
}

func TestDiscoveredSchemaAndRuntimeAcceptTheSameBoundaries(t *testing.T) {
	// Resolve every advertised tool, including inherited structs and defaults.
	for _, d := range Definitions {
		compiledSchema(reflect.TypeOf(d.Args))
	}
	base := schemaMap{"request_id": "schema-check", "node_id": wire.ID()}
	for _, test := range []struct {
		name   string
		typ    any
		fields schemaMap
		valid  bool
	}{
		{"scan bytes", InputNodeRequest{}, schemaMap{"action": "scancodes", "scancodes": []string{"1d", "A0"}}, true},
		{"unknown input", InputNodeRequest{}, schemaMap{"action": "keyboard"}, false},
		{"empty scan", InputNodeRequest{}, schemaMap{"action": "scancodes", "scancodes": []string{}}, false},
		{"bad scan byte", InputNodeRequest{}, schemaMap{"action": "scancodes", "scancodes": []string{"100"}}, false},
		{"too many scan bytes", InputNodeRequest{}, schemaMap{"action": "scancodes", "scancodes": make([]string, 257)}, false},
		{"click", InputNodeRequest{}, schemaMap{"action": "click", "button": 4}, true},
		{"bad button", InputNodeRequest{}, schemaMap{"action": "click", "button": 3}, false},
		{"mixed input", InputNodeRequest{}, schemaMap{"action": "move", "scancodes": []string{"01"}}, false},
		{"scroll max", InputNodeRequest{}, schemaMap{"action": "scroll", "dz": 100}, true},
		{"scroll over", InputNodeRequest{}, schemaMap{"action": "scroll", "dz": 101}, false},
		{"resize", DisplayRequest{}, schemaMap{"width": 320, "height": 200}, true},
		{"partial resize", DisplayRequest{}, schemaMap{"width": 320}, false},
		{"auto resize", DisplayRequest{}, schemaMap{"auto_resize": false}, true},
		{"no display request", DisplayRequest{}, schemaMap{}, false},
		{"desktop", StartRequest{}, schemaMap{"argv": []string{"fixture"}, "context": "desktop"}, true},
		{"bad context", StartRequest{}, schemaMap{"argv": []string{"fixture"}, "context": "auto"}, false},
		{"relative cwd", StartRequest{}, schemaMap{"argv": []string{"fixture"}, "cwd": "relative"}, false},
		{"windows cwd", StartRequest{}, schemaMap{"argv": []string{"fixture"}, "cwd": `C:\Task`}, true},
		{"input path", PutRequest{}, schemaMap{"path": "/tmp/fixture", "upload_id": wire.ID()}, true},
		{"udp endpoint", EndpointRequest{}, schemaMap{"protocol": "udp", "port": 65535}, true},
		{"invalid endpoint", EndpointRequest{}, schemaMap{"protocol": "tcp", "port": 65536}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := schemaMap{}
			for k, x := range base {
				v[k] = x
			}
			for k, x := range test.fields {
				v[k] = x
			}
			typ := reflect.TypeOf(test.typ)
			advertised := compiledSchema(typ).full.Validate(v) == nil
			runtime := validateArguments(typ, wire.JSON(v)) == nil
			if advertised != test.valid || runtime != test.valid {
				t.Fatal(advertised, runtime, test.valid)
			}
		})
	}
	for _, test := range []struct {
		v     schemaMap
		valid bool
	}{
		{schemaMap{"request_id": "file", "kind": "file"}, true},
		{schemaMap{"request_id": "cache", "kind": "cache"}, true},
	} {
		if ok := validateArguments(reflect.TypeFor[TransferRequest](), wire.JSON(test.v)) == nil; ok != test.valid {
			t.Fatal(test)
		}
	}
}

func TestSchemaRejectsBytesWithoutEchoingContentOrAllocatingReceipt(t *testing.T) {
	s, err := New(testConfig(t), &fakeBackend{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	marker := "synthetic-sensitive-value"
	for _, args := range []schemaMap{
		{"request_id": "invalid-argument", "node_id": wire.ID(), "argv": []string{strings.Repeat(marker, 2000)}},
		{"request_id": "byte-limit", "node_id": wire.ID(), "argv": []string{strings.Repeat("界", 12000)}},
	} {
		_, err = s.accept(context.Background(), "command_start", wire.JSON(args))
		if err == nil || wire.Safe(err).Code != "invalid_argument" || wire.Safe(err).Category != "validation" || strings.Contains(string(wire.JSON(wire.Safe(err))), marker) {
			t.Fatal(err)
		}
	}
	if len(s.operations) != 0 || len(s.requests) != 0 {
		t.Fatal("invalid input allocated a mutation receipt")
	}
}

func TestUnknownFieldsReportAllowedFieldsAndPerToolIdempotencyAndZeroWait(t *testing.T) {
	s, _ := testServer(t)

	// 1. Unknown field diagnostics name unknown_fields and allowed_fields without allocating receipts.
	_, err := s.Call(context.Background(), "execution_log", wire.JSON(schemaMap{"execution_id": wire.ID(), "max_bytes": 1024}))
	e := wire.Safe(err)
	if e == nil || e.Code != "invalid_argument" {
		t.Fatal("expected invalid_argument for unknown field, got", err)
	}
	unknown, _ := e.Details["unknown_fields"].([]string)
	allowed, _ := e.Details["allowed_fields"].([]string)
	if len(unknown) != 1 || unknown[0] != "max_bytes" || len(allowed) != 3 {
		t.Fatalf("unexpected field details: %+v", e.Details)
	}
	_, err = s.Call(context.Background(), "wait", wire.JSON(schemaMap{"targets": []any{schemaMap{"kind": "lab", "id": wire.ID(), "condition": "ready"}}, "timeout": 5}))
	if e = wire.Safe(err); e == nil || e.Code != "invalid_argument" || e.Details["unknown_fields"] == nil {
		t.Fatalf("wait did not report unknown field: %+v", err)
	}

	// 2. Per-tool (tool, request_id) idempotency allows reusing the same request_id across upload_create and nix_job_create,
	// while rejecting parameter changes on the same tool.
	sharedReqID := "run-shared-idempotency-key"
	upOp := successful(t, s, "upload_create", TransferRequest{Change: Change{RequestID: sharedReqID}, Kind: "cache"})
	upView := upOp.Result.(map[string]any)
	upID, upURL := upView["id"].(string), upView["url"].(string)
	drv, objects := cacheObjects()
	declareUpload(t, s, upID, upURL, drv, objects)
	for name, data := range objects {
		transferHTTP(t, s, "PUT", upURL+"/"+name, data, 200)
	}
	zero := 0
	jobReplyRaw, err := s.Call(context.Background(), "nix_job_create", wire.JSON(NixRequest{Change: Change{RequestID: sharedReqID}, CacheID: upID, WaitSeconds: &zero}))
	if err != nil {
		t.Fatal("nix_job_create rejected shared request_id from upload_create:", err)
	}
	jobReply := jobReplyRaw.(map[string]any)
	waitPart := jobReply["wait"].(WaitResult)
	if len(waitPart.Targets) != 1 || len(waitPart.Continue) != 1 || waitPart.Continue[0].Kind != "nix_job" {
		t.Fatalf("wait_seconds=0 did not return immediate snapshot and continuation: %+v", waitPart)
	}
	if _, err = s.accept(context.Background(), "upload_create", wire.JSON(TransferRequest{Change: Change{RequestID: sharedReqID}, Kind: "file"})); wire.Safe(err).Code != "request_conflict" {
		t.Fatal("same-tool changed arguments did not trigger request_conflict:", err)
	}

	// 3. wait(wait_seconds=0) returns immediately with current revision cursor.
	started := time.Now()
	snapRaw, err := s.Call(context.Background(), "wait", wire.JSON(WaitRequest{Targets: waitPart.Continue, WaitSeconds: &zero}))
	if err != nil || time.Since(started) > 2*time.Second {
		t.Fatal("wait_seconds=0 blocked or failed:", err, time.Since(started))
	}
	snap := snapRaw.(WaitResult)
	if len(snap.Targets) != 1 {
		t.Fatal(snap)
	}
}

func TestEndpointFailuresHaveDistinctRecoveryConditions(t *testing.T) {
	task := newTask("lab", 60)
	task.State = "running"
	task.lab().Network = "degraded"
	if e := wire.Safe(endpointAdmission(task)); e.Code != "network_not_ready" || e.Details["network"] != "degraded" {
		t.Fatal(e)
	}
	task.lab().Network = "ready"
	for range 128 {
		task.lab().Endpoints = append(task.lab().Endpoints, &Endpoint{ID: wire.ID(), State: "closed"})
	}
	if err := endpointAdmission(task); err != nil {
		t.Fatal("closed history occupied endpoint capacity", err)
	}
	for range 128 {
		task.lab().Endpoints = append(task.lab().Endpoints, &Endpoint{ID: wire.ID(), State: "open"})
	}
	if e := wire.Safe(endpointAdmission(task)); e.Code != "endpoint_limit" || e.Details["active"] != 128 || e.Category != "capacity" {
		t.Fatal(e)
	}
	task.lab().Endpoints = nil
	if e := endpointAdmission(task); e != nil {
		t.Fatal(e)
	}
	task.State = "finished"
	if e := wire.Safe(endpointAdmission(task)); e.Code != "invalid_state" || e.Details["required_phase"] != "running" {
		t.Fatal(e)
	}
}
