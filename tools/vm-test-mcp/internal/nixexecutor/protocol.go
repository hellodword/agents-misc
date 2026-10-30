// Package nixexecutor owns privileged Nix sessions and their resource receipts.
package nixexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Request struct {
	WorkerInstance string     `json:"worker_instance,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	SessionID      string     `json:"session_id,omitempty"`
	Deadline       *time.Time `json:"deadline,omitempty"`
	Reason         string     `json:"reason,omitempty"`
}

type Session struct {
	DaemonError      *wire.Error       `json:"daemon_error,omitempty"`
	ID               string            `json:"id"`
	ExecutorInstance string            `json:"executor_instance"`
	WorkerInstance   string            `json:"worker_instance"`
	TaskID           string            `json:"task_id"`
	Store            string            `json:"store"`
	BootID           string            `json:"boot_id"`
	Invocation       string            `json:"invocation"`
	Group            string            `json:"group"`
	GroupInode       uint64            `json:"group_inode"`
	Created          time.Time         `json:"created"`
	Deadline         time.Time         `json:"deadline"`
	Stopping         *time.Time        `json:"stopping,omitempty"`
	Resources        wire.NixResources `json:"resources"`
}

type Description struct {
	Build          wire.BuildIdentity `json:"build"`
	InstanceID     string             `json:"instance_id"`
	Ready          bool               `json:"ready"`
	Active         []Session          `json:"active"`
	StopGrace      int                `json:"stop_grace_seconds"`
	ReleaseTimeout int                `json:"release_timeout_seconds"`
}

type Caller interface {
	Call(context.Context, string, Request, any) error
}

type Client struct{ http *http.Client }

func NewClient(socket string) *Client {
	return &Client{http: &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}}
}

func (c *Client) Call(ctx context.Context, method string, params Request, result any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://executor/call", bytes.NewReader(wire.JSON(struct {
		Method string  `json:"method"`
		Params Request `json:"params"`
	}{method, params})))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return unavailable(method)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, wire.MaxJSON+1))
	if err != nil {
		return unavailable(method)
	}
	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error *wire.Error `json:"error"`
		}
		if wire.Decode(data, &body) != nil || body.Error == nil {
			return unavailable(method)
		}
		return body.Error
	}
	return wire.Decode(data, result)
}

func unavailable(method string) error {
	return &wire.Error{Code: "executor_unavailable", Message: "Executor response was not confirmed", Retry: "reconcile", NextAction: "Inspect or restart just nix-executor, then recover the existing job", Details: map[string]any{"stage": "executor_control", "method": method, "required_actor": "administrator"}}
}

func cloneSession(s *Session) Session {
	var v Session
	_ = json.Unmarshal(wire.JSON(s), &v)
	return v
}
