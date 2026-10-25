package control

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type schemaMap = map[string]any

// Constraints are added to the closed structural schema, then the same schema
// is compiled for request validation. Stateful resource policy stays in control.
func constrainSchema(t reflect.Type, schema schemaMap) {
	p := schema["properties"].(schemaMap)
	field := func(name string, values schemaMap) {
		current, ok := p[name].(schemaMap)
		if !ok {
			panic("schema field not found: " + t.Name() + "." + name)
		}
		for k, v := range values {
			current[k] = v
		}
	}
	rangeOf := func(name string, min, max int) { field(name, schemaMap{"minimum": min, "maximum": max}) }
	enum := func(name string, values ...string) { field(name, schemaMap{"enum": values}) }
	for name := range p {
		if name == "request_id" {
			field(name, schemaMap{"pattern": requestPattern.String(), "description": "Stable idempotency key. Reuse unchanged arguments until its receipt is reconciled; changed parameters require a new key."})
		} else if strings.HasSuffix(name, "_id") {
			field(name, schemaMap{"pattern": wire.IDPattern.String()})
		}
	}
	if _, ok := p["screen"]; ok {
		rangeOf("screen", 0, 63)
	}
	if _, ok := p["offset"]; ok {
		field("offset", schemaMap{"minimum": 0, "description": "Byte offset, independently maintained for each output stream."})
	}
	if _, ok := p["argv"]; ok {
		field("argv", schemaMap{"minItems": 1, "maxItems": 256, "items": schemaMap{"type": "string", "maxLength": 32768, "x-maxBytes": 32768, "pattern": "^[^\\x00]*$"}, "description": "Executable and exact arguments; no shell expansion. Use an explicit shell executable when needed."})
	}
	if _, ok := p["cwd"]; ok {
		field("cwd", schemaMap{"maxLength": 32768, "x-maxBytes": 32768, "pattern": "^(?:/|[A-Za-z]:[\\\\/])[^\\x00]*$", "description": "Absolute guest directory; omitted command_start cwd is the selected account home. node_exec inherits Guest Control defaults."})
	}
	if _, ok := p["env"]; ok {
		field("env", schemaMap{"propertyNames": schemaMap{"minLength": 1, "pattern": "^[^=\\x00]*$"}, "additionalProperties": schemaMap{"type": "string", "maxLength": 32768, "x-maxBytes": 32768, "pattern": "^[^\\x00]*$"}, "description": "Per-command overrides, without changing template settings; Windows names are case-insensitive."})
	}
	switch t {
	case reflect.TypeFor[ListRequest](), reflect.TypeFor[EventsRequest]():
		field("cursor", schemaMap{"minimum": 0})
		rangeOf("limit", 0, 200)
		field("limit", schemaMap{"default": 50, "description": "0 or omission uses 50."})
	case reflect.TypeFor[TaskListRequest]():
		enum("phase", "active", "all", "queued", "preparing", "running", "ended")
		field("phase", schemaMap{"default": "active"})
		field("cursor", schemaMap{"pattern": wire.IDPattern.String(), "description": "Last scanned task ID from next_cursor; omit for a new scan."})
		rangeOf("limit", 0, 200)
		field("limit", schemaMap{"default": 50})
	case reflect.TypeFor[LogRequest](), reflect.TypeFor[ReadRequest]():
		rangeOf("limit", 0, 65536)
		field("limit", schemaMap{"default": 65536, "description": "0 or omission uses 65536 bytes."})
		if t == reflect.TypeFor[ReadRequest]() {
			enum("stream", "stdout", "stderr")
		}
	case reflect.TypeFor[ObserveRequest]():
		rangeOf("seconds", 0, 240)
		field("seconds", schemaMap{"default": 30, "description": "0 or omission uses a 30-second window."})
	case reflect.TypeFor[FinishRequest]():
		enum("conclusion", "passed", "failed", "inconclusive")
		field("summary", schemaMap{"minLength": 1, "maxLength": 8192, "x-maxBytes": 8192, "pattern": "\\S", "description": "Evidence-based conclusion; requires a running lab, then releases resources."})
	case reflect.TypeFor[ExtendRequest]():
		rangeOf("seconds", 1, 86400)
		field("reason", schemaMap{"minLength": 1, "maxLength": 1024, "x-maxBytes": 1024, "pattern": "\\S", "description": "Actual continued work and its purpose; effective total limit comes from service_get."})
	case reflect.TypeFor[NixRequest]():
		enum("system", "x86_64-linux", "aarch64-linux")
		field("derivation", schemaMap{"pattern": `^/nix/store/[0-9abcdfghijklmnpqrsvwxyz]{32}-[A-Za-z0-9+._?=-]+\.drv$`})
		rangeOf("timeout_seconds", 0, 86400)
	case reflect.TypeFor[StartRequest]():
		enum("context", "service", "desktop")
		field("context", schemaMap{"default": "service", "description": "service: Linux root or Windows LocalSystem. desktop: Windows logged-in elevated administrator; rejected if unavailable. See node guest.command_environment."})
		rangeOf("timeout_seconds", 0, 30*86400)
		field("timeout_seconds", schemaMap{"default": 0, "description": "Independent process deadline; 0 leaves cancellation to task lifecycle (guest maximum 30 days)."})
	case reflect.TypeFor[ExecRequest]():
		rangeOf("timeout_seconds", 0, 30)
		field("timeout_seconds", schemaMap{"default": 10, "description": "0 or omission uses 10 seconds. Diagnostic Guest Control execution, without durable supervisor semantics."})
	case reflect.TypeFor[CommandGetRequest]():
		field("input_from", schemaMap{"minimum": 0, "description": "Explicitly read retained stdin starting at this sequence; omitted reads metadata only."})
		rangeOf("input_limit", 0, 16)
		field("input_limit", schemaMap{"default": 16})
		schema["dependentRequired"] = map[string][]string{"input_limit": {"input_from"}}
	case reflect.TypeFor[InputRequest]():
		rangeOf("sequence", 0, wire.InputPacketCount-1)
		field("data", schemaMap{"maxLength": wire.InputPacketLimit, "x-maxBytes": wire.InputPacketLimit, "description": "Raw UTF-8 text, including NUL if required; not base64. Maximum 65536 UTF-8 bytes."})
		field("eof", schemaMap{"default": false, "description": "Close stdin after this packet; no later packet is accepted. Explicit EOF is required."})
	case reflect.TypeFor[TransferRequest]():
		enum("kind", "file", "cache")
		schema["if"] = schemaMap{"properties": schemaMap{"kind": schemaMap{"const": "file"}}}
		schema["then"] = schemaMap{"required": []string{"task_id"}}
		schema["else"] = schemaMap{"not": schemaMap{"required": []string{"task_id"}}}
		field("task_id", schemaMap{"description": "Required for file uploads on queued/preparing/running labs; forbidden for caches prepared before nixos_run."})
	case reflect.TypeFor[PutRequest](), reflect.TypeFor[GetRequest]():
		field("path", schemaMap{"maxLength": 32768, "x-maxBytes": 32768, "pattern": "^(?:/|[A-Za-z]:[\\\\/])[^\\x00]*$", "description": "Absolute guest path. For put, create and verify the parent directory first; transfer must be fully uploaded. Get archives a file/directory without host extraction."})
	case reflect.TypeFor[PowerRequest]():
		enum("action", "start", "shutdown", "reboot", "poweroff", "pause", "resume", "reset")
		field("action", schemaMap{"description": "Hypervisor/node power control. pause/resume does not test guest OS sleep or hibernation."})
	case reflect.TypeFor[GUIRequest]():
		enum("max_guest_resolution", "any", "auto")
	case reflect.TypeFor[EndpointRequest]():
		enum("protocol", "tcp", "udp")
		rangeOf("port", 1, 65535)
	case reflect.TypeFor[DisplayRequest]():
		rangeOf("width", 320, 16384)
		rangeOf("height", 200, 16384)
		schema["dependentRequired"] = map[string][]string{"width": {"height"}, "height": {"width"}}
		schema["anyOf"] = []any{schemaMap{"required": []string{"width", "height"}}, schemaMap{"required": []string{"auto_resize"}}}
	case reflect.TypeFor[InputNodeRequest]():
		enum("action", "move", "click", "drag", "scroll", "release", "scancodes")
		field("button", schemaMap{"enum": []int{0, 1, 2, 4}, "default": 0, "description": "0/omission selects left for click/drag; 1 left, 2 right, 4 middle. Other actions ignore button."})
		rangeOf("dz", -100, 100)
		rangeOf("dw", -100, 100)
		for _, name := range []string{"x", "y", "end_x", "end_y"} {
			rangeOf(name, -2147483648, 2147483647)
		}
		field("scancodes", schemaMap{"minItems": 1, "maxItems": 256, "items": schemaMap{"type": "string", "pattern": scanCode.String()}, "description": "Two hex digits per scan byte; coordinate fields do not apply."})
		schema["if"] = schemaMap{"properties": schemaMap{"action": schemaMap{"const": "scancodes"}}}
		schema["then"] = schemaMap{"required": []string{"scancodes"}}
		schema["else"] = schemaMap{"not": schemaMap{"required": []string{"scancodes"}}}
	case reflect.TypeFor[MediaRequest]():
		enum("device", "camera", "audio_input", "audio_output")
		schema["if"] = schemaMap{"properties": schemaMap{"device": schemaMap{"const": "camera"}}}
		schema["then"] = schemaMap{"required": []string{"camera"}}
		schema["else"] = schemaMap{"not": schemaMap{"required": []string{"camera"}}}
	case reflect.TypeFor[CaptureRequest]():
		enum("kind", "video", "nic", "metrics")
	case reflect.TypeFor[LabRequest]():
		field("nodes", schemaMap{"maxItems": 64, "description": "0..64 independent nodes; [] creates a router-only lab. Effective resource limits are in service_get."})
		rangeOf("timeout_seconds", 0, 86400)
		field("lan", schemaMap{"description": "Private IPv4 CIDR; omitted uses service_get defaults."})
	case reflect.TypeFor[NodeRequest]():
		field("name", schemaMap{"pattern": wire.NamePattern.String()})
		field("template", schemaMap{"pattern": wire.NamePattern.String(), "description": "Template alias from service_get; availability does not attest optional platform capabilities."})
		field("cpus", schemaMap{"minimum": 0, "description": "0/omission uses template default; maximum from service_get."})
		field("memory_mib", schemaMap{"minimum": 0, "description": "0/omission uses template default; effective value >=128 and <= template maximum."})
	}
}

