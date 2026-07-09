---
id: project-type.frontend-only
kind: project-type
triggers:
  - "frontend-only"
  - "SPA"
  - "Next.js"
  - "React"
  - "Vue"
  - "browser app"
summary: Apply defaults for standalone frontend applications and browser UI behavior.
load_with: []
---

# Frontend-only Project Rules

## Applicability

Use these defaults only for new projects, greenfield scaffolding, or repositories without a clear existing convention.

Prefer coherent local conventions.

## Default stack

- TypeScript.
- npm.
- React + Vite + shadcn/ui for SPA-style product UI.
- Next.js + shadcn/ui for SSR, SEO, App Router, or server-integrated React apps.
- Vue only when the project already uses Vue, the user asks for Vue, or Vue is clearly a better product fit.
- English and Simplified Chinese UI.
- No backend by default.
- No deployment setup by default.

## Design

- Model user flows, empty states, loading states, error states, permission states, unavailable/offline states, and responsive layouts.
- Prefer simple local state before global state.
- Keep API mock/data boundaries clear.
- Use plain HTML only for very small static demos.

## Validation

- Type checks.
- Narrow unit/component tests when behavior is non-trivial.
- Browser E2E only for critical flows.
- AI visual review only when explicitly requested.
