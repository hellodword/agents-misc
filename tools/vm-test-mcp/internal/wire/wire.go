// Package wire defines the bounded, strict messages shared by the controller,
// worker, router and guest supervisor. It contains no host execution policy.
package wire

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"
)

const Version = "0.14.0"
const MaxJSON = 1 << 20

// NixExecution reports worker execution independently of scheduler and daemon
// cleanup. Process results describe the local Nix client, not a QEMU exit.
type NixExecution struct {
	Resources        NixResources      `json:"resources"`
	Evidence         NixEvidence       `json:"evidence"`
	Imported         bool              `json:"imported"`
	Stage            string            `json:"stage"`
	Process          *NixProcessResult `json:"process,omitempty"`
	Error            *Error            `json:"error,omitempty"`
	PersistenceError *Error            `json:"persistence_error,omitempty"`
	Finished         *time.Time        `json:"finished,omitempty"`
}

// NixResources is a durable observation of a sealed executor session. A client
// process exit is recorded separately and cannot supply this observation.
type NixResources struct {
	Attempts         int        `json:"attempts"`
	ReleaseStarted   *time.Time `json:"release_started,omitempty"`
	ReleaseDeadline  *time.Time `json:"release_deadline,omitempty"`
	State            string     `json:"state"`
	SessionID        string     `json:"session_id,omitempty"`
	ExecutorInstance string     `json:"executor_instance,omitempty"`
	Confirmation     string     `json:"confirmation,omitempty"`
	Reason           string     `json:"reason,omitempty"`
	Observed         *time.Time `json:"observed,omitempty"`
	Error            *Error     `json:"error,omitempty"`
}

func (r NixResources) Released() bool { return r.State == "released" || r.State == "unallocated" }

type NixEvidence struct {
	State       string     `json:"state"`
	Attempts    int        `json:"attempts"`
	Updated     *time.Time `json:"updated,omitempty"`
	NextAttempt *time.Time `json:"next_attempt,omitempty"`
	Error       *Error     `json:"error,omitempty"`
}
type NixProcessResult struct {
	Stage      string `json:"stage"`
	Source     string `json:"source"`
	Exited     bool   `json:"exited"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	ExitSignal string `json:"exit_signal,omitempty"`
	CoreDumped bool   `json:"core_dumped,omitempty"`
}

type Error struct {
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	Category   string         `json:"category,omitempty"`
	Retry      string         `json:"retry,omitempty"`
	NextAction string         `json:"next_action,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string        { return e.Code + ": " + e.Message }
func Fail(code, message string) error { return classifyError(&Error{Code: code, Message: message}) }
func Safe(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return classifyError(e)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return classifyError(&Error{Code: "backend_timeout", Message: "Operation exceeded its time budget"})
	}
	if errors.Is(err, context.Canceled) {
		return classifyError(&Error{Code: "backend_cancelled", Message: "Operation was cancelled before its result was confirmed"})
	}
	return classifyError(&Error{Code: "backend_error", Message: "Operation failed; inspect task events and administrator diagnostics"})
}

// CheckJSON rejects duplicate keys at every depth before any decoder can discard
// them. Limit input and depth independently of the selected transport.
func CheckJSON(data []byte) error { return checkJSON(data, MaxJSON) }
func checkJSON(data []byte, limit int) error {
	if len(data) > limit {
		return Fail("too_large", "JSON exceeds its byte limit")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("JSON nesting exceeds limit")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, e := d.Token()
					if e != nil {
						return e
					}
					k, ok := key.(string)
					if !ok || seen[k] {
						return fmt.Errorf("duplicate JSON key")
					}
					seen[k] = true
					if err := value(depth + 1); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := value(depth + 1); err != nil {
						return err
					}
				}
			default:
				return fmt.Errorf("unexpected delimiter")
			}
			_, err = d.Token()
		}
		return err
	}
	if err := value(0); err != nil {
		return Fail("invalid_argument", "Invalid JSON or duplicate field")
	}
	if _, err := d.Token(); err != io.EOF {
		return Fail("invalid_argument", "Expected one JSON value")
	}
	return nil
}
func Decode(data []byte, v any) error { return DecodeLimit(data, v, MaxJSON) }
func DecodeLimit(data []byte, v any, limit int) error {
	if err := checkJSON(data, limit); err != nil {
		return err
	}
	var object any
	if err := json.Unmarshal(data, &object); err != nil {
		return Fail("invalid_argument", "Invalid JSON")
	}
	target := reflect.TypeOf(v)
	if target == nil || target.Kind() != reflect.Pointer {
		return Fail("invalid_argument", "Expected a decode destination")
	}
	if !exactFields(object, target.Elem()) {
		return Fail("invalid_argument", "Unknown field or invalid field type")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return Fail("invalid_argument", "Unknown field or invalid field type")
	}
	return nil
}

