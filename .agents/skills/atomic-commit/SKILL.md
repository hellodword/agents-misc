---
name: atomic-commit
description: Create one narrow, verified, reviewable commit using explicit staging and repository message conventions. Use only after the user, task, or project policy authorizes a commit; do not trigger merely because work has multiple steps or validation passed.
---

# Atomic Commit

1. Use commit authorization already provided by the user, task, or project policy and stay within its scope. Do not ask again. If no authorization exists, leave the changes uncommitted.
2. Verify that the change is semantically complete and its selected validation passed. Review `git status --short` and the task diff, preserving unrelated work.
3. Check the intended commit against the root safety rules. Include fixtures, baselines, or generated sources only when they are intentional project inputs.
4. Stage explicit task paths only. Never use bulk staging or force-add. Inspect the staged diff for unrelated or accidental content before committing.
5. Follow the repository's message convention. Otherwise use an appropriate Conventional Commit type and an English imperative subject without a trailing period. Add a body when needed to explain coupled changes or compatibility.
6. If `AI_COMMIT_COAUTHOR` is non-empty, append its value as the final `Co-authored-by:` trailer. Do not expose other environment values.
7. Commit non-interactively and report the hash, scope, validation, and limitations. If changes cannot be separated safely or commit creation fails, preserve user work and report the blocker without widening staging.
