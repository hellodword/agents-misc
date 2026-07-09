---
id: toolchain.browser-e2e
kind: toolchain
triggers:
  - "browser E2E"
  - "Playwright"
  - "critical flow"
  - "screenshot"
  - "web verification"
summary: Choose minimal browser validation with safe artifact and profile handling.
load_with: []
---

# Browser E2E Rules

## Tool choice

Use Playwright MCP for exploratory agent-driven browser verification.

Use temporary Playwright scripts under `tmp/` for one-off deterministic checks that should not be committed.

Use committed Playwright tests for durable regression coverage.

Use AI visual review only when the user explicitly asks for screenshot-based visual review or image editing.

## Browser choice

For project-owned Playwright scripts, first read `PLAYWRIGHT_CDP_ENDPOINT`.

If `PLAYWRIGHT_CDP_ENDPOINT` exists, connect to that CDP endpoint.

CDP connection applies to Chromium-family browsers.

If no endpoint exists, probe `PATH` in this order:

1. `google-chrome`
2. `microsoft-edge`
3. `chromium`

Use the first available Chromium-family browser.

Do not install browsers automatically.

## Headed/headless

Default local browser launch is headful.

Add headless mode only when explicitly requested or when a durable test runner already defines headless behavior.

## Container flags

Detect container mode using `/.dockerenv` first. If absent, cgroup/container markers may be used as fallback diagnostics.

When launching a local Chromium-family browser inside a container/devcontainer, add:

    --no-sandbox

Outside containers/devcontainers, do not add `--no-sandbox` by default.

Check `/dev/shm` before launching Chromium-family browsers.

Default threshold:

    1 GiB

If inside a container/devcontainer and `/dev/shm` total size is less than 1 GiB, add:

    --disable-dev-shm-usage

If `/dev/shm` is 1 GiB or larger, do not add `--disable-dev-shm-usage` by default.

When container startup flags are under user control, prefer increasing shared memory or using `--ipc=host` over relying on `--disable-dev-shm-usage`.

## Artifacts

Put browser profiles, traces, screenshots, videos, downloads, and temporary scripts under `tmp/`.

Do not commit browser artifacts.

If no browser, display, or CDP endpoint is available, report the environment blocker instead of installing system packages.
