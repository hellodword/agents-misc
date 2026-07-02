# Code Quality

- Prefer clear, typed, cohesive code over premature abstraction.
- Keep functions small enough to explain by purpose.
- Validate inputs at IO boundaries.
- Use explicit error handling with actionable messages.
- Avoid hidden global state.
- Avoid time, randomness, environment, filesystem, and network access in pure domain logic.
- Inject clocks, random sources, and external clients when behavior must be tested.
- Keep concurrency simple; document ownership, cancellation, and cleanup.
- Prefer idempotent scripts and migrations.
- Do not swallow errors.
- Add comments for invariants, trade-offs, and non-obvious constraints; do not add comments that merely restate code.
- Remove dead code rather than preserving speculative future paths.
