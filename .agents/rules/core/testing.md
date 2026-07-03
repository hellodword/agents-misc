---
id: core.testing
kind: core
triggers:
  - 'test'
  - 'validation'
  - 'regression'
  - 'race detector'
  - 'coverage'
  - 'flaky test'
---

# Testing Rules

- Prefer narrow tests for the current change.
- Add regression tests for bug fixes.
- Test behavior boundaries, not private implementation details.
- Use unit tests for pure logic.
- Use integration tests for database, filesystem, HTTP, process, and FFI boundaries.
- Use contract/schema tests for protocols and APIs.
- Use component/widget tests for UI behavior where useful.
- Use browser E2E only for critical user flows or browser-specific behavior.
- Use AI visual review only when explicitly requested by the user.
- Keep tests deterministic:
  - fixed time;
  - seeded randomness;
  - isolated temp dirs;
  - no real network unless explicitly integration-scoped;
  - no dependency on test order.
- Do not split, weaken, or delete tests to hide failures.
- Reuse existing helpers before adding new helpers.
- Keep low-reuse helpers near their tests.
- Cross-domain helpers must expose clear inputs/outputs and must not hide fixture, permission, time, locale, or auth semantics.

## Go race validation

For Go changes involving any of the following, include `go test -race` on the narrowest relevant package set when practical:

- goroutines;
- channels;
- mutexes;
- atomics;
- shared maps/slices;
- caches;
- background workers;
- HTTP handlers;
- database connection lifecycle;
- context cancellation;
- timers;
- file watchers;
- signal handling.

Do not run broad race tests when they are known to be too slow for the task. Prefer narrow packages first.

If race validation is skipped, report why.
