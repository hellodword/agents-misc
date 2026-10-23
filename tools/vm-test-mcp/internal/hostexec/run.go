package hostexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

type Runner interface {
	Run(context.Context, string, []string, io.Reader) ([]byte, error)
}
type Exec struct{}
type bounded struct {
	bytes.Buffer
	max int
}

func (b *bounded) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < b.max {
		b.Buffer.Write(p[:min(n, b.max-b.Len())])
	}
	return n, nil
}
func (Exec) Run(ctx context.Context, exe string, args []string, input io.Reader) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, exe, args...)
	// Keep machine-readable diagnostics in English without forcing an ASCII
	// character set: VirtualBox decodes guest paths from the host locale.
	c.Env = append(os.Environ(), "LC_ALL=C.UTF-8", "LANGUAGE=C")
	c.Stdin = input
	c.WaitDelay = 2 * time.Second
	out := &bounded{max: 1 << 20}
	errout := &bounded{max: 8192}
	c.Stdout = out
	c.Stderr = errout
	if e := c.Run(); e != nil {
		return nil, fmt.Errorf("configured executable failed: %w", e)
	}
	return out.Bytes(), nil
}
