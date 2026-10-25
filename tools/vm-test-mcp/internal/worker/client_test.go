package worker

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientKeepsWorkerFailureBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, body, code, category, retry string
		status                            int
	}{
		{"isolation", `{"error":{"code":"policy_denied","message":"Expected a sandboxed KVM test derivation","details":{"stage":"derivation_policy"}}}`, "policy_denied", "authorization", "after_change", 400},
		{"occupied", `{"error":{"code":"resource_busy","message":"Worker requires cleanup or is executing"}}`, "resource_busy", "capacity", "after_recovery", 400},
		{"identity", "", "forbidden", "authorization", "after_change", 403},
		{"invalid JSON", "private upstream body", "worker_protocol_error", "execution", "reconcile", 502},
		{"oversized", strings.Repeat("x", wire.MaxJSON+1), "worker_protocol_error", "execution", "reconcile", 500},
		{"missing error", `{}`, "worker_protocol_error", "execution", "reconcile", 400},
		{"missing code", `{"error":{"message":"private upstream body"}}`, "worker_protocol_error", "execution", "reconcile", 400},
		{"duplicates", `{"error":{"code":"policy_denied","code":"backend_error","message":"private upstream body"}}`, "worker_protocol_error", "execution", "reconcile", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &Client{http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			_, err := client.Call(context.Background(), "start", Request{})
			if err == nil {
				t.Fatal("rejection accepted")
			}
			e := wire.Safe(err)
			if e.Code != tc.code || e.Category != tc.category || e.Retry != tc.retry || e.Details["boundary"] != "nix_worker" || e.Details["method"] != "start" || e.Details["http_status"] != tc.status {
				t.Fatalf("lost worker failure: %+v", e)
			}
			if tc.name == "isolation" && e.Details["stage"] != "derivation_policy" {
				t.Fatal("lost rejection evidence", e)
			}
			if strings.Contains(string(wire.JSON(e)), "private upstream body") {
				t.Fatal("unparsed body disclosed")
			}
		})
	}
}

func TestClientUnconfirmedResponseRequiresReconciliation(t *testing.T) {
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}}
	_, err := client.Call(context.Background(), "start", Request{})
	e := wire.Safe(err)
	if e.Code != "worker_unavailable" || e.Retry != "reconcile" || e.Details["method"] != "start" {
		t.Fatal(e)
	}
}
