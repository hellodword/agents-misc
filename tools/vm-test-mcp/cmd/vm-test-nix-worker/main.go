package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/config"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/worker"
)

func main() { os.Exit(run()) }
func run() int {
	f := flag.NewFlagSet("vm-test-nix-worker", flag.ContinueOnError)
	file := f.String("config", "", "shared YAML configuration file")
	version := f.Bool("version", false, "print JSON build and running-binary identity without configuration or state access")
	check := f.Bool("check-config", false, "validate worker configuration without starting it or creating state")
	if e := f.Parse(os.Args[1:]); e != nil {
		return 2
	}
	if *version {
		fmt.Println(string(wire.JSON(wire.Identity())))
		return 0
	}
	if *file == "" || f.NArg() != 0 {
		return 2
	}
	cfg, e := config.LoadWorker(*file)
	if e != nil {
		slog.Error("configuration failed", "error", e)
		return 1
	}
	if *check {
		fmt.Println("{\"valid\":true}")
		return 0
	}
	w, e := worker.New(cfg)
	if e != nil {
		slog.Error("worker state failed", "error", e)
		return 1
	}
	defer w.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	probe, stop := context.WithTimeout(ctx, 30*time.Second)
	e = w.Preflight(probe)
	stop()
	if e != nil {
		slog.Error("Nix isolation preflight failed", "error", e)
		return 1
	}
	listener, e := w.Listen()
	if e != nil {
		slog.Error("socket failed", "error", e)
		return 1
	}
	defer listener.Close()
	server := w.HTTPServer()
	go func() {
		<-ctx.Done()
		c, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(c)
	}()
	if e = server.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
		slog.Error("worker failed", "error", e)
		return 1
	}
	return 0
}
