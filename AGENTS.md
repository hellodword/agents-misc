# Agent Rules

## Purpose and priority

This file is the shared entrypoint for coding agents. Shared rules and workflows live under `.agents/`. An optional, project-owned `.project-agent/project.md` entrypoint records scoped requirements and locates project context.

The [Agent Rules Kit license notice](.agents/LICENSE.agent-rules-kit) accompanies this shared payload. It does not license the consuming project or independently added rules and skills.

Apply instructions in this order:

1. Platform, system, and developer instructions.
2. Explicit user instructions for the current task, within the safety and authorization boundaries below.
3. Safety and authorization rules in this file.
4. Explicit project requirements within their declared scope.
5. Established local repository conventions in the touched area.
6. Routed shared rules, then generic shared defaults.
7. Greenfield defaults, only when the repository or scoped subproject is new and unconstrained.

Higher priority wins. Within one priority, prefer the narrower scope. Judge repository evidence by authority, relevance, and recency. A declared toolchain, lockfile, formatter config, CI command, or generated schema can be authoritative for the behavior it owns. Resolve conflicting evidence by inspecting the relevant consumer or command; do not require a fixed number of signals or dismiss evidence merely because it is generated.

Distinguish requirements from descriptive facts. A document's location does not make a factual claim authoritative; verify changeable details against their owning configuration or command. An explicit scoped requirement remains a constraint when implementation diverges; investigate the mismatch before treating the requirement as obsolete.

Shared technology preferences apply to new unconstrained choices and projects that already adopted them. Preserve established choices unless the task calls for changing them. Investigate uncertainty before asking the user. Make ordinary reversible implementation choices autonomously; ask when an unresolved choice materially changes the requested outcome, required compatibility, or authorization. Report material assumptions.

## Project context

- Use `.project-agent/project.md` for concise scoped requirements and navigation to existing authoritative sources. No README or fixed documentation layout is required. Prefer links over duplicating versions, dependencies, or commands owned by another source.
- Create or update project context when useful to the task and based on confirmed, durable facts, requirements, decisions, or navigation. Keep temporary progress, investigations, and unconfirmed assumptions in task records. Do not scaffold an entry merely because it is absent.
- Record authorization already established by the user or applicable project policy. Do not edit instructions to grant yourself authorization for an otherwise blocked operation. Ordinary task-scoped documentation maintenance needs no separate approval; the specific authorization and exception sources below still apply.

## Targeted loading

1. Read this file first.
2. If `.project-agent/project.md` exists, read it before changing or relying on project behavior. Otherwise use existing code, configuration, and documentation to establish project context.
3. Follow task-relevant project references through further documents as needed for requirements or evidence. Preserve each requirement's scope and revisit relevant guidance when a new boundary appears. Avoid bulk directory reads, cycles, and rereading unchanged context.
4. At the start of every repository task, confirm that [.agents/rules/index.md](.agents/rules/index.md) has been read. This includes read-only review, planning, documentation, new files, and commit work; reuse unchanged guidance already in context.
5. Load the index routes that match work actually being performed. When a new boundary becomes relevant, load its route then. Avoid rereading unchanged guidance or loading workflows from incidental keywords.
6. Read a skill-local asset or reference only when its `SKILL.md` directly instructs you to do so.

Never infer unseen instructions from a filename or directory name.

## Operating loop

1. Inspect the smallest file set needed to classify the task and establish facts.
2. Read applicable contracts and authoritative configuration before relying on behavior.
3. Make the smallest semantic change that satisfies the request and preserves unrelated work.
4. Select validation for each touched boundary; run focused checks before broader repository checks.
5. Investigate failures as introduced, pre-existing, or environment-caused. Retry after a relevant change or to gather useful evidence; avoid unchanged retries that teach nothing.
6. Review the final diff and repository state.
7. Report changed files, validation results, known limitations, and commit status.

## Safety and authorization

