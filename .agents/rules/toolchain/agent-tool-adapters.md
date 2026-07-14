---
id: toolchain.agent-tool-adapters
kind: toolchain
triggers:
  - "Codex"
  - "OpenCode"
  - "codex exec"
  - "opencode run"
  - "subagent"
  - "one-shot review"
summary: Use external Codex/OpenCode adapters only when explicitly requested or required by the active platform workflow.
companions:
  references:
    - id: agent-tool-adapter-examples
      when: adapter command shapes are needed
---

# Agent Tool Adapter Rules

Use an external Codex/OpenCode adapter only when the user explicitly requests an additional-agent workflow or the active platform requires that adapter. Otherwise perform the work in the main agent context. A desire for independent review or isolated context does not by itself authorize launching an external adapter.

## Probe before use

Before using tool-specific commands or flags:

1. Run `command -v codex` or `command -v opencode` for the intended adapter.
2. Run the relevant `--help` command when available.
3. Use only flags shown by the current environment.
4. If schema enforcement is unavailable, write output to a file and validate it separately.

Treat command examples as shapes, not guaranteed stable invocations. Verify current CLI help before use.

Reference command shapes live in `.agents/references/agent-tool-adapter-examples.md`.

## Codex

Use `codex exec` for one-shot non-mutating or workspace-write sub-agent tasks when available and verified in the current environment.

Use Codex-specific flags only when the current environment exposes them.

Suitable tasks include:

- screenshot batch review;
- structured finding extraction;
- synthesis of review outputs;
- independent compatibility or security review;
- non-mutating exploration that benefits from isolated context.

## OpenCode

Use `opencode run` for the corresponding one-shot sub-agent or non-interactive task when available and verified in the current environment.

Use OpenCode-specific flags only when the current environment exposes them.

Prefer passing a compact prompt, explicit input files, explicit output path, and a clear instruction to avoid modifying source files for review-only tasks.

## Generic fallback

If the current agent cannot spawn sub-agents or enforce an output schema, perform the workflow in the main context with smaller batches.

When using the fallback:

- reduce batch size;
- keep prompts compact;
- write intermediate outputs under `tmp/`;
- preserve the same rubric, schema expectations, and issue taxonomy;
- report that the workflow used the generic fallback.

## Safety

Use non-mutating execution for review tasks.

Use workspace-write only when the task intentionally writes visual-review mockups, exploratory review edits, reports, or other outputs under an ignored temporary path.

Do not assume Codex CLI flags work in OpenCode.

Do not assume OpenCode CLI flags work in Codex.
