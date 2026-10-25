package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

type TransferObject struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	File   string `json:"file"`
}

func (d *DB) CreateTransfer(ctx context.Context, doc Document, objects map[string]TransferObject) error {
	if doc.Kind != "transfer" {
		return fmt.Errorf("expected a transfer document")
	}
	return d.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
		body, err := json.Marshal(doc.Value)
		if err != nil {
			return err
		}
		if _, err = putDocument(ctx, tx, doc.Kind, doc.ID, body); err != nil {
			return err
		}
		for name, object := range objects {
			body, err = json.Marshal(object)
			if err != nil {
				return err
			}
			if err = putTransferObject(ctx, tx, doc.ID, name, body); err != nil {
				return err
			}
		}
		return nil
	})
}

// StoreTransferObject records one object without rewriting the cache inventory.
// Its file must already be durable and immutable. The index commit publishes it;
// failure leaves the prior index and file intact.
func (d *DB) StoreTransferObject(ctx context.Context, id, name string, object TransferObject) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return d.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
		return putTransferObject(ctx, tx, id, name, body)
	})
}

func putTransferObject(ctx context.Context, tx *sql.Tx, id, name string, body []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO transfer_objects(transfer_id,name,body) VALUES(?,?,?)
 ON CONFLICT(transfer_id,name) DO UPDATE SET body=excluded.body`, id, name, body)
	return err
}

func (d *DB) TransferObjects(ctx context.Context, id string) (map[string]TransferObject, int64, error) {
	rows, err := d.QueryContext(ctx, "SELECT name,body FROM transfer_objects WHERE transfer_id=? ORDER BY name", id)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	objects := map[string]TransferObject{}
	var total int64
	for rows.Next() {
		var name string
		var body []byte
		var value TransferObject
		if err = rows.Scan(&name, &body); err != nil {
			return nil, 0, err
		}
		if err = json.Unmarshal(body, &value); err != nil {
			return nil, 0, fmt.Errorf("invalid transfer object metadata: %w", err)
		}
		if value.Size < 0 || value.Size > math.MaxInt64-total {
			return nil, 0, fmt.Errorf("invalid transfer object size")
		}
		objects[name] = value
		total += value.Size
	}
	return objects, total, rows.Err()
}

func (d *DB) migrateTransfers(version int) error {
	if version == 11 {
		return nil
	}
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stop()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE transfer_objects (
 transfer_id TEXT NOT NULL, name TEXT NOT NULL, body BLOB NOT NULL,
 document_kind TEXT NOT NULL DEFAULT 'transfer' CHECK(document_kind='transfer'),
 PRIMARY KEY(transfer_id,name),
 FOREIGN KEY(document_kind,transfer_id) REFERENCES documents(kind,id));`); err != nil {
		return err
	}
	var after int64
	for {
		var id string
		var body []byte
		err = tx.QueryRowContext(ctx, "SELECT sequence,id,body FROM documents WHERE kind='transfer' AND sequence>? ORDER BY sequence LIMIT 1", after).Scan(&after, &id, &body)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		var header map[string]json.RawMessage
		if err = json.Unmarshal(body, &header); err != nil {
			return err
		}
		var objects map[string]json.RawMessage
		if raw := header["objects"]; len(raw) != 0 {
			if err = json.Unmarshal(raw, &objects); err != nil {
				return fmt.Errorf("migrate transfer %s: %w", id, err)
			}
		}
		var total int64
		for name, raw := range objects {
			var value TransferObject
			if err = json.Unmarshal(raw, &value); err != nil {
				return fmt.Errorf("migrate transfer %s: %w", id, err)
			}
			if value.Size < 0 || value.Size > math.MaxInt64-total {
				return fmt.Errorf("migrate transfer %s: invalid object size", id)
			}
			total += value.Size
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(raw, &fields); err != nil || fields == nil {
				return fmt.Errorf("migrate transfer %s: invalid object metadata", id)
			}
			fields["file"], _ = json.Marshal(name)
			raw, err = json.Marshal(fields)
			if err != nil {
				return err
			}
			if err = putTransferObject(ctx, tx, id, name, raw); err != nil {
				return err
			}
		}
		// Preserve unknown header and object fields in their original JSON.
		delete(header, "objects")
		body, err = json.Marshal(header)
		if err != nil {
			return err
		}
		if _, err = putDocument(ctx, tx, "transfer", id, body); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=11"); err != nil {
		return err
	}
	return tx.Commit()
}
