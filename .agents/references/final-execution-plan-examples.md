# Final Execution Plan Examples

Use these examples only as style references. Do not copy placeholders into final answers.

## Small one-step example

Workload Estimate: S.

Step Splitting Decision: one normal execution step is safe because the change touches one module, has one direct validation command, and does not change public contracts or persistence.

Commit Mode and Commit Strategy: `deferred` unless the user explicitly requested a commit. Report the exact files that would be staged and a proposed Conventional Commit message.

## Multi-step example

Workload Estimate: L.

Step Splitting Decision: split into three normal execution steps because schema work, backend behavior, and frontend UI are independently verifiable and have different failure modes.

Checkpoint: after S3, run integration validation that exercises schema, backend API, and frontend flow together.

Commit Mode and Commit Strategy: `auto-commit` only when explicitly requested or enabled by repository policy. Otherwise, each verified step reports changed files, validation, exact staging paths, and suggested commit message.

## Evidence wording example

Evidence:

- `internal/api/contract.md` defines the API boundary.
- `migrations/` contains durable schema changes.
- `just --list` exposes project validation commands.
- The user requested no deployment changes.

## Assumption wording example

Default Assumptions:

- The existing package manager remains unchanged.
- Existing local project conventions take precedence over greenfield defaults.
- Reversible local implementation details use the smallest convention-aligned choice; persisted data is treated as real unless project evidence proves it disposable.
