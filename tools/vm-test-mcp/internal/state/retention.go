package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (d *DB) DeleteExecution(ctx context.Context, id string) error {
	return d.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
		var eligible int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM executions WHERE id=? AND cleanup='complete' AND NOT EXISTS(SELECT 1 FROM evidence WHERE task_id=? AND pinned=1)", id, id).Scan(&eligible); err != nil {
			return err
		}
		if eligible != 1 {
			return fmt.Errorf("execution is absent or protected")
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM revisions WHERE owner_id=?", id); err != nil {
			return err
		}
		for _, table := range []string{"events", "command_specs", "operations"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE task_id=?", id); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM executions WHERE id=? AND cleanup='complete' AND NOT EXISTS(SELECT 1 FROM evidence WHERE task_id=? AND pinned=1)", id, id)
		return err
	})
}
func (d *DB) PruneDetails(ctx context.Context, id string) error {
	return d.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
		for _, table := range []string{"events", "command_specs", "commands", "nodes", "endpoints", "captures"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE task_id=?", id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM evidence WHERE task_id=? AND pinned=0", id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM dispatch_intents WHERE operation_id IN(SELECT id FROM operations WHERE task_id=? AND state<>'pending')", id)
		return err
	})
}
func (d *DB) SweepBodies(ctx context.Context) error {
	rows, err := d.QueryContext(ctx, `SELECT file FROM command_specs UNION SELECT file FROM command_inputs UNION SELECT file FROM dispatch_intents`)
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
	entries, err := os.ReadDir(filepath.Join(d.dir, "bodies"))
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
		if err = os.Remove(filepath.Join(d.dir, "bodies", entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
func (d *DB) Compact(ctx context.Context) error {
	_, err := d.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE); PRAGMA incremental_vacuum(256)")
	return err
}
func (d *DB) InputProgress(ctx context.Context, task, command string, delivered, consumed int) error {
	_, err := d.ExecContext(ctx, `UPDATE command_inputs SET delivered=MAX(delivered,sequence<?),consumed=MAX(consumed,sequence<?) WHERE task_id=? AND command_id=? AND (sequence<? AND delivered=0 OR sequence<? AND consumed=0)`, delivered, consumed, task, command, delivered, consumed)
	return err
}

func (d *DB) DeliveredInput(ctx context.Context, task, command string, sequence int) (int, error) {
	if _, err := d.ExecContext(ctx, "UPDATE command_inputs SET delivered=1 WHERE task_id=? AND command_id=? AND sequence=?", task, command, sequence); err != nil {
		return 0, err
	}
	var next int
	err := d.QueryRowContext(ctx, `SELECT COALESCE(MIN(CASE WHEN delivered=0 THEN sequence END),MAX(sequence)+1,0) FROM command_inputs WHERE task_id=? AND command_id=?`, task, command).Scan(&next)
	return next, err
}
