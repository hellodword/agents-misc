package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (d *repository) CreateCommand(ctx context.Context, task *Task, id string, spec wire.CommandSpec) error {
	body, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	file, size, hash, err := d.PublishBody(body)
	if err != nil {
		return err
	}
	return d.CreateCommandReference(ctx, task, id, BodyRef{file, size, hash})
}
func (d *repository) CreateCommandReference(ctx context.Context, task *Task, id string, ref BodyRef) error {
	// Admission payload and command specification share one immutable object.
	err := d.Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := task.store(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE commands SET spec_file=?,spec_size=?,spec_sha256=? WHERE task_id=? AND id=?", ref.File, ref.Size, ref.SHA256, task.ID, id)
		return err
	})
	if err == nil {
		task.pendingEvents = nil
		task.repository = d
	}
	return err
}

func (d *repository) AppendInput(ctx context.Context, task *Task, command string, packet wire.Input) error {
	file, size, hash, err := d.PublishBody([]byte(packet.Data))
	if err != nil {
		return err
	}
	return d.AppendInputReference(ctx, task, command, packet, BodyRef{file, size, hash})
}
func (d *repository) AppendInputReference(ctx context.Context, task *Task, command string, packet wire.Input, ref BodyRef) error {
	return d.transaction(ctx, []record{task}, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO command_inputs(task_id,command_id,sequence,file,size,sha256,eof) VALUES(?,?,?,?,?,?,?)", task.ID, command, packet.Sequence, ref.File, ref.Size, ref.SHA256, packet.EOF)
		return err
	})
}

func (d *repository) CommandSpec(ctx context.Context, task, command string) (wire.CommandSpec, error) {
	var file, hash string
	var size int64
	var v wire.CommandSpec
	err := d.QueryRowContext(ctx, "SELECT spec_file,spec_size,spec_sha256 FROM commands WHERE task_id=? AND id=?", task, command).Scan(&file, &size, &hash)
	if err == nil {
		var body []byte
		body, err = d.ReadBody(file, size, hash)
		if err == nil {
			err = json.Unmarshal(body, &v)
		}
	}
	v.ID = command
	return v, err
}

func (d *repository) Inputs(ctx context.Context, task, command string, after, limit int) ([]wire.Input, error) {
	if after < 0 || limit < 1 || limit > 16 {
		return nil, fmt.Errorf("input range must start at a nonnegative sequence with limit 1..16")
	}
	rows, err := d.QueryContext(ctx, "SELECT sequence,file,size,sha256,eof FROM command_inputs WHERE task_id=? AND command_id=? AND sequence>=? ORDER BY sequence LIMIT ?", task, command, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []wire.Input{}
	for rows.Next() {
		var file, hash string
		var size int64
		var v wire.Input
		if err = rows.Scan(&v.Sequence, &file, &size, &hash, &v.EOF); err != nil {
			return nil, err
		}
		body, err := d.ReadBody(file, size, hash)
		if err != nil {
			return nil, err
		}
		v.Data = string(body)
		values = append(values, v)
	}
	return values, rows.Err()
}
