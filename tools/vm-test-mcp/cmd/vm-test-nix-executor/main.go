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
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/nixexecutor"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func main() { os.Exit(run()) }
func run() int {
	f := flag.NewFlagSet("vm-test-nix-executor", flag.ContinueOnError)
	file := f.String("config", "config.yaml", "shared YAML; sudo startup authorizes its executor policy")
	version := f.Bool("version", false, "print JSON build identity without opening state")
	check := f.Bool("check-config", false, "validate executor configuration values")
	if err := f.Parse(os.Args[1:]); err != nil || f.NArg() != 0 {
		return 2
	}
	if *version {
		fmt.Println(string(wire.JSON(wire.Identity())))
		return 0
	}
	cfg, err := config.LoadExecutor(*file)
	if err != nil {
		slog.Error("executor policy failed", "error", err)
		return 1
	}
	if *check {
		fmt.Println(`{"valid":true}`)
		return 0
	}
	e, err := nixexecutor.New(cfg)
	if err != nil {
		slog.Error("executor startup failed", "error", err)
		return 1
	}
	listener, err := e.Listen()
	if err != nil {
		slog.Error("executor socket failed", "error", err)
		return 1
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := e.HTTPServer()
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
	case err = <-served:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("executor serving failed", "error", err)
		}
	}
	// Ctrl+C has its own budget; TimeoutStopSec covers systemctl stop. If
	// this deadline expires, exiting MainPID invokes the service's mixed kill.
	shutdown, stop := context.WithTimeout(context.Background(), cfg.ReleaseTimeout.Value()+5*time.Second)
	defer stop()
	if err = e.Shutdown(shutdown); err != nil {
		slog.Error("executor resources remain recorded for reconciliation", "error", err)
		return 1
	}
	_ = server.Shutdown(shutdown)
	e.Close()
	return 0
}
