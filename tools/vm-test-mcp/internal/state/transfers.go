package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
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
		if err = d.putEntity(ctx, tx, doc.Kind, doc.ID, body); err != nil {
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
