# Scripts and Command Orchestration

- Use a direct shell command or short shell wrapper for simple, linear invocation with straightforward arguments.
- Use Python when behavior needs structured parsing, complex quoting, directory traversal, retries, cleanup, subprocess orchestration, state transitions, or focused tests.
- Prefer the Python standard library unless a verified project dependency materially simplifies required behavior.
- Pass subprocess arguments as a structured list. Do not construct a shell string from untrusted data.
- Give durable scripts explicit inputs, deterministic outputs, actionable errors, and nonzero failure status.
- Put reusable project behavior under `scripts/`; keep one-off diagnostics under a confirmed ignored temp path and remove them after use.
- Give a public Just recipe only to a stable project-level command that multiple contributors will run repeatedly and whose environment, arguments, or project semantics benefit from one shared entrypoint. Do not create recipes for one-off diagnostics, CI-internal steps, subsystem-private implementation, or obvious direct commands.
- Keep each recipe documented and limited to at most three simple linear command invocations. Fixed arguments, fixed environment values, and transparent argument forwarding are allowed.
- Do not put loops, branching, parsing, retries, traps, cleanup, complex quoting, or stateful orchestration in a recipe. Move durable complex behavior into a testable script without assuming that the script also needs a recipe.
- Do not create a `dev` recipe that only runs `nix develop`, call or depend on another recipe, build private helper-recipe layers, or invoke Just through `nix develop`.
