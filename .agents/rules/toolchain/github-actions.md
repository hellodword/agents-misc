---
id: toolchain.github-actions
kind: toolchain
triggers:
  - 'GitHub Actions'
  - 'workflow'
  - 'CI'
  - 'action pinning'
  - 'permissions'
summary: Add GitHub Actions only on request and keep workflows minimal and explicit.
load_with: []
---

# GitHub Actions Rules

Do not create or modify GitHub Actions unless the user explicitly asks.

If a workflow is required, keep it minimal and non-deploying by default.

Default triggers:

    on:
      push:
        branches: [master]
      workflow_dispatch:

Use least-privilege permissions by default:

    permissions:
      contents: read

Allowed action owners:

- `actions/*`
- `github/codeql-action/*`

Use latest stable major tags, such as:

- `actions/checkout@vN`
- `actions/setup-node@vN`
- `github/codeql-action/analyze@vN`

Never use `uses: owner/action@main`.

Do not actively add GitHub Actions cache implementation by default.

This means:

- do not add `actions/cache` steps;
- do not add custom cache keys or restore/save logic;
- do not add cache-specific setup-action inputs solely for speed.

This rule does not require overriding the default behavior of a referenced official action. Only add explicit cache configuration when the user asks or the workflow requirement clearly needs it.

Do not add release, package publish, deployment, cloud auth, or secret-dependent behavior unless explicitly requested.

When generating a workflow, verify the current latest stable major tag for every used official action.
