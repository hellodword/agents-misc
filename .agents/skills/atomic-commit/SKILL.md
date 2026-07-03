---
name: atomic-commit
description: Use this when the user explicitly asks for a commit, the task prompt says auto-commit, or the repository has an explicit agent auto-commit policy and a verified step should be committed. Do not create commits automatically merely because a plan has multiple steps.
---

# Atomic Commit

## Purpose

Create a small, explicit, reviewable commit for one verified unit of work when commit mode allows commits.

## When to use

Use this skill when:

- the user explicitly asks for a commit;
- the task prompt says auto-commit;
- the repository has an explicit agent auto-commit policy and a verified step should be committed;
- a checkpoint repair has passed validation and commit mode allows its own commit.

Do not use this skill to create a commit automatically merely because the plan has multiple normal execution steps.

## Step boundary

A normal execution step is an independent, verifiable, semantically complete implementation unit.

The following do not create multiple normal steps by themselves:

- implementation and validation of the same change;
- repair before the same step passes validation;
- acceptance criteria for the same step;
- checkpoint validation;
- documentation sync that belongs to the same implementation unit.

## Workflow

1. Verify that the relevant step has reached verified status.
2. Verify that commit mode allows a commit:
   - explicit user request: commit the verified scope;
   - task prompt says auto-commit: commit the verified scope;
   - repository policy says auto-commit: commit according to that policy;
   - otherwise: do not commit, and report the deferred commit details.
3. Run `git status --short`.
4. Identify files changed by the current verified step only.
5. If unrelated user changes are present and cannot be cleanly separated, do not commit automatically.
6. Exclude ignored paths, local artifacts, logs, databases, screenshots, coverage, browser traces, `.work/`, and temporary files.
7. Review diffs for the explicit target files.
8. Stage only explicit file paths.
9. Never run `git add .`, `git add -A`, or `git add --all`.
10. Never stage ignored files.
11. Write a Conventional Commit header using only:
    - `feat`
    - `fix`
    - `chore`
    - `docs`
    - `refactor`
    - `test`
12. Use an English imperative subject with no trailing period.
13. Add a body only when it clarifies key changes, validation, migrations, generated artifacts, flake lock alignment, or documentation sync.
14. If `$AI_COMMIT_COAUTHOR` is non-blank, append `Co-authored-by: $AI_COMMIT_COAUTHOR` as the final line.
15. Commit non-interactively.

## Validation

Report:

- commit mode;
- files committed;
- commit hash;
- validation performed;
- any known limitations.

## Failure handling

If commits are not allowed, report:

- changed files;
- explicit files that would be staged;
- validation performed;
- suggested commit message;
- reason the commit is deferred.
