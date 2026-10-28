package control

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func consoleFixture(t *testing.T) (*VBoxBackend, *Task, *bootConsole) {
	t.Helper()
	b := &VBoxBackend{Config: testConfig(t)}
	task := newTask("lab", 60)
	task.lab().Router = &Node{UUID: wire.UUID()}
	c, err := newBootConsole(b.consoleDir(task))
	if err != nil {
		t.Fatal(err)
	}
	b.bootConsoles.Store(task.lab().Router.UUID, c)
	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Error(err)
		}
	})
	return b, task, c
}

func consoleWrite(t *testing.T, c *bootConsole, data []byte) {
	t.Helper()
	conn, err := net.DialTimeout("unix", c.socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(conn, bytes.NewReader(data)); err != nil {
		t.Fatal("console did not drain", err)
	}
}

func readConsole(t *testing.T, b *VBoxBackend, task *Task) (bootConsoleStatus, []byte) {
	t.Helper()
	local := filepath.Join(t.TempDir(), "artifact")
	raw, err := b.consoleLog(context.Background(), task, local)
	if err != nil {
		t.Fatal(err)
	}
	var status bootConsoleStatus
	if err = json.Unmarshal(raw, &status); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	return status, data
}

func TestBootConsoleRetainsBoundedPrefixAndDrainsAcrossReconnect(t *testing.T) {
	b, task, c := consoleFixture(t)
	prefix := bytes.Repeat([]byte("kernel boot\r\n"), bootConsoleLimit/13+1)
	consoleWrite(t, c, prefix)
	consoleWrite(t, c, bytes.Repeat([]byte("later"), bootConsoleLimit))
	want := int64(len(prefix) + 5*bootConsoleLimit)
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return *c.status.ObservedBytes == want && c.status.State == "disconnected"
	})
	status, data := readConsole(t, b, task)
	if status.LimitBytes != bootConsoleLimit || status.RetainedBytes != bootConsoleLimit || *status.ObservedBytes != want || !*status.Truncated || !bytes.Equal(data, prefix[:bootConsoleLimit]) {
		t.Fatal("console size, prefix or truncation differs", status)
	}
	info, err := os.Stat(filepath.Join(b.consoleDir(task), "console.log"))
	if err != nil || info.Size() != bootConsoleLimit || info.Mode().Perm() != 0600 {
		t.Fatal("console disk bound or privacy differs", info, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, retained := readConsole(t, b, task)
	if recovered.State != "interrupted" || recovered.ObservedBytes != nil || recovered.Truncated != nil || !bytes.Equal(retained, data) {
		t.Fatal("recovery invented observations or lost bytes", recovered)
	}
}

func TestBootConsoleStopsWithNoClientOrIdleClientAndNeverSendsInput(t *testing.T) {
	for _, connected := range []bool{false, true} {
		t.Run(map[bool]string{false: "listening", true: "connected"}[connected], func(t *testing.T) {
			b, task, c := consoleFixture(t)
			var conn net.Conn
			if connected {
				var err error
				conn, err = net.DialTimeout("unix", c.socket, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				waitFor(t, func() bool {
					c.mu.Lock()
					defer c.mu.Unlock()
					return c.status.State == "connected"
				})
			}
			status, data := readConsole(t, b, task)
			if len(data) != 0 || *status.ObservedBytes != 0 || *status.Truncated {
				t.Fatal("empty console observations differ", status)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := b.stopConsole(ctx, task.lab().Router.UUID); err != nil {
				t.Fatal(err)
			}
			if connected {
				if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if n, err := conn.Read(make([]byte, 1)); n != 0 || err != io.EOF {
					t.Fatal("collector wrote input or retained connection", n, err)
				}
			}
			if _, err := os.Stat(filepath.Join(b.consoleDir(task), "console.sock")); !os.IsNotExist(err) {
				t.Fatal("collector socket was not removed", err)
			}
		})
	}
}

func TestBootConsoleWriteFailureStillDrainsAndReportsPartialEvidence(t *testing.T) {
	b, task, c := consoleFixture(t)
	c.mu.Lock()
	c.file.Close()
	c.mu.Unlock()
	data := bytes.Repeat([]byte("x"), 2*bootConsoleLimit)
	consoleWrite(t, c, data)
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return *c.status.ObservedBytes == int64(len(data))
	})
	status, retained := readConsole(t, b, task)
	if len(retained) != 0 || status.Error == nil || status.Error.Code != "console_write_failed" || !*status.Truncated {
		t.Fatal("write failure was hidden", status)
	}
}

func TestBootConsoleCleanupDeadlineCanBeRetriedWithoutLosingBytes(t *testing.T) {
	b, task, c := consoleFixture(t)
	consoleWrite(t, c, []byte("boot evidence"))
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.status.RetainedBytes == int64(len("boot evidence"))
	})
	c.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	err := b.stopConsole(ctx, task.lab().Router.UUID)
	cancel()
	c.mu.Unlock()
	if err == nil {
		t.Fatal("cleanup ignored its lock deadline")
	}
	if _, ok := b.bootConsoles.Load(task.lab().Router.UUID); !ok {
		t.Fatal("unconfirmed cleanup discarded the collector")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	_, data := readConsole(t, b, task)
	if string(data) != "boot evidence" {
		t.Fatal("cleanup discarded retained bytes")
	}
}

func TestBootConsoleRecoveryRejectsMissingOversizedAndSymlinkLogs(t *testing.T) {
	for _, kind := range []string{"missing", "oversized", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			b := &VBoxBackend{Config: testConfig(t)}
			task := newTask("lab", 60)
			task.lab().Router = &Node{UUID: wire.UUID()}
			dir := b.consoleDir(task)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "oversized" {
				err = os.WriteFile(filepath.Join(dir, "console.log"), make([]byte, bootConsoleLimit+1), 0600)
			} else if kind == "symlink" {
				err = os.Symlink("/etc/hostname", filepath.Join(dir, "console.log"))
			}
			if err != nil {
				t.Fatal(err)
			}
			local := filepath.Join(t.TempDir(), "artifact")
			if _, err := b.consoleLog(context.Background(), task, local); err == nil {
				t.Fatal("unverified console evidence was accepted", kind)
			}
			if _, err := os.Stat(local); !os.IsNotExist(err) {
				t.Fatal("failed collection published bytes", kind, err)
			}
		})
	}
}
