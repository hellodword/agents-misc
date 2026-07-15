---
name: browser-e2e
description: Add or run durable, project-owned Playwright tests for browser-specific behavior and primary user flows. Use for trusted local applications when lower-level tests cannot cover the boundary; do not use for ordinary tests, arbitrary external pages, untrusted browsing sessions, or screenshot-only review.
---

# Browser E2E

1. Define the smallest browser-specific user flow and its observable assertions.
2. Use the project's existing browser runner and configuration. For a new Playwright setup, use a project-local locked dependency and the established package manager.
3. Keep durable tests in project-owned files and expose them through a package script.
4. Apply [the system-browser helper](assets/playwright-system-browser.ts) only for a new policy or a project that explicitly adopts it; copy it into project-owned code rather than importing from the skill.
5. Preserve the required search order: `google-chrome`, `chromium`, then `microsoft-edge`.
6. Run headful. Fail clearly when no supported browser or Linux display exists; never download a browser or silently switch to headless.
7. Add `--no-sandbox` only inside a detected container. Add `--disable-dev-shm-usage` only there when `/dev/shm` is below 1 GiB.
8. Use accessibility-first locators and stable behavioral assertions. Do not navigate arbitrary external pages or execute an untrusted browsing session.
9. Preserve configured artifact roots. If none exists, use an ignored `tmp/playwright/` path. Confirm every in-worktree trace, video, screenshot, download, report, and profile root is ignored.
10. Report the durable test, package command, helper/config behavior, actual artifact roots and ignore evidence, command result, and environment blockers.
