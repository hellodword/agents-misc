package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type Client struct{ http *http.Client }

var errorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

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
		return nil, workerError("worker_unavailable", "Nix worker response was not confirmed; inspect accepted tasks before retrying", method, 0)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusForbidden {
		return nil, workerError("forbidden", "Nix worker rejected the controller peer identity", method, response.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(response.Body, wire.MaxJSON+1))
	if e != nil {
		return nil, workerError("worker_unavailable", "Nix worker response could not be read completely", method, response.StatusCode)
	}
	if e = wire.CheckJSON(b); e != nil {
		return nil, workerError("worker_protocol_error", "Nix worker returned an invalid or oversized response", method, response.StatusCode)
	}
	if response.StatusCode != http.StatusOK {
		var envelope struct {
			Error *wire.Error `json:"error"`
		}
		if wire.Decode(b, &envelope) != nil || envelope.Error == nil || !errorCode.MatchString(envelope.Error.Code) || !wire.ValidText(envelope.Error.Message, 1024) || envelope.Error.Message == "" {
			return nil, workerError("worker_protocol_error", "Nix worker returned an invalid error response", method, response.StatusCode)
		}
		err := wire.Safe(envelope.Error)
		if err.Details == nil {
			err.Details = map[string]any{}
		}
		err.Details["boundary"], err.Details["method"], err.Details["http_status"] = "nix_worker", method, response.StatusCode
		return nil, err
	}
	return b, nil
}

func workerError(code, message, method string, status int) *wire.Error {
	return wire.Safe(&wire.Error{Code: code, Message: message, Details: map[string]any{"boundary": "nix_worker", "method": method, "http_status": status}})
}
