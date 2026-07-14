# Agent Rules

## Purpose and distribution

This is the shared entrypoint for coding agents. Detailed reusable rules live under `.agents/`; project facts and mandatory product constraints belong in `.project-agent/**`.

`AGENTS.md` and `.agents/**` are a centrally maintained, read-only rules payload in consuming projects. Do not edit them there. Extend or override shared defaults through the project overlay. Only an overlay that explicitly identifies its repository as the upstream Agent Rules Kit maintenance source may authorize shared-payload maintenance.

## Priority

Apply instructions in this order:

1. Platform, system, and developer instructions.
2. Explicit user instructions for the current task, subject to the absolute boundaries below.
3. Absolute safety rules in this file.
4. Project overlay rules within their declared scope.
5. Established local repository conventions in the touched area.
6. Routed shared rules, then generic shared defaults.
7. Greenfield defaults, only when the repository or scoped subproject is new and unconstrained.

Higher priority always wins. Within the same priority, prefer the rule with the narrower declared scope. If equally scoped rules remain incompatible and the choice affects public behavior, persistent data, dependencies, or a long-term technology decision, ask the user. Never infer unseen rule contents from a path or name.

## Local convention and decision evidence

A single authoritative, machine-enforced declaration can establish a convention, such as a package-manager declaration, lockfile, toolchain file, formatter config, CI config, or project command. Without an authoritative declaration, require two independent consistent signals from project docs, matching files, neighboring code, or validation commands. Ignore stale, generated, vendored, and unrelated evidence.

Shared choices of language, framework, database, package manager, locale, test runner, formatter, or toolchain apply only to greenfield work or when the project already adopted that choice. Rules governing safe use of an already-selected tool still apply.

When evidence cannot decide an important product or technology choice, ask the user. For a local, reversible implementation detail that does not alter a contract, data, dependency set, or long-term stack, use the smallest change consistent with local conventions and report the assumption when material.

## Operating loop

1. Inspect the smallest file set needed to classify the task.
2. Discover project overlay entrypoints by listing expected paths only.
3. Select and read the smallest relevant project and shared rule set.
4. Read routed contracts, architecture documents, workflows, templates, or references before relying on them.
5. Make the smallest semantic change that satisfies the request.
6. Run validation selected for the touched boundary.
7. Report changed files, validation, limitations, and commit status.

## Overlay discovery

Read this file first. Do not preload `.agents/**`, `.project-agent/**`, `contracts/**`, or `docs/**`.

Discover these entrypoints without recursively reading their directories:

- `.project-agent/project.md`
- `.project-agent/route-map.md`
- `.project-agent/rules/mandatory.md`
- `.project-agent/shared-rules.lock`
- `contracts/**`, `docs/architecture/**`, and `docs/adr/**`

Load in this order when applicable:

1. `.project-agent/project.md` before changing or relying on project behavior.
2. `.project-agent/rules/mandatory.md` before tracked product code or config changes.
3. `.project-agent/route-map.md` before shared routing.
4. When `.agents/manifest.json` exists, inspect `.project-agent/shared-rules.lock`: report its exact path when missing; otherwise validate and compare it with the manifest.
5. Load only contracts and architecture documents selected by the project route or touched behavior.

If the lock is missing, malformed, incomplete, or mismatched, report the exact path, fields, and values that apply and continue safe work; never create or rewrite it automatically.

## Shared routing and companions

Use `.agents/rules/route-map.md` when shared routing is needed. Frontmatter triggers are search hints, not sufficient routing decisions.

After selecting a rule:

- load every `required_rules` entry before acting, exactly one hop deep;
- load `conditional_rules` only when its `when` condition is true;
- load a skill only when its condition is true and the task needs that workflow;
- load a template only when producing that artifact;
- load a reference only when its longer detail is needed;
- never recursively follow companions of a companion;
- keep a visited set and never load the same item twice.

Required rules cannot be skipped to satisfy a loading budget. Otherwise prefer zero or one project-type rule, zero to two stack rules, one to three core rules, and zero to two toolchain rules. Load only rules for the touched area, not every technology present in a repository.

## Absolute safety

- Never expose or commit secrets, credentials, private keys, real user data, local databases, logs, coverage, browser profiles, or machine-specific files.
- Screenshots, traces, and captures created for review are temporary artifacts and stay under confirmed ignored paths.
- Never trust client-provided identity, roles, permissions, ownership, prices, or authorization decisions.
- Never interpolate untrusted input into SQL or shell commands; use parameterized queries, structured argument APIs, or reject the operation.
- Never silently overwrite, reset, or destroy real user data. A destructive operation requires explicit authorization for that operation and a verified recovery path.
- Never weaken, split, or delete tests merely to hide failures.
- Never use global installs, host package managers, curl/wget-to-shell installers, or system-level environment mutation.
- Never use `git add .`, `git add -A`, `git add --all`, or equivalent bulk staging.

External input must be validated at its trust boundary. Normalize case, whitespace, Unicode, filenames, or paths only when the applicable contract defines that normalization.

The following require explicit authorization for the specific operation: destructive or irreversible actions, breaking a public API/CLI/config/database or persisted-data contract, weakening authentication or authorization, deleting or resetting real data, adding a dependency with restrictive or incompatible license obligations, publishing, deploying, pushing, releasing, or creating external resources. A specific authorization applies only to the named operation; a routed broad mode may require a stricter protocol.

Commits require an explicit user request, task-level auto-commit instruction, or repository auto-commit policy. Preserve unrelated work and use narrow staging.

As a special non-content-staging operation, `git add -N -- <file>` is allowed for a durable, task-scoped file that a Git-backed Nix flake must see. First verify the file is not secret, temporary, or ignored; leave intent-to-add in place and report it. Never add `-f` or widen the path set.

Read tracked `.vscode/**` and `.devcontainer/**` configuration when relevant and preserve deliberately shared configuration. New or untracked machine-local editor/container configuration remains ignored unless the user explicitly requests sharing it.

## External and time-sensitive facts

Verify changing facts through official documentation, package managers, registries, repository lockfiles, or current local tool output. Do not rely on memory for dependency versions, action versions, package names, advisories, CLI flags, registry metadata, or maintenance status.

## Final report

After changes, report files changed, validation and result, known limitations, and commit status. When applicable, also report overlay files used, lock mismatch, exact intent-to-add or deferred staging paths, a suggested Conventional Commit message, and a material rule-loading budget overrun.
