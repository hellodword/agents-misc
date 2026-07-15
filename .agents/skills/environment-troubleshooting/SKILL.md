---
name: environment-troubleshooting
description: Diagnose blockers involving devcontainers, Nix, browsers, SQLite, filesystems, PATH, permissions, or missing local tools and retry the narrow command. Use when environment failure blocks work; do not use to mutate the host, globally install tools, or redesign a working project environment.
---

# Environment Troubleshooting

1. Capture the narrow failing command and exact error.
2. Classify the blocker as a missing project tool, missing environment capability, container limitation, PATH problem, browser/display issue, SQLite/filesystem permission, or upstream build failure.
3. Inspect only relevant tracked `.devcontainer/**`, `.vscode/**`, project commands, toolchain files, and lockfiles.
4. Discover project tools through the declared environment and environment capabilities through ordinary `PATH` lookup.
5. Do not introduce Nix as a diagnostic side effect, use global installs, invoke a host package manager, or mutate system configuration.
6. Put one-off diagnostics under a confirmed ignored `tmp/agent/<task-id>/` path and clean them up. Use the system temporary directory when no project temp path is confirmed.
7. If a durable project tool is missing and edits are authorized, add it through the existing project environment and validate its consumer.
8. If resolution requires a user-controlled host/container change, state the exact capability or permission required.
9. Re-run the narrow command and report blocker class, evidence, change or user action, retry, outcome, and remaining limitation.
