package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/guest"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func main() { os.Exit(run()) }
func run() int {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		if err := json.NewEncoder(os.Stdout).Encode(wire.Identity()); err != nil {
			return 1
		}
		return 0
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: vm-test-guest prepare|firstboot|service|probe|run|read|input|cancel|archive|reboot [flags]")
		return 2
	}
	mode := os.Args[1]
	f := flag.NewFlagSet(mode, flag.ContinueOnError)
	root := f.String("root", guest.DefaultRoot, "guest work directory")
	controlUser := f.String("control-user", "", "local Windows Guest Control account permitted to observe service/supervisor process identity")
	request := f.String("request", "", "JSON request file")
	id := f.String("id", "", "command ID")
	path := f.String("path", "", "guest source path")
	output := f.String("output", "", "archive file")
	stdoutOffset := f.Int64("stdout-offset", 0, "confirmed stdout byte offset")
	stderrOffset := f.Int64("stderr-offset", 0, "confirmed stderr byte offset")
	limit := f.Int("limit", 65536, "maximum bytes per output stream")
	if e := f.Parse(os.Args[2:]); e != nil {
		return 2
	}
	if f.NArg() != 0 {
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var e error
	switch mode {
	case "prepare":
		e = guest.Prepare(*root)
	case "firstboot":
		e = guest.FirstBoot(ctx, *root)
	case "service":
		e = guest.Service(*root, *controlUser)
	case "execute":
		if e = guest.AllowProcessObservation(*controlUser); e == nil {
			e = guest.Execute(ctx, *root, *request)
		}
	case "probe":
		e = json.NewEncoder(os.Stdout).Encode(guest.Probe(*root))
	case "run":
		if *root == "" || *request == "" {
			return 2
		}
		e = guest.Run(ctx, *root, *request)
	case "input":
		e = guest.Input(*root, *id, *request)
	case "read":
		var snapshot any
		snapshot, e = guest.Read(*root, *id, *stdoutOffset, *stderrOffset, *limit)
		if e == nil {
			e = json.NewEncoder(os.Stdout).Encode(snapshot)
		}
	case "cancel":
		e = guest.Cancel(*root, *id)
	case "archive":
		e = guest.Archive(*path, *output)
	case "reboot":
		e = guest.Reboot()
	default:
		return 2
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	return 0
}
