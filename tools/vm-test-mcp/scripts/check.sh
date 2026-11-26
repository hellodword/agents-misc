#!/usr/bin/env bash
set -euo pipefail
cd tools/vm-test-mcp
go vet -tags sqlite_omit_load_extension ./...
go test -race -tags sqlite_omit_load_extension ./...
# Cross compilation is distinct from real Windows and cgroup acceptance.
vm_test_check_dir=$(mktemp -d)
trap 'rm -rf "$vm_test_check_dir"' EXIT
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o "$vm_test_check_dir/guest.exe" ./cmd/vm-test-guest
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$vm_test_check_dir/guest" ./cmd/vm-test-guest
