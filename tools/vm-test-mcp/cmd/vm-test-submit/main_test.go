package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIUsage(t *testing.T) {
	for _, test := range []struct {
		args []string
		code int
	}{
		{[]string{"--help"}, 0},
		{nil, 2},
		{[]string{"--unknown"}, 2},
		{[]string{"--config", "unused.yaml", "--cache-id", "manual-cache"}, 2},
		{[]string{"--config", "unused.yaml", "--timeout-seconds", "-1"}, 2},
		{[]string{"--config", "unused.yaml", "unexpected"}, 2},
	} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(test.args, &stdout, &stderr); code != test.code || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", test.args, code, stdout.String(), stderr.String())
		}
	}
}

func TestManualFailureKeepsStdoutEmpty(t *testing.T) {
	var stdout, stderr bytes.Buffer
	dir := t.TempDir()
	code := runCLI([]string{"--test", ".#test", "--request-id", "manual", "--cache-id", strings.Repeat("a", 32), "--cache-url", "http://127.0.0.1:1/files/synthetic", "--work-dir", dir, "--nix", filepath.Join(dir, "missing-nix")}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