// encoding/json accepts case-folded field names. The protocol deliberately does
// not: otherwise differently cased duplicate fields could silently overwrite.
func exactFields(value any, t reflect.Type) bool {
	if value == nil {
		return t.Kind() == reflect.Pointer || t.Kind() == reflect.Map || t.Kind() == reflect.Slice || t.Kind() == reflect.Interface
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if reflect.PointerTo(t).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return true
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := value.(map[string]any)
		if !ok {
			return false
		}
		fields := map[string]reflect.Type{}
		var add func(reflect.Type)
		add = func(t reflect.Type) {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.Anonymous {
					ft := f.Type
					for ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if ft.Kind() == reflect.Struct {
						add(ft)
						continue
					}
				}
				if f.PkgPath != "" {
					continue
				}
				name := strings.Split(f.Tag.Get("json"), ",")[0]
				if name == "-" {
					continue
				}
				if name == "" {
					name = f.Name
				}
				fields[name] = f.Type
			}
		}
		add(t)
		for k, v := range m {
			ft, ok := fields[k]
			if !ok || !exactFields(v, ft) {
				return false
			}
		}
	case reflect.Slice:
		list, ok := value.([]any)
		if !ok {
			return false
		}
		for _, v := range list {
			if !exactFields(v, t.Elem()) {
				return false
			}
		}
	case reflect.Map:
		m, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for _, v := range m {
			if !exactFields(v, t.Elem()) {
				return false
			}
		}
	}
	return true
}
func JSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func UUID() string {
	b := ID()
	return b[:8] + "-" + b[8:12] + "-4" + b[13:16] + "-8" + b[17:20] + "-" + b[20:]
}

var IDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var NamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
var UUIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var StorePattern = regexp.MustCompile(`^/nix/store/[0-9abcdfghijklmnpqrsvwxyz]{32}-[A-Za-z0-9+._?=-]+$`)

func ValidText(s string, max int) bool { return len(s) <= max && !strings.ContainsRune(s, 0) }

type CommandSpec struct {
	ID             string            `json:"id"`
	Argv           []string          `json:"argv"`
	Context        string            `json:"context,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}
type Input struct {
	Sequence int    `json:"sequence"`
	Data     string `json:"data"`
	EOF      bool   `json:"eof"`
}
type CommandStatus struct {
	State        string            `json:"state"`
	ExitCode     *int              `json:"exit_code,omitempty"`
	ExitSignal   string            `json:"exit_signal,omitempty"`
	CoreDumped   bool              `json:"core_dumped,omitempty"`
	WrittenInput int               `json:"written_input_sequence"`
	InputClosed  bool              `json:"input_closed"`
	Error        string            `json:"error,omitempty"`
	Stage        string            `json:"stage,omitempty"`
	Final        bool              `json:"final"`
	Started      *time.Time        `json:"started,omitempty"`
	Deadline     *time.Time        `json:"deadline,omitempty"`
	Updated      *time.Time        `json:"updated,omitempty"`
	Finished     *time.Time        `json:"finished,omitempty"`
	Supervisor   *ProcessIdentity  `json:"supervisor,omitempty"`
	Termination  *Termination      `json:"termination,omitempty"`
	Output       *CommandOutput    `json:"output,omitempty"`
	Execution    *CommandExecution `json:"execution,omitempty"`
}

type CommandExecution struct {
	Context         string   `json:"context"`
	Identity        string   `json:"identity"`
	Cwd             string   `json:"cwd"`
	Inherited       string   `json:"inherited"`
	EnvironmentKeys []string `json:"environment_keys"`
}

type Termination struct {
	Reason           string    `json:"reason"`
	Requested        time.Time `json:"requested"`
	ProcessExited    bool      `json:"process_exited"`
	RequestDelivered *bool     `json:"request_delivered,omitempty"`
	GroupStopped     bool      `json:"group_stopped"`
	Source           string    `json:"source"`
	Error            *Error    `json:"error,omitempty"`
}
type CommandOutput struct {
	State           string `json:"state"`
	StdoutBytes     int64  `json:"stdout_bytes"`
	StderrBytes     int64  `json:"stderr_bytes"`
	StdoutSHA256    string `json:"stdout_sha256,omitempty"`
	StderrSHA256    string `json:"stderr_sha256,omitempty"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	Error           *Error `json:"error,omitempty"`
}

func (s CommandSpec) Validate() error {
	if s.Context != "" && s.Context != "service" && s.Context != "desktop" {
		return Fail("invalid_argument", "context must be service or desktop")
	}
	if !IDPattern.MatchString(s.ID) || len(s.Argv) == 0 || len(s.Argv) > 256 || s.Argv[0] == "" || s.TimeoutSeconds < 0 || s.TimeoutSeconds > 86400*30 {
		return Fail("invalid_argument", "Invalid command")
	}
	for _, a := range s.Argv {
		if !ValidText(a, 32768) {
			return Fail("invalid_argument", "Invalid command argument")
		}
	}
	if !ValidText(s.Cwd, 32768) {
		return Fail("invalid_argument", "Invalid guest directory")
	}
	for k, v := range s.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || !ValidText(v, 32768) {
			return Fail("invalid_argument", "Invalid environment")
		}
	}
	return nil
}
