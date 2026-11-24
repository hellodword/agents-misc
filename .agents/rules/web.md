# Browser Web Frontends

Apply to browser web applications and static web sites; native mobile, React Native, and desktop host shells follow their own platform conventions.

## Greenfield browser React SPA

- Use TypeScript strict mode, accessible HTML semantics, and project-owned styling tokens (initial framework and package-manager defaults live in [New project](new-project.md)).
- Build a static SPA, pre-rendering known SEO/content routes while serving a SPA fallback for client routes; proxy `/api` in monorepo development and serve `build/client` in production.
- Default to ESLint with React Hooks rules, Prettier defaults, Vitest, and React Testing Library.

## Existing or selected Vue web application

- Preserve the project's Vue toolchain with TypeScript, `vue-i18n` when localized, and Vitest + Vue Test Utils for component behavior.

## Shared browser UI behavior

- Make loading, empty, error, disabled, keyboard-focus, and responsive states explicit.
- Apply relevant WCAG 2.2 A/AA criteria (semantic landmarks, labels, keyboard navigation, contrast) to changed views without claiming whole-product conformance unverified.
- Escape or sanitize untrusted content under an explicit contract; prefer component tests and reserve browser E2E for real-browser boundaries.
