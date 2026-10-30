package state

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"net/url"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	*sql.DB
	dir        string
	Role       string
	InstanceID string
}

func OpenController(dir string) (*DB, error) { return open(dir, "controller", controllerSchema) }
func OpenWorker(dir string) (*DB, error)     { return open(dir, "nix-worker", workerSchema) }
func OpenExecutor(dir string) (*DB, error)   { return open(dir, "nix-executor", executorSchema) }

func open(dir, role, schema string) (*DB, error) {
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
	d := &DB{DB: db, dir: dir, Role: role}
	if version == 0 {
		var tables int
		if e = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); e != nil || tables != 0 {
			db.Close()
			return nil, fmt.Errorf("new state requires an empty database")
		}
		e = d.Transaction(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, schema+metadataSchema); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "INSERT INTO store(singleton,role,instance_id) VALUES(1,?,?)", role, wire.ID())
			return err
		})
		if e != nil {
			db.Close()
			return nil, e
		}

	}
	var storedRole string
	if e = db.QueryRow("SELECT role,instance_id FROM store WHERE singleton=1").Scan(&storedRole, &d.InstanceID); e != nil || storedRole != role {
		db.Close()
		return nil, fmt.Errorf("state database belongs to a different role or has no instance identity")
	}
	return d, nil
}

func (d *DB) Directory() string { return d.dir }
func (d *DB) Transaction(ctx context.Context, change func(context.Context, *sql.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = change(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (d *DB) Compact(ctx context.Context) error {
	_, err := d.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE); PRAGMA incremental_vacuum(256)")
	return err
}
