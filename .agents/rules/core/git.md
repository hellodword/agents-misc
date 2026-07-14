---
id: core.git
kind: core
triggers:
  - "git"
  - "commit"
  - "staging"
  - "branch"
  - "status"
  - "Conventional Commit"
summary: Keep commits atomic, explicit, and safe around unrelated user changes.
companions:
  required_rules:
    - core.repo-hygiene
  skills:
    - id: atomic-commit
      when: commit workflow guidance is needed
    - id: repo-hygiene-review
      when: staging or artifact boundaries are non-trivial
---

# Git Rules

## Branch convention

Use `master` for a new repository unless the project establishes another convention. For an existing repository, detect and preserve its current default branch. The shared GitHub Actions rule intentionally fixes generated workflow push triggers to `master`; that is a workflow-policy exception, not permission to rename an existing repository branch.

## Commit boundary

Commit is the smallest unit of agent progress only when commit mode allows commits.

A normal execution step is an independent, verifiable, semantically complete unit of implementation.

The following do not count as separate normal execution steps:

- implementation and validation of the same change;
- repair of the same step before it passes validation;
- acceptance criteria for the same step;
- checkpoint validation;
- documentation sync required by the same implementation step.

The following usually count as multiple normal execution steps:

- distinct feature slices with separate behavior;
- separate backend and frontend slices when each is independently verifiable;
- schema or migration work followed by dependent application behavior;
- generated artifact setup followed by consumer code;
- pure patch fetch/apply/refresh/build phases;
- separate compatibility, security, or data-risk repairs.

## Commit policy

Commit mode is active only when one of these is true:

- the user explicitly requests commits;
- the task prompt says auto-commit;
- the repository has an explicit agent auto-commit policy.

Do not create commits automatically merely because a plan has multiple steps.

For multi-step implementation without explicit commit permission, after each verified step report changed files, validation run, suggested commit message, and exact files that would be staged.

When commit mode is active:

- commit after each normal step reaches verified status;
- commit checkpoint repairs separately after their repair validation passes;
- keep each commit non-interactive and reviewable;
- run `git status --short` before committing;
- if unrelated user changes are present and cannot be cleanly separated, do not commit automatically. Report the intended staging paths and defer.

If commits are forbidden by mode, user instruction, execution environment, or repository state, provide changed file list, explicit files to stage, verification performed, suggested commit message, and reason the commit is deferred.

## Staging

Never run bulk staging commands.

Forbidden commands include:

- `git add .`
- `git add -A`
- `git add --all`

Stage explicit target files only.

Never stage ignored files.

Never use force-add for ignored files unless the ignore policy is explicitly changed first.

## Commit message

Use the repository's existing Conventional Commit type set when available.

Default allowed types:

- `feat`
- `fix`
- `docs`
- `test`
- `refactor`
- `chore`
- `build`
- `ci`
- `perf`
- `style`
- `revert`

Header format:

- `type(scope): subject`
- `type: subject`

Subject must be an English imperative phrase.

Subject must not end with a period.

Body may describe key changes, migrations, generated artifacts, or documentation sync.

If `$AI_COMMIT_COAUTHOR` is non-blank, append `Co-authored-by: $AI_COMMIT_COAUTHOR` as the final line.
