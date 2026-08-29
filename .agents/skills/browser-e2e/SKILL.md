---
name: browser-e2e
description: Add or run durable, project-owned Playwright tests for browser-specific behavior and primary user flows. Use for trusted local applications when lower-level tests cannot cover the boundary; do not use for ordinary tests, arbitrary external pages, untrusted browsing sessions, or screenshot-only review.
---

# Browser E2E

1. Define the smallest browser-specific user flow and its observable assertions.
2. Inspect the project's browser runner, package-manager lock, Nix flake and lock, browser configuration, display mode, and supported systems. Preserve an existing explicit project browser policy unless the task authorizes changing it.
3. For a new Playwright setup or an implicit host-browser policy, use a project-local locked Playwright dependency and the established package manager, then provision the browser from the project's locked nixpkgs. Prefer `pkgs.chromium`; use branded `pkgs.google-chrome` only for an explicit product need with authorization for its unfree license and supported-system limits.
4. Read [the Nix browser reference](references/nix-browser.md) when adding or changing browser provisioning. Pass `lib.getExe` through `PLAYWRIGHT_NIX_BROWSER_PATH`, set `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` before dependency installation, and use the default Nix development shell unless an established project contract requires a named shell. Do not search host `PATH`, use an ambient system browser, silently download a browser, or silently fall back.
5. Apply [the Nix browser helper](assets/playwright-nix-browser.ts) only when the project adopts this policy; copy it into project-owned code rather than importing it from the skill.
6. Treat the nixpkgs lock and package-manager lock as one compatibility boundary: the former pins the browser and the latter pins Playwright. Because Playwright does not guarantee arbitrary executable compatibility, run a focused real-browser smoke flow after either lock changes and report a mismatch instead of falling back.
7. Keep durable tests in project-owned files and expose them through a package script.
8. Treat an explicitly configured project display mode as authoritative unless the user explicitly overrides it. When neither project configuration nor the user selects a mode, run headless. Run headful when project configuration selects it or the user explicitly requests it; then require the configured Nix browser and a Linux display, fail clearly when either is absent, and never silently switch modes.
9. Start a fresh, task-owned browser run for each E2E command by default. Do not attach to or reuse a browser process, remote endpoint, browser context, or persistent profile from another run unless the user explicitly requests reuse. Within the current Playwright Test command, allow runner-managed worker browser reuse and keep each test in its isolated browser context.
10. Use Playwright Test fixtures for runner-managed teardown. In direct Playwright Library code, close the context and browser in `finally`. On success, failure, timeout, or interruption, wait for the task-owned runner and browser processes to exit; terminate only task-owned leftovers and never kill unrelated browsers by process name.
11. Add `--no-sandbox` only inside a detected container. Add `--disable-dev-shm-usage` only there when `/dev/shm` is below 1 GiB.
12. Use accessibility-first locators and stable behavioral assertions. Do not navigate arbitrary external pages or execute an untrusted browsing session.
13. Preserve configured artifact roots. If none exists, use an ignored `tmp/playwright/` path. Confirm every in-worktree trace, video, screenshot, download, report, and profile root is ignored.
14. Report the durable test, package command, nixpkgs input and browser package, Playwright lock, actual display mode, isolation or requested reuse, cleanup result, helper/config behavior, actual artifact roots and ignore evidence, command result, compatibility evidence, and environment blockers.
