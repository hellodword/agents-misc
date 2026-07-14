---
id: toolchain.github-actions
kind: toolchain
triggers:
  - "GitHub Actions"
  - ".github/workflows"
  - "GitHub workflow"
  - "action pinning"
  - "permissions"
summary: Add GitHub Actions only on request and restrict default dependencies to official actions with least privilege.
companions: {}
---

# GitHub Actions Rules

Do not create or modify GitHub Actions unless the user explicitly asks. Keep requested workflows minimal and non-deploying by default.

For a new requested workflow, use this baseline unless the user explicitly requests additional events:

```yaml
on:
  push:
    branches: [master]
  workflow_dispatch:
```

Use `permissions: {}` when no repository read is needed. Use `contents: read` when the workflow checks out repository contents, and add only the exact additional permissions required by requested jobs.

Preserve existing action versions unless the task upgrades them. For new dependencies, use only GitHub's official `actions/*` actions by default. Any other owner, including `github/codeql-action`, requires an explicit user request plus review of ownership, maintenance status, requested permissions, input handling, network/secret access, and release-tag policy. Verify the current latest stable major and use a major tag such as `actions/checkout@vN`. This policy accepts the mutability risk of official major tags and does not require a full commit SHA. Never use `@main`.

Do not add cache steps, custom cache keys, or cache-only setup inputs by default. Do not add release, publishing, deployment, cloud authentication, or secret-dependent behavior unless explicitly requested.
