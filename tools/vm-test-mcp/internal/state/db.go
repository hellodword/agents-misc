package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct{ *sql.DB }

func Open(dir string) (*DB, error) {
	u := url.URL{Scheme: "file", Path: filepath.Join(dir, "state.sqlite")}
	q := u.Query()
	q.Set("_foreign_keys", "on")
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
	if e = db.QueryRow("PRAGMA user_version").Scan(&version); e != nil || (version != 0 && version != 9) {
		db.Close()
		return nil, fmt.Errorf("unsupported state schema version: %d", version)
	}
	_, e = db.Exec(`CREATE TABLE IF NOT EXISTS documents (
 kind TEXT NOT NULL, id TEXT NOT NULL, sequence INTEGER PRIMARY KEY AUTOINCREMENT,
 body BLOB NOT NULL, UNIQUE(kind,id));
 CREATE TABLE IF NOT EXISTS events (
 sequence INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, body BLOB NOT NULL);
 PRAGMA user_version=9;`)
	if e != nil {
		db.Close()
		return nil, e
	}
	return &DB{db}, nil
}

type Document struct {
	Kind, ID string
	Value    any
}

func (d *DB) Save(ctx context.Context, docs ...Document) error {
	tx, e := d.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, doc := range docs {
		b, e := json.Marshal(doc.Value)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO documents(kind,id,body) VALUES(?,?,?) ON CONFLICT(kind,id) DO UPDATE SET body=excluded.body`, doc.Kind, doc.ID, b); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (d *DB) Load(ctx context.Context, kind, id string, v any) error {
	var b []byte
	if e := d.QueryRowContext(ctx, "SELECT body FROM documents WHERE kind=? AND id=?", kind, id).Scan(&b); e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func (d *DB) List(ctx context.Context, kind string) ([][]byte, error) {
	rows, e := d.QueryContext(ctx, "SELECT body FROM documents WHERE kind=? ORDER BY sequence", kind)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := [][]byte{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
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
