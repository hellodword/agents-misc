---
name: git-add
description: Safely stage Git working tree changes by inspecting current changes and running git add only with explicit resolved paths. Reject broad git add commands, force add, ignored files, blocked directories, archives, and likely secrets.
allowed-tools: Bash
---

# Git Add Skill

## Purpose

Use this skill when the user asks to stage Git changes, add changes to the Git index, prepare changes for commit, or automatically stage changes after a coding task.

This skill may update only the Git index. It must not edit files, commit, push, run tests, run hooks, install dependencies, or otherwise modify the working tree.

The skill does not require the user to provide paths. If paths are not provided, inspect the current Git changes, resolve the intended candidate paths, filter unsafe paths, and stage only those resolved explicit paths.

## Core Rule

Always stage with explicit resolved paths:

```bash
git add -- "${paths[@]}"
```

Never stage with broad Git commands.

Do not run:

```bash
git add .
git add -A
git add --all
git add -u
git add *
git add ./*
git add :/
git add -- .
git add -f
git add --force
```

Do not use aliases, shell globs, generated shell expansion, root pathspecs, command substitution, or pathspec magic to simulate broad staging.

## Rules

1. Stage only resolved explicit file paths.
2. If the user provides paths, resolve and stage only those paths after validation.
3. If the user does not provide paths, inspect current Git changes and stage the safe candidate files that match the user’s intent.
4. For automatic coding workflows, stage only files changed by the current task when that can be determined.
5. Do not stage files that had pre-existing user changes before the task if they are mixed with current-task changes and cannot be separated safely.
6. Do not stage ignored files.
7. Do not use `git add -f` or `git add --force`.
8. Do not stage blocked directories or anything inside them.
9. Do not stage archive or compressed files.
10. Do not stage likely real secrets or credentials.
11. Do not commit, amend, tag, push, rebase, merge, stash, reset, restore, checkout, or switch branches.
12. Do not edit, format, generate, delete, move, or otherwise modify files.
13. Do not run tests, builds, linters, formatters, type checks, installs, dependency updates, or hooks.
14. If no safe candidate paths remain after filtering, do not run `git add`.
15. Existing staged changes must not be unstaged or altered except when the resolved candidate path is intentionally staged.

## Scope Resolution

Resolve paths from the user request and repository state.

### Explicit user paths

If the user names files or directories, use those as the initial candidate paths.

Examples:

```text
Stage README.md.
Stage src/auth.ts and src/session.ts.
Add docs/ to the Git index.
Stage package.json and pnpm-lock.yaml.
```

Allowed command form:

```bash
paths=("README.md" "src/auth.ts")
git add -- "${paths[@]}"
```

### No explicit paths

If the user asks to stage changes without naming paths, inspect Git state and resolve candidate files from current changes.

Examples:

```text
Stage the current changes.
Add the changes I just made.
Stage the safe changes.
Prepare the current work for commit.
After finishing the task, stage the files you changed.
```

Use Git inspection to resolve explicit file paths, then run `git add --` with the resolved list.

Do not ask the user for paths when the intended candidate files can be determined from the current task and Git status.

### Broad intent

If the user asks to stage all changes, do not use a broad Git command. Instead:

1. Inspect current changes.
2. Resolve the changed files into an explicit path list.
3. Filter blocked, ignored, archive, unsafe, and secret-bearing files.
4. Stage only the remaining explicit paths.

If unsafe files are skipped, report them.

## Blocked Paths

Reject any requested path or resolved candidate file under these directory components:

```text
tmp
temp
output
outputs
node_modules
dist
build
coverage
.cache
.next
.nuxt
out
logs
```

Reject archive or compressed files ending with:

```text
.zip
.tar
.tar.gz
.tgz
.tar.bz2
.tbz2
.tar.xz
.txz
.rar
.7z
.gz
.bz2
.xz
.zst
```

Use checks like:

```bash
blocked_dir_re='(^|/)(tmp|temp|output|outputs|node_modules|dist|build|coverage|\.cache|\.next|\.nuxt|out|logs)(/|$)'
blocked_archive_re='(\.zip|\.tar|\.tar\.gz|\.tgz|\.tar\.bz2|\.tbz2|\.tar\.xz|\.txz|\.rar|\.7z|\.gz|\.bz2|\.xz|\.zst)$'
```

