package control

import (
	"encoding/base64"
	"encoding/json"
	"reflect"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// Public resources carry one identity. Ownership is resolved from the durable
// index, after idempotency lookup so retries remain valid after target deletion.
func (s *Server) resolveArguments(name string, args any) (any, error) {
	switch a := args.(type) {
	case JobID:
		return TaskID{a.JobID}, nil
	case JobChange:
		return TaskChange{a.Change, TaskID{a.JobID.JobID}}, nil
	case JobExtendRequest:
		return ExtendRequest{TaskChange{a.Change, TaskID{a.JobID.JobID}}, a.Seconds, a.Reason}, nil
	case ExecutionChange:
		return TaskChange{a.Change, TaskID{a.ExecutionID}}, nil
	}
	v := reflect.New(reflect.TypeOf(args))
	v.Elem().Set(reflect.ValueOf(args))
	scope := v.Elem().FieldByName("TaskID")
	if !scope.IsValid() || scope.Kind() != reflect.Struct {
		return args, nil
	}
	owner := ""
	if f := v.Elem().FieldByName("ExecutionID"); f.IsValid() {
		owner = f.String()
	}
	for field, table := range map[string]string{"NodeID": "nodes", "CommandID": "commands", "EndpointID": "endpoints", "CaptureID": "captures", "ArtifactID": "evidence"} {
		f := v.Elem().FieldByName(field)
		if f.IsValid() && f.Kind() == reflect.Struct {
			f = f.FieldByName(field)
		}
		if !f.IsValid() || f.Kind() != reflect.String || f.String() == "" {
			continue
		}
		if err := s.db.QueryRowContext(s.ctx, "SELECT task_id FROM "+table+" WHERE id=?", f.String()).Scan(&owner); err != nil {
			if field == "NodeID" {
				_ = s.db.QueryRowContext(s.ctx, "SELECT id FROM labs WHERE json_extract(body,'$.router.id')=?", f.String()).Scan(&owner)
			}
			if owner == "" {
				return nil, wire.Fail("not_found", "Resource is no longer retained")
			}
		}
		break
	}
	if owner != "" {
		scope.FieldByName("TaskID").SetString(owner)
	}
	return v.Elem().Interface(), nil
}
func argumentExecution(args any) string {
	v := reflect.ValueOf(args)
	if v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName("TaskID")
	if f.IsValid() {
		if f.Kind() == reflect.String {
			return f.String()
		}
		if f.Kind() == reflect.Struct {
			return f.FieldByName("TaskID").String()
		}
	}
	return ""
}

func argumentTarget(args any) string {
	v := reflect.ValueOf(args)
	for _, field := range []string{"CommandID", "EndpointID", "CaptureID", "ArtifactID", "TransferID", "NodeID", "TaskID"} {
		f := v.FieldByName(field)
		if f.IsValid() && f.Kind() == reflect.Struct {
			f = f.FieldByName(field)
		}
		if f.IsValid() && f.Kind() == reflect.String && f.String() != "" {
			return f.String()
		}
	}
	return ""
}

func (s *Server) collection(name string, a CollectionRequest) (any, error) {
	table := map[string]string{"command_list": "commands", "endpoint_list": "endpoints", "capture_list": "captures", "upload_list": "transfers"}[name]
	if a.LabID == "" && name != "upload_list" {
		return nil, wire.Fail("invalid_argument", "lab_id is required")
	}
	limit := a.Limit
	if limit == 0 {
		limit = 50
	}
	// Child rows have stable positions within their execution; uploads have a
	// global admission sequence. Cursors never require the previous row to exist.
	column := "sequence"
	rows, err := s.db.QueryContext(s.ctx, "SELECT "+column+",body FROM "+table+" WHERE (?='' OR task_id=?) AND "+column+">=? ORDER BY "+column+" LIMIT ?", a.LabID, a.LabID, a.Cursor, limit+1)
	if err != nil {
		return nil, err
	}
	type entry struct {
		cursor int64
		body   []byte
	}
	entries := []entry{}
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.cursor, &e.body); err != nil {
			break
		}
		entries = append(entries, e)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	items := []any{}
	next := a.Cursor
	eof := len(entries) <= limit
	for _, e := range entries[:min(limit, len(entries))] {
		next = e.cursor + 1
		var view any
		if table == "commands" {
			var c Command
			if err = json.Unmarshal(e.body, &c); err != nil {
				return nil, err
			}
			view = c.publicStatus()
		} else if table == "transfers" {
			var t Transfer
			if err = json.Unmarshal(e.body, &t); err != nil {
				return nil, err
			}
			view = map[string]any{"id": t.ID, "kind": t.Kind, "execution_id": t.TaskID, "state": t.State, "bytes": t.Bytes}
		} else if err = json.Unmarshal(e.body, &view); err != nil {
			return nil, err
		}
		items = append(items, view)
	}
	return map[string]any{"items": items, "next_cursor": next, "eof": eof}, nil
}
func (s *Server) inputHistory(a CommandInputReadRequest) (any, error) {
	budget := a.ByteLimit
	if budget == 0 {
		budget = 65536
	}
	if a.Offset < 0 || a.Sequence < 0 {
		return nil, wire.Fail("invalid_argument", "Input cursor must be nonnegative")
	}
	packets, err := s.db.Inputs(s.ctx, a.TaskID.TaskID, a.CommandID, a.Sequence, 16)
	if err != nil {
		return nil, err
	}
	items := []any{}
	next, offset, size := a.Sequence, a.Offset, 0
	for _, p := range packets {
		if p.Sequence != next {
			offset = 0
		}
		if offset > len(p.Data) {
			return nil, wire.Fail("invalid_argument", "Input offset exceeds packet length")
		}
		end := min(len(p.Data), offset+budget-size)
		var delivered, written bool
		if err = s.db.QueryRowContext(s.ctx, "SELECT delivered,written FROM command_inputs WHERE command_id=? AND sequence=?", a.CommandID, p.Sequence).Scan(&delivered, &written); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"sequence": p.Sequence, "offset": offset, "data": base64.StdEncoding.EncodeToString([]byte(p.Data[offset:end])), "size": len(p.Data), "eof": p.EOF, "delivered": delivered, "written": written})
		size += end - offset
		next, offset = p.Sequence, end
		if end == len(p.Data) {
			next++
			offset = 0
		}
		if size == budget {
			break
		}
	}
	return map[string]any{"command_id": a.CommandID, "lab_id": a.TaskID.TaskID, "items": items, "encoding": "base64", "next_sequence": next, "next_offset": offset, "bytes": size}, nil
}
