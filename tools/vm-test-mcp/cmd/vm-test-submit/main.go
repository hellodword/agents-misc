package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/submit"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func main() { os.Exit(run()) }
func run() int {
	return runCLI(os.Args[1:], os.Stdout, os.Stderr)
}
func runCLI(args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("vm-test-submit", flag.ContinueOnError)
	f.SetOutput(stderr)
	o := submit.Options{}
	version := f.Bool("version", false, "print JSON build and running-binary identity without configuration, evaluation or upload")
	configFile := f.String("config", "", "client YAML with endpoint and token: allocate, submit, observe and download evidence")
	project := f.String("project", ".", "locked project for the default three-node smoke test")
	timeout := f.Int("timeout-seconds", 0, "remote task deadline; 0 uses the server default")
	evidence := f.String("evidence", "all", "automatic mode: all downloads registered artifacts; manifest saves their inventory")
	f.StringVar(&o.Nix, "nix", "nix", "Nix executable in this container")
	f.StringVar(&o.Test, "test", "", "locked project test flake attribute; with --config, omission selects the smoke test")
	f.StringVar(&o.RequestID, "request-id", "", "execution identity; generated in automatic mode, reused on retries")
	f.StringVar(&o.CacheURL, "upload-url", "", "cache upload capability URL")
	f.StringVar(&o.CacheID, "upload-id", "", "input upload ID")
	f.StringVar(&o.WorkDir, "work-dir", "", "absolute retained workspace; automatic mode defaults to tmp/agent/vm-test-runs/ID")
	f.StringVar(&o.CacheDir, "cache-dir", "", "shared local export cache; defaults to the user cache directory")
	f.Int64Var(&o.CacheBytes, "cache-bytes", 16<<30, "retained local export cache byte budget")
	f.StringVar(&o.SigningKey, "signing-key", "", "optional cache signing key trusted by administrator")
	if e := f.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *version {
		if err := json.NewEncoder(stdout).Encode(wire.Identity()); err != nil {
			return 1
		}
		return 0
	}
	if f.NArg() != 0 || o.CacheBytes < 1 || o.CacheDir != "" && !filepath.IsAbs(o.CacheDir) || *timeout < 0 || *timeout > 86400 ||
		(*configFile != "" && (o.CacheID != "" || o.CacheURL != "")) ||
		(*evidence != "all" && *evidence != "manifest") ||
		(*configFile == "" && (o.Test == "" || o.RequestID == "" || o.CacheID == "" || o.CacheURL == "" || o.WorkDir == "")) {
		f.Usage()
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	o.Progress = stderr
	var result any
	var e error
	if *configFile != "" {
		cfg, err := submit.LoadConnection(*configFile)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		_, resumeErr := os.Stat(filepath.Join(o.WorkDir, "run.json"))
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		o.SmokeFile = filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "share", "vm-test-mcp", "vm-smoke.nix"))
		if o.Test == "" && (o.WorkDir == "" || resumeErr != nil) {
			o.SmokeProject, err = filepath.Abs(*project)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
		var raw json.RawMessage
		raw, e = submit.Run(ctx, submit.RunOptions{Options: o, Endpoint: cfg.Endpoint, Token: cfg.Token, TimeoutSeconds: *timeout, Progress: stderr, Evidence: *evidence})
		if len(raw) != 0 {
			result = raw
		}
	} else {
		var prepared map[string]any
		prepared, e = submit.Prepare(ctx, o)
		if prepared != nil {
			if *timeout != 0 {
				prepared["timeout_seconds"] = *timeout
			}
			result = prepared
		}
	}
	if result != nil {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if e != nil {
		fmt.Fprintln(stderr, e)
		return submit.ExitCode(e)
	}
	return 0
}
