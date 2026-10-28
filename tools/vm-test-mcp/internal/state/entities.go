package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Each row owns one entity. Unchanged entities are never rewritten, including
// when a caller saves a snapshot containing other commands or evidence.
func (d *DB) putEntity(ctx context.Context, tx *sql.Tx, kind, id string, body []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	str := func(k string) string { var v string; _ = json.Unmarshal(fields[k], &v); return v }
	switch kind {
	case "task":
		children := map[string]json.RawMessage{}
		for _, name := range []string{"nodes", "commands", "endpoints", "captures", "artifacts"} {
			children[name] = fields[name]
			delete(fields, name)
		}
		lab, job := map[string]json.RawMessage{}, map[string]json.RawMessage{}
		for _, name := range []string{"router", "router_password", "router_iso", "router_boot_id", "network_updated", "network_lease_expires", "network_policy_hash", "network_observation", "gui_ownership", "lan", "dhcp", "dns", "internet", "network"} {
			if v, ok := fields[name]; ok {
				lab[name] = v
				delete(fields, name)
			}
		}
		for _, name := range []string{"cache_id", "derivation", "system", "nix_execution", "handoff_complete"} {
			if v, ok := fields[name]; ok {
				job[name] = v
				delete(fields, name)
			}
		}
		header, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO executions(id,kind,state,cleanup,created,ended,body) VALUES(?,?,?,?,?,?,?)
   ON CONFLICT(id) DO UPDATE SET state=excluded.state,cleanup=excluded.cleanup,ended=excluded.ended,body=excluded.body WHERE executions.body<>excluded.body`, id, str("kind"), str("state"), str("cleanup"), str("created"), str("ended"), header)
		if err != nil {
			return err
		}
		for table, values := range map[string]map[string]json.RawMessage{"labs": lab, "nix_jobs": job} {
			if str("kind") == "lab" && table == "nix_jobs" || str("kind") == "nixos" && table == "labs" {
				continue
			}
			if len(values) == 0 {
				continue
			}
			b, _ := json.Marshal(values)
			if unchangedObservation(ctx, tx, table, id, b) {
				continue
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO "+table+"(id,body) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body WHERE "+table+".body<>excluded.body", id, b); err != nil {
				return err
			}
		}
		for name, raw := range children {
			table := name
			if name == "artifacts" {
				table = "evidence"
			}
			if err = d.putChildren(ctx, tx, table, id, raw); err != nil {
				return err
			}
		}
		return nil
	case "operation":
		_, err := tx.ExecContext(ctx, `INSERT INTO operations(id,request_id,task_id,state,created,body) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET task_id=excluded.task_id,state=excluded.state,body=excluded.body WHERE operations.body<>excluded.body`, id, str("request_id"), str("task_id"), str("state"), str("created"), body)
		return err
	case "transfer":
		_, err := tx.ExecContext(ctx, `INSERT INTO transfers(id,task_id,state,updated,body) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET task_id=excluded.task_id,state=excluded.state,updated=excluded.updated,body=excluded.body WHERE transfers.body<>excluded.body`, id, str("task_id"), str("state"), str("updated"), body)
		return err
	case "job":
		files := fields["files"]
		delete(fields, "files")
		header, _ := json.Marshal(fields)
		_, err := tx.ExecContext(ctx, `INSERT INTO worker_jobs(id,state,cleanup,body) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,cleanup=excluded.cleanup,body=excluded.body WHERE worker_jobs.body<>excluded.body`, id, str("state"), str("cleanup"), header)
		if err != nil {
			return err
		}
		return d.putChildren(ctx, tx, "worker_files", id, files)
	default:
		return fmt.Errorf("unknown entity kind %q", kind)
	}
}

func (d *DB) putChildren(ctx context.Context, tx *sql.Tx, table, id string, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var children []json.RawMessage
	if err := json.Unmarshal(raw, &children); err != nil {
		return err
	}
	for i, b := range children {
		var row struct {
			ID     string
			Size   int64
			Pinned bool
		}
		if err := json.Unmarshal(b, &row); err != nil {
			return err
		}
		if (table == "nodes" || table == "commands") && unchangedObservation(ctx, tx, table, row.ID, b) {
			continue
		}
		var err error
		if table == "evidence" {
			_, err = tx.ExecContext(ctx, `INSERT INTO evidence(id,task_id,position,pinned,size,body) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET pinned=excluded.pinned,body=excluded.body WHERE evidence.body<>excluded.body`, row.ID, id, i, row.Pinned, row.Size, []byte(b))
		} else {
			_, err = tx.ExecContext(ctx, "INSERT INTO "+table+"(id,task_id,position,body) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body WHERE "+table+".body<>excluded.body", row.ID, id, i, []byte(b))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func entityTable(kind string) (string, error) {
	switch kind {
	case "task":
		return "executions", nil
	case "operation":
		return "operations", nil
	case "transfer":
		return "transfers", nil
	case "job":
		return "worker_jobs", nil
	}
	return "", fmt.Errorf("unknown entity kind")
}

func (d *DB) Load(ctx context.Context, kind, id string, v any) error {
	table, err := entityTable(kind)
	if err != nil {
		return err
	}
	var body []byte
	if err = d.QueryRowContext(ctx, "SELECT body FROM "+table+" WHERE id=?", id).Scan(&body); err != nil {
		return err
	}
	if kind == "task" || kind == "job" {
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(body, &fields); err != nil {
			return err
		}
		if kind == "task" {
			for _, table := range []string{"labs", "nix_jobs"} {
				var b []byte
				err = d.QueryRowContext(ctx, "SELECT body FROM "+table+" WHERE id=?", id).Scan(&b)
				if err == sql.ErrNoRows {
					continue
				}
				if err != nil {
					return err
				}
				var values map[string]json.RawMessage
				if err = json.Unmarshal(b, &values); err != nil {
					return err
				}
				for k, v := range values {
					fields[k] = v
				}
			}
		}
		tables := map[string]string{"nodes": "nodes", "commands": "commands", "endpoints": "endpoints", "captures": "captures", "artifacts": "evidence"}
		if kind == "job" {
			tables = map[string]string{"files": "worker_files"}
		}
		for key, table := range tables {
			rows, e := d.QueryContext(ctx, "SELECT body FROM "+table+" WHERE task_id=? ORDER BY position", id)
			if e != nil {
				return e
			}
			values := []json.RawMessage{}
			for rows.Next() {
				var b []byte
				if e = rows.Scan(&b); e != nil {
					break
				}
				values = append(values, b)
			}
			err = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if err != nil {
				return err
			}
			fields[key], _ = json.Marshal(values)
		}
		body, err = json.Marshal(fields)
		if err != nil {
			return err
		}
	}
	return json.Unmarshal(body, v)
}

func (d *DB) List(ctx context.Context, kind string) ([][]byte, error) {
	return d.listWhere(ctx, kind, "")
}
func (d *DB) Active(ctx context.Context, kind string) ([][]byte, error) {
	filter := ""
	switch kind {
	case "task":
		filter = " WHERE cleanup<>'complete'"
	case "operation":
		filter = " WHERE state='pending'"
	case "job":
		filter = " WHERE cleanup<>'complete'"
	case "transfer":
		filter = " WHERE state IN ('open','sealed')"
	}
	return d.listWhere(ctx, kind, filter)
}
func (d *DB) listWhere(ctx context.Context, kind, filter string) ([][]byte, error) {
	table, err := entityTable(kind)
	if err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, "SELECT id FROM "+table+filter+" ORDER BY sequence")
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	e := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if e != nil {
		return nil, e
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

func (d *DB) OperationForRequest(ctx context.Context, request string, v any) error {
	var b []byte
	if err := d.QueryRowContext(ctx, "SELECT body FROM operations WHERE request_id=?", request).Scan(&b); err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Admit commits the idempotency identity, target and dispatch intent together.
// Dispatch is detached from the HTTP observer only after this commit succeeds.
func (d *DB) Admit(ctx context.Context, op Document, args []byte) error {
	file, size, hash, err := d.publishBody(args)
	if err != nil {
		return err
	}
	return d.transaction(ctx, []Document{op}, func(ctx context.Context, tx *sql.Tx) error {
		// The intent's FK is deferred by inserting the operation within this closure.
		b, err := json.Marshal(op.Value)
		if err != nil {
			return err
		}
		if err = d.putEntity(ctx, tx, op.Kind, op.ID, b); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO dispatch_intents(operation_id,file,size,sha256) VALUES(?,?,?,?)", op.ID, file, size, hash)
		return err
	})
}
