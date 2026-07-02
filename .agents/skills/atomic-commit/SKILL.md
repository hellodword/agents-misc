---
name: atomic-commit
description: Use this when a verified step should be committed, or when the user explicitly asks for a commit. In a one-step or unsplit plan, do not create the commit unless the user explicitly requested automatic commits. In a multi-step plan, use this after each verified normal step.
---

# Atomic Commit

## Purpose

Create a small, explicit, reviewable commit for one verified unit of work.

## When to use

Use this skill when:

- automatic commit mode is active because the plan has two or more normal execution steps;
- a checkpoint repair has passed validation and needs its own commit;
- the user explicitly asks for a commit;
- the repository rules require a verified step to be committed.

Do not use this skill to create a commit automatically when the plan has no split or only one normal execution step unless the user explicitly requested automatic commits.

## Step boundary

A normal execution step is an independent, verifiable, semantically complete implementation unit.

The following do not create multiple normal steps by themselves:

- implementation and validation of the same change;
- repair before the same step passes validation;
- acceptance criteria for the same step;
- checkpoint validation;
- documentation sync that belongs to the same implementation unit.

## Workflow

1. Confirm that the relevant step has reached verified status.
2. Confirm that commit mode allows a commit:
   - multi-step plan: automatic commit is enabled by default;
   - one-step or unsplit plan: commit only if the user explicitly requested it.
3. Run `git status --short`.
4. Identify files changed by the current verified step only.
5. Exclude ignored paths, local artifacts, logs, databases, screenshots, coverage, browser traces, `.work/`, and temporary files.
6. Review diffs for the explicit target files.
7. Stage only explicit file paths.
8. Never run `git add .`, `git add -A`, or `git add --all`.
9. Never stage ignored files.
10. Write a Conventional Commit header using only:
    - `feat`
    - `fix`
    - `chore`
    - `docs`
    - `refactor`
    - `test`
11. Use an English imperative subject with no trailing period.
12. Add a body only when it clarifies key changes, validation, migrations, generated artifacts, flake lock alignment, or documentation sync.
13. If `$AI_COMMIT_COAUTHOR` is non-blank, append `Co-authored-by: $AI_COMMIT_COAUTHOR` as the final line.
14. Commit non-interactively.

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
