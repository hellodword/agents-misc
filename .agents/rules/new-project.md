# New Project Preferences

Apply only to a new, unconstrained repository or scoped subsystem. Existing project choices take precedence. Use these preferences for decisions the task actually requires.

- Name the initial branch `master`.
- Use a Nix development shell for project tools and Just for useful shared commands; the Nix route owns environment and output policy.
- For a full-stack application, start with one repository and a modular monolith organized by product slice. Introduce boundaries as the product needs them.
- Prefer Go for the backend, React Router Framework Mode with `ssr: false` for the frontend, and npm with its generated lockfile.
- Prefer shadcn/ui when a component library helps.
- Write user-visible UI copy in English unless the product requires another locale.
- Prefer JSON over HTTP with a Markdown interface description; use the contracts route for interface choices and the databases route when selecting persistence.
- Do not invent licensing, deployment, cloud, or telemetry requirements.
