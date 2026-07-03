---
id: project-type.cli
kind: project-type
triggers:
  - 'CLI project'
  - 'command line tool'
  - 'subcommands'
  - 'flags'
  - 'terminal app'
---

# CLI Project Rules

## Applicability

Use these defaults only for new CLI projects, greenfield scaffolding, or repositories without a clear existing CLI convention. Do not introduce these stacks into an existing project merely because they are preferred here.

## Default languages

- Prefer Go or Rust.
- Use Python when the ecosystem strongly favors it.
- Use Node.js when the CLI is tightly coupled to frontend, npm, browser automation, or Node tooling ecosystems.

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

- Send machine-readable output to stdout.
- Send human diagnostics to stderr.
- Support `--help`.
- Use useful error messages.
- Avoid hidden network access.
- Avoid shelling out unless necessary.
- Keep output deterministic unless the command is explicitly interactive or time-sensitive.

## Validation

- Unit tests for parsing and pure logic.
- Integration tests for filesystem/process behavior.
- Golden/snapshot tests only when output is intentionally stable.
