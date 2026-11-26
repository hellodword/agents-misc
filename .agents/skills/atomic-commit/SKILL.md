---
name: atomic-commit
description: Create one narrow, verified, reviewable commit (or emit the exact commit command when requested) using explicit staging and repository message conventions. Use only after the user, task, approved staged plan, or project policy authorizes a commit; do not trigger merely because work has multiple steps or validation passed.
---

# Atomic Commit

1. Confirm commit authorization from the user (which applies across follow-up turns and plans in the same session unless narrowed), task, approved staged execution plan (for its defined local commit checkpoints), or project policy, and stay within its scope without asking again. Leave changes uncommitted when unauthorized.
2. Verify that the unit of work is semantically complete and its selected validation passed. Review `git status --short` and the diff while preserving unrelated work.
3. Verify root safety rules: exclude secrets, local runtime state, and temporary captures; include synthetic fixtures, baselines, or generated sources only when they are intentional tracked inputs.
4. If the user asks for commit commands rather than direct commit creation (for example, to sign with a host hardware key), output the exact `git add -- <explicit-paths> && git commit -m ...` command after completing the status and diff checks above, without modifying Git history.
5. Stage explicit task file paths only (`git add -- <file>...`). Never use bulk staging (`.`, `-A`, `--all`) or `-f`. Inspect `git diff --cached` before committing.
6. Follow the repository's commit message convention; otherwise use Conventional Commits with an English imperative subject and no trailing period, adding a body when needed for coupled changes or compatibility context. If `AI_COMMIT_COAUTHOR` is non-empty, append `Co-authored-by: <value>` as the final trailer without exposing other environment variables.
7. Commit non-interactively while preserving Git hooks; if configured commit signing fails in the current environment, retry with `git commit --no-gpg-sign` rather than blocking and note the unsigned commit in the report. Report the commit hash, scope, validation, and any remaining limitations. If unrelated edits prevent safe separation or commit creation still fails, preserve user work and report the blocker without widening staging.
