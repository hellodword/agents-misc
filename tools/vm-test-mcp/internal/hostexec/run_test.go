package hostexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestProcessFixture(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "hostexec-fixture" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "exit":
		fmt.Fprint(os.Stdout, "partial output")
		fmt.Fprint(os.Stderr, "diagnostic reason")
		os.Exit(7)
	case "timeout":
		fmt.Fprint(os.Stdout, "before timeout")
		time.Sleep(time.Minute)
	case "bounded":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), (1<<20)+10))
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("y"), 9000))
	case "headtail":
		_, _ = os.Stdout.Write([]byte("BOOT-HEAD\n"))
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("m"), (1<<20)+4096))
		_, _ = os.Stdout.Write([]byte("\nCRASH-TAIL"))
	}
	os.Exit(0)
}

func TestCapturedExitTimeoutAndLimits(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, mode string) (Result, error) {
		return Capture(ctx, Exec{}, exe, []string{"-test.run=^TestProcessFixture$", "--", "hostexec-fixture", mode}, nil)
	}
	out, err := run(context.Background(), "exit")
	var failure *Failure
	if !errors.As(err, &failure) || out.ExitCode != 7 || string(out.Stdout) != "partial output" || string(out.Stderr) != "diagnostic reason" {
		t.Fatal(out, err)
	}
	if out.ProcessID <= 1 || !out.Reaped {
		t.Fatal("missing observed host process identity", out)
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	out, err = run(ctx, "timeout")
	if !errors.Is(err, context.DeadlineExceeded) || !out.TimedOut || string(out.Stdout) != "before timeout" {
		t.Fatal(out, err)
	}
	if out.ProcessID <= 1 || !out.Reaped {
		t.Fatal("host timeout was not reaped", out)
	}
	out, err = run(context.Background(), "bounded")
	if err != nil || len(out.Stdout) != 1<<20 || len(out.Stderr) != 8192 || !out.StdoutTruncated || !out.StderrTruncated {
		t.Fatalf("limits: %d %d %v", len(out.Stdout), len(out.Stderr), err)
	}
	ht, err := CaptureHeadTail(context.Background(), Exec{}, exe, []string{"-test.run=^TestProcessFixture$", "--", "hostexec-fixture", "headtail"}, 256<<10, 768<<10)
	if err != nil || !ht.StdoutTruncated || len(ht.Stdout) > 1<<20 || !bytes.HasPrefix(ht.Stdout, []byte("BOOT-HEAD\n")) || !bytes.HasSuffix(ht.Stdout, []byte("\nCRASH-TAIL")) || !bytes.Contains(ht.Stdout, []byte("omitted")) {
		t.Fatalf("head+tail capture failed: len=%d truncated=%t observed=%d err=%v", len(ht.Stdout), ht.StdoutTruncated, ht.ObservedStdout, err)
	}
}
