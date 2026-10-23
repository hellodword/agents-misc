package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/control"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/hostexec"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/worker"
)

func main() { os.Exit(run()) }
func run() int {
	f := flag.NewFlagSet("vm-test-mcp", flag.ContinueOnError)
	file := f.String("config", "", "shared YAML configuration file")
	check := f.Bool("check-config", false, "validate configuration without starting services or creating state")
	reconcile := f.String("reconcile", "", "retry cleanup for a locally verified task")
	version := f.Bool("version", false, "print version")
	if e := f.Parse(os.Args[1:]); e != nil {
		return 2
	}
	if *version {
		fmt.Println(wire.Version)
		return 0
	}
	if *file == "" || f.NArg() != 0 {
		return 2
	}
	cfg, e := config.Load(*file)
	if e != nil {
		slog.Error("configuration failed", "error", e)
		return 1
	}
	if *check {
		fmt.Println("{\"valid\":true}")
		return 0
	}
	b := &control.VBoxBackend{Config: cfg, Runner: hostexec.Exec{}}
	if cfg.Nix.Socket != "" {
		b.Worker = worker.NewClient(cfg.Nix.Socket)
	}
	s, e := control.New(cfg, b)
	if e != nil {
		slog.Error("state initialization failed", "error", e)
		return 1
	}
	defer s.Close()
	b.CacheURL = func(id string) string { u, _ := url.Parse(s.WorkerCacheURL(id)); return u.Path }
	if *reconcile != "" {
		if e = s.Reconcile(*reconcile); e != nil {
			slog.Error("cleanup remains unresolved", "error", e)
			return 1
		}
		fmt.Println("{\"cleanup\":\"complete\"}")
		return 0
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := s.HTTPServer()
	s.Start()
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("VM Test MCP listening", "address", cfg.Service.Listen, "version", wire.Version)
	if e = server.ListenAndServe(); e != nil && !errors.Is(e, http.ErrServerClosed) {
		slog.Error("HTTP service failed", "error", e)
		return 1
	}
	return 0
}
