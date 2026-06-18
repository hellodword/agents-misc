---
name: git-commit
description: Create a Git commit from already staged changes using a Conventional Commit message. Use when the user asks to commit changes, create a git commit, commit staged changes, or use /commit.
allowed-tools: Bash
---

# Git Commit Skill

## Purpose

Use this skill when the user asks to create a Git commit.

The commit must include only changes that are already staged. The agent must not stage files, modify files, run tests, run hooks, or push unless the user explicitly asks.

## Rules

1. Commit only already staged changes.
2. Do not run `git add`, `git commit -a`, `git reset`, `git restore`, or any command that changes the staged set.
3. Do not edit files before committing.
4. Do not run tests, builds, linters, formatters, type checks, installs, or dependency updates unless explicitly requested.
5. Use `git commit --no-verify` to avoid commit hooks.
6. First try committing with Git’s default signing behavior.
7. If the commit fails because signing is rejected or unavailable, retry once with `--no-gpg-sign`.
8. Retry without signing only for signing-related failures.
9. Do not amend, rebase, squash, tag, or push unless explicitly requested.
10. If nothing is staged, do not commit.
11. If the staged diff appears to contain real secrets or credentials, stop and report it.

## Inspect Staged Changes

Inspect only staged changes:

```bash
git status --short
git diff --cached --stat
git diff --cached --name-status
git diff --cached
```

Base the commit message only on `git diff --cached`.

Unstaged and untracked files must not affect the message except to note after the commit that they were not included.

## Commit Message

Use Conventional Commits:

```text
<type>(<scope>): <summary>

<body>

<footer>
```

Use a scope only when it is obvious.

Common types:

- `feat`: user-facing feature
- `fix`: bug fix
- `docs`: documentation
- `style`: formatting-only change
- `refactor`: restructuring without behavior change
- `perf`: performance improvement
- `test`: tests
- `build`: build system, dependencies, packaging, lockfiles
- `ci`: CI configuration
- `chore`: maintenance
- `revert`: revert a previous commit

Header rules:

- Use lowercase type and scope.
- Use imperative mood.
- Keep the summary concise.
- Do not end the summary with a period.
- Prefer the first line to stay under 72 characters.

Body rules:

- Use a body when the reason or impact is not obvious.
- Explain what changed and why.
- Do not merely list files or repeat the diff.

Footer rules:

- Use `BREAKING CHANGE: ...` when the staged diff introduces a breaking change.
- Add issue references only when clearly grounded in the user request, staged diff, branch name, or project context.
- Add `Co-authored-by` only when explicitly configured.

## AI Co-author

Resolve the optional AI co-author identity in this priority order:

1. `AI_COMMIT_COAUTHOR` environment variable
2. `git config --local ai.commitCoAuthor`
3. `git config --global ai.commitCoAuthor`
4. If none is configured, omit `Co-authored-by`

Do not auto-detect the agent. Do not infer the co-author from model names, API keys, executables, shell prompts, repository names, or provider configuration.

A valid co-author value must be:

```text
Name <email@example.com>
```

Disabled values omit the trailer:

```text
none
false
no
off
0
null
```

Empty value also omits the trailer.

If a configured value is neither disabled nor a valid `Name <email>` value, stop and report the invalid configuration instead of committing.

When present, `Co-authored-by` must be the final trailer.

## Co-author Resolver

```bash
resolve_ai_commit_coauthor() {
  is_disabled_value() {
    case "$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')" in
      ""|none|false|no|off|0|null) return 0 ;;
      *) return 1 ;;
    esac
  }

  is_valid_coauthor() {
    printf '%s' "$1" | grep -Eq '^[^<>[:cntrl:]]+ <[^[:space:]<>@]+@[^[:space:]<>]+>$'
  }

  value=""
  source=""

  if [ "${AI_COMMIT_COAUTHOR+x}" = "x" ]; then
    value="$AI_COMMIT_COAUTHOR"
    source="AI_COMMIT_COAUTHOR"
  else
    value="$(git config --local --get ai.commitCoAuthor 2>/dev/null || true)"
    if [ -n "$value" ]; then
      source="git config --local ai.commitCoAuthor"
    else
      value="$(git config --global --get ai.commitCoAuthor 2>/dev/null || true)"
      if [ -n "$value" ]; then
        source="git config --global ai.commitCoAuthor"
      fi
    fi
  fi

  if [ -z "$source" ]; then
    return 0
  fi

  if is_disabled_value "$value"; then
    return 0
  fi

  if is_valid_coauthor "$value"; then
    printf '%s\n' "$value"
    return 0
  fi

  printf 'Invalid AI co-author value from %s: %s\n' "$source" "$value" >&2
  return 2
}
```

## Commit Procedure

If no staged changes exist:

```bash
git diff --cached --quiet && {
  echo "No staged changes found. Nothing was committed."
  exit 0
}
```

Create the commit message in a temporary file:

```bash
message_file="$(mktemp)"
coauthor="$(resolve_ai_commit_coauthor)" || exit $?

cat > "$message_file" <<'EOF'
<type>(<scope>): <summary>

<body>
EOF

if [ -n "$coauthor" ]; then
  {
    printf '\n'
    printf 'Co-authored-by: %s\n' "$coauthor"
  } >> "$message_file"
fi
```

Commit with Git’s default signing behavior first:

```bash
err_file="$(mktemp)"

if git commit --no-verify -F "$message_file" 2>"$err_file"; then
  signing_status="default"
else
  err_text="$(cat "$err_file")"

  if printf '%s\n' "$err_text" | grep -Eiq 'gpg failed to sign|failed to sign|signing failed|failed to write commit object|No secret key|No private key|secret key not available|agent refused operation|signing key|gpg\.ssh|ssh signing'; then
    if git commit --no-verify --no-gpg-sign -F "$message_file"; then
      signing_status="unsigned fallback"
    else
      exit 1
    fi
  else
    printf '%s\n' "$err_text" >&2
    exit 1
  fi
fi

rm -f "$message_file" "$err_file"
```

Replace placeholders with a real message based only on the staged diff.

## After Commit

Run only lightweight Git inspection:

```bash
git status --short
git log -1 --pretty=oneline
```

Report briefly:

```text
Committed staged changes:

<short-hash> <subject>

Signing: <default | unsigned fallback>
Remaining unstaged or untracked files were not included.
```

If the unsigned fallback was used, mention that the initial signed commit attempt failed and the commit was retried once without signing.

If no staged changes existed:

```text
No staged changes found. Nothing was committed.
```

If co-author configuration was invalid:

```text
Invalid AI co-author configuration. Nothing was committed.

<source>: <value>
Expected format: Name <email@example.com>
```

If a likely secret was found:

```text
Potential secret detected in staged changes. Nothing was committed.
```
