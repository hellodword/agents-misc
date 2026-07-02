---
name: atomic-commit
description: Use this when a verifiable semantic task is complete and a non-interactive explicit-path Conventional Commit should be created. Do not use when commits are forbidden by user instruction or read-only mode.
---

# Atomic Commit

## Purpose

Create a small, explicit, reviewable commit for one completed task.

## Workflow

1. Run `git status --short`.
2. Identify files changed by the current semantic task.
3. Exclude ignored paths, local artifacts, logs, databases, screenshots, coverage, browser traces, `.work/`, and temporary files.
4. Review diffs for the explicit target files.
5. Stage only explicit file paths.
6. Never run `git add .`, `git add -A`, or `git add --all`.
7. Never stage ignored files.
8. Write a Conventional Commit header using only:
   - `feat`
   - `fix`
   - `chore`
   - `docs`
   - `refactor`
   - `test`
9. Use an English imperative subject with no trailing period.
10. Add a body only when it clarifies key changes, validation, migrations, generated artifacts, or documentation sync.
11. If `$AI_COMMIT_COAUTHOR` is non-blank, append `Co-authored-by: $AI_COMMIT_COAUTHOR` as the final line.
12. Commit non-interactively.

## Validation

Report:

- files committed;
- commit hash;
- validation performed;
- any known limitations.

## Failure handling

If commits are not allowed, report the changed files, validation performed, and suggested commit message.
