---
name: browser-e2e
description: Use this when durable Playwright browser validation or project-owned E2E tests are needed. Do not use for ordinary non-browser tests.
---

# Browser E2E Workflow

## Purpose

Implement the smallest meaningful browser regression flow using the project's established browser runner. Apply the shared system-browser policy only for a greenfield Playwright setup or a project that explicitly adopts it.

## Workflow

1. Define the user flow and expected observable behavior.
2. Keep regression-worthy tests in project files such as `e2e/*.spec.ts` and expose them through a package script.
3. Preserve existing runner/config/browser behavior. For a new Playwright setup, use a project-local locked `@playwright/test` dev dependency and npm.
4. Under the shared system-browser policy, put selection in project Playwright configuration or a copied project helper following `.agents/references/playwright-system-browser.ts`. The agent does not probe browsers.
5. Search order must be `google-chrome`, `chromium`, then `microsoft-edge`.
6. Default to headful. Fail clearly when no browser or display is available; never install a browser or silently switch to headless.
7. Inside a container, add `--no-sandbox`; if `/dev/shm` is below 1 GiB, also add `--disable-dev-shm-usage`.
8. Prefer accessibility-first locators and stable behavioral assertions.
9. Preserve every project-configured artifact root. If none exists, use `tmp/playwright/`.
10. For every selected root inside the worktree, confirm that Git ignore rules cover it; an existing broader rule is sufficient. Add the narrowest ignore entry when an existing configured root is not ignored. Report an outside-worktree root instead of adding an ignore entry.
11. Store traces, videos, screenshots, downloads, and profiles only under the selected roots.

## Validation report

Report the durable test and package script, selected project-owned helper/config behavior, command and result, every actual artifact root and how its ignore or outside-worktree status was verified, and environment blockers.
