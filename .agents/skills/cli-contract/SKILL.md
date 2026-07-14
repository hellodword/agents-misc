---
name: cli-contract
description: Use this when creating or changing a CLI command, flags, arguments, stdout/stderr behavior, exit codes, config files, or environment variables.
---

# CLI Contract

## Purpose

Keep CLI behavior stable, scriptable, and documented.

## Workflow

1. Define command name, subcommands, flags, args, config, environment variables, stdin, stdout, stderr, and exit codes.
2. Put the primary result on stdout and diagnostics, progress, warnings, and human-readable errors on stderr.
3. Keep stdout pure whenever a structured-output mode is selected. Add such a mode only for an established or requested consumer.
4. Preserve existing config precedence. For greenfield work without another requirement, use flags, environment variables, config file, then defaults.
5. Prefer additive changes for durable CLIs.
6. Give renamed flags a deprecated alias or another durable migration path unless an exact specific exception or confirmed aggressive scope authorizes the break.
7. Record authorization evidence for every non-durable compatibility mode.
8. Add tests for parsing, stdout/stderr, exit codes, structured output, and precedence as applicable.

## Output

Report the CLI contract changes, compatibility mode and evidence, tests added, and examples updated.
