package control

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

const bootConsoleLimit = 1 << 20

type bootConsoleStatus struct {
	State         string      `json:"state"`
	LimitBytes    int64       `json:"limit_bytes"`
	RetainedBytes int64       `json:"retained_bytes"`
	ObservedBytes *int64      `json:"observed_bytes"`
	Truncated     *bool       `json:"truncated"`
	Error         *wire.Error `json:"error,omitempty"`
}

// The private socket only receives bytes. It exposes neither a physical port
// nor a guest input channel. Retaining a prefix bounds disk use; draining after
// that limit prevents a verbose guest from blocking its console writes.
type bootConsole struct {
	mu       sync.Mutex
	listener *net.UnixListener
	conn     *net.UnixConn
	file     *os.File
	socket   string
	done     chan struct{}
	stopping bool
	status   bootConsoleStatus
}

func newBootConsole(dir string) (*bootConsole, error) {
	root, err := safefs.Private(dir, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	// An open directory gives Linux a short, task-owned socket path even when
	// state_dir exceeds sockaddr_un's path limit. Keep the descriptor alive
	// until the listener closes; no socket or registry is left outside runtime.
	anchor, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	socket := fmt.Sprintf("/proc/%d/fd/%d/console.sock", os.Getpid(), anchor.Fd())
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		anchor.Close()
		return nil, err
	}
	file, err := root.OpenFile("console.log", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		listener.Close()
		anchor.Close()
		return nil, err
	}
	var observed int64
	var truncated bool
	c := &bootConsole{listener: listener, file: file, socket: socket, done: make(chan struct{}), status: bootConsoleStatus{State: "listening", LimitBytes: bootConsoleLimit, ObservedBytes: &observed, Truncated: &truncated}}
	go func() {
		defer close(c.done)
		defer anchor.Close()
		defer listener.Close()
		defer file.Close()
		c.receive()
	}()
	return c, nil
}

func (c *bootConsole) receive() {
	var observed int64
	var truncated bool
	writeFailed := false
	buffer := make([]byte, 4096)
	for {
		conn, err := c.listener.AcceptUnix()
		c.mu.Lock()
		if c.stopping || err != nil {
			if conn != nil {
				conn.Close()
			}
			if !c.stopping {
				c.status.Error = &wire.Error{Code: "console_read_failed", Message: "Router console connection could not be accepted"}
			}
			c.status.State = "stopped"
			c.mu.Unlock()
			return
		}
		c.conn, c.status.State = conn, "connected"
		c.status.ObservedBytes, c.status.Truncated = &observed, &truncated
		c.mu.Unlock()
		for {
			n, readErr := conn.Read(buffer)
			c.mu.Lock()
			observed += int64(n)
			keep := min(int64(n), bootConsoleLimit-c.status.RetainedBytes)
			if keep > 0 && !writeFailed {
				written, writeErr := c.file.Write(buffer[:keep])
				c.status.RetainedBytes += int64(written)
				if writeErr != nil || int64(written) != keep {
					writeFailed = true
					c.status.Error = &wire.Error{Code: "console_write_failed", Message: "Router console bytes could not be retained"}
				}
			}
			truncated = observed > c.status.RetainedBytes
			if readErr != nil {
				if !c.stopping && !errors.Is(readErr, io.EOF) {
					c.status.Error = &wire.Error{Code: "console_read_failed", Message: "Router console stream ended with a read error"}
				}
				c.conn, c.status.State = nil, "disconnected"
			}
			c.mu.Unlock()
			if readErr != nil {
				conn.Close()
				break
			}
		}
	}
}

func (c *bootConsole) stop(ctx context.Context) error {
	if err := lockContext(ctx, &c.mu); err != nil {
		return err
	}
	c.stopping = true
	c.listener.Close()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return wire.Fail("cleanup_timeout", "Router console collector did not stop within the cleanup budget")
	}
}

func (b *VBoxBackend) consoleDir(t *Task) string {
	return filepath.Join(b.runtime(t), "router-console")
}

func (b *VBoxBackend) consoleLog(ctx context.Context, t *Task, local string) ([]byte, error) {
	status := bootConsoleStatus{State: "interrupted", LimitBytes: bootConsoleLimit}
	if value, ok := b.bootConsoles.Load(t.Router.UUID); ok {
		c := value.(*bootConsole)
		if err := lockContext(ctx, &c.mu); err != nil {
			return nil, err
		}
		status = c.status
		if status.ObservedBytes != nil {
			observed, truncated := *status.ObservedBytes, *status.Truncated
			status.ObservedBytes, status.Truncated = &observed, &truncated
		}
		c.mu.Unlock()
	}
	// Recovery can retain the bounded file after a controller crash. It cannot
	// infer the former connection, total byte count or truncation from its size.
	root, err := safefs.Private(b.consoleDir(t), false)
	if err != nil {
		return nil, wire.Fail("console_unavailable", "Router console storage is unavailable")
	}
	defer root.Close()
	file, err := root.Regular("console.log")
	if err != nil {
		return nil, wire.Fail("console_unavailable", "No retained router console is available")
	}
	defer file.Close()
	limit := status.RetainedBytes
	if status.State == "interrupted" {
		limit = bootConsoleLimit + 1
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil || len(data) > bootConsoleLimit || status.State != "interrupted" && int64(len(data)) != limit {
		return nil, wire.Fail("console_read_failed", "Retained router console bytes could not be verified")
	}
	status.RetainedBytes = int64(len(data))
	if err = os.WriteFile(local, data, 0600); err != nil {
		return nil, err
	}
	return wire.JSON(status), nil
}

func (b *VBoxBackend) stopConsole(ctx context.Context, uuid string) error {
	if value, ok := b.bootConsoles.Load(uuid); ok {
		if err := value.(*bootConsole).stop(ctx); err != nil {
			return err
		}
		b.bootConsoles.Delete(uuid)
	}
	return nil
}

func (b *VBoxBackend) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var result error
	b.bootConsoles.Range(func(key, _ any) bool {
		result = errors.Join(result, b.stopConsole(ctx, key.(string)))
		return true
	})
	return result
}
