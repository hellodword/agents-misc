---
id: project-type.fullstack-go-web
kind: project-type
triggers:
  - "full-stack"
  - "Go backend"
  - "TypeScript frontend"
  - "SQLite web app"
  - "API frontend"
summary: Apply defaults for full-stack Go backend and TypeScript frontend products.
companions: {}
---

# Full-stack Go + Web Project Rules

Preserve an existing project's stack, package manager, locale set, persistence, config format, and command workflow. The following are greenfield defaults only:

## Greenfield defaults

- Backend: Go.
- Frontend: TypeScript.
- Frontend framework:
  - React + Vite + shadcn/ui for SPA-style product UI;
  - Next.js + shadcn/ui for SSR, SEO, App Router, or server-integrated React apps;
  - Vue when the user selects it.
- Frontend package manager for new projects: npm.
- UI locale resources: `en` and `zh-CN`.
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
- Frontend component tests for changed UI state or interaction behavior.
- Browser E2E only for a primary browser flow, browser-specific behavior, or a browser-only regression.
- AI visual review only when explicitly requested.
