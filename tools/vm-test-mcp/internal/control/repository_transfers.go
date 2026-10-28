package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/state"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"math"
)

func (d *repository) CreateTransfer(ctx context.Context, doc *Transfer, objects map[string]state.TransferObject) error {
	return d.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
		if err := doc.store(ctx, tx); err != nil {
			return err
		}
		for name, object := range objects {
			body, err := json.Marshal(object)
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
func (d *repository) StoreTransferObject(ctx context.Context, id, name string, object state.TransferObject) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return d.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE transfer_objects SET body=? WHERE transfer_id=? AND name=? AND body IS NULL AND expected_size=? AND expected_sha256=?`, body, id, name, object.Size, object.SHA256)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil || changed == 1 {
			return err
		}
		var retained []byte
		if err = tx.QueryRowContext(ctx, "SELECT body FROM transfer_objects WHERE transfer_id=? AND name=?", id, name).Scan(&retained); err != nil {
			return err
		}
		if string(retained) != string(body) {
			return wire.Fail("object_conflict", "Upload object does not match its immutable publication")
		}
		return nil
	})
}

func putTransferObject(ctx context.Context, tx *sql.Tx, id, name string, body []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO transfer_objects(transfer_id,name,expected_size,expected_sha256,body) VALUES(?,?,json_extract(?,'$.size'),json_extract(?,'$.sha256'),?)`, id, name, body, body, body)
	return err
}

func (d *repository) TransferObjects(ctx context.Context, id string) (map[string]state.TransferObject, int64, error) {
	rows, err := d.QueryContext(ctx, "SELECT name,body FROM transfer_objects WHERE transfer_id=? AND body IS NOT NULL ORDER BY name", id)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	objects := map[string]state.TransferObject{}
	var total int64
	for rows.Next() {
		var name string
		var body []byte
		var value state.TransferObject
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

func (d *repository) DeclareUpload(ctx context.Context, doc *Transfer, items []wire.UploadObject) error {
	return d.transaction(ctx, []record{doc}, func(ctx context.Context, tx *sql.Tx) error {
		for _, o := range items {
			if _, err := tx.ExecContext(ctx, "INSERT INTO transfer_objects(transfer_id,name,expected_size,expected_sha256) VALUES(?,?,?,?)", doc.ID, o.Name, o.Size, o.SHA256); err != nil {
				return err
			}
		}
		return nil
	})
}
func (d *repository) ExpectedObject(ctx context.Context, id, name string) (int64, string, error) {
	var size int64
	var hash string
	err := d.QueryRowContext(ctx, "SELECT expected_size,expected_sha256 FROM transfer_objects WHERE transfer_id=? AND name=?", id, name).Scan(&size, &hash)
	return size, hash, err
}
