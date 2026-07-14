---
name: environment-troubleshooting
description: Use this when work is blocked by a devcontainer, Nix, browser, SQLite, filesystem, PATH, permissions, or a missing local tool.
---

# Environment Troubleshooting

## Purpose

Diagnose local environment blockers without global installation or system mutation.

## Workflow

1. Capture the failing command and exact error.
2. Classify the blocker: missing project tool, missing environment capability, container limitation, PATH issue, browser/display issue, SQLite/filesystem permission, or upstream build issue.
3. Inspect relevant tracked `.devcontainer/**` or `.vscode/**` configuration when it can explain the environment.
4. Check project-provided commands and locked dependencies first.
5. Use the project's declared toolchain for project tools and ordinary PATH lookup for environment capabilities; do not introduce Nix as a diagnostic side effect.
6. Do not use global installs, host package managers, or system mutation.
7. Put temporary diagnostic output under the project's confirmed ignored temp path.
8. If a durable project tool is missing and edits are authorized, add it through the existing project environment.
9. If resolution requires a user-controlled host or container change, report the exact requirement.
10. Re-run the narrow failing command.

## Output

Report the blocker class, evidence, change or user action needed, command retried, and result.
