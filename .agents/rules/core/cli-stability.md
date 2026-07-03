---
id: core.cli-stability
kind: core
triggers:
  - 'CLI contract'
  - 'flags'
  - 'exit codes'
  - 'stdout'
  - 'stderr'
  - 'machine-readable output'
summary: Keep CLI commands, flags, output, and exit behavior stable and scriptable.
load_with:
  rules:
    - project-type.cli
    - core.compatibility
    - core.testing
  skills:
    - cli-contract
  templates:
    - cli-contract
---

# CLI Stability Rules

CLI behavior is a contract.

## Output

- stdout is for machine-readable command output.
- stderr is for diagnostics, progress, warnings, and human-readable errors.
- Do not mix progress logs into stdout when stdout may be piped.
- Offer JSON output for durable automation when practical.
- Keep JSON fields additive by default.

## Exit codes

- `0`: success.
- Non-zero: failure.
- Keep exit code meanings stable once documented.
- Document special exit codes when used.

## Flags and arguments

- Keep documented flags compatible by default.
- Prefer additive flags.
- For renamed flags, provide deprecated aliases when practical.
- In aggressive early-stage mode, incompatible flag changes are allowed but must update docs/tests.

## Config and env

- Document precedence:
  1. CLI flags;
  2. environment variables;
  3. config file;
  4. defaults.
- Avoid hidden network access.
- Avoid interactive prompts unless command is explicitly interactive.
- Provide `--yes` or equivalent only when safe.

## Testing

- Test parsing.
- Test stdout/stderr separation.
- Test exit codes.
- Test config/env precedence.
- Use golden tests only when output is intentionally stable.
