# Agent Rules

## Purpose and priority

This file is the shared entrypoint for coding agents. Shared rules and workflows live under `.agents/`; project facts and mandatory product constraints belong in a project overlay.

Apply instructions in this order:

1. Platform, system, and developer instructions.
2. Explicit user instructions for the current task, subject to the absolute safety boundaries below.
3. Absolute safety rules in this file.
4. Project overlay rules within their declared scope.
5. Established local repository conventions in the touched area.
6. Routed shared rules, then generic shared defaults.
7. Greenfield defaults, only when the repository or scoped subproject is new and unconstrained.

Higher priority wins. Within one priority, prefer the narrower scope. One authoritative, machine-enforced declaration can establish a convention, such as a package-manager declaration, lockfile, toolchain file, formatter config, CI config, or project command. Otherwise require two independent, consistent signals from project docs, matching files, neighboring code, or validation commands. Ignore stale, generated, vendored, and unrelated evidence.

Shared language, framework, database, package-manager, locale, test-runner, formatter, and toolchain choices apply only to greenfield work or a project that already adopted them. When evidence cannot decide a public contract, persistent-data behavior, dependency, security policy, or long-term technology choice, ask the user. For a local reversible detail, use the smallest convention-aligned change and report a material assumption.

## Deterministic loading

1. Read this file first.
2. If `.project-agent/project.md` exists, read it before changing or relying on project behavior.
3. Read only links that `project.md` directly marks as required for the current task. Do not recursively scan overlay, contract, or documentation directories.
4. Before changing tracked code, configuration, or data, read [.agents/rules/index.md](.agents/rules/index.md).
5. Match the index routing table once and load every listed rule and skill whose condition is true. Do not recursively discover companions or hidden dependencies.
6. Read a skill-local asset or reference only when its `SKILL.md` directly instructs you to do so.

Never infer unseen instructions from a filename or directory name.

## Operating loop

1. Inspect the smallest file set needed to classify the task and establish facts.
2. Read applicable contracts and authoritative configuration before relying on behavior.
3. Make the smallest semantic change that satisfies the request and preserves unrelated work.
4. Select validation for each touched boundary; run focused checks before broader repository checks.
5. Re-run the narrowest failing command and classify failures as introduced, pre-existing, or environment-caused.
6. Review the final diff and repository state.
7. Report changed files, validation results, known limitations, and commit status.

## Absolute safety

- Never expose or commit secrets, credentials, private keys, real user data, local databases, logs, coverage, browser profiles, or machine-specific files.
- Keep screenshots, traces, captures, databases, backups, and other review/runtime artifacts under confirmed ignored paths.
- Treat external input as untrusted and validate it at its trust boundary.
- Never trust client-provided identity, roles, permissions, ownership, prices, or authorization decisions; enforce them server-side.
- Never interpolate untrusted input into SQL or shell commands. Use parameterized queries, structured process arguments, or reject the operation.
- Prevent path traversal and unsafe uploads with contract-defined validation and containment checks.
- Normalize case, whitespace, Unicode, filenames, or paths only when the applicable contract defines normalization.
- Never silently overwrite, reset, or destroy real user data. Destructive real-data work requires explicit authorization for that exact operation and a verified recovery path.
- Never weaken, split, skip, or delete tests merely to hide failures.
- Never use global installs, host package managers, curl/wget-to-shell installers, or system-level environment mutation by default.
- Never use `git add .`, `git add -A`, `git add --all`, or an equivalent bulk-staging operation. Stage explicit paths only.
- Preserve unrelated user changes. Never reset or overwrite them to simplify the task.

The following require explicit authorization for the specific operation: destructive or irreversible actions; breaking a public API, CLI, config, database, persisted-data, or generated contract; weakening authentication or authorization; deleting or resetting real data; adding a dependency with restrictive or incompatible license obligations; publishing; deploying; pushing; releasing; or creating external resources. Authorization applies only to the named operation.

Do not commit unless the user requests it, the task explicitly enables auto-commit, or a project policy directly enables it. Before any authorized commit, review status and diff, exclude unrelated or ignored files, and stage only explicit task paths.

As a special non-content-staging operation, `git add -N -- <file>` is allowed when a Git-backed Nix flake must see a durable task-scoped untracked file. First verify that the file is not secret, temporary, or ignored; use one exact path, leave intent-to-add in place, and report it. Never add `-f` or widen the path set.

Preserve deliberately tracked `.vscode/**` and `.devcontainer/**` configuration when relevant. Do not share new machine-local editor or container configuration unless the user requests it.

Verify changing external facts through official documentation, registries, lockfiles, package managers, or current local tool output. Do not rely on memory for versions, action majors, package names, advisories, CLI flags, registry metadata, or maintenance status.

### Narrow GitHub-hosted Nix exception

A project-owned workflow may use the `nix-workflow` skill's GitHub-hosted Ubuntu recipes when that workflow explicitly adopts them. On a one-use GitHub-hosted Ubuntu runner only, those recipes may use `apt`, the official Nix installation script, and documented disk preparation. Never apply this exception on a developer machine or self-hosted runner.

## Temporary files

Put one-off scripts and diagnostics in a confirmed ignored `tmp/agent/<task-id>/` directory and clean them up when the task ends. If no ignored project path is confirmed, use the system temporary directory. Promote a script to `scripts/` only when it is durable, reusable project behavior.

## Final report

Report:

- files changed and user-visible behavior;
- validation commands and outcomes;
- known limitations or unverified boundaries;
- commit status.

When relevant, also report the overlay files used, data recovery or migration handling, generated-artifact decisions, formatter churn, and exact intent-to-add or deferred staging paths.
