package guest

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func readJSON(file string, v any) error {
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, wire.MaxJSON+1))
	if e != nil {
		return e
	}
	return wire.Decode(b, v)
}
func atomic(file string, v any) error {
	name := file + "." + wire.ID() + ".partial"
	f, e := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer os.Remove(name)
	_, e = f.Write(wire.JSON(v))
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	// Windows Guest Control may briefly open the old status without delete sharing.
	for i := 0; i < 20; i++ {
		if e = os.Rename(name, file); e == nil {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return e
}

type Child interface {
	Wait() (int, error)
	Kill() error
	Close() error
}
type launchFunc func(context.Context, wire.CommandSpec, io.Reader, io.Writer, io.Writer) (Child, error)
type limitWriter struct {
	f *os.File
	n int64
}

func (w *limitWriter) Write(b []byte) (int, error) {
	n := len(b)
	if w.n < 64<<20 {
		p := b[:min(int64(n), (64<<20)-w.n)]
		written, e := w.f.Write(p)
		w.n += int64(written)
		if e != nil {
			return written, e
		}
	}
	return n, nil
}

func Run(ctx context.Context, root, request string) error     { return submit(ctx, root, request) }
func Execute(ctx context.Context, root, request string) error { return run(ctx, root, request, launch) }
func run(ctx context.Context, root, request string, start launchFunc) error {
	var spec wire.CommandSpec
	if e := readJSON(request, &spec); e != nil {
		return e
	}
	if e := spec.Validate(); e != nil {
		return e
	}
	dir := filepath.Join(root, spec.ID)
	if e := os.Mkdir(dir, 0700); e != nil {
		return e
	}
	status := wire.CommandStatus{State: "starting"}
	statusPath := filepath.Join(dir, "status.json")
	if e := atomic(statusPath, status); e != nil {
		return e
	}
	out, e := os.OpenFile(filepath.Join(dir, "stdout"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer out.Close()
	errout, e := os.OpenFile(filepath.Join(dir, "stderr"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer errout.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if spec.TimeoutSeconds > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, time.Duration(spec.TimeoutSeconds)*time.Second)
		defer stop()
	}
	stdinR, stdinW := io.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()
	child, e := start(ctx, spec, stdinR, &limitWriter{f: out}, &limitWriter{f: errout})
	if e != nil {
		status.State = "failed"
		status.Error = "Could not start supervised process"
		_ = atomic(statusPath, status)
		return e
	}
	defer child.Close()
	status.State = "running"
	if e = atomic(statusPath, status); e != nil {
		child.Kill()
		return e
	}
	done := make(chan struct{})
	var code int
	var waitErr error
	go func() { code, waitErr = child.Wait(); close(done) }()
	var mu sync.Mutex
	inputDone := make(chan struct{})
	inputCtx, inputCancel := context.WithCancel(ctx)
	defer inputCancel()
	go func() {
		defer close(inputDone)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-inputCtx.Done():
				return
			case <-done:
				return
			case <-tick.C:
				mu.Lock()
				seq := status.NextInput
				closed := status.InputClosed
				mu.Unlock()
				if closed {
					continue
				}
				var packet wire.Input
				if e := readJSON(filepath.Join(dir, fmt.Sprintf("input-%08d.json", seq)), &packet); e != nil {
					if os.IsNotExist(e) {
						continue
					}
					cancel()
					return
				}
				if packet.Sequence != seq {
					cancel()
					return
				}
				if _, e := io.WriteString(stdinW, packet.Data); e != nil {
					return
				}
				if packet.EOF {
					stdinW.Close()
				}
				mu.Lock()
				status.NextInput++
				status.InputClosed = packet.EOF
				e := atomic(statusPath, status)
				mu.Unlock()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	cancelled := false
waiting:
	for {
		select {
		case <-done:
			break waiting
		case <-ctx.Done():
			cancelled = true
			child.Kill()
			<-done
			break waiting
		case <-tick.C:
			if _, e := os.Stat(filepath.Join(dir, "cancel")); e == nil {
				cancelled = true
				child.Kill()
				<-done
				break waiting
			}
		}
	}
	inputCancel()
	stdinR.Close()
	stdinW.Close()
	<-inputDone
	_ = out.Sync()
	_ = errout.Sync()
	mu.Lock()
	defer mu.Unlock()
	status.State = "exited"
	status.ExitCode = &code
	if cancelled {
		status.State = "cancelled"
	}
	if waitErr != nil && code < 0 {
		status.State = "failed"
		status.Error = "Process supervision failed"
	}
	return atomic(statusPath, status)
}
func Input(root, id, request string) error {
	if !wire.IDPattern.MatchString(id) {
		return wire.Fail("invalid_argument", "Invalid command ID")
	}
	var p wire.Input
	if e := readJSON(request, &p); e != nil {
		return e
	}
	if p.Sequence < 0 || p.Sequence > 1000000 || len(p.Data) > 65536 {
		return wire.Fail("invalid_argument", "Invalid input")
	}
	dir := filepath.Join(root, id)
	file := filepath.Join(dir, fmt.Sprintf("input-%08d.json", p.Sequence))
	var old wire.Input
	if err := readJSON(file, &old); err == nil {
		if old != p {
			return wire.Fail("input_conflict", "Sequence already has different content")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	var status wire.CommandStatus
	if e := readJSON(filepath.Join(dir, "status.json"), &status); e != nil {
		return e
	}
	if status.State != "running" && status.State != "starting" {
		return wire.Fail("invalid_state", "Command is not running")
	}
	data := wire.JSON(p)
	temporary := file + "." + wire.ID() + ".partial"
	defer os.Remove(temporary)
	f, e := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(data); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	// Publish only a complete, durable packet. Link is atomic and never replaces
	// an existing sequence, on both NTFS and Linux filesystems.
	e = os.Link(temporary, file)
	if os.IsExist(e) {
		var old wire.Input
		if e = readJSON(file, &old); e != nil {
			return e
		}
		if old != p {
			return wire.Fail("input_conflict", "Sequence already has different content")
		}
		return nil
	}
	if e != nil {
		return e
	}
	return e
}
func Cancel(root, id string) error {
	if !wire.IDPattern.MatchString(id) {
		return wire.Fail("invalid_argument", "Invalid command ID")
	}
	f, e := os.OpenFile(filepath.Join(root, id, "cancel"), os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	e = f.Sync()
	f.Close()
	return e
}
func Probe(roots ...string) map[string]any {
	host, _ := os.Hostname()
	v := map[string]any{"version": wire.Version, "hostname": host}
	if exe, err := os.Executable(); err == nil {
		if f, err := os.Open(exe); err == nil {
			h := sha256.New()
			if _, err = io.Copy(h, f); err == nil {
				v["sha256"] = hex.EncodeToString(h.Sum(nil))
			}
			f.Close()
		}
	}
	root := DefaultRoot
	if len(roots) > 0 && roots[0] != "" {
		root = roots[0]
	}
	privileged, prepared := readiness(root)
	v["privileged"], v["prepared"] = privileged, prepared
	return v
}

// Archive runs inside the disposable guest. Host code stores the resulting tar
// as opaque data and never extracts its paths or follows its symlinks.
func Archive(source, output string) error {
	base, e := filepath.Abs(source)
	if e != nil {
		return e
	}
	out, e := filepath.Abs(output)
	if e != nil {
		return e
	}
	if out == base || strings.HasPrefix(out, base+string(filepath.Separator)) {
		return errors.New("archive output is inside input")
	}
	f, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	defer tw.Close()
	count := 0
	e = filepath.WalkDir(base, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		count++
		if count > 100000 {
			return fmt.Errorf("archive entry limit")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, e = os.Readlink(p)
			if e != nil {
				return e
			}
		}
		h, e := tar.FileInfoHeader(info, link)
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(filepath.Dir(base), p)
		if e != nil {
			return e
		}
		h.Name = filepath.ToSlash(relative)
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if info.Mode().IsRegular() {
			r, e := os.Open(p)
			if e != nil {
				return e
			}
			_, e = io.Copy(tw, r)
			r.Close()
			return e
		}
		return nil
	})
	if e != nil {
		return e
	}
	if e = tw.Close(); e != nil {
		return e
	}
	return f.Sync()
}
