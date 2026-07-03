# Final Execution Plan

## Goal

State the final outcome in one or two sentences.

## Scope

List the files, directories, systems, workflows, or user-visible behavior covered by the plan.

## Non-goals

List what the plan intentionally does not do.

## Default Assumptions

State safe assumptions required to make the plan executable.

## Evidence

List concrete evidence used to justify the plan:

- repository files or directories inspected;
- commands or outputs observed;
- user constraints;
- relevant contracts, schemas, APIs, or migration files.

## Target Behavior

Describe the expected final behavior.

## Design

Describe the implementation design, boundaries, contracts, data flow, and important trade-offs.

## Files and Directories

| Path | Purpose |
| --- | --- |
| `path/to/file` | Why this file is involved |

## Workload Estimate

Use one bucket: XS, S, M, L, or XL.

Explain the estimate briefly.

## Step Splitting Decision

State whether the work is one normal execution step or multiple normal execution steps.

Explain why the split is safe and useful.

## Execution Steps

### S1 — Step title

- Status: `pending`
- Goal:
- Files or areas:
- Actions:
- Validation:
- Acceptance criteria:
- Commit or staging note:

### S2 — Step title

- Status: `pending`
- Goal:
- Files or areas:
- Actions:
- Validation:
- Acceptance criteria:
- Commit or staging note:

## Checkpoints

For every three normal execution steps, or at natural risk boundaries, define checkpoint acceptance criteria.

## Validation Plan

List exact validation commands or manual checks.

## Acceptance Criteria

List final acceptance criteria for the whole plan.

## Commit Mode and Commit Strategy

State one of:

- `auto-commit`: commits are explicitly requested or enabled by repository policy;
- `deferred`: commits are not allowed, not requested, or unsafe due to repository state;
- `not-applicable`: the plan does not involve repository changes.

For each commit or deferred commit, provide:

- exact files to stage;
- Conventional Commit message;
- validation performed.

## Rollback or Recovery

Describe how to revert, recover data, or return to a known-safe state when useful.

## Risks and Mitigations

List the main risks and how the plan controls them.
