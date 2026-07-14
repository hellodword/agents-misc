# Agent Tool Adapter Command Shapes

These are command shapes, not guaranteed stable invocations. Verify current CLI help before use.

## Probe

    command -v codex
    codex --help
    codex exec --help

    command -v opencode
    opencode --help
    opencode run --help

Use only flags exposed by the installed command.

## Codex review shape

    codex exec \
      <verified-non-mutating-or-sandbox-flags> \
      <verified-output-flags> \
      "Review the listed inputs using the rubric. Return only structured JSON."

If structured output flags are unavailable, redirect stdout to a file under `tmp/` and validate it separately.

## Codex synthesis shape

    codex exec \
      <verified-non-mutating-or-sandbox-flags> \
      <verified-output-flags> \
      "Merge findings, deduplicate, resolve conflicts, and produce one proposed implementation plan."

## OpenCode review shape

    opencode run \
      "Review the listed inputs using the rubric. Return only structured JSON." \
      > tmp/<workflow>/<run-id>/<output>.json

## OpenCode synthesis shape

    opencode run \
      "Merge findings, deduplicate, resolve conflicts, and produce one proposed implementation plan as JSON." \
      > tmp/<workflow>/<run-id>/<output>.json

## Generic fallback shape

When no verified one-shot adapter is available:

1. Split the inputs into smaller batches.
2. Keep the same rubric and schema expectations.
3. Write intermediate JSON under `tmp/`.
4. Validate shape separately.
5. Report that the generic fallback was used.