Reject unsafe pathspecs:

```bash
case "$path" in
  ""|"."|"./"|"/"|"*"|"./*"|"**"|":/"|":/*")
    echo "Unsafe broad path rejected: $path"
    exit 0
    ;;
esac

if printf '%s\n' "$path" | grep -Eq '(^|/)\.\.(/|$)|[*?\[]|:\('; then
  echo "Unsafe pathspec rejected: $path"
  exit 0
fi
```

## Repository Check

First confirm the command is inside a Git working tree:

```bash
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || {
  echo "Not inside a Git repository. Nothing was staged."
  exit 0
}
```

## Inspect Current Changes

Use lightweight Git inspection only:

```bash
git status --short --untracked-files=all
git diff --stat
git diff --name-status
git diff --cached --stat
git diff --cached --name-status
```

When paths are provided, inspect those paths:

```bash
git status --short --untracked-files=all -- "${input_paths[@]}"
git diff --stat -- "${input_paths[@]}"
git diff --name-status -- "${input_paths[@]}"
git diff -- "${input_paths[@]}"
git ls-files --others --exclude-standard -- "${input_paths[@]}"
```

When paths are not provided, inspect all visible working tree changes, then resolve explicit candidate files:

```bash
candidate_file="$(mktemp)"

{
  git diff --name-only -z
  git ls-files --others --exclude-standard -z
} | sort -zu > "$candidate_file"
```

For provided paths, resolve changed files under those paths:

```bash
candidate_file="$(mktemp)"

{
  git diff --name-only -z -- "${input_paths[@]}"
  git ls-files --others --exclude-standard -z -- "${input_paths[@]}"
} | sort -zu > "$candidate_file"
```

If no candidate files exist:

```bash
if [ ! -s "$candidate_file" ]; then
  echo "No matching changes found. Nothing was staged."
  rm -f "$candidate_file"
  exit 0
fi
```

## Ignored File Check

Do not stage ignored files.

For resolved candidates:

```bash
ignored_file="$(mktemp)"

while IFS= read -r -d '' file; do
  if git check-ignore -q -- "$file"; then
    printf '%s\n' "$file" >> "$ignored_file"
  fi
done < "$candidate_file"
```

Ignored files must be skipped, not force-added.

## Blocked Candidate Check

Filter blocked generated, dependency, archive, and compressed files:

```bash
safe_file="$(mktemp)"
blocked_file="$(mktemp)"

while IFS= read -r -d '' file; do
  normalized="${file#./}"

  if printf '%s\n' "$normalized" | grep -Eq "$blocked_dir_re"; then
    printf '%s\n' "$file" >> "$blocked_file"
    continue
  fi

  if printf '%s\n' "$normalized" | grep -Eiq "$blocked_archive_re"; then
    printf '%s\n' "$file" >> "$blocked_file"
    continue
  fi

  if git check-ignore -q -- "$file"; then
    printf '%s\n' "$file" >> "$ignored_file"
    continue
  fi

  printf '%s\0' "$file" >> "$safe_file"
done < "$candidate_file"
```

If no safe files remain:

```bash
if [ ! -s "$safe_file" ]; then
  echo "No safe candidate paths found. Nothing was staged."
  echo

  if [ -s "$blocked_file" ]; then
    echo "Blocked paths:"
    sort -u "$blocked_file"
  fi

  if [ -s "$ignored_file" ]; then
    echo "Ignored paths:"
    sort -u "$ignored_file"
  fi

  rm -f "$candidate_file" "$safe_file" "$blocked_file" "$ignored_file"
  exit 0
fi
```

## Secret Check

Before staging, check safe candidate files for likely real secrets.

