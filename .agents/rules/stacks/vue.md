# Vue Rules

- Use Vue when the project already uses Vue, the user asks for Vue, or Vue is clearly a better product fit.
- Use TypeScript.
- Use `vue-i18n` by default unless the project already uses a different i18n approach.
- If using shadcn-style components in Vue, verify current shadcn-vue docs and project conventions first.
- Keep user-facing strings in locale messages for durable apps.
- Default locales:
  - `en`
  - `zh-CN`
- Use Vite for new Vue projects unless project requirements point elsewhere.
- Prefer project-local components and design tokens.
