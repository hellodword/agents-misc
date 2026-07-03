---
id: project-type.fullstack-go-web
kind: project-type
triggers:
  - 'full-stack'
  - 'Go backend'
  - 'TypeScript frontend'
  - 'SQLite web app'
  - 'API frontend'
---

# Full-stack Go + Web Project Rules

## Applicability

Use these defaults only for new full-stack projects, greenfield scaffolding, or repositories without clear existing backend/frontend conventions. Do not introduce these stacks into an existing project merely because they are preferred here.

## Default stack

- Backend: Go.
- Frontend: TypeScript.
- Frontend framework:
  - React + Vite + shadcn/ui for SPA-style product UI;
  - Next.js + shadcn/ui for SSR, SEO, App Router, or server-integrated React apps;
  - Vue only when the project already uses Vue, the user asks for Vue, or Vue is clearly a better product fit.
- Frontend package manager for new projects: npm.
- UI locales: English and Simplified Chinese.
- Persistence: SQLite.
- API docs: Markdown contract first.
- Project-developed application config files: YAML when format is optional.
- Toolchain: Nix + Just.

## Architecture

- Prefer one repository.
- Prefer modular monolith.
- Keep frontend, backend, database, and contract boundaries explicit.
- Define API contract before implementing cross-boundary behavior.
- Keep handlers thin and move business logic into application/domain layers.
- Use narrow vertical slices.

## Validation

- Backend unit tests for domain logic.
- Backend integration tests for database/API boundaries.
- Go race validation for concurrency-sensitive changes.
- Frontend component tests where useful.
- Browser E2E only for critical user flows or browser-specific behavior.
- AI visual review only when explicitly requested.
