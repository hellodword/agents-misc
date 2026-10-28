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
		request := InputNodeRequest{NodeChange: NodeChange{Change: Change{RequestID: "default-" + action}, NodeID: NodeID{TaskID: TaskID{task.ID}, NodeID: task.Nodes[0].ID}}, Action: action, X: 12, Y: 24, EndX: 30, EndY: 40}
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
	id := wire.ID()
	base := schemaMap{"request_id": "schema-check", "task_id": id, "node_id": wire.ID()}
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
		{"input path", PutRequest{}, schemaMap{"path": "/tmp/fixture", "transfer_id": wire.ID()}, true},
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
		{schemaMap{"request_id": "file", "kind": "file", "task_id": id}, true},
		{schemaMap{"request_id": "file", "kind": "file"}, false},
		{schemaMap{"request_id": "cache", "kind": "cache"}, true},
		{schemaMap{"request_id": "cache", "kind": "cache", "task_id": id}, false},
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
		{"request_id": "invalid-argument", "task_id": wire.ID(), "node_id": wire.ID(), "argv": []string{strings.Repeat(marker, 2000)}},
		{"request_id": "byte-limit", "task_id": wire.ID(), "node_id": wire.ID(), "argv": []string{strings.Repeat("界", 12000)}},
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

func TestEndpointFailuresHaveDistinctRecoveryConditions(t *testing.T) {
	task := newTask("lab", 60)
	task.State = "running"
	task.Network = "degraded"
	if e := wire.Safe(endpointAdmission(task)); e.Code != "network_not_ready" || e.Details["network"] != "degraded" {
		t.Fatal(e)
	}
	task.Network = "ready"
	task.Endpoints = make([]*Endpoint, 128)
	if e := wire.Safe(endpointAdmission(task)); e.Code != "endpoint_limit" || e.Details["scope"] != "task_history" || e.Category != "capacity" {
		t.Fatal(e)
	}
	task.Endpoints = nil
	if e := endpointAdmission(task); e != nil {
		t.Fatal(e)
	}
	task.State = "finished"
	if e := wire.Safe(endpointAdmission(task)); e.Code != "invalid_state" || e.Details["required_phase"] != "running" {
		t.Fatal(e)
	}
}
