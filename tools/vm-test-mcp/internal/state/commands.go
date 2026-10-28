package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// InputQueue describes controller acceptance. Guest NextInput independently
// acknowledges consumption; delivery never removes the retained input history.
type InputQueue struct {
	NextSequence int   `json:"next_sequence"`
	Delivered    int   `json:"delivered_sequence"`
	Consumed     int   `json:"consumed_sequence"`
	Bytes        int64 `json:"bytes"`
	Closed       bool  `json:"closed"`
}

func (q *InputQueue) Accept(p wire.Input) {
	q.NextSequence++
	q.Bytes += int64(len(p.Data))
	q.Closed = p.EOF
}

func (d *DB) CreateCommand(ctx context.Context, task Document, id string, spec wire.CommandSpec) error {
	body, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	file, size, hash, err := d.publishBody(body)
	if err != nil {
		return err
	}
	return d.transaction(ctx, []Document{task}, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO command_specs(task_id,command_id,file,size,sha256) VALUES(?,?,?,?,?)", task.ID, id, file, size, hash)
		return err
	})
}

func (d *DB) AppendInput(ctx context.Context, task Document, command string, packet wire.Input) error {
	file, size, hash, err := d.publishBody([]byte(packet.Data))
	if err != nil {
		return err
	}
	return d.transaction(ctx, []Document{task}, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO command_inputs(task_id,command_id,sequence,file,size,sha256,eof) VALUES(?,?,?,?,?,?,?)", task.ID, command, packet.Sequence, file, size, hash, packet.EOF)
		return err
	})
}

func (d *DB) CommandSpec(ctx context.Context, task, command string) (wire.CommandSpec, error) {
	var file, hash string
	var size int64
	var v wire.CommandSpec
	err := d.QueryRowContext(ctx, "SELECT file,size,sha256 FROM command_specs WHERE task_id=? AND command_id=?", task, command).Scan(&file, &size, &hash)
	if err == nil {
		var body []byte
		body, err = d.readBody(file, size, hash)
		if err == nil {
			err = json.Unmarshal(body, &v)
		}
	}
	return v, err
}

func (d *DB) Inputs(ctx context.Context, task, command string, after, limit int) ([]wire.Input, error) {
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
		body, err := d.readBody(file, size, hash)
		if err != nil {
			return nil, err
		}
		v.Data = string(body)
		values = append(values, v)
	}
	return values, rows.Err()
}

// PurgeCommandData is only called by the existing explicit/retention purge path.
// Ordinary status updates never rewrite or delete accepted command payloads.
func (d *DB) PurgeCommandData(ctx context.Context, task Document) error {
	return d.transaction(ctx, []Document{task}, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM command_specs WHERE task_id=?", task.ID)
		return err
	})
}