```bash
secret_pattern='(-----BEGIN [A-Z ]*PRIVATE KEY-----|AKIA[0-9A-Z]{16}|ASIA[0-9A-Z]{16}|github_pat_[A-Za-z0-9_]+|gh[pousr]_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{20,}|xox[baprs]-[A-Za-z0-9-]{20,}|npm_[A-Za-z0-9]{20,}|pypi-[A-Za-z0-9_-]{20,}|sk-[A-Za-z0-9_-]{20,}|SG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}|postgres://[^[:space:]]+:[^[:space:]@]+@|mysql://[^[:space:]]+:[^[:space:]@]+@|mongodb(\+srv)?://[^[:space:]]+:[^[:space:]@]+@)'
secret_file="$(mktemp)"
```

Check added lines in tracked changes:

```bash
while IFS= read -r -d '' file; do
  if git ls-files --error-unmatch -- "$file" >/dev/null 2>&1; then
    if git diff --unified=0 -- "$file" |
      grep -E '^\+[^+]' |
      grep -Eiq "$secret_pattern"; then
      printf '%s\n' "$file" >> "$secret_file"
    fi
  fi
done < "$safe_file"
```

Check untracked text files:

```bash
while IFS= read -r -d '' file; do
  git ls-files --error-unmatch -- "$file" >/dev/null 2>&1 && continue
  [ -f "$file" ] || continue

  if file --mime "$file" 2>/dev/null | grep -Eq 'charset=binary|application/octet-stream'; then
    continue
  fi

  if grep -Eiq "$secret_pattern" -- "$file"; then
    printf '%s\n' "$file" >> "$secret_file"
  fi
done < "$safe_file"
```

If likely secrets are found, do not stage anything:

```bash
if [ -s "$secret_file" ]; then
  echo "Potential secret detected in changes to be staged. Nothing was staged."
  echo
  sort -u "$secret_file" | while IFS= read -r file; do
    printf '%s: redacted credential-like content\n' "$file"
  done

  rm -f "$candidate_file" "$safe_file" "$blocked_file" "$ignored_file" "$secret_file"
  exit 0
fi
```

Never print full secret values.

## Build Explicit Path Array

Convert safe candidates into a Bash array:

```bash
paths=()

while IFS= read -r -d '' file; do
  paths+=("$file")
done < "$safe_file"

if [ "${#paths[@]}" -eq 0 ]; then
  echo "No safe candidate paths found. Nothing was staged."
  rm -f "$candidate_file" "$safe_file" "$blocked_file" "$ignored_file" "$secret_file"
  exit 0
fi
```

## Staging Procedure

Stage only explicit resolved paths:

```bash
git add -- "${paths[@]}"
```

Do not replace this with any broad command.

Remove temporary files:

```bash
rm -f "$candidate_file" "$safe_file" "$blocked_file" "$ignored_file" "$secret_file"
```

## After Staging

Run only lightweight Git inspection:

```bash
git status --short --untracked-files=all
git diff --cached --stat
git diff --cached --name-status
```

Do not run tests, builds, linters, formatters, type checks, installs, hooks, or commits.

## Final Responses

If paths were staged:

```text
Staged resolved paths:

<name-status summary>

No commit was created.
```

If blocked paths were skipped:

```text
Skipped blocked generated, dependency, archive, or compressed paths:

<paths>
```

If ignored paths were skipped:

```text
Skipped ignored paths:

<paths>
```

If no matching changes existed:

```text
No matching changes found. Nothing was staged.
```

If no safe candidates remained:

```text
No safe candidate paths found. Nothing was staged.
```

If likely secrets were found:

```text
Potential secret detected in changes to be staged. Nothing was staged.

<file>: redacted credential-like content
```

If the repository is not valid:

```text
Not inside a Git repository. Nothing was staged.
```

## Reporting Guidelines

The final response should be concise and factual.

Include:

- Which resolved paths were staged
- Which paths were skipped and why
- Whether unstaged or untracked files remain
- Confirmation that no commit was created

Do not include full diffs unless the user asks.

Do not include secret values.

## Coordination With Commit Workflow

This skill only stages changes.

If the user asks to stage changes and then create a commit, handle the request in order:

1. Use this skill to inspect current changes and stage only safe resolved explicit paths.
2. Create the commit only from already staged changes.
3. Do not add any other files during the commit step.
4. Do not create a commit if staging was skipped or blocked.
