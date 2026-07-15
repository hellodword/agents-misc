# Final Execution Plan

## Goal

State the final outcome in one or two sentences.

## Scope and non-goals

List covered paths, systems, workflows, behavior, and explicit exclusions.

## Evidence and assumptions

Separate observed repository files, commands, contracts, and user constraints from necessary assumptions.

## Target behavior and design

Describe the final behavior, boundaries, contracts, data flow, important trade-offs, and compatibility/recovery handling.

## Files and directories

| Path           | Purpose               |
| -------------- | --------------------- |
| `path/to/file` | Why this path changes |

## Workload and split decision

Choose XS, S, M, L, or XL. Explain whether the work is one semantic step or multiple independently verifiable steps.

## Execution steps

For each step include:

- ID and title
- status (`pending` initially)
- goal
- files or areas
- actions
- exact validation
- acceptance criteria
- commit or staging note

Add an integration checkpoint at natural risk boundaries or after every three normal steps.

## Validation and acceptance

List exact focused and broad commands, manual checks, failure attribution, and final acceptance criteria.

## Commit mode and strategy

State `auto-commit`, `deferred`, or `not-applicable`. For each commit or deferred commit, list exact staging paths, validation, and a Conventional Commit message. Never use bulk staging.

## Recovery, risks, and mitigations

Explain rollback or data recovery where useful and list material risks with their controls.
