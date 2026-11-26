# Testing and Validation

Select validation proportionate to changed behavior, failure risk, and project gates:

| Boundary                                       | Focused validation                       |
| ---------------------------------------------- | ---------------------------------------- |
| Pure logic or state machine                    | Unit test                                |
| Database, filesystem, HTTP, process, or FFI    | Integration test                         |
| API, config, CLI, protocol, or persisted state | Contract test                            |
| Generated artifact                             | Generator plus a consuming build or test |
| UI state or interaction                        | Component or widget test                 |
| Real-browser behavior or regression            | Project-owned browser E2E                |

- Run focused checks first; widen to integration or full repository gates when shared contracts, build/CI wiring, dependency graphs, or project policy require it.
- Keep time deterministic, randomness seeded, temporary directories isolated, and tests offline outside explicit integration scopes.
- Test observable contracts, state transitions, error boundaries, and cross-layer invariants rather than private assembly steps, source wording, or echoed literal constants. When refactoring, consolidate fragmented regression checks into cohesive contract tests rather than accumulating narrow single-literal tests.
- For boundaries governed by payload size, framing, encoding density, pagination, MTU, or QR/barcode capacity, test with synthetic payloads at realistic upper-bound dimensions rather than toy strings.
- For Go code touching goroutines, channels, mutexes, atomics, shared maps/slices, concurrent handlers, timers, signals, or connection pools, run `go test -race` on the affected packages (reporting environment limits if the race detector is unavailable).
- For C/C++, unsafe Rust, FFI ABI boundaries, or untrusted binary/media parsers, run configured sanitizers (such as ASan/UBSan) and bounded fuzz or corpus checks when available.
- Verify layout geometry, theme contrast, and dense visual rendering in an available real viewport when UI presentation changes; run the `ai-visual-review` workflow only when explicitly requested by the user.
