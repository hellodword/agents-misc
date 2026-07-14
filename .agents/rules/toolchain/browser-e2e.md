---
id: toolchain.browser-e2e
kind: toolchain
triggers:
  - "browser E2E"
  - "Playwright"
  - "Playwright E2E"
  - "browser regression"
summary: Keep meaningful Playwright E2E tests project-owned, locked, system-browser-based, and explicit about environment failures.
companions:
  conditional_rules:
    - id: core.testing
      when: durable browser regression coverage is added or changed
    - id: core.environment
      when: browser, display, container, PATH, or shared-memory capability blocks execution
  skills:
    - id: browser-e2e
      when: choosing or implementing browser validation
  references:
    - id: playwright-system-browser.ts
      when: a project needs a Playwright system-browser selection helper
---

# Browser E2E Rules

## Project ownership

Meaningful browser validation belongs in durable project files such as `e2e/*.spec.ts` and a package script. Do not use agent-only browser exploration or temporary agent-authored Playwright programs as the substitute for regression-worthy E2E coverage.

Preserve an existing project's browser runner, Playwright configuration, browser policy, package manager, and lockfile. The system-browser policy below applies when adding Playwright to a greenfield project or when the project has explicitly adopted this policy.

Use the project's locked, local `@playwright/test` dev dependency. Greenfield projects use npm. Do not use a global Playwright installation, a floating `npx` download, or `playwright install`.

## System browser selection

Browser selection is implemented by the project's Playwright configuration or helper, never by agent probing. Search `PATH` in exactly this order:

1. `google-chrome`
2. `chromium`
3. `microsoft-edge`

Use the first executable as Playwright's `executablePath`. The reference `.agents/references/playwright-system-browser.ts` is copyable guidance; projects own their copied helper and do not import runtime code from `.agents/`.

Do not install a browser automatically. A missing browser must produce a clear nonzero failure.

## Launch behavior

Default to headful execution. If a Linux display is unavailable, fail clearly; do not silently fall back to headless. A project may deliberately define a separate headless workflow when its contract requires one.

When launching inside a container or devcontainer, always add `--no-sandbox`; this kit knowingly accepts that risk. If container `/dev/shm` is smaller than 1 GiB, also add `--disable-dev-shm-usage`.

## Artifacts

Preserve every artifact root already configured by the project's Playwright configuration, scripts, or documented convention. If the project has no configured artifact root, use `tmp/playwright/`.

For each selected root inside the worktree, confirm that Git ignore rules cover it. An existing broader ignore rule is sufficient. If an existing configured root is not ignored, add the narrowest ignore entry for that root instead of silently moving the output. A configured root outside the worktree needs no ignore entry; report that boundary explicitly.

Keep profiles, traces, screenshots, videos, and downloads under the selected roots. Do not commit them as ordinary artifacts. Use accessibility-first locators and assert observable behavior rather than implementation details.
