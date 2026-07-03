---
id: stack.frontend-typescript
kind: stack
triggers:
  - 'TypeScript frontend'
  - 'React'
  - 'Vite'
  - 'Next.js'
  - 'frontend state'
  - 'locales'
---

# Frontend TypeScript Rules
## Applicability

Use these defaults only for new projects, greenfield scaffolding, or when the existing repository has no clear convention.

Do not introduce this stack, package manager, framework, database, toolchain, workflow, or directory structure into an existing project merely because it is preferred here.

Prefer the current local convention when it is coherent and working.

Use these defaults only for new projects, greenfield scaffolding, or repositories with no clear convention.

Do not introduce a preferred frontend framework into an existing project merely because it is listed here.

- Prefer TypeScript for durable frontend work.
- Do not force a specific framework when the current shadcn ecosystem provides a better fit.
- For SPA-style product UI, prefer React + Vite + shadcn/ui.
- For SSR, SEO, App Router, or server-integrated React behavior, prefer Next.js + shadcn/ui.
- Use Vue when the project already uses Vue, the user asks for Vue, or Vue is clearly a better product fit.
- Prefer npm for new projects.
- Prefer English and Simplified Chinese for user-facing UI.
- Model loading, empty, error, permission, unavailable/offline, and responsive states.
- Keep components accessible by default.
- Use project-local package scripts through Nix/Just.
- Commit lockfiles for applications.
- Do not add deployment setup unless explicitly requested.
