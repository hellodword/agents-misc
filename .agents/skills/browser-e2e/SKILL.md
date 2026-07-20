---
name: browser-e2e
description: Add or run durable, project-owned Playwright tests for browser-specific behavior and primary user flows. Use for trusted local applications when lower-level tests cannot cover the boundary; do not use for ordinary tests, arbitrary external pages, untrusted browsing sessions, or screenshot-only review.
---

# Browser E2E

1. Define the smallest browser-specific user flow and its observable assertions.
2. Use the project's existing browser runner and configuration, including an explicitly configured display mode. For a new Playwright setup, use a project-local locked dependency and the established package manager.
3. Keep durable tests in project-owned files and expose them through a package script.
4. Apply [the system-browser helper](assets/playwright-system-browser.ts) only for a new policy or a project that explicitly adopts it; copy it into project-owned code rather than importing from the skill.
5. Preserve the required search order: `google-chrome`, `chromium`, then `microsoft-edge`.
6. Treat an explicitly configured project display mode as authoritative unless the user explicitly overrides it. When neither project configuration nor the user selects a mode, run headless. Run headful when project configuration selects it or the user explicitly requests it; then require a supported browser and Linux display, fail clearly when either is absent, and never download a browser or silently switch modes.
7. Start a fresh, task-owned browser run for each E2E command by default. Do not attach to or reuse a browser process, remote endpoint, browser context, or persistent profile from another run unless the user explicitly requests reuse. Within the current Playwright Test command, allow runner-managed worker browser reuse and keep each test in its isolated browser context.
8. Use Playwright Test fixtures for runner-managed teardown. In direct Playwright Library code, close the context and browser in `finally`. On success, failure, timeout, or interruption, wait for the task-owned runner and browser processes to exit; terminate only task-owned leftovers and never kill unrelated browsers by process name.
9. Add `--no-sandbox` only inside a detected container. Add `--disable-dev-shm-usage` only there when `/dev/shm` is below 1 GiB.
10. Use accessibility-first locators and stable behavioral assertions. Do not navigate arbitrary external pages or execute an untrusted browsing session.
11. Preserve configured artifact roots. If none exists, use an ignored `tmp/playwright/` path. Confirm every in-worktree trace, video, screenshot, download, report, and profile root is ignored.
12. Report the durable test, package command, actual display mode, isolation or requested reuse, cleanup result, helper/config behavior, actual artifact roots and ignore evidence, command result, and environment blockers.
