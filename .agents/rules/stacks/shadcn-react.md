---
id: stack.shadcn-react
kind: stack
triggers:
  - "shadcn"
  - "React components"
  - "components.json"
  - "Tailwind"
  - "design tokens"
summary: Apply shadcn React defaults for components, registries, tokens, and accessibility.
companions: {}
---

# shadcn React Rules

## Rules

- Use the project's pinned shadcn CLI/version when present. Otherwise, for an explicitly requested greenfield shadcn setup, verify and review the current official stable CLI before selecting a version.
- Inspect `components.json` before adding or modifying shadcn components.
- Use the project's package runner and a reviewed explicit shadcn version for durable project automation:
  - npm: `npx shadcn@<reviewed-version> ...`
  - pnpm: `pnpm dlx shadcn@<reviewed-version> ...`
  - yarn: `yarn dlx shadcn@<reviewed-version> ...`
  - bun: `bunx --bun shadcn@<reviewed-version> ...`
- Replace `<reviewed-version>` with a version already pinned by the project or with an explicit version reviewed for the current change.
- Do not commit durable scripts, docs, or config that depend on a floating shadcn version.
- Prefer `shadcn info --json` for project context.
- Use `shadcn docs <component>` before implementing unfamiliar components.
- Use `--dry-run` before adding or overwriting components when local customizations may exist.
- Do not overwrite customized components without reviewing diffs.
- Use presets only when the user asks or the project is still in design exploration.
- Keep shadcn components as source code owned by the project.
- Prefer accessibility-first composition and semantic HTML.
- Preserve existing i18n libraries. For greenfield React SPA work, use `react-i18next`; for greenfield Next.js work, use `next-intl`.
- Prefer public shadcn registry patterns before inventing new component structure.
- Keep design tokens and Tailwind class patterns consistent with existing components.
