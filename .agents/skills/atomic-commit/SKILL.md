---
name: atomic-commit
description: Create one narrow, verified, reviewable commit using explicit staging and repository message conventions. Use only after the user, task, or project policy authorizes a commit; do not trigger merely because work has multiple steps or validation passed.
---

# Atomic Commit

1. Confirm explicit commit authorization and its exact scope. If absent, do not commit; report a deferred message and paths.
2. Verify that the intended change is semantically complete and its selected validation passed.
3. Run `git status --short` and identify only task-owned paths. Preserve unrelated user changes.
4. Review the diff for those paths. Exclude ignored files, secrets, logs, databases, screenshots, coverage, browser traces, `.work/`, and temporary artifacts.
5. Stage only explicit paths. Never use `git add .`, `git add -A`, `git add --all`, force-add, or an equivalent bulk operation.
6. Follow the repository's established commit convention. Otherwise use an appropriate Conventional Commit type and an English imperative subject without a trailing period.
7. Add a body only when it clarifies compatibility, migration, generated output, or coupled documentation changes.
8. If `AI_COMMIT_COAUTHOR` is non-empty, append its value as the final `Co-authored-by:` trailer. Do not expose other environment values.
9. Commit non-interactively, then report the hash, exact paths, message, validation, and limitations.
10. If unrelated changes cannot be separated safely or commit creation fails, leave user work intact and report the blocker without widening staging.
