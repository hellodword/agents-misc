---
id: stack.vue
kind: stack
triggers:
  - 'Vue'
  - 'vue-i18n'
  - 'Vite'
  - 'shadcn-vue'
  - 'Vue components'
---

# Vue Rules
## Applicability

Use these defaults only for new projects, greenfield scaffolding, or when the existing repository has no clear convention.

Do not introduce this stack, package manager, framework, database, toolchain, workflow, or directory structure into an existing project merely because it is preferred here.

Prefer the current local convention when it is coherent and working.

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
