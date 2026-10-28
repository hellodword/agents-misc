package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
	dir string
}

func Open(dir string) (*DB, error) {
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "state.sqlite")}
	q := u.Query()
	q.Set("_foreign_keys", "on")
	q.Set("_auto_vacuum", "incremental")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "FULL")
	q.Set("_busy_timeout", "5000")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite3", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	// Refuse a build accidentally compiled with SQLite's extension loader enabled.
	var omitted int
	e = db.QueryRow("SELECT sqlite_compileoption_used('OMIT_LOAD_EXTENSION')").Scan(&omitted)
	if e != nil || omitted != 1 {
		db.Close()
		return nil, fmt.Errorf("build with -tags sqlite_omit_load_extension")
	}
	var version int
	if e = db.QueryRow("PRAGMA user_version").Scan(&version); e != nil || (version != 0 && version != SchemaVersion) {
		db.Close()
		return nil, fmt.Errorf("state schema %d is incompatible: stop old services, confirm resource release, and use a new empty state directory", version)
	}
	d := &DB{DB: db, dir: dir}
	if version == 0 {
		var tables int
		if e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); e != nil || tables != 0 {
			db.Close()
			return nil, fmt.Errorf("new state requires an empty database")
		}
		if _, e = db.Exec(schema); e != nil {
			db.Close()
			return nil, e
		}
	}

	return d, nil
}

type Document struct {
	Kind, ID string
	Value    any
}

func (d *DB) Save(ctx context.Context, docs ...Document) error {
	return d.transaction(ctx, docs, nil)
}

func (d *DB) transaction(ctx context.Context, docs []Document, change func(context.Context, *sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := d.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if change != nil {
		if e = change(ctx, tx); e != nil {
			return e
		}
	}
	for _, doc := range docs {
		b, e := json.Marshal(doc.Value)
		if e != nil {
			return e
		}
		if e = d.putEntity(ctx, tx, doc.Kind, doc.ID, b); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (d *DB) Event(ctx context.Context, task string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	_, e = d.ExecContext(ctx, "INSERT INTO events(task_id,body) VALUES(?,?)", task, b)
	return e
}
func (d *DB) Events(ctx context.Context, task string, after int64, limit int) ([]json.RawMessage, int64, error) {
	rows, e := d.QueryContext(ctx, "SELECT sequence,body FROM events WHERE task_id=? AND sequence>? ORDER BY sequence LIMIT ?", task, after, limit)
	if e != nil {
		return nil, after, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&after, &b); e != nil {
			return nil, after, e
		}
		out = append(out, b)
	}
	return out, after, rows.Err()
}
