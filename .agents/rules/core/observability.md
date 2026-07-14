---
id: core.observability
kind: core
triggers:
  - "logging"
  - "metrics"
  - "tracing"
  - "observability"
  - "diagnostics"
summary: Add useful logs and diagnostics without leaking sensitive information.
companions: {}
---

# Observability Rules

- Prefer structured logs for services and CLI diagnostics.
- Include useful safe context:
  - operation;
  - request/job ID when available;
  - entity identifiers when safe;
  - duration;
  - error cause.
- Do not log secrets, tokens, cookies, auth headers, or sensitive PII.
- Errors should explain:
  - what failed;
  - safe context;
  - whether retry is useful.
- Add metrics/tracing only when the project already uses them or the task requires them.
- Do not add external observability services by default.
- For local-first apps, ensure logs are useful without a cloud dashboard.
