---
name: nix-browser-e2e
description: Add or run project-owned Playwright E2E in projects that use Nix or are adopting it in the current task, provisioning the browser in the project dev shell as needed. Use for trusted local browser-specific flows; do not use for non-Nix projects, ordinary tests, untrusted browsing, or screenshot-only review.
---

# Nix Browser E2E

1. Define the browser-specific flow and observable assertions. Inspect the existing runner, dependency manager, Nix shell, browser policy, display mode, and supported systems.
2. For a new setup, use a project-local locked Playwright dependency and provision Chromium from locked nixpkgs in the appropriate development shell. A missing browser is ordinary setup work. Preserve an established browser policy; branded Google Chrome requires a product need and authorization for its unfree dependency.
3. Read [the Nix browser reference](references/nix-browser.md) when adding or changing provisioning. Pass an explicit executable path through `PLAYWRIGHT_NIX_BROWSER_PATH` and set `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` before installation. Do not select an ambient host browser, silently download one, or fall back to another browser.
4. Apply [the Nix browser helper](assets/playwright-nix-browser.ts) when the project adopts this policy; copy it into project-owned test support. Keep durable tests and a package command in the project.
5. Validate a focused real-browser smoke flow on initial setup and when the browser version, Playwright version, or relevant launch configuration changes. Unrelated lockfile updates do not require it. Custom executable compatibility is the project's responsibility; resolve mismatches through supported Nix/dependency updates or configuration changes, and report any remaining incompatibility.
6. Preserve an explicitly configured display mode. Otherwise default to headless. A requested or configured headful run requires a working display on Linux; do not silently switch modes when it is missing.
7. Start a task-owned run by default. Reuse a browser process, endpoint, context, or persistent profile from another run only when requested. Runner-managed worker reuse within one command is allowed, with isolated test contexts.
8. Use Playwright Test fixtures for teardown, or `finally` in direct library code. On completion or interruption, wait for owned processes to exit and terminate only owned leftovers. Add `--no-sandbox` only in a detected container, and `--disable-dev-shm-usage` only there when `/dev/shm` is below 1 GiB.
9. Use stable behavioral assertions and accessibility-first locators. This workflow is for trusted project flows, not arbitrary untrusted browsing. Preserve configured artifact roots and follow the project's test policy for intentional visual golden baselines.
10. Report the flow, command result, and any compatibility, display, or cleanup limitation. For setup or compatibility changes, include the selected browser/Playwright versions and configuration needed to reproduce the result.
