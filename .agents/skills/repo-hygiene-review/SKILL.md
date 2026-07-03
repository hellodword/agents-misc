---
name: repo-hygiene-review
description: Use this before committing or after generating files to check for temporary artifacts, ignored paths, secrets, oversized files, and misplaced outputs.
---

# Repository Hygiene Review

## Purpose

Keep the repository small, durable, and reviewable.

## Workflow

1. Run `git status --short --ignored`.
2. Identify files created or changed by the current task.
3. Verify each new file has a durable home.
4. Keep temporary output under `tmp/`.
5. Keep pure patch upstream checkouts under `.work/`.
6. Exclude logs, screenshots, browser traces, coverage, databases, archives, build outputs, and local credentials.
7. Check for accidental `.env`, token, secret, upload, or local database files.
8. Check generated files against `.agents/rules/core/generated-artifacts.md`.
9. Check large fixtures or snapshots against the minimum useful scope.
10. Report files that should be moved, ignored, deleted, or explicitly justified.

## Validation

Provide a concise hygiene report:

- safe to commit;
- move/delete recommendations;
- suspicious files;
- generated artifact decisions;
- ignored files that must not be staged.
