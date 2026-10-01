# Web Frontends

## Greenfield React application

- Use TypeScript strict mode with accessible semantics and project-owned styling tokens. The new-project route owns the initial framework and package-manager choices.
- Produce a static single-page application. Pre-render known SEO or content routes; retain a correct SPA fallback for all other client routes.
- In a Go or Rust monorepo, proxy `/api` during development. In production, let the backend or reverse proxy serve `build/client` with static assets and the correct SPA fallback.
- Use ESLint with React Hooks rules, Prettier defaults, Vitest, and React Testing Library.

## Existing or explicitly selected Vue application

- Preserve the established Vue toolchain and use TypeScript.
- Use `vue-i18n` when localization is required.
- Use Vitest and Vue Test Utils for component behavior.

## Shared UI behavior

- Keep user-visible copy, loading, empty, error, disabled, focus, and responsive states explicit.
- Use semantic HTML, keyboard access, visible focus, labels, and sufficient contrast.
- Apply relevant WCAG 2.2 A/AA success criteria to changed behavior; do not claim whole-product conformance without a full audit.
- Validate untrusted content before rendering. Do not bypass framework escaping without a reviewed sanitizer and contract.
- Prefer component tests. Use project-owned browser E2E only for behavior that depends on a real browser boundary.
