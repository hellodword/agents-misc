# Testing and Validation

Choose validation from changed behavior, risk, existing coverage, and project gates. These are useful options, not a requirement to add a new test for every touched file:

| Boundary                                               | Focused validation                       |
| ------------------------------------------------------ | ---------------------------------------- |
| Pure logic                                             | Unit test                                |
| Database, filesystem, HTTP, process, or FFI            | Integration test                         |
| API, config, CLI, protocol, or persisted format        | Contract test and example                |
| Persisted-data transition                              | Required supported data/schema paths     |
| Generated artifact                                     | Generator plus a consuming build or test |
| UI state or interaction                                | Component or widget test                 |
| Browser-specific behavior or a browser-only regression | Project-owned E2E                        |

- Run focused checks first. Broaden when build/CI wiring, dependency graphs, public shared contracts, migration infrastructure, generated infrastructure, or project policy requires it.
- Keep time fixed, randomness seeded, temporary directories isolated, and tests independent of execution order.
- Avoid real network access outside an explicit integration scope. Use small synthetic fixtures, never real user data.
- Add or extend tests when they provide meaningful coverage of observable behavior, a supported contract, or a concrete failure risk. Reuse existing coverage; simple reversible changes can be verified through existing checks or direct inspection.
- Assert meaningful outcomes at the smallest suitable boundary. Avoid tests that merely repeat private implementation steps or check incidental documentation wording, source-code spelling, or implementation shape.
- Do not set an arbitrary coverage percentage. Cover important decisions, contracts, failures, and regressions.
- Attribute failures as introduced, pre-existing, or environment-caused with evidence. Retry the narrow command after a relevant change or to resolve uncertainty, rather than repeating an unchanged failure mechanically.
- For Go changes involving goroutines, channels, mutexes, atomics, shared maps, slices or caches, workers, concurrent HTTP handlers, database connection lifecycle, context cancellation, timers, watchers, or signals, run the affected package or command with `go test -race`. If the race detector is unavailable in the declared environment, report that limitation instead of silently substituting an ordinary test.
- Use browser E2E only for browser-specific behavior, a primary browser flow, or a regression that cannot be covered below that boundary.
- Use AI visual review only when the user explicitly requests it.
