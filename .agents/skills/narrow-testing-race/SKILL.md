---
name: narrow-testing-race
description: Use this when choosing validation for code changes, especially Go changes involving concurrency, handlers, caches, database access, or background workers.
---

# Narrow Testing and Race Validation

## Purpose

Choose the smallest useful validation set and include Go race checks when relevant.

## Workflow

1. Identify behavior changed by the task.
2. Map behavior to tests:
   - pure logic: unit tests;
   - database/filesystem/HTTP/process/FFI: integration tests;
   - API/schema/protocol: contract tests;
   - UI behavior: component/widget tests;
   - browser flow: E2E smoke test.
3. For Go, check whether the change touches concurrency or shared mutable state.
4. If yes, run narrow `go test -race` on affected packages when practical.
5. Do not run broad tests first unless shared contracts or user flows are affected.
6. Do not weaken or delete tests to hide failures.
7. Report environment blockers separately from implementation failures.

## Output

Report:

- tests selected;
- commands run;
- race validation decision;
- failures;
- limitations.