- Never expose or commit secrets, credentials, private keys, real user data, or machine-private state.
- Keep local runtime state and temporary review output out of commits. Synthetic fixtures, visual golden baselines, and required generated sources may be tracked project inputs after checking for sensitive content.
- Never silently overwrite, reset, or destroy real user data. Destructive real-data work requires explicit authorization for that exact operation and a verified recovery path.
- Never weaken, split, skip, or delete tests merely to hide failures.
- Never use global installs, host package managers, curl/wget-to-shell installers, or system-level environment mutation by default.
- Treat a dependency lockfile as tool-generated data when the active project's dependency tooling consumes it to record or pin a resolved dependency, toolchain, input graph, or integrity metadata. This includes `Cargo.lock`, `flake.lock`, `package-lock.json`, and equivalent files.
- Never modify dependency-lockfile content with `apply_patch`, a text editor, search and replace, or an ad hoc script. This prohibition covers versions, checksums, sources, dependency edges, metadata, and merge-conflict hunks.
- Update dependency lockfiles only through the repository's documented command or the project's declared dependency tool.
- If the declared command is unavailable or fails, diagnose or report the blocker instead of editing the lockfile. Manual content editing is allowed only when `.project-agent/project.md`, a contract it directly marks as required, or official documentation published by the dependency-tool maintainer explicitly requires that exact operation for that exact lockfile. Read and verify the exception source before editing; memory, inferred conventions, and a user request alone are not exceptions.
- Never use `git add .`, `git add -A`, `git add --all`, or an equivalent bulk-staging operation. Stage explicit paths only.
- Preserve unrelated user changes. Never reset or overwrite them to simplify the task.

Task authorization includes the ordinary reversible work needed to deliver it: implementation, tests, documentation, project development tools, and necessary adaptations to existing CI. It does not authorize unrelated changes or external effects. Reuse authorization already given; do not ask again for the same operation.

The following require explicit authorization for the specific operation: destructive or irreversible actions; breaking a contract with external or durable consumers or an explicit stability promise; weakening authentication or authorization; deleting or resetting real data; adding a dependency with restrictive or incompatible license obligations; publishing; deploying; pushing; releasing; or creating external resources. New CI workflows, additional Nix product outputs, and third-party GitHub Actions retain the explicit-choice requirements in their routed rules. Authorization applies only to its stated scope.

Do not commit unless the user requests it, the task explicitly enables auto-commit, or a project policy directly enables it. Before any authorized commit, review status and diff, exclude unrelated or ignored files, and stage only explicit task paths.

As a special non-content-staging operation, `git add -N -- <file>` is allowed when a Git-backed Nix flake must see a durable task-scoped untracked file. First verify that the file is not secret, temporary, or ignored; use one exact path, leave intent-to-add in place, and report it. Never add `-f` or widen the path set.

Preserve deliberately tracked `.vscode/**` and `.devcontainer/**` configuration when relevant. Do not share new machine-local editor or container configuration unless the user requests it.

Verify changing external facts through official documentation, registries, lockfiles, package managers, or current local tool output. Do not rely on memory for versions, action majors, package names, advisories, CLI flags, registry metadata, or maintenance status.

## Temporary workspace and retention

- For agent-created scratch, prefer an established ignored workspace scratch directory; otherwise use `tmp/agent/<task-id>/` at the repository root. Reuse the task directory when resuming work and organize subdirectories as needed.
- Reuse effective ignore rules. When none covers the default location, first verify that root `tmp/` is available for scratch work, contains no tracked content, and that the new rule will not obscure existing user files. Then create the task directory and add `/tmp/` to the root `.gitignore` as ordinary authorized setup work.
- Use the system temporary directory when directory usage conflicts, ignore configuration cannot be safely updated, the workspace is not writable, or there is no Git repository.
- When establishing workspace scratch or local runtime output locations, confirm they are untracked and ignored. Reuse that verification until the location or relevant configuration changes. Preserve configured tool output and cache locations.
- Review scratch use after large artifact generation, repeated test runs, phase transitions, and before delivery. Release task-owned intermediates, duplicate copies, temporary installers, and rebuildable runtime environments when their purpose ends. Prefer established project tool outputs or package-manager caches for reusable dependencies; remove task-local download and extraction copies once installation or use is complete.
- For successful and failed tasks alike, retain the minimum set of deliverables, review reports, relevant failure evidence, irreplaceable reproduction inputs, and state still needed for recovery. Reproducibility does not require keeping every build product, dependency copy, or failed run directory. Interrupted work retains what is needed to resume safely.
- Record retained sets of paths with their purpose and release condition in the task record or delivery report; reassess them when resuming work. Do not maintain an inventory of every temporary file. A turn ending or budget exhaustion does not establish that recovery material is disposable.
- Delete only clearly task-owned files whose lifecycle is complete. Preserve active work, other tasks, user files, useful caches, and directories with uncertain ownership or purpose. Being ignored or old does not establish permission to delete them. Tools that produce temporary data should release it at their own confirmed lifecycle boundaries.

## Final report

Summarize:

- files changed and user-visible behavior;
- validation commands and outcomes;
- known limitations or unverified boundaries;
- commit status.

Include recovery handling, generated-output decisions, formatter churn, or other provenance when material to the result. Always disclose exact intent-to-add paths if used.
