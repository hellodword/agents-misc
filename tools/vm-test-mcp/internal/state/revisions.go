package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

func (d *DB) Revision(ctx context.Context, key, owner string, value []byte) (int64, error) {
	hash := sha256.Sum256(value)
	digest := hex.EncodeToString(hash[:])
	var rev int64
	var old string
	if err := d.QueryRowContext(ctx, "SELECT revision,digest FROM revisions WHERE key=?", key).Scan(&rev, &old); err == nil && old == digest {
		return rev, nil
	}
	err := d.QueryRowContext(ctx, `INSERT INTO revisions(key,owner_id,revision,digest) VALUES(?,?,1,?) ON CONFLICT(key) DO UPDATE SET revision=revisions.revision+1,digest=excluded.digest RETURNING revision`, key, owner, digest).Scan(&rev)
	return rev, err
}
