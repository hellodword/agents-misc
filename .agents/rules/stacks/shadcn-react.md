# shadcn React Rules

- Use the official shadcn skill when installed.
- Inspect `components.json` before adding or modifying shadcn components.
- Use the project's package runner:
  - npm: `npx shadcn@latest ...`
  - pnpm: `pnpm dlx shadcn@latest ...`
  - yarn: `yarn dlx shadcn@latest ...`
  - bun: `bunx --bun shadcn@latest ...`
- Prefer `shadcn info --json` for project context.
- Use `shadcn docs <component>` before implementing unfamiliar components.
- Use `--dry-run` before adding or overwriting components when local customizations may exist.
- Do not overwrite customized components without reviewing diffs.
- Use presets only when the user asks or the project is still in design exploration.
- Keep shadcn components as source code owned by the project.
- Prefer accessibility-first composition and semantic HTML.
- For React SPA i18n, prefer `react-i18next`.
- For Next.js i18n, prefer `next-intl`.
- Prefer public shadcn registry patterns before inventing new component structure.
- Keep design tokens and Tailwind class patterns consistent with existing components.
