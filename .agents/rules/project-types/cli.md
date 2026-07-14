---
id: project-type.cli
kind: project-type
triggers:
  - "CLI project"
  - "command line tool"
  - "subcommands"
  - "CLI flags"
  - "terminal app"
summary: Apply defaults for durable command-line tools and CLI contracts.
companions: {}
---

# CLI Project Rules

Preserve the language and CLI framework of an existing project.

## Greenfield language choice

- Go and Rust are both supported defaults. If their tradeoff changes distribution, interoperability, performance, or long-term maintenance and the user has not chosen, ask before selecting one.
- Select Python only when the user asks or ecosystem/project evidence favors it.
- Select Node.js only when the user asks or the CLI is coupled to frontend, npm, browser automation, or Node tooling.

## CLI contract

- Define command name.
- Define subcommands.
- Define flags and args.
- Define config file behavior.
- Define environment variables.
- Define stdin/stdout/stderr behavior.
- Define exit codes.
- Define machine-readable output stability.

## General CLI rules

- Send the command's primary result to stdout and diagnostics to stderr; keep stdout pure in structured-output mode.
- Support `--help`.
- Use useful error messages.
- Avoid hidden network access.
- Avoid shelling out unless necessary.
- Keep output deterministic unless the command is explicitly interactive or time-sensitive.

## Validation

- Unit tests for parsing and pure logic.
- Integration tests for filesystem/process behavior.
- Golden/snapshot tests only when output is intentionally stable.
