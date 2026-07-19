# Agent Rules Kit

Agent Rules Kit is a small, project-scoped instruction payload for coding agents. It uses the open [AGENTS.md format](https://agents.md/) for shared instructions and the open [Agent Skills specification](https://agentskills.io/specification) for on-demand workflows.

## Distribution boundary

Only [AGENTS.md](AGENTS.md) and `.agents/**` are distributed. Consuming repositories provide both at their root and normally mount or copy them read-only. The external distribution system chooses and records a Git commit, version, or tag; this repository does not maintain a payload version or consumer lock.

Repository maintenance files such as `.project-agent/**`, the checker, tests, Just recipes, and Nix checks are not part of the payload. The unrelated `codex/**`, `tools/**`, and `.github/**` trees are outside rules-kit maintenance.

## Loading model

A consumer has one fixed overlay entrypoint: `.project-agent/project.md`. That file links requirements directly and states when they apply:

```md
# Project instructions

| When                       | Read                                  |
| -------------------------- | ------------------------------------- |
| Any tracked product change | [Mandatory rules](rules/mandatory.md) |
| Public API work            | [HTTP contract](../contracts/http.md) |
```

Agents read the project entrypoint, follow only the directly applicable links, then route shared guidance through [.agents/rules/index.md](.agents/rules/index.md). There is no recursive metadata or hidden dependency traversal.

## What belongs where

- A **rule** is always-applicable guidance for a recognizable technical boundary. Rules are plain Markdown with one H1 and no YAML frontmatter.
- A **skill** is a procedural workflow worth loading only for a particular task. Each `SKILL.md` uses only the required `name` and `description` frontmatter.
- A skill-local **asset** is a template, schema, or reusable source file consumed by that workflow.
- A skill-local **reference** is longer optional guidance that the workflow tells the agent when to read.

Keep supporting files beside their owning skill and link them directly from `SKILL.md`. Do not create global template or reference catalogs.

## Shared rules

The 19 flat rules are:

- `index.md`, `defaults.md`, `architecture.md`, `environment.md`, `scripts.md`
- `security.md`, `contracts.md`, `data.md`, `dependencies.md`, `testing.md`
- `formatting.md`, `generated-artifacts.md`, `cli.md`, `go.md`, `rust.md`
- `web.md`, `flutter-rust.md`, `nix.md`, `github-actions.md`

## Workflow skills

The 11 skills are:

- `ai-visual-review`
- `atomic-commit`
- `browser-e2e`
- `compatibility-review`
- `converge-codebase`
- `environment-troubleshooting`
- `generated-artifacts-review`
- `nix-workflow`
- `pure-patch-workflow`
- `sqlite-migration-backup`
- `standalone-final-execution-plan`

## Maintenance and validation

Read [.project-agent/project.md](.project-agent/project.md) before modifying the kit. The required checks are:

```sh
nix fmt
nix develop .#dev --command python3 scripts/check-agent-rules.py --root .
nix develop .#dev --command python3 -m unittest discover -s tests -p 'test_*.py'
nix build --no-link .#checks.x86_64-linux.agent-rules
nix flake check
git status --short --ignored
```

The deterministic checker validates structure, links, skill ownership, JSON Schemas, and the scenario-eval corpus. The eval cases are repository fixtures for human or fresh-agent regression; CI validates their structure and coverage without using an LLM as a gate. Maintenance checks require no new task-created temporary artifacts; pre-existing ignored work is preserved and disclosed rather than deleted incidentally.
