---
id: core.testing
kind: core
triggers:
  - "test"
  - "validation"
  - "regression"
  - "race detector"
  - "coverage"
  - "flaky test"
summary: Map touched boundaries to deterministic validation, attribute failures, and never weaken tests to hide them.
companions:
  skills:
    - id: validation-selection
      when: validation choice is not obvious
---

# Testing Rules

Map the changed boundary to validation:

- pure logic: unit tests;
- database, filesystem, HTTP, process, or FFI: focused integration tests;
- API, config, schema, protocol, CLI, or persisted format: contract tests and examples;
- migration: empty-database migration and previous-schema migration when available;
- generated artifact: generator plus a consuming build or test;
- UI state or interaction: component/widget test;
- browser-specific behavior, a primary browser user flow, or a regression reproducible only in a browser: project-owned E2E.

Use AI visual review only when the user explicitly requests it. Do not use browser E2E merely because a screenshot or generic web page is involved.

Run focused validation first. Broaden to full-repository validation only when the change affects build or CI wiring, a lockfile or dependency graph, a public/shared contract, migration infrastructure, generated infrastructure, or when project rules require the broader command.

Keep tests deterministic with fixed time, seeded randomness, isolated temp directories, no real network outside an explicit integration scope, and no dependence on test order. Reuse existing helpers; keep low-reuse helpers near their tests and make cross-domain helper inputs, permissions, time, locale, auth, and fixture semantics explicit.

Never split, weaken, or delete tests to hide failures. Add a regression test for a bug fix when the changed behavior can be reproduced deterministically.

## Failure attribution

When validation fails, re-run the narrowest failing command, classify the failure as introduced, pre-existing, or environment-caused, and report evidence. Do not fix an unrelated pre-existing failure unless the task requires it.

## Go race validation

Run `go test -race` on affected packages when touched code involves goroutines, channels, mutexes, atomics, shared maps/slices, caches, background workers, HTTP handlers, database connection lifecycle, context cancellation, timers, file watchers, or signal handling. If the environment cannot run the race detector, report the exact limitation and the command that remains.
