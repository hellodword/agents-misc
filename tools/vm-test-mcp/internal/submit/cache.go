package submit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

const defaultCacheBytes int64 = 16 << 30

var cacheInfoName = regexp.MustCompile(`^[0-9abcdfghijklmnpqrsvwxyz]{32}\.narinfo$`)
var cacheNarName = regexp.MustCompile(`^nar/[A-Za-z0-9][A-Za-z0-9._-]{0,190}\.nar(\.(xz|bz2|zst))?$`)

type cachedObject struct {
	Size              int64  `json:"size"`
	SHA256            string `json:"sha256"`
	Device, Inode     uint64
	Modified, Changed int64
	Used              int64  `json:"used"`
	Nar               string `json:"nar,omitempty"`
}

type exportCache struct {
	root    *safefs.Root
	lock    *os.File
	objects map[string]cachedObject
	limit   int64
}

func openExportCache(ctx context.Context, o Options) (*exportCache, error) {
	dir := o.CacheDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(base, "vm-test-submit")
	}
	root, err := safefs.Private(dir, false)
	if err != nil {
		return nil, err
	}
	lock, err := root.OpenFile("export.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		root.Close()
		return nil, err
	}
	c := &exportCache{root: root, lock: lock, limit: o.CacheBytes, objects: map[string]cachedObject{}}
	if c.limit == 0 {
		c.limit = defaultCacheBytes
	}
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			c.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			c.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	data, err := root.Read("index.json", 32<<20)
	if err == nil {
		err = json.Unmarshal(data, &c.objects)
	}
	if err != nil && !os.IsNotExist(err) {
		c.Close()
		return nil, err
	}
	if c.objects == nil {
		c.objects = map[string]cachedObject{}
	}
	if err = root.MkdirAll("files", 0700); err != nil {
		c.Close()
		return nil, err
	}
	// Recover files exported before an interrupted inventory commit. Only the
	// Nix binary-cache namespace belongs to this cache's reclamation policy.
	err = filepath.WalkDir(filepath.Join(root.Path, "files"), func(file string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name, err := filepath.Rel(filepath.Join(root.Path, "files"), file)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		if name != "nix-cache-info" && !cacheInfoName.MatchString(name) && !cacheNarName.MatchString(name) {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("local export cache contains a nonregular object")
		}
		if _, ok := c.objects[name]; !ok {
			st, err := d.Info()
			if err != nil {
				return err
			}
			c.objects[name] = cachedObject{Size: st.Size()}
		}
		return nil
	})
	if err != nil {
		c.Close()
		return nil, err
	}
	for name, obj := range c.objects {
		if name != "nix-cache-info" && !cacheInfoName.MatchString(name) && !cacheNarName.MatchString(name) {
			c.Close()
			return nil, fmt.Errorf("invalid local cache inventory")
		}
		if !cacheInfoName.MatchString(name) {
			continue
		}
		if obj.Nar == "" {
			obj.Nar, err = c.narReference(name)
			if os.IsNotExist(err) {
				delete(c.objects, name)
				continue
			}
			if err != nil {
				c.Close()
				return nil, err
			}
			c.objects[name] = obj
		}
		if !cacheNarName.MatchString(obj.Nar) {
			c.Close()
			return nil, fmt.Errorf("invalid cached NAR reference")
		}
		if _, err = root.Lstat(path.Join("files", obj.Nar)); os.IsNotExist(err) {
			if err = root.Remove(path.Join("files", name)); err != nil {
				c.Close()
				return nil, err
			}
			delete(c.objects, name)
		} else if err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, nil
}

func (c *exportCache) Close() { c.lock.Close(); c.root.Close() }

func (c *exportCache) narReference(name string) (string, error) {
	data, err := c.root.Read(path.Join("files", name), 65536)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "URL: "); ok && cacheNarName.MatchString(value) {
			return value, nil
		}
	}
	return "", fmt.Errorf("cache narinfo %s has no valid NAR reference", name)
}

