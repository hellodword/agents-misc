---
id: stack.vue
kind: stack
triggers:
  - "Vue"
  - "vue-i18n"
  - "Vite"
  - "shadcn-vue"
  - "Vue components"
summary: Apply Vue defaults for TypeScript, i18n, Vite, and local component conventions.
companions: {}
---

# Vue Rules

- Use this rule only when the project already uses Vue or the user selects Vue.
- Preserve the existing language mode and i18n library. For greenfield Vue work, use TypeScript and `vue-i18n`.
- If using shadcn-style components in Vue, verify current shadcn-vue docs and project conventions first.
- Keep user-facing strings in locale messages for durable apps.
- For greenfield user-facing UI, create locale messages for:
  - `en`
  - `zh-CN`
- Use Vite for greenfield Vue work unless the product requirements select another runtime.
- Prefer project-local components and design tokens.
