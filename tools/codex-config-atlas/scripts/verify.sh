#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$repo_root"

os_name="$(uname -s)"
arch_name="$(uname -m)"
case "${os_name}:${arch_name}" in
  Linux:x86_64) system="x86_64-linux" ;;
  Linux:aarch64 | Linux:arm64) system="aarch64-linux" ;;
  Darwin:arm64) system="aarch64-darwin" ;;
  *)
    printf 'error: unsupported platform %s/%s\n' "$os_name" "$arch_name" >&2
    exit 1
    ;;
esac

stray=()
for candidate in \
  example.config.toml \
  tools/codex-config-atlas/data \
  tools/codex-config-atlas/site \
  tools/codex-config-atlas/.schemas-sync-* \
  tools/codex-config-atlas/.schemas-backup-* \
  tools/codex-config-atlas/*.toml; do
  if [[ "$candidate" == "tools/codex-config-atlas/pyproject.toml" ]]; then
    continue
  fi
  if [[ -e "$candidate" ]]; then
    stray+=("$candidate")
  fi
done
if [[ ${#stray[@]} -gt 0 ]]; then
  printf 'error: remove generated or temporary files from the working tree before verification:\n' >&2
  printf '  %s\n' "${stray[@]}" >&2
  exit 1
fi

untracked_schemas=()
while IFS= read -r path; do
  [[ -n "$path" ]] && untracked_schemas+=("$path")
done < <(git ls-files --others --exclude-standard -- tools/codex-config-atlas/schemas)

if [[ ${#untracked_schemas[@]} -gt 0 ]]; then
  printf 'error: untracked files under tools/codex-config-atlas/schemas are invisible to Nix flakes.\n' >&2
  printf 'Run exact-path intent-to-add first:\n  git add -N --' >&2
  printf ' %q' "${untracked_schemas[@]}" >&2
  printf '\n' >&2
  exit 1
fi

code_changes="$(git status --porcelain -- tools/codex-config-atlas/src tools/codex-config-atlas/web tools/codex-config-atlas/tests nix/codex-config-atlas.nix)"
if [[ -n "$code_changes" ]]; then
  printf 'note: code/test files outside schemas/ are modified in this worktree:\n%s\n' "$code_changes" >&2
fi

printf '==> Running CI-equivalent Nix checks on %s (pairwise historical diff check takes ~2-4 min)...\n' "$system" >&2
nix build --no-link --accept-flake-config --no-write-lock-file --print-build-logs \
  ".#checks.${system}.formatting" \
  ".#checks.${system}.codex-config-atlas-registry" \
  ".#checks.${system}.codex-config-atlas-data" \
  ".#checks.${system}.codex-config-atlas-site" \
  ".#checks.${system}.codex-config-atlas-tests"

summary_line="$(python3 -c 'import json; m = json.load(open("tools/codex-config-atlas/schemas/manifest.json")); v = m["versions"]; first, last = v[0]["version"], v[-1]["version"]; print(f"{len(v)} versions ({first} .. {last}), current={last}")')"

intent_paths=()
while IFS= read -r line; do
  if [[ "$line" == " A "* ]]; then
    intent_paths+=("${line:3}")
  fi
done < <(git status --porcelain=v1 -- tools/codex-config-atlas)

printf '\n=== Atlas Verification Summary ===\n'
printf 'Status: PASS (formatting, registry, data, site, tests)\n'
printf 'Registry: %s\n' "$summary_line"
if [[ ${#intent_paths[@]} -gt 0 ]]; then
  printf 'Intent-to-add paths (disclose in report if leaving uncommitted):\n'
  printf '  %s\n' "${intent_paths[@]}"
else
  printf 'Intent-to-add paths: none\n'
fi
printf 'Git status (tools/codex-config-atlas):\n'
git status --short -- tools/codex-config-atlas
