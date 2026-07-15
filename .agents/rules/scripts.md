# Scripts and Command Orchestration

- Use a direct shell command or short shell wrapper for simple, linear invocation with straightforward arguments.
- Use Python when behavior needs structured parsing, complex quoting, directory traversal, retries, cleanup, subprocess orchestration, state transitions, or focused tests.
- Prefer the Python standard library unless a verified project dependency materially simplifies required behavior.
- Pass subprocess arguments as a structured list. Do not construct a shell string from untrusted data.
- Give durable scripts explicit inputs, deterministic outputs, actionable errors, and nonzero failure status.
- Put reusable project behavior under `scripts/`; keep one-off diagnostics under a confirmed ignored temp path and remove them after use.
- Keep Just recipes thin: document the recipe, establish the project environment, and call the durable command or script.
- Move loops, branching, retry logic, cleanup traps, or parsing out of a Just recipe and into a testable script.
