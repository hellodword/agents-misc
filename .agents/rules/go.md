# Go

- Format with `gofmt`, use `log/slog` without leaking secrets into fields, and configure explicit HTTP `ReadHeaderTimeout`, `ReadTimeout`, `WriteTimeout`, `IdleTimeout`, and request-body limits on `net/http` servers.
- Decode external JSON/YAML strictly when contracts reject unknown fields; use `github.com/goccy/go-yaml` for YAML and `lumberjack` only when log rotation is needed (verifying current module paths and versions before adding).
- Run `go test -race` whenever changes touch concurrency, shared state, timers, signals, or connection lifecycles.
