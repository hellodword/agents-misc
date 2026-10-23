// Package safefs confines service data to directory handles. Names supplied by
// guests are never interpreted as host paths.
package safefs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Root struct {
	*os.Root
	Path string
	lock *os.File
}

func Private(dir string, lock bool) (*Root, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, fmt.Errorf("state_dir must be an absolute clean path")
	}
	// Check every existing component before creation, then open with an anchored
	// handle. The administrator owns ancestor directories; clients cannot rename them.
	cur := string(filepath.Separator)
	for _, p := range strings.Split(strings.TrimPrefix(dir, cur), string(filepath.Separator)) {
		cur = filepath.Join(cur, p)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			if err = os.Mkdir(cur, 0700); err != nil {
				return nil, err
			}
			fi, err = os.Lstat(cur)
		}
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("state path contains a non-directory")
		}
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() || fi.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("state directory must be owned by this user with mode 0700")
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	root := &Root{Root: r, Path: dir}
	if lock {
		f, e := r.OpenFile("service.lock", os.O_CREATE|os.O_RDWR, 0600)
		if e == nil {
			e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		}
		if e != nil {
			r.Close()
			if f != nil {
				f.Close()
			}
			return nil, fmt.Errorf("state already in use: %w", e)
		}
		root.lock = f
	}
	return root, nil
}
func (r *Root) Close() error {
	if r.lock != nil {
		r.lock.Close()
	}
	return r.Root.Close()
}
func Name(name string) error {
	if name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		return wire.Fail("invalid_path", "Invalid relative object path")
	}
	for _, p := range strings.Split(name, "/") {
		if p == ".." || p == "." || p == "" {
			return wire.Fail("invalid_path", "Invalid relative object path")
		}
	}
	return nil
}
func (r *Root) Regular(name string) (*os.File, error) {
	if err := Name(name); err != nil {
		return nil, err
	}
	cur := ""
	for _, p := range strings.Split(name, "/") {
		cur = path.Join(cur, p)
		fi, err := r.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, wire.Fail("invalid_path", "Symbolic links are not transferable")
		}
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, wire.Fail("invalid_path", "Expected ordinary file")
	}
	return f, nil
}
func (r *Root) Write(name string, src io.Reader, limit int64) (int64, string, error) {
	if err := Name(name); err != nil {
		return 0, "", err
	}
	if err := r.MkdirAll(path.Dir(name), 0700); err != nil {
		return 0, "", err
	}
	tmp := name + "." + wire.ID() + ".partial"
	f, err := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, "", err
	}
	defer r.Remove(tmp)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(src, limit+1))
	if n > limit {
		err = wire.Fail("quota_exceeded", "Object exceeds transfer limit")
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, "", err
	}
	if err = r.Rename(tmp, name); err != nil {
		return 0, "", err
	}
	d, err := r.Open(path.Dir(name))
	if err != nil {
		return 0, "", err
	}
	err = d.Sync()
	d.Close()
	return n, hex.EncodeToString(h.Sum(nil)), err
}
func (r *Root) Read(name string, max int64) ([]byte, error) {
	f, err := r.Regular(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if int64(len(b)) > max {
		return nil, wire.Fail("too_large", "Object exceeds read limit")
	}
	return b, err
}
