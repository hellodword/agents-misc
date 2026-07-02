---
name: cli-contract
description: Use this when creating or changing a CLI command, flags, arguments, stdout/stderr behavior, exit codes, config files, or environment variables.
---

# CLI Contract

## Purpose

Keep CLI behavior stable, scriptable, and documented.

## Workflow

1. Define command name, subcommands, flags, args, config, env vars, stdin/stdout/stderr, and exit codes.
2. Keep machine-readable output on stdout.
3. Keep diagnostics and progress on stderr.
4. Document config precedence:
   - flags;
   - environment variables;
   - config file;
   - defaults.
5. Prefer additive changes for durable CLIs.
6. Provide deprecated aliases for renamed flags when practical.
7. Use aggressive mode only when explicitly requested.
8. Add tests for parsing, stdout/stderr, exit codes, and precedence.

## Output

Report:

- CLI contract changes;
- compatibility impact;
- tests added;
- examples updated.
