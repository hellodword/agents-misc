package state

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Bodies are immutable and fsynced before a transaction publishes their index.
// A failed transaction leaves an unreferenced object for SweepBodies.
func (d *DB) PublishBody(body []byte) (string, int64, string, error) {
	h := sha256.Sum256(body)
	hash := hex.EncodeToString(h[:])
	name := filepath.Join("bodies", hash)
	dir := filepath.Join(d.dir, "bodies")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", 0, "", err
	}
	if existing, err := os.ReadFile(filepath.Join(d.dir, name)); err == nil {
		if sha256.Sum256(existing) != h {
			return "", 0, "", fmt.Errorf("body integrity failure")
		}
		return name, int64(len(body)), hash, nil
	}
	f, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return "", 0, "", err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(d.dir, name))
	}
	if err == nil {
		var directory *os.File
		directory, err = os.Open(dir)
		if err == nil {
			err = directory.Sync()
			directory.Close()
		}
	}
	return name, int64(len(body)), hash, err
}
func (d *DB) ReadBody(name string, size int64, hash string) ([]byte, error) {
	if name != filepath.Join("bodies", hash) || len(hash) != 64 {
		return nil, fmt.Errorf("invalid body reference")
	}
	b, err := os.ReadFile(filepath.Join(d.dir, name))
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(b)
	if int64(len(b)) != size || hex.EncodeToString(h[:]) != hash {
		return nil, fmt.Errorf("body integrity failure")
	}
	return b, nil
}
