package worker

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

type Client struct{ http *http.Client }

func NewClient(socket string) *Client {
	return &Client{http: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}}
}
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	r, e := http.NewRequestWithContext(ctx, "POST", "http://worker/call", bytes.NewReader(wire.JSON(map[string]any{"method": method, "params": params})))
	if e != nil {
		return nil, e
	}
	r.Header.Set("Content-Type", "application/json")
	response, e := c.http.Do(r)
	if e != nil {
		return nil, wire.Fail("worker_unavailable", "Nix worker could not be reached; inspect accepted tasks before retrying")
	}
	defer response.Body.Close()
	b, e := io.ReadAll(io.LimitReader(response.Body, wire.MaxJSON+1))
	if e != nil {
		return nil, e
	}
	if e = wire.CheckJSON(b); e != nil {
		return nil, e
	}
	if response.StatusCode != 200 {
		return nil, wire.Fail("worker_error", "Worker rejected the operation")
	}
	return b, nil
}
