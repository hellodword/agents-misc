---
name: browser-e2e
description: Use this when browser validation, Playwright MCP, temporary Playwright scripts, or durable Playwright E2E tests are needed. Do not use for ordinary non-browser tests.
---

# Browser E2E Workflow

## Purpose

Choose and run the smallest useful browser validation path.

## Decision

- Use Playwright MCP for exploratory agent-driven verification.
- Use a temporary script under `tmp/` for one-off deterministic checks.
- Use committed Playwright tests for durable regression coverage.
- Use AI visual review only when the user explicitly asks.

## Workflow

1. Identify the user flow and expected observable behavior.
2. Decide MCP vs temporary script vs durable test.
3. Prefer accessibility-first locators.
4. For project-owned scripts, read `PLAYWRIGHT_CDP_ENDPOINT` first.
5. If no endpoint exists, probe `google-chrome`, `microsoft-edge`, then `chromium` in `PATH`.
6. In container/devcontainer environments, pass `--no-sandbox` for local Chromium-family launch.
7. If container `/dev/shm` is less than 1 GiB, add `--disable-dev-shm-usage`.
8. Store traces, videos, screenshots, downloads, and profiles under `tmp/`.
9. Promote only stable, regression-worthy flows into committed tests.

## Validation

Report:

- chosen E2E path;
- command or MCP flow used;
- observed result;
- artifacts created under `tmp/`;
- whether a durable test was added.
