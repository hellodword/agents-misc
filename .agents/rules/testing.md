# Testing and Validation

Map each changed boundary to the smallest durable validation:

| Boundary                                               | Focused validation                                |
| ------------------------------------------------------ | ------------------------------------------------- |
| Pure logic                                             | Unit test                                         |
| Database, filesystem, HTTP, process, or FFI            | Integration test                                  |
| API, config, CLI, protocol, or persisted format        | Contract test and example                         |
| Migration                                              | Empty database plus the previous supported schema |
| Generated artifact                                     | Generator plus a consuming build or test          |
| UI state or interaction                                | Component or widget test                          |
| Browser-specific behavior or a browser-only regression | Project-owned E2E                                 |

- Run focused checks first. Broaden when build/CI wiring, dependency graphs, public shared contracts, migration infrastructure, generated infrastructure, or project policy requires it.
- Keep time fixed, randomness seeded, temporary directories isolated, and tests independent of execution order.
- Avoid real network access outside an explicit integration scope. Use small synthetic fixtures, never real user data.
- Add a deterministic regression test for a fixed bug when its behavior can be reproduced.
- Do not set an arbitrary coverage percentage. Cover important decisions, contracts, failures, and regressions.
- Never weaken, split, skip, or delete tests to hide a failure.
- Re-run the narrowest failing command and attribute the failure as introduced, pre-existing, or environment-caused with evidence.
- For Go changes involving goroutines, channels, mutexes, atomics, shared maps, slices or caches, workers, concurrent HTTP handlers, database connection lifecycle, context cancellation, timers, watchers, or signals, run the affected package or command with `go test -race`. If the race detector is unavailable in the declared environment, report that limitation instead of silently substituting an ordinary test.
- Use browser E2E only for browser-specific behavior, a primary browser flow, or a regression that cannot be covered below that boundary.
- Use AI visual review only when the user explicitly requests it.
