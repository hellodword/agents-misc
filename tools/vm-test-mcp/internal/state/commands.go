package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// InputQueue describes controller acceptance. Guest NextInput independently
// acknowledges consumption; delivery never removes the retained input history.
type InputQueue struct {
	NextSequence int   `json:"next_sequence"`
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
	return d.transaction(ctx, []Document{task}, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO command_specs(task_id,command_id,body) VALUES(?,?,?)", task.ID, id, body)
		return err
	})
}

func (d *DB) AppendInput(ctx context.Context, task Document, command string, packet wire.Input) error {
	body, err := json.Marshal(packet)
	if err != nil {
		return err
	}
	return d.transaction(ctx, []Document{task}, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO command_inputs(task_id,command_id,sequence,body) VALUES(?,?,?,?)", task.ID, command, packet.Sequence, body)
		return err
	})
}

func (d *DB) CommandSpec(ctx context.Context, task, command string) (wire.CommandSpec, error) {
	var body []byte
	var v wire.CommandSpec
	err := d.QueryRowContext(ctx, "SELECT body FROM command_specs WHERE task_id=? AND command_id=?", task, command).Scan(&body)
	if err == nil {
		err = json.Unmarshal(body, &v)
	}
	return v, err
}

func (d *DB) Inputs(ctx context.Context, task, command string, after, limit int) ([]wire.Input, error) {
	if after < 0 || limit < 1 || limit > 16 {
		return nil, fmt.Errorf("input range must start at a nonnegative sequence with limit 1..16")
	}
	rows, err := d.QueryContext(ctx, "SELECT body FROM command_inputs WHERE task_id=? AND command_id=? AND sequence>=? ORDER BY sequence LIMIT ?", task, command, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []wire.Input{}
	for rows.Next() {
		var body []byte
		var v wire.Input
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &v); err != nil {
			return nil, err
		}
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

func (d *DB) migrateCommands(version int) error {
	if version >= 10 {
		return nil
	}
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stop()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE command_specs (
 task_id TEXT NOT NULL, command_id TEXT NOT NULL, body BLOB NOT NULL,
 PRIMARY KEY(task_id,command_id));
 CREATE TABLE command_inputs (
 task_id TEXT NOT NULL, command_id TEXT NOT NULL, sequence INTEGER NOT NULL,
 body BLOB NOT NULL, PRIMARY KEY(task_id,command_id,sequence),
 FOREIGN KEY(task_id,command_id) REFERENCES command_specs(task_id,command_id) ON DELETE CASCADE);`); err != nil {
		return err
	}
	// Read one historical task at a time. Keep raw JSON for unrelated fields so
	// migration cannot discard data unknown to the current controller model.
	var after int64
	for {
		var id string
		var body []byte
		err = tx.QueryRowContext(ctx, "SELECT sequence,id,body FROM documents WHERE kind='task' AND sequence>? ORDER BY sequence LIMIT 1", after).Scan(&after, &id, &body)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		body, err = migrateTask(ctx, tx, id, body)
		if err != nil {
			return fmt.Errorf("migrate task %s: %w", id, err)
		}
		if _, err = putDocument(ctx, tx, "task", id, body); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=10"); err != nil {
		return err
	}
	return tx.Commit()
}

func migrateTask(ctx context.Context, tx *sql.Tx, id string, body []byte) ([]byte, error) {
	var task map[string]json.RawMessage
	if err := json.Unmarshal(body, &task); err != nil {
		return nil, err
	}
	var commands []map[string]json.RawMessage
	if raw := task["commands"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &commands); err != nil {
			return nil, err
		}
	}
	for _, c := range commands {
		var command string
		if err := json.Unmarshal(c["id"], &command); err != nil || command == "" {
			return nil, fmt.Errorf("missing command identity")
		}
		var packets []wire.Input
		if raw := c["input"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &packets); err != nil {
				return nil, err
			}
		}
		if spec := c["spec"]; len(spec) > 0 {
			if _, err := tx.ExecContext(ctx, "INSERT INTO command_specs(task_id,command_id,body) VALUES(?,?,?)", id, command, []byte(spec)); err != nil {
				return nil, err
			}
		}
		var q InputQueue
		for i, p := range packets {
			if p.Sequence != i || q.Closed {
				return nil, fmt.Errorf("command %s has inconsistent input sequence", command)
			}
			q.Accept(p)
			packet, err := json.Marshal(p)
			if err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO command_inputs(task_id,command_id,sequence,body) VALUES(?,?,?,?)", id, command, i, packet); err != nil {
				return nil, err
			}
		}
		delete(c, "spec")
		delete(c, "input")
		c["stdin"], _ = json.Marshal(q)
	}
	if commands != nil {
		task["commands"], _ = json.Marshal(commands)
	}
	return json.Marshal(task)
}
