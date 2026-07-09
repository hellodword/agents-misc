---
name: validation-selection
description: Use this when choosing the smallest useful validation for code changes, including unit, integration, contract, UI, browser, migration, generated artifact, and Go race validation.
---

# Validation Selection

## Purpose

Choose the smallest useful validation set for the touched behavior, then escalate only when the change crosses contracts, persistence, security, generated artifacts, browser behavior, or concurrency boundaries.

## Workflow

1. Identify the behavior changed by the task.
2. Identify the touched boundary:
   - pure logic;
   - database, filesystem, HTTP, process, or FFI;
   - API, config, schema, protocol, CLI, or persisted contract;
   - generated artifact or generated consumer;
   - UI component or widget behavior;
   - browser-specific user flow;
   - security, permission, privacy, or data-loss boundary;
   - concurrency or shared mutable state.
3. Map the boundary to the narrowest validation path:
   - pure logic: unit tests;
   - database/filesystem/HTTP/process/FFI: integration tests;
   - API/schema/protocol/CLI/config: contract tests and examples;
   - migrations: empty-database migration and previous-schema migration when available;
   - generated artifacts: generator command plus consumer build/test;
   - UI behavior: component/widget tests where useful;
   - browser flow: E2E smoke test only for browser-visible or browser-specific behavior.
4. Run package-level, file-level, or focused tests before broad repository checks.
5. Escalate to broader validation only when the touched behavior affects shared contracts, public APIs, database migrations, generated artifacts, security boundaries, FFI boundaries, or important user workflows.
6. Do not weaken, split, or delete tests to hide failures.
7. Re-run the narrowest failing command when cheap and useful.
8. Attribute failures as introduced, pre-existing, or environment-caused.
9. Report unrelated failures separately from implementation failures.

## Go race branch

For Go changes, check whether the touched code involves concurrency or shared mutable state.

Run narrow `go test -race` on affected packages when practical if the change touches any of:

- goroutines;
- channels;
- mutexes;
- atomics;
- shared maps or slices;
- caches;
- background workers;
- HTTP handlers;
- database connection lifecycle;
- context cancellation;
- timers;
- file watchers;
- signal handling.

Do not run broad race tests first when they are known to be too slow for the task. Prefer narrow affected packages.

If race validation is skipped, report why.

## Output

Report:

- changed behavior;
- validation selected;
- commands run;
- escalation decision;
- Go race decision when Go is involved;
- failures;
- limitations.
