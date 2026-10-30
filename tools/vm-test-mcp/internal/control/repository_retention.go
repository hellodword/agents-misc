package control

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (d *repository) DeleteExecution(ctx context.Context, id string) error {
	return d.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
		var eligible int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM executions WHERE id=? AND settled=1", id).Scan(&eligible); err != nil {
			return err
		}
		if eligible != 1 {
			return fmt.Errorf("execution is absent or protected")
		}
		for _, table := range []string{"events"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE task_id=?", id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM evidence WHERE task_id=? AND pinned=0", id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM executions WHERE id=? AND settled=1", id)
		return err
	})
}
func (d *repository) PruneDetails(ctx context.Context, id string) error {
	return d.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
		for _, table := range []string{"events", "commands", "nodes", "endpoints", "captures"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE task_id=?", id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM evidence WHERE task_id=? AND pinned=0", id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE operations SET intent_file='',intent_size=0,intent_sha256='',body=json_remove(body,'$.intent','$.payload') WHERE task_id=? AND state<>'pending'", id)
		return err
	})
}
func (d *repository) SweepBodies(ctx context.Context) error {
	rows, err := d.QueryContext(ctx, `SELECT spec_file FROM commands UNION SELECT file FROM command_inputs UNION SELECT intent_file FROM operations UNION SELECT json_extract(body,'$.payload.file') FROM operations WHERE json_extract(body,'$.payload.file') IS NOT NULL`)
	if err != nil {
		return err
	}
	refs := map[string]bool{}
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			break
		}
		refs[p] = true
	}
	e := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if e != nil {
		return e
	}
	entries, err := os.ReadDir(filepath.Join(d.Directory(), "bodies"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if refs[filepath.Join("bodies", entry.Name())] || !entry.Type().IsRegular() {
			continue
		}
		st, err := entry.Info()
		if err != nil {
			return err
		}
		if time.Since(st.ModTime()) < time.Hour {
			continue
		}
		if err = os.Remove(filepath.Join(d.Directory(), "bodies", entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
func (d *repository) DeliveredInput(ctx context.Context, task *Task, c *Command, sequence int) (int, error) {
	previous := c.Stdin.Delivered
	next := previous
	err := d.Transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE command_inputs SET delivered=1 WHERE command_id=? AND sequence=?", c.ID, sequence); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MIN(CASE WHEN delivered=0 THEN sequence END),MAX(sequence)+1,0) FROM command_inputs WHERE command_id=?`, c.ID).Scan(&next); err != nil {
			return err
		}
		c.Stdin.Delivered = next
		return task.store(ctx, tx)
	})
	if err != nil {
		c.Stdin.Delivered = previous
	} else {
		task.pendingEvents = nil
		task.repository = d
	}
	return next, err
}
