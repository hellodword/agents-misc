# Frontend TypeScript Rules

- TypeScript is the default for durable frontend projects.
- Do not force a specific framework when the current shadcn ecosystem provides a better fit.
- For SPA-style product UI, prefer React + Vite + shadcn/ui.
- For SSR, SEO, App Router, or server-integrated React behavior, prefer Next.js + shadcn/ui.
- Consider TanStack Start only when its routing/data model clearly fits the product.
- For existing Vue projects, keep Vue and use `vue-i18n` by default.
- Default package manager for new frontend projects: npm.
- Keep user-facing strings localizable.
- Default locales:
  - `en`
  - `zh-CN`
- Prefer project-local components and design tokens.
- Model loading, empty, error, disabled, permission-denied, offline/unavailable, and validation states.
- Use plain HTML only for very small static demos.
