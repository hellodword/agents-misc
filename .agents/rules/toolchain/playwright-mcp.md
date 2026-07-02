# Playwright MCP Rules

Use Playwright MCP for exploratory, agent-driven browser verification.

Use project-owned Playwright scripts/tests for durable, repeatable, assertion-heavy verification.

Do not treat MCP exploration as a regression test. Distill stable flows into deterministic Playwright tests before committing them.

Use isolated MCP mode for tests that should not reuse login/session state.

Use persistent MCP profile only when session continuity is useful and safe.

Do not enable arbitrary Playwright code execution through MCP for untrusted clients.

Commit only example MCP configuration under `.agents/references/`.

Do not commit machine-specific MCP config, secrets, browser profiles, storage state, cookies, or local endpoints.

When MCP needs container-specific launch flags, generate a temporary config under `tmp/` rather than hard-coding local environment assumptions in the committed example.
