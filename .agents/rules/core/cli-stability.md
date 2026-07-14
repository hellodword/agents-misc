---
id: core.cli-stability
kind: core
triggers:
  - "CLI contract"
  - "CLI flags"
  - "exit codes"
  - "stdout"
  - "stderr"
  - "machine-readable output"
summary: Keep CLI commands, flags, output, and exit behavior stable and scriptable.
companions:
  required_rules:
    - core.compatibility
    - core.testing
  conditional_rules:
    - id: project-type.cli
      when: the repository or task is CLI-focused
  skills:
    - id: cli-contract
      when: producing or updating a CLI contract workflow is needed
  templates:
    - id: cli-contract
      when: producing or updating a CLI contract artifact
---

# CLI Stability Rules

CLI behavior is a contract.

## Output

- Put the command's primary result on stdout.
- Put diagnostics, progress, warnings, and human-readable errors on stderr.
- When a structured-output mode is selected, keep stdout exclusively in that format; never mix progress or commentary into it.
- Add JSON or another structured mode only when the existing project contract, user request, or durable automation consumer requires it. Keep structured fields additive by default.

## Exit codes

- `0` means success; nonzero means failure.
- Keep documented meanings stable and document any special codes.

## Flags and arguments

- Preserve documented flags by default and prefer additive flags.
- A renamed flag needs a durable migration path, such as a deprecated alias, unless the exact break is covered by a specific exception or confirmed aggressive scope under `core.compatibility`.
- Update docs and tests for every renamed or removed flag.

## Config and environment

- Preserve the existing precedence contract. For greenfield work without another requirement, use flags, then environment variables, then config file, then defaults.
- Avoid hidden network access and interactive prompts unless the command contract calls for them.
- Provide `--yes` or equivalent only when the underlying operation is safe to authorize that way.

## Testing

- Test parsing, stdout/stderr separation, exit codes, and config/environment precedence.
- Use golden tests only when exact output is intentionally durable.
