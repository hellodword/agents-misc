package control

import (
	"bytes"
	"context"
	"crypto/subtle"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ServerInstructions = "VM Test MCP manages disposable VirtualBox labs (lab_*) and sandboxed host-KVM NixOS tests (nix_job_*) on a shared FIFO scheduler. " +
	"Authenticate /mcp requests with the configured Bearer token; /files/ transfers use scoped capability URLs without Bearer headers. " +
	"Every mutating tool scopes idempotency by (tool, request_id): reuse the exact arguments when retrying an uncertain call on that tool, and generate a fresh request_id whenever arguments change. " +
	"Executions use execution_id across nix_job_get, nix_job_cancel, nix_job_extend, execution_log, execution_events, evidence_list, execution_recover and execution_delete (lab tools address their lab via lab_id). " +
	"lab_create, nix_job_create and wait accept optional wait_seconds (0..600, default 600): pass 0 to return immediately after admission or a single snapshot with wait.continue cursors, or configure client tool timeouts >= 660s for full 600s observation windows. " +
	"Track resources.state (released/unallocated vs blocked) separately from evidence.state (complete vs blocked); use execution_recover(scope=\"resources\"|\"evidence\") after resolving a recorded blocker."

func (s *Server) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "vm-test-mcp", Version: wire.Version}, &mcp.ServerOptions{Instructions: ServerInstructions})
	for _, d := range Definitions {
		server.AddTool(&mcp.Tool{Name: d.Name, Description: d.Description, InputSchema: Schema(reflect.TypeOf(d.Args)), OutputSchema: outputSchema(d.Name)}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if stream, ok := ctx.Value(heartbeatKey{}).(*heartbeatWriter); ok {
				stream.start()
			}
			value, e := s.Call(ctx, d.Name, r.Params.Arguments)
			if e != nil {
				value = map[string]any{"error": wire.Safe(e)}
			}
			return &mcp.CallToolResult{IsError: e != nil, Content: []mcp.Content{&mcp.TextContent{Text: string(wire.JSON(value))}}, StructuredContent: value}, nil
		})
	}
	mcpHTTP := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.cfg.Service.Hosts) > 0 && !slices.Contains(s.cfg.Service.Hosts, r.Host) {
			httpError(w, 403, wire.Fail("host_denied", "Host is not allowed"))
			return
		}
		if o := r.Header.Get("Origin"); o != "" && len(s.cfg.Service.Origins) > 0 && !slices.Contains(s.cfg.Service.Origins, o) {
			httpError(w, 403, wire.Fail("origin_denied", "Origin is not allowed"))
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/files/") {
			s.Files(w, r)
			return
		}
		if r.URL.Path != "/mcp" {
			httpError(w, 404, wire.Fail("not_found", "Route not found"))
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.cfg.Service.Token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpError(w, 401, wire.Fail("unauthorized", "Bearer authentication required"))
			return
		}
		if r.Method == "POST" {
			data, e := io.ReadAll(io.LimitReader(r.Body, wire.MaxJSON+1))
			if e != nil {
				httpError(w, 400, wire.Fail("invalid_argument", "Cannot read request"))
				return
			}
			if e = wire.CheckJSON(data); e != nil {
				httpError(w, 400, e)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
		}
		stream := &heartbeatWriter{ResponseWriter: w, done: make(chan struct{}), finished: make(chan struct{}), interval: TransportHeartbeat}
		r = r.WithContext(context.WithValue(r.Context(), heartbeatKey{}, stream))
		mcpHTTP.ServeHTTP(stream, r)
		stream.close()
	})
}
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{Addr: s.cfg.Service.Listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 5 * time.Minute, WriteTimeout: 660 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}

type heartbeatKey struct{}

// SDK delivery and transport comments share a writer lock. Heartbeats start only
// inside an accepted tool handler, after SDK validation and header setup. They
// never create MCP notifications, database events or observer history.
type heartbeatWriter struct {
	http.ResponseWriter
	mu             sync.Mutex
	once           sync.Once
	done, finished chan struct{}
	interval       time.Duration
	started        bool
}

func (w *heartbeatWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseWriter.Write(p)
}
func (w *heartbeatWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ResponseWriter.WriteHeader(status)
}
func (w *heartbeatWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *heartbeatWriter) start() {
	w.once.Do(func() {
		w.mu.Lock()
		w.started = true
		_, _ = io.WriteString(w.ResponseWriter, ": observing\n\n")
		_ = http.NewResponseController(w.ResponseWriter).Flush()
		w.mu.Unlock()
		go func() {
			defer close(w.finished)
			tick := time.NewTicker(w.interval)
			defer tick.Stop()
			for {
				select {
				case <-w.done:
					return
				case <-tick.C:
					w.mu.Lock()
					_, err := io.WriteString(w.ResponseWriter, ": heartbeat\n\n")
					if err == nil {
						err = http.NewResponseController(w.ResponseWriter).Flush()
					}
					w.mu.Unlock()
					if err != nil {
						return
					}
				}
			}
		}()
	})
}
func (w *heartbeatWriter) close() {
	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if started {
		close(w.done)
		<-w.finished
	}
}
