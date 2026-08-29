# Greenfield Defaults

Apply this file only to a new, unconstrained repository or subsystem. Existing authoritative configuration and established local conventions win.

## Repository and toolchain

- Name the initial branch `master`.
- For an ordinary project, use a Nix flake that exposes one `devShells.<system>.default` plus a small `justfile`. Put the tools needed to build, run, test, format, lint, and otherwise develop the project in that shell, and enter it with `nix develop`.
- Run Just directly outside the development shell. Give recipes only stable shared project commands, let them enter the default shell or call an explicitly adopted Nix output, and do not add a `dev` alias or recipe-to-recipe orchestration.
- Add packages, apps, checks, formatter, modules, overlays, or named development shells only when the user requests the interface or applicable project instructions explicitly make Nix a project-wide product interface. Pure Nix products may expose their required outputs and do not require Just.
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
