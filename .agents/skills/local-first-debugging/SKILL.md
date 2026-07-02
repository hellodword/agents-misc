---
name: local-first-debugging
description: Use this when a task is blocked by local environment, devcontainer, Nix, browser, CDP, SQLite, filesystem, PATH, or missing tool issues.
---

# Local-first Debugging

## Purpose

Diagnose local blockers without global installs or system mutation.

## Workflow

1. Identify the failing command and exact error.
2. Classify blocker:
   - missing project tool;
   - missing environment capability;
   - devcontainer limitation;
   - PATH discovery issue;
   - browser/CDP issue;
   - SQLite/file permission issue;
   - upstream build issue.
3. Check project-provided commands first.
4. Use Nix for project tools.
5. Use PATH discovery for environment capability commands.
6. Do not run global installs or host package managers.
7. Put diagnostic output under `tmp/`.
8. If `flake.nix` needs a project tool and edits are allowed, update it.
9. If the blocker requires user-controlled host/container changes, report exact steps.
10. Re-run the narrow failing command.

## Output

Report:

- blocker class;
- evidence;
- change made or user action needed;
- command retried;
- result.
