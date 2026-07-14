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
companions: {}
---

# Frontend-only Project Rules

Preserve an existing project's framework, package manager, locale set, state model, and validation entrypoints. The following are greenfield defaults only:

## Greenfield defaults

- TypeScript.
- npm.
- React + Vite + shadcn/ui for SPA-style product UI.
- Next.js + shadcn/ui for SSR, SEO, App Router, or server-integrated React apps.
- Vue only when the user selects Vue; existing Vue projects keep Vue.
- Locale resources for `en` and `zh-CN` in user-facing UI.
- No backend by default.
- No deployment setup by default.

## Design

- Model only states the product can actually enter: user flows, empty, loading, error, permission, unavailable/offline, and responsive behavior as applicable.
- Prefer simple local state before global state.
- Keep API mock/data boundaries clear.
- Use plain HTML only for very small static demos.

## Validation

- Type checks.
- Narrow unit/component tests when behavior is non-trivial.
- Browser E2E only for a primary browser flow, browser-specific behavior, or a browser-only regression.
- AI visual review only when explicitly requested.