type compiledArguments struct {
	full       *jsonschema.Resolved
	properties map[string]*jsonschema.Resolved
	schema     schemaMap
}

var argumentSchemas sync.Map

func compiledSchema(t reflect.Type) *compiledArguments {
	if v, ok := argumentSchemas.Load(t); ok {
		return v.(*compiledArguments)
	}
	value := Schema(t)
	compile := func(v any) *jsonschema.Resolved {
		var s jsonschema.Schema
		if err := json.Unmarshal(wire.JSON(v), &s); err != nil {
			panic(err)
		}
		r, err := s.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
		if err != nil {
			panic(err)
		}
		return r
	}
	c := &compiledArguments{full: compile(value), schema: value, properties: map[string]*jsonschema.Resolved{}}
	for name, p := range value["properties"].(schemaMap) {
		c.properties[name] = compile(p)
	}
	v, _ := argumentSchemas.LoadOrStore(t, c)
	return v.(*compiledArguments)
}

func validByteLengths(schema schemaMap, value any) bool {
	if s, ok := value.(string); ok {
		if max, ok := schema["x-maxBytes"].(int); ok && len(s) > max {
			return false
		}
	}
	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(schemaMap)
		additional, _ := schema["additionalProperties"].(schemaMap)
		for key, v := range object {
			p, _ := properties[key].(schemaMap)
			if p == nil {
				p = additional
			}
			if p != nil && !validByteLengths(p, v) {
				return false
			}
		}
	}
	if items, ok := value.([]any); ok {
		if item, ok := schema["items"].(schemaMap); ok {
			for _, v := range items {
				if !validByteLengths(item, v) {
					return false
				}
			}
		}
	}
	return true
}

func validateArguments(t reflect.Type, raw []byte) error {
	c := compiledSchema(t)
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return wire.Fail("invalid_argument", "Expected tool arguments object")
	}
	if c.full.Validate(value) == nil && validByteLengths(c.schema, value) {
		return nil
	}
	// Library errors contain caller values. Return field names/constraints rather
	// than echoing command arguments, credentials or input in a diagnostic.
	fields := []string{}
	props := c.schema["properties"].(schemaMap)
	for name, validator := range c.properties {
		v, exists := value[name]
		if exists && (validator.Validate(v) != nil || !validByteLengths(props[name].(schemaMap), v)) {
			fields = append(fields, name)
		}
	}
	for _, name := range c.schema["required"].([]string) {
		if _, ok := value[name]; !ok {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	if len(fields) == 0 {
		fields = append(fields, "cross_field_constraints")
	}
	return &wire.Error{Code: "invalid_argument", Message: "Arguments do not match the tool input schema", Details: map[string]any{"fields": fields, "next_action": "Inspect tools/list for field constraints; correct parameters and use a new request_id for a changed mutation"}}
}
