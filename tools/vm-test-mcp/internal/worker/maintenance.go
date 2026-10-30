package worker

import (
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// A crash between directory creation and job admission can leave an unindexed
// directory. The worker mutex excludes new admission while ownership is checked.
func (w *Worker) sweepUnreferenced(now time.Time) error {
	entries, err := os.ReadDir(w.root.Path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !wire.IDPattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if now.Sub(info.ModTime()) < time.Hour {
			continue
		}
		var count int
		if err = w.db.QueryRow("SELECT count(*) FROM worker_jobs WHERE id=?", entry.Name()).Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			if err = w.root.RemoveAll(entry.Name()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Worker) maintain() {
	defer w.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.maintenanceStop:
			return
		case now := <-ticker.C:
			w.mu.Lock()
			for _, j := range w.jobs {
				if j.Cleanup != "complete" {
					w.observeLocked(j)
				} else {
					w.collectLocked(j)
				}
			}
			_ = w.expireCompleted(now.UTC())
			w.mu.Unlock()
		}
	}
}

// Physical copies count once per inode. Store outputs referenced by retained
// evidence are counted until handoff; host Nix store GC remains independent.
func (w *Worker) evidenceUsage() (int64, error) {
	seen := map[[2]uint64]bool{}
	var total int64
	add := func(name string) error {
		info, err := os.Stat(name)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		st := info.Sys().(*syscall.Stat_t)
		key := [2]uint64{st.Dev, st.Ino}
		if !seen[key] {
			total += info.Size()
			seen[key] = true
		}
		return nil
	}
	err := filepath.WalkDir(w.root.Path, func(name string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Type().IsRegular() && filepath.Dir(name) != w.root.Path {
			return add(name)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	rows, err := w.db.Query("SELECT root,relative FROM worker_files")
	if err != nil {
		return 0, err
	}
	var files []string
	for rows.Next() {
		var root, relative string
		if err = rows.Scan(&root, &relative); err != nil {
			rows.Close()
			return 0, err
		}
		files = append(files, filepath.Join(root, relative))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, file := range files {
		if err = add(file); err != nil {
			return 0, err
		}
	}
	// Output roots count before their asynchronous file manifest is ready.
	rows, err = w.db.Query("SELECT DISTINCT value FROM worker_jobs,json_each(outputs) WHERE copies_released=0")
	if err != nil {
		return 0, err
	}
	var roots []string
	for rows.Next() {
		var root string
		if err = rows.Scan(&root); err != nil {
			break
		}
		roots = append(roots, root)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, root := range roots {
		if err = filepath.WalkDir(root, func(name string, d os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if d.Type().IsRegular() {
				return add(name)
			}
			return nil
		}); err != nil {
			return 0, err
		}
	}
	return total, nil
}
