#!/bin/sh
set -eu

mkdir -p /tmp/cfg
cp -r /opt/tokscale-config/. /tmp/cfg/

stage_dir() {
  src="$1"
  dst="$2"
  if [ -d "$src" ] && [ -n "$(ls -A "$src" 2>/dev/null)" ]; then
    mkdir -p "$dst"
    cp -r "$src"/. "$dst"/
  fi
}

stage_dir /mnt/antigravity-cli/conversations /tmp/.gemini/antigravity-cli/conversations
stage_dir /mnt/codex/sessions /tmp/.codex/sessions
stage_dir /mnt/codex/archived_sessions /tmp/.codex/archived_sessions
stage_dir /mnt/claude/projects /tmp/.claude/projects
stage_dir /mnt/claude/transcripts /tmp/.claude/transcripts
stage_dir /mnt/gemini/tmp /tmp/.gemini/tmp
stage_dir /mnt/opencode/storage/message /tmp/.local/share/opencode/storage/message

for f in /mnt/opencode/db/*; do
  if [ -f "$f" ] && [ -s "$f" ]; then
    mkdir -p /tmp/.local/share/opencode
    cp "$f" /tmp/.local/share/opencode/
  fi
done

export HOME=/tmp
export GEMINI_CLI_HOME=/tmp/.gemini
export CODEX_HOME=/tmp/.codex
export CLAUDE_CONFIG_DIR=/tmp/.claude
export XDG_DATA_HOME=/tmp/.local/share
export TOKSCALE_CONFIG_DIR=/tmp/cfg

has_client_arg=0
for arg in "$@"; do
  case "$arg" in
    -c|--client|--client=*)
      has_client_arg=1
      break
      ;;
  esac
done

if [ "$has_client_arg" -eq 0 ] && [ -n "${TOKSCALE_CLIENTS:-}" ]; then
  exec tokscale --offline --client "$TOKSCALE_CLIENTS" "$@"
fi

exec tokscale --offline "$@"
