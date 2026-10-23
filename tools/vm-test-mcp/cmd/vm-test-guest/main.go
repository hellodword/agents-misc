package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/guest"
)

func main() { os.Exit(run()) }
func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: vm-test-guest prepare|firstboot|service|probe|run|input|cancel|archive|reboot [flags]")
		return 2
	}
	mode := os.Args[1]
	f := flag.NewFlagSet(mode, flag.ContinueOnError)
	root := f.String("root", guest.DefaultRoot, "guest work directory")
	request := f.String("request", "", "JSON request file")
	id := f.String("id", "", "command ID")
	path := f.String("path", "", "guest source path")
	output := f.String("output", "", "archive file")
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
		e = guest.FirstBoot(*root)
	case "service":
		e = guest.Service(*root)
	case "execute":
		e = guest.Execute(ctx, *root, *request)
	case "probe":
		e = json.NewEncoder(os.Stdout).Encode(guest.Probe(*root))
	case "run":
		if *root == "" || *request == "" {
			return 2
		}
		e = guest.Run(ctx, *root, *request)
	case "input":
		e = guest.Input(*root, *id, *request)
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
