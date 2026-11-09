package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type slotPool struct {
	slots chan struct{}
	users int
}

type keyedSlots struct {
	mu    sync.Mutex
	pools map[string]*slotPool
}

// Waiters hold references too; removing an idle key cannot create two pools for
// an in-flight digest or transfer. Memory follows concurrent work, not history.
func (s *keyedSlots) acquire(ctx context.Context, key string, capacity int) (func(), error) {
	s.mu.Lock()
	if s.pools == nil {
		s.pools = map[string]*slotPool{}
	}
	p := s.pools[key]
	if p == nil {
		p = &slotPool{slots: make(chan struct{}, capacity)}
		s.pools[key] = p
	}
	p.users++
	s.mu.Unlock()
	drop := func() {
		s.mu.Lock()
		p.users--
		if p.users == 0 {
			delete(s.pools, key)
		}
		s.mu.Unlock()
	}
	select {
	case p.slots <- struct{}{}:
		return func() { <-p.slots; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

func inputObjectFile(hash string) string { return path.Join("input-objects", hash) }

// Admission and reclamation share s.mu with manifest and object publication.
// Declared live manifests protect both published bytes and unfinished writers.
func (s *Server) reserveInputSpace(ctx context.Context, items []wire.UploadObject) error {
	requested := make(map[string]int64, len(items))
	for _, item := range items {
		if size, ok := requested[item.SHA256]; ok && size != item.Size {
			return wire.Fail("integrity_mismatch", "One input digest declares different sizes")
		}
		requested[item.SHA256] = item.Size
	}
	rows, err := s.db.QueryContext(ctx, "SELECT sha256,size FROM input_objects")
	if err != nil {
		return err
	}
	var used, extra int64
	for _, size := range requested {
		extra += size
	}
	for rows.Next() {
		var hash string
		var size int64
		if err = rows.Scan(&hash, &size); err != nil {
			break
		}
		used += size
		if want, ok := requested[hash]; ok {
			if want != size {
				err = wire.Fail("integrity_mismatch", "Input digest has a different recorded size")
				break
			}
			extra -= size
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if used+extra <= s.cfg.Storage.InputBytes {
		return nil
	}
	rows, err = s.db.QueryContext(ctx, `SELECT sha256,size FROM input_objects o WHERE NOT EXISTS(
	 SELECT 1 FROM transfer_objects x JOIN transfers t ON t.id=x.transfer_id
	 WHERE x.expected_sha256=o.sha256 AND t.state NOT IN ('retained','deleting')) ORDER BY updated,sha256`)
	if err != nil {
		return err
	}
	type candidate struct {
		hash string
		size int64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.hash, &c.size); err != nil {
			break
		}
		if _, protected := requested[c.hash]; !protected {
			candidates = append(candidates, c)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range candidates {
		refs, err := s.db.QueryContext(ctx, "SELECT transfer_id,name FROM transfer_objects WHERE expected_sha256=? AND body IS NOT NULL", c.hash)
		if err != nil {
			return err
		}
		type reference struct{ id, name string }
		var references []reference
		for refs.Next() {
			var ref reference
			if err = refs.Scan(&ref.id, &ref.name); err != nil {
				break
			}
			references = append(references, ref)
		}
		if err == nil {
			err = refs.Err()
		}
		refs.Close()
		if err != nil {
			return err
		}
		if err = s.db.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE transfer_objects SET body=NULL WHERE expected_sha256=? AND body IS NOT NULL", c.hash); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "DELETE FROM input_objects WHERE sha256=?", c.hash)
			return err
		}); err != nil {
			return err
		}
		for _, ref := range references {
			if t := s.transfers[ref.id]; t != nil {
				t.Bytes -= t.Objects[ref.name].Size
				delete(t.Objects, ref.name)
			}
		}
		if err = s.root.Remove(inputObjectFile(c.hash)); err != nil && !os.IsNotExist(err) {
			return err
		}
		used -= c.size
		if used+extra <= s.cfg.Storage.InputBytes {
			return nil
		}
	}
	return wire.Fail("quota_exceeded", "Unique input objects and live reservations exceed the input budget")
}

func (s *Server) reuseInputObjects(ctx context.Context, t *Transfer) error {
	if err := s.db.ReuseInputObjects(ctx, t.ID); err != nil {
		return err
	}
	objects, size, err := s.db.TransferObjects(ctx, t.ID)
	if err == nil {
		t.Objects, t.Bytes = objects, size
	}
	return err
}

// An interrupted publication can leave a file without an index entry. An index
// entry whose bytes were lost becomes a reservation and can be uploaded again.
func (s *Server) recoverInputObjects(ctx context.Context) error {
	if err := s.root.MkdirAll("input-objects", 0700); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT sha256,body FROM input_objects WHERE body IS NOT NULL")
	if err != nil {
		return err
	}
	known := map[string]bool{}
	var missing []string
	for rows.Next() {
		var hash string
		var body []byte
		var object Object
		if err = rows.Scan(&hash, &body); err != nil {
			break
		}
		if err = json.Unmarshal(body, &object); err != nil {
			break
		}
		if !sha256Pattern.MatchString(hash) || object.File != inputObjectFile(hash) || object.SHA256 != hash {
			err = wire.Fail("invalid_transfer", "Invalid retained input object identity")
			break
		}
		st, statErr := s.root.Lstat(object.File)
		if os.IsNotExist(statErr) || statErr == nil && st.Mode().IsRegular() && st.Size() != object.Size {
			missing = append(missing, hash)
			continue
		}
		if statErr != nil {
			err = statErr
			break
		}
		if !st.Mode().IsRegular() {
			err = wire.Fail("invalid_path", "Input object must be a regular file")
			break
		}
		known[hash] = true
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, hash := range missing {
		if err = s.db.transaction(ctx, nil, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE input_objects SET body=NULL WHERE sha256=?", hash); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, "UPDATE transfer_objects SET body=NULL WHERE expected_sha256=?", hash)
			return err
		}); err != nil {
			return err
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE transfers SET state='open',body=json_set(body,'$.state','open') WHERE state='ready' AND remaining>0`); err != nil {
			return err
		}
	}
	dir, err := s.root.Open("input-objects")
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && !known[name] && (sha256Pattern.MatchString(name) || strings.HasPrefix(name, ".object-") && transferScratch.MatchString(name)) {
			if err = s.root.Remove(path.Join("input-objects", name)); err != nil {
				return err
			}
		}
	}
	return nil
}
