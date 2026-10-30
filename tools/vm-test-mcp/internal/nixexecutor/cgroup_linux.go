package nixexecutor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type group struct {
	root  *os.Root
	fd    *os.File
	inode uint64
}

func delegatedRoot() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		p, ok := strings.CutPrefix(line, "0::")
		if ok && filepath.Base(p) == "supervisor" && filepath.Base(filepath.Dir(p)) == "vm-test-nix-executor.service" {
			return filepath.Join("/sys/fs/cgroup", filepath.Dir(p)), nil
		}
	}
	return "", fmt.Errorf("start with just nix-executor: a delegated cgroup v2 service with DelegateSubgroup=supervisor is required")
}

func openGroup(name string) (*group, error) {
	r, err := os.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	f, err := r.Open(".")
	if err != nil {
		r.Close()
		return nil, err
	}
	var fs unix.Statfs_t
	err = unix.Fstatfs(int(f.Fd()), &fs)
	if err != nil || fs.Type != unix.CGROUP2_SUPER_MAGIC {
		f.Close()
		r.Close()
		return nil, fmt.Errorf("expected a cgroup v2 directory")
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		r.Close()
		return nil, err
	}
	return &group{root: r, fd: f, inode: st.Sys().(*syscall.Stat_t).Ino}, nil
}

func (g *group) close() { g.fd.Close(); g.root.Close() }
func (g *group) populated() (bool, error) {
	data, err := g.root.ReadFile("cgroup.events")
	if err != nil {
		return false, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if line == "populated 0" {
			return false, nil
		}
		if line == "populated 1" {
			return true, nil
		}
	}
	return false, fmt.Errorf("cgroup.events has no populated observation")
}
func (g *group) kill() error {
	f, err := g.root.OpenFile("cgroup.kill", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString("1")
	closed := f.Close()
	if err == nil {
		err = closed
	}
	return err
}

// Empty delegated groups contain kernel attribute files which cannot be
// unlinked. Remove only directories, after admission is sealed and populated=0.
func removeGroups(name string) error {
	entries, err := os.ReadDir(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if err := removeGroups(filepath.Join(name, entry.Name())); err != nil {
				return err
			}
		}
	}
	return os.Remove(name)
}
