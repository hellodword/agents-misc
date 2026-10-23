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
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Server) Handler() http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "vm-test-mcp", Version: wire.Version}, nil)
	for _, d := range Definitions {
		server.AddTool(&mcp.Tool{Name: d.Name, Description: d.Description, InputSchema: Schema(reflect.TypeOf(d.Args))}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			value, e := s.Call(ctx, d.Name, r.Params.Arguments)
			if e != nil {
				value = map[string]any{"error": wire.Safe(e)}
			}
			return &mcp.CallToolResult{IsError: e != nil, Content: []mcp.Content{&mcp.TextContent{Text: string(wire.JSON(value))}}, StructuredContent: value}, nil
		})
	}
	mcpHTTP := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
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
		mcpHTTP.ServeHTTP(w, r)
	})
}
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{Addr: s.cfg.Service.Listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 5 * time.Minute, WriteTimeout: 5 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
}
