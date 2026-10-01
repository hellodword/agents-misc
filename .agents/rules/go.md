# Go

- Use Go modules and `gofmt`.
- Use `log/slog` for structured application logging and keep sensitive values out of fields.
- Pass `context.Context` through request, process, and storage boundaries; honor cancellation and deadlines.
- For HTTP, start with `net/http`. Configure explicit read-header, read, write, and idle timeouts and bound request bodies.
- Decode external JSON and config strictly when the contract rejects unknown fields.
- For YAML, use `github.com/goccy/go-yaml` with strict decoding and verify the current package/version before addition.
- Add file rotation only when retention or disk limits require it; use lumberjack after verifying its current module path and version.
- Return or wrap errors with operation context without duplicating noisy logs at every layer.
- Run focused package tests and broader Go validation when public packages, integration wiring, or dependencies change. Use the race detector for concurrency or shared mutable state.
