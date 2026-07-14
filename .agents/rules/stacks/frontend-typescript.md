---
id: stack.frontend-typescript
kind: stack
triggers:
  - "TypeScript frontend"
  - "React"
  - "Vite"
  - "Next.js"
  - "frontend state"
  - "locales"
summary: Apply TypeScript frontend defaults for framework choice, state, i18n, and validation.
companions: {}
---

# Frontend TypeScript Rules

Preserve an existing project's language mode, framework, package manager, locale set, and command workflow.

For greenfield work:

- use TypeScript for durable frontend work;
- use React + Vite + shadcn/ui for an SPA-style product UI;
- use Next.js + shadcn/ui for SSR, SEO, App Router, or server-integrated React behavior;
- use Vue only when the user selects it;
- use npm;
- structure locales from the start with `en` and `zh-CN` when the UI is user-facing.

Model loading, empty, error, permission, unavailable/offline, and responsive states only when the product can enter them.

- Keep components accessible by default.
- Use the project's established command entrypoints. In a project that already adopts Nix/Just, run package scripts through that workflow.
- Commit lockfiles for applications.
- Do not add deployment setup unless explicitly requested.
