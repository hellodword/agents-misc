package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/router"
	"os"
	"strings"
)

func main() { os.Exit(run()) }
func run() int {
	if len(os.Args) < 2 {
		return 2
	}
	f := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	request := f.String("request", "", "structured configuration file")
	if e := f.Parse(os.Args[2:]); e != nil {
		return 2
	}
	ctx, cancel := router.Context()
	defer cancel()
	var e error
	switch os.Args[1] {
	case "init":
		e = router.Init(ctx, *request)
	case "recover":
		e = router.Init(ctx, *request)
		if e == nil {
			e = os.Remove(*request)
		}
	case "probe":
		e = router.Probe(ctx)
	case "apply":
		p, err := router.ReadPolicy(*request)
		e = err
		if e == nil {
			e = router.Apply(ctx, p)
		}
	default:
		return 2
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "router configuration failed")
		return 1
	}
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ready": true, "boot_id": strings.TrimSpace(string(boot))})
	return 0
}
