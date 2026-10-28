package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type repository struct{ *state.DB }

func openRepository(dir string) (*repository, error) {
	db, err := state.OpenController(dir)
	if err != nil {
		return nil, err
	}
	return &repository{db}, nil
}

type record interface {
	store(context.Context, *sql.Tx) error
}

func (d *repository) Save(ctx context.Context, records ...record) error {
	return d.transaction(ctx, records, nil)
}
func (d *repository) transaction(ctx context.Context, records []record, change func(context.Context, *sql.Tx) error) error {
	err := d.Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if change != nil {
			if err := change(ctx, tx); err != nil {
				return err
			}
		}
		for _, r := range records {
			if err := r.store(ctx, tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		for _, r := range records {
			if t, ok := r.(*Task); ok {
				t.pendingEvents = nil
				t.Artifacts = nil
				t.repository = d
			}
		}
	}
	return err
}
func (t *Task) store(ctx context.Context, tx *sql.Tx) error {
	t.PrepareSave()
	header := wire.JSON(t.executionRecord)
	_, err := tx.ExecContext(ctx, `INSERT INTO executions(id,kind,state,cleanup,created,ended,revision,body) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,cleanup=excluded.cleanup,ended=excluded.ended,revision=excluded.revision,body=excluded.body WHERE executions.body<>excluded.body`, t.ID, t.Kind, t.State, t.Cleanup, t.Created.Format(time.RFC3339Nano), dbTime(t.Ended), t.Revision, header)
	if err != nil {
		return err
	}
	if t.Kind == "lab" {
		// Resource rows are independent of lab configuration and of each other.
		meta := *t.lab()
		meta.Nodes, meta.Commands, meta.Endpoints, meta.Captures = nil, nil, nil, nil
		meta.NetworkUpdated, meta.NetworkLeaseExpires = zeroTime, zeroTime
		if meta.NetworkObservation != nil && meta.NetworkObservation.Error == nil {
			meta.NetworkObservation = nil
		}
		if meta.Router != nil {
			meta.Router = durableNode(meta.Router)
		}
		if err = putMetadata(ctx, tx, "labs", t.ID, wire.JSON(meta)); err != nil {
			return err
		}
		for i, n := range t.lab().Nodes {
			if err = putChild(ctx, tx, "nodes", t.ID, n.ID, i, wire.JSON(durableNode(n))); err != nil {
				return err
			}
		}
		for _, c := range t.lab().Commands {
			if _, err = tx.ExecContext(ctx, `UPDATE command_inputs SET delivered=MAX(delivered,sequence<?),written=MAX(written,sequence<?) WHERE command_id=? AND (sequence<? AND delivered=0 OR sequence<? AND written=0)`, c.Stdin.Delivered, c.Stdin.Written, c.ID, c.Stdin.Delivered, c.Stdin.Written); err != nil {
				return err
			}

			value := *c
			value.Schedule = PollSchedule{}
			value.Observation = durableObservation(c.Observation)
			value.Updated = nil
			if _, err = tx.ExecContext(ctx, `INSERT INTO commands(id,task_id,node_id,needs_poll,revision,body) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET needs_poll=excluded.needs_poll,revision=excluded.revision,body=excluded.body WHERE commands.body<>excluded.body`, c.ID, t.ID, c.NodeID, c.Polling(), c.Revision, wire.JSON(value)); err != nil {
				return err
			}
		}
		for i, e := range t.lab().Endpoints {
			if err = putChild(ctx, tx, "endpoints", t.ID, e.ID, i, wire.JSON(e)); err != nil {
				return err
			}
		}
		for i, c := range t.lab().Captures {
			if err = putChild(ctx, tx, "captures", t.ID, c.ID, i, wire.JSON(c)); err != nil {
				return err
			}
		}
	} else {
		if err = putMetadata(ctx, tx, "nix_jobs", t.ID, wire.JSON(t.nix())); err != nil {
			return err
		}
	}
	for i, a := range t.Artifacts {
		a.ExecutionID, a.ExecutionKind = t.ID, t.Kind
		_, err = tx.ExecContext(ctx, `INSERT INTO evidence(id,task_id,position,pinned,size,body) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET pinned=excluded.pinned,body=excluded.body WHERE evidence.body<>excluded.body`, a.ID, t.ID, i, a.Pinned, a.Size, wire.JSON(a))
		if err != nil {
			return err
		}
	}
	if t.Cleanup == "complete" && t.DetailExpires != nil {
		if _, err = tx.ExecContext(ctx, "UPDATE evidence SET protected=0,expires=?,success=? WHERE task_id=? AND protected=1", dbTime(t.DetailExpires), t.State == "finished" && t.Conclusion == "passed", t.ID); err != nil {
			return err
		}
	}
	for _, event := range t.pendingEvents {
		if _, err = tx.ExecContext(ctx, "INSERT INTO events(task_id,body) VALUES(?,?)", t.ID, wire.JSON(event)); err != nil {
			return err
		}
	}
	return nil
}
func putMetadata(ctx context.Context, tx *sql.Tx, table, id string, body []byte) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO "+table+"(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body WHERE "+table+".body<>excluded.body", id, body)
	return err
}
func putChild(ctx context.Context, tx *sql.Tx, table, owner, id string, position int, body []byte) error {
	if table == "endpoints" || table == "captures" {
		_, err := tx.ExecContext(ctx, "INSERT INTO "+table+"(id,task_id,position,state,body) VALUES(?,?,?,json_extract(?,'$.state'),?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,body=excluded.body WHERE "+table+".body<>excluded.body", id, owner, position, body, body)
		return err
	}

	_, err := tx.ExecContext(ctx, "INSERT INTO "+table+"(id,task_id,position,body) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body WHERE "+table+".body<>excluded.body", id, owner, position, body)
	return err
}
func (o *Operation) store(ctx context.Context, tx *sql.Tx) error {
	o.PrepareSave()
	_, err := tx.ExecContext(ctx, `INSERT INTO operations(id,request_id,task_id,state,created,ended,revision,intent_stage,intent_file,intent_size,intent_sha256,body) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET task_id=excluded.task_id,state=excluded.state,ended=excluded.ended,revision=excluded.revision,intent_stage=excluded.intent_stage,intent_file=excluded.intent_file,intent_size=excluded.intent_size,intent_sha256=excluded.intent_sha256,body=excluded.body WHERE operations.body<>excluded.body`, o.ID, o.RequestID, o.TaskID, o.State, o.Created.Format(time.RFC3339Nano), dbTime(o.Ended), o.Revision, o.IntentStage, o.Intent.File, o.Intent.Size, o.Intent.SHA256, wire.JSON(o))
	return err
}
func (t *Transfer) store(ctx context.Context, tx *sql.Tx) error {
	t.PrepareSave()
	_, err := tx.ExecContext(ctx, `INSERT INTO transfers(id,task_id,state,updated,revision,body) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET task_id=excluded.task_id,state=excluded.state,updated=excluded.updated,revision=excluded.revision,body=excluded.body WHERE transfers.body<>excluded.body`, t.ID, t.TaskID, t.State, t.Updated.Format(time.RFC3339Nano), t.Revision, wire.JSON(t))
	return err
}

func (d *repository) loadTask(ctx context.Context, id string) (*Task, error) {
	t := &Task{repository: d}
	var body []byte
	if err := d.QueryRowContext(ctx, "SELECT body FROM executions WHERE id=?", id).Scan(&body); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &t.executionRecord); err != nil {
		return nil, err
	}
	table := "labs"
	t.domain = &LabState{}
	if t.Kind == "nixos" {
		table = "nix_jobs"
		t.domain = &NixJobState{}
	}
	target := t.domain
	if err := d.QueryRowContext(ctx, "SELECT body FROM "+table+" WHERE id=?", id).Scan(&body); err != nil && err != sql.ErrNoRows {
		return nil, err
	} else if err == nil {
		if err = json.Unmarshal(body, target); err != nil {
			return nil, err
		}
	}
	if t.Kind == "lab" {
		if err := loadChildren(ctx, d, "nodes", id, &t.lab().Nodes); err != nil {
			return nil, err
		}
		if err := loadChildren(ctx, d, "commands", id, &t.lab().Commands); err != nil {
			return nil, err
		}
		if err := loadChildren(ctx, d, "endpoints", id, &t.lab().Endpoints); err != nil {
			return nil, err
		}
		if err := loadChildren(ctx, d, "captures", id, &t.lab().Captures); err != nil {
			return nil, err
		}
	}
	return t, nil
}
func loadChildren[T any](ctx context.Context, d *repository, table, id string, values *[]*T) error {
	order, filter := "position", ""
	if table == "commands" {
		order, filter = "sequence", " AND needs_poll=1"
	}
	if table == "endpoints" {
		order, filter = "sequence", " AND state<>'closed'"
	}
	if table == "captures" {
		order, filter = "sequence", " AND state NOT IN ('stopped','released')"
	}
	rows, err := d.QueryContext(ctx, "SELECT body FROM "+table+" WHERE task_id=?"+filter+" ORDER BY "+order, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	*values = []*T{}
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			return err
		}
		var v T
		if err = json.Unmarshal(body, &v); err != nil {
			return err
		}
		*values = append(*values, &v)
	}
	return rows.Err()
}
func (d *repository) Load(ctx context.Context, kind, id string, target any) error {
	if kind == "task" {
		v, err := d.loadTask(ctx, id)
		if err != nil {
			return err
		}
		if t, ok := target.(*Task); ok {
			*t = *v
			return nil
		}
		return json.Unmarshal(wire.JSON(v), target)
	}
	table := map[string]string{"operation": "operations", "transfer": "transfers"}[kind]
	if table == "" {
		return fmt.Errorf("unknown record kind")
	}
	var body []byte
	if err := d.QueryRowContext(ctx, "SELECT body FROM "+table+" WHERE id=?", id).Scan(&body); err != nil {
		return err
	}
	return json.Unmarshal(body, target)
}
func (d *repository) Active(ctx context.Context, kind string) ([][]byte, error) {
	table, where := "executions", "cleanup<>'complete'"
	switch kind {
	case "operation":
		table, where = "operations", "state='pending'"
	case "transfer":
		table, where = "transfers", "state IN ('open','ready','settling','deleting')"
	}
	return d.list(ctx, kind, table, where)
}
func (d *repository) List(ctx context.Context, kind string) ([][]byte, error) {
	table := map[string]string{"task": "executions", "operation": "operations", "transfer": "transfers"}[kind]
	return d.list(ctx, kind, table, "1")
}
func (d *repository) list(ctx context.Context, kind, table, where string) ([][]byte, error) {
	rows, err := d.QueryContext(ctx, "SELECT id FROM "+table+" WHERE "+where+" ORDER BY sequence")
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}
	out := [][]byte{}
	for _, id := range ids {
		var value json.RawMessage
		if err = d.Load(ctx, kind, id, &value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}
func (d *repository) OperationForRequest(ctx context.Context, request string, v any) error {
	var body []byte
	if err := d.QueryRowContext(ctx, "SELECT body FROM operations WHERE request_id=?", request).Scan(&body); err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}
func (d *repository) Admit(ctx context.Context, op *Operation, args []byte, records ...record) error {
	op.IntentStage = "confirmed"
	if len(args) != 0 {
		var err error
		op.Intent.File, op.Intent.Size, op.Intent.SHA256, err = d.PublishBody(args)
		if err != nil {
			return err
		}
		op.IntentStage = "prepared"
	}
	return d.transaction(ctx, append([]record{op}, records...), nil)
}
func (d *repository) DispatchIntent(ctx context.Context, id string) ([]byte, string, error) {
	var file, hash, stage string
	var size int64
	err := d.QueryRowContext(ctx, "SELECT intent_file,intent_size,intent_sha256,intent_stage FROM operations WHERE id=?", id).Scan(&file, &size, &hash, &stage)
	if err != nil {
		return nil, "", err
	}
	if file == "" {
		return nil, stage, nil
	}
	body, err := d.ReadBody(file, size, hash)
	return body, stage, err
}
func (d *repository) MarkDispatched(ctx context.Context, id string) error {
	var op Operation
	if err := d.Load(ctx, "operation", id, &op); err != nil {
		return err
	}
	op.IntentStage = "dispatched"
	return d.Save(ctx, &op)
}
func (d *repository) Events(ctx context.Context, id string, after int64, limit int) ([]json.RawMessage, int64, error) {
	rows, err := d.QueryContext(ctx, "SELECT sequence,body FROM events WHERE task_id=? AND sequence>? ORDER BY sequence LIMIT ?", id, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&after, &body); err != nil {
			return nil, after, err
		}
		out = append(out, body)
	}
	return out, after, rows.Err()
}

func dbTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func (d *repository) command(ctx context.Context, owner, id string) (*Command, error) {
	var body []byte
	if err := d.QueryRowContext(ctx, "SELECT body FROM commands WHERE task_id=? AND id=?", owner, id).Scan(&body); err != nil {
		return nil, wire.Fail("not_found", "Command is no longer retained")
	}
	var c Command
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (d *repository) child(ctx context.Context, table, id string, target any) error {
	var body []byte
	if err := d.QueryRowContext(ctx, "SELECT body FROM "+table+" WHERE id=?", id).Scan(&body); err != nil {
		return wire.Fail("not_found", "Resource is no longer retained")
	}
	return json.Unmarshal(body, target)
}
