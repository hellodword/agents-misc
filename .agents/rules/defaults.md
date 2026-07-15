# Greenfield Defaults

Apply this file only to a new, unconstrained repository or subsystem. Existing authoritative configuration and established local conventions win.

## Repository and toolchain

- Name the initial branch `master`.
- Use a Nix flake plus a thin `justfile` for ordinary projects. Pure Nix projects do not require Just.
- Use treefmt-nix in every Nix project and expose it through `nix fmt` and flake checks.
- Do not choose or create project licensing, deployment, cloud, or telemetry behavior without a requirement.

## Full-stack default

- Use one repository and a modular monolith organized by vertical product slice.
- Use Go for the backend.
- Use React Router Framework Mode with `ssr: false` for the web frontend.
- Use npm and commit its lockfile.
- Use shadcn/ui components when a component library is helpful.
- Use SQLite when the product fits a local or single-service relational database.
- Write user-visible UI copy in English unless the product requires another locale.
- Use JSON over HTTP and document the contract in Markdown.

These are starting points, not reasons to rewrite an existing stack. Ask the user when a choice materially changes a public contract, durable data, dependency set, security posture, or long-term architecture and repository evidence does not decide it.
