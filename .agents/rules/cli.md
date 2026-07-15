# Command-Line Interfaces

## Language choice for greenfield CLIs

- Use Go when no constraint selects a language.
- Use Rust when the CLI shares a Rust core or requires systems-level behavior.
- Use Python for Python ecosystems, automation, or data-science workflows.
- Use Node with TypeScript for frontend, npm, or browser-tooling workflows.

Preserve an existing project's language and command framework.

## Framework defaults

- Go: use `flag` for one command; use Cobra when subcommands or shell completion are required.
- Rust: use clap derive.
- Python: use `argparse` and the project's uv environment.
- Node: use TypeScript, `tsc --noEmit`, an esbuild Node bundle, npm, and its lockfile.

## Contract

- Define command name, subcommands, positional arguments, flags, defaults, stdin, stdout, stderr, and exit status.
- For greenfield behavior, start with flags and arguments. Do not create config files or environment overrides without a requirement.
- Put the primary result on stdout and human diagnostics on stderr. Keep structured output free of progress text.
- Return 0 for success, 2 for usage or input errors, and 1 for runtime failure.
- Preserve scriptable output and existing precedence. Treat renames, defaults, output, exit codes, config, and environment behavior as compatibility-sensitive.
- Validate parsing, representative output, stderr, exit status, and precedence when those behaviors change.