func (c *exportCache) object(name string) (wire.UploadObject, error) {
	f, err := c.root.Regular(path.Join("files", name))
	if err != nil {
		return wire.UploadObject{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return wire.UploadObject{}, err
	}
	stat := st.Sys().(*syscall.Stat_t)
	obj := c.objects[name]
	if obj.SHA256 == "" || obj.Size != st.Size() || obj.Device != stat.Dev || obj.Inode != stat.Ino || obj.Modified != stat.Mtim.Nano() || obj.Changed != stat.Ctim.Nano() {
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			return wire.UploadObject{}, err
		}
		obj = cachedObject{Size: st.Size(), SHA256: hex.EncodeToString(h.Sum(nil)), Device: stat.Dev, Inode: stat.Ino, Modified: stat.Mtim.Nano(), Changed: stat.Ctim.Nano()}
	}
	obj.Used = time.Now().UnixNano()
	if cacheInfoName.MatchString(name) && obj.Nar == "" {
		obj.Nar, err = c.narReference(name)
		if err != nil {
			return wire.UploadObject{}, err
		}
	}
	c.objects[name] = obj
	return wire.UploadObject{Name: name, Size: obj.Size, SHA256: obj.SHA256}, nil
}

func (c *exportCache) retain(active map[string]bool) error {
	var total, protected int64
	names := make([]string, 0, len(c.objects))
	for name, obj := range c.objects {
		if _, err := c.root.Lstat(path.Join("files", name)); os.IsNotExist(err) {
			delete(c.objects, name)
			continue
		} else if err != nil {
			return err
		}
		total += obj.Size
		if active[name] {
			protected += obj.Size
		} else {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		av, bv := c.objects[a].Used, c.objects[b].Used
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
		return strings.Compare(a, b)
	})
	for _, name := range names {
		if total <= max(c.limit, protected) {
			break
		}
		if err := c.root.Remove(path.Join("files", name)); err != nil {
			return err
		}
		total -= c.objects[name].Size
		delete(c.objects, name)
	}
	// A cache lookup uses narinfo as its validity marker. Remove markers whose
	// NAR was reclaimed before the next Nix copy can reuse them.
	for name, obj := range c.objects {
		if obj.Nar != "" {
			if _, ok := c.objects[obj.Nar]; !ok {
				if err := c.root.Remove(path.Join("files", name)); err != nil {
					return err
				}
				delete(c.objects, name)
			}
		}
	}
	_, _, err := c.root.Write("index.json", bytes.NewReader(wire.JSON(c.objects)), 32<<20)
	return err
}

func (c *exportCache) export(ctx context.Context, o Options, meta Metadata, log io.Writer) (wire.UploadManifest, error) {
	m := wire.UploadManifest{RunID: meta.RunID, UploadID: o.CacheID, Derivation: meta.Derivation, System: meta.System}
	query := exec.CommandContext(ctx, o.Nix, "path-info", "--recursive", meta.Derivation)
	query.Stderr = log
	raw, err := query.Output()
	if err != nil {
		return m, fmt.Errorf("query saved derivation closure: %w", err)
	}
	paths := strings.Fields(string(raw))
	if len(paths) == 0 || len(paths) > 100000 {
		return m, fmt.Errorf("invalid derivation closure size")
	}
	destination := url.URL{Scheme: "file", Path: filepath.Join(c.root.Path, "files")}
	q := destination.Query()
	q.Set("compression", "xz")
	if o.SigningKey != "" {
		q.Set("secret-key", o.SigningKey)
	}
	destination.RawQuery = q.Encode()
	cmd := exec.CommandContext(ctx, o.Nix, "copy", "--derivation", "--to", destination.String(), meta.Derivation)
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Run(); err != nil {
		return m, fmt.Errorf("export saved derivation closure: %w", err)
	}
	if _, _, err = c.root.Write("files/nix-cache-info", strings.NewReader("StoreDir: /nix/store\nWantMassQuery: 1\nPriority: 30\n"), 1024); err != nil {
		return m, err
	}
	selected := map[string]bool{"nix-cache-info": true}
	for _, p := range paths {
		if !wire.StorePattern.MatchString(p) {
			return m, fmt.Errorf("invalid closure path")
		}
		name := strings.SplitN(path.Base(p), "-", 2)[0] + ".narinfo"
		nar, err := c.narReference(name)
		if err != nil {
			return m, err
		}
		selected[name], selected[nar] = true, true
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		obj, err := c.object(name)
		if err != nil {
			return m, err
		}
		m.Objects = append(m.Objects, obj)
	}
	return m, c.retain(selected)
}
