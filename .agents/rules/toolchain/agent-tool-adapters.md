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
summary: Probe available agent CLIs before using Codex, OpenCode, or generic one-shot review workflows.
load_with:
  references:
    - agent-tool-adapter-examples
---

# Agent Tool Adapter Rules

Use this rule when a workflow needs a one-shot sub-agent, structured review, sandboxed review, or tool-specific command invocation.

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

Use workspace-write only when the task intentionally writes generated mockups, image edits, reports, or other outputs under an ignored temporary path.

Do not assume Codex CLI flags work in OpenCode.

Do not assume OpenCode CLI flags work in Codex.
