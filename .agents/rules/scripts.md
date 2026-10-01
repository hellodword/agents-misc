# Scripts and Command Orchestration

- Use a shell command or short wrapper for simple linear invocation. Prefer Python for structured parsing, complex quoting, state, or orchestration that benefits from tests; preserve an established project scripting language.
- Use suitable existing libraries or the standard library. Add a dependency when its quality and maintenance benefits justify it.
- Pass subprocess arguments as a structured list when possible, with explicit failure handling.
- Put durable reusable project behavior under `scripts/`.
- Give Just recipes clear names for useful shared commands. Recipe composition and use inside or outside a development shell are allowed. Keep environment entry and dependencies understandable, without cycles or accidental recursive invocation.
- Move logic into a script when complexity, reuse, or focused testing warrants it. There is no fixed command-count limit or mandatory wrapper for a simple command.
