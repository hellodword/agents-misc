---
id: core.data-privacy
kind: core
triggers:
  - "privacy"
  - "user data"
  - "uploads"
  - "analytics"
  - "telemetry"
  - "fixtures"
summary: Minimize, protect, and avoid exposing personal or sensitive data.
load_with: []
---

# Data and Privacy Rules

- Minimize stored user data.
- Do not commit user uploads, local production-like data, database snapshots, or exported personal data.
- Prefer anonymized or synthetic fixtures.
- Redact sensitive values in logs and test output.
- Keep local dev data under ignored paths.
- Document data retention and deletion behavior when persistence is user-visible.
- Avoid analytics or telemetry unless the user explicitly asks.
