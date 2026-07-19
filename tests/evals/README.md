# Agent behavior evals

This directory contains the Agent Rules Kit's routing, safety, and skill-trigger
scenarios. The deterministic repository check validates their structure and
coverage. `scripts/run-agent-evals.py` optionally sends them to a real Codex
model; live model results are diagnostic and are not a `nix flake check` gate.

## What is tested

Each JSONL record has two distinct contracts:

- `expected_rules`, `forbidden_rules`, `expected_skills`, and
  `forbidden_skills` test routing as exact sets.
- `behavior_checks` asks neutral yes/no questions after the routed rule and
  skill bodies are loaded. Every record contains both a `true` and a `false`
  expectation to expose agreement and ordering bias.
- `expected_behavior` remains a concise human-readable scenario description.
  The runner never serializes it, the expected route sets, or the expected
  booleans into a model prompt.

The run has two independent, ephemeral `codex exec` turns per scenario, each
with a fresh temporary home and synthetic repository. The route turn sees the
task, the full rule index, automatic `AGENTS.md` instructions, and automatic
skill metadata. Only an exactly correct route can advance. The behavior turn
disables automatic skill metadata and sees only the task, selected rule and
skill bodies, directly linked skill resources, and questions without answers.

## Isolation contract

Before any authenticated request, the runner:

1. Creates a synthetic repository containing exactly `AGENTS.md` and
   `.agents/**`; symlinks and non-UTF-8 payload files are rejected.
2. Creates private temporary `HOME`, `CODEX_HOME`, and XDG directories. It does
   not load the default `~/.codex` config, state, plugins, hooks, MCP servers,
   rules, memories, or skills.
3. Copies only `approval_policy`, `sandbox_mode`, and the bounded
   `sandbox_workspace_write` table from `--policy-config` when their CLI values
   are `inherit`. Model, feature, provider, and instruction settings are never
   inherited.
4. Uses the Codex bundled model catalog for the requested model, preserves its
   built-in instructions, and removes the model's `apply_patch` capability.
5. Disables shell, unified exec, web, browser, MCP/app/plugin, hook, image
   generation, memory, and subagent feature surfaces.
6. Runs `codex debug prompt-input` and rejects skill sources outside the
   synthetic `.agents/skills` tree or a loaded maintenance overlay.
7. Sends one unauthenticated request to a loopback-only fake Responses endpoint
   and verifies that the outgoing tool list is a subset of the versioned
   `allowed_tools` contract in `codex-runtime-contract.json`. A Codex upgrade
   without a reviewed contract or any unreviewed tool fails closed.

The Codex 0.144.1 allowlist contains the non-execution helpers
`request_user_input`, `update_plan`, and `view_image`. A model may expose any
subset: Responses Lite models can omit the `tools` field after code mode is
disabled, which is treated as an empty and therefore safe tool set. Prompts
prohibit every tool call, the runner rejects tool/action items in the JSONL
event stream, and non-tool error items fail separately with their diagnostics.

This is prompt-source and tool-surface isolation, not an OS filesystem or
network sandbox around the Codex process. The model has no file or command tool
with which to inspect other content, but the process itself runs under the
selected/inherited Codex sandbox policy. Use a container or other external
sandbox if process-level isolation is required.

## Authentication

The runner never uses `~/.codex/auth.json` directly during an eval. Seed its
independent persistent ChatGPT credential vault once:

```sh
just -- agent-evals-auth-init
```

The default vault is
`$XDG_STATE_HOME/agents-misc/agent-evals/auth.json`, or
`~/.local/state/agents-misc/agent-evals/auth.json` when `XDG_STATE_HOME` is
unset. The source and vault must be current-user-owned regular files without
group or other access. Initialization refuses to overwrite a vault unless
`--replace` is explicit. Runs lock the vault, copy it to the temporary
`CODEX_HOME`, and atomically persist refreshed ChatGPT tokens back to the vault.
Credentials are never written to eval artifacts or stdout.

## Run and inspect

Run the local, unauthenticated isolation preflight first:

```sh
just -- agent-evals-preflight --model gpt-5.4 --reasoning-effort high
```

Run one scenario, a corpus, or the full suite:

```sh
just -- agent-evals --model gpt-5.4 --reasoning-effort high --id routing-existing-vue
just -- agent-evals --model gpt-5.4 --corpus safety --repeat 2
just -- agent-evals --model gpt-5.4
```

The model must be advertised by the pinned Codex binary's bundled catalog, and
the selected reasoning effort must be supported by that model. Use
`--approval-policy` and `--sandbox-mode` to replace selective inheritance; use
`--policy-config` or `--state-dir` to select different explicit sources.

Stdout is one JSON result object. Progress and actionable errors go to stderr.
Exit status `0` means every selected attempt passed, `1` means a preflight,
runtime, route, or behavior failure, and `2` means invalid CLI/input data.
Ignored diagnostic artifacts are written beneath
`tmp/agent/evals/<run-id>/`; each case records redacted events, final structured
output, scores, and stderr. The final suite summary conforms to
`schemas/run-summary.schema.json`.

For a manual smoke test, verify that preflight reports exactly 11 route-stage
skill sources, zero automatic behavior-stage skills, and only tools from the
reviewed allowlist. An empty list is valid for a model that omits tools. Then
run one positive and one near-miss skill case, inspect both stage scores, and
confirm no `auth.json` exists below `tmp/agent/evals`.

## Maintain the corpus

Keep every ID globally unique and lowercase kebab-case. Add or update cases when
rule routing or a skill trigger changes. Each skill needs a positive and a
near-miss negative case, and every behavior record needs meaningful true and
false decisions rather than restating only the route name. Validate structural
changes with:

```sh
just check-agent-rules
```

The schemas in `schemas/` are durable local contracts. Update the checker,
runner, docs, fixtures, and tests atomically when changing them.
