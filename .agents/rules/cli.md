# Command-Line Interfaces

Preserve an existing CLI's language, parser, output format, exit codes, and precedence.

## Greenfield defaults

- Language: Go by default; Rust when sharing a Rust core or requiring systems-level control; Python (`argparse`) for Python/automation ecosystems; TypeScript (`tsc --noEmit`, esbuild bundle, npm) for frontend/browser tooling.
- Go parser: standard `flag` for single-command tools; Cobra when subcommands or shell completions are needed. Rust parser: `clap` derive.

## Contract

- Start with explicit flags and arguments rather than inventing config files or environment overrides without a concrete use case.
- Write primary machine-readable or generated output to `stdout` and human diagnostics/progress to `stderr`.
- For new CLIs, exit `0` on success, `2` on usage/argument validation errors, and `1` on runtime failures. Test argument parsing, representative stdout/stderr separation, and exit codes when changing CLI contracts.
