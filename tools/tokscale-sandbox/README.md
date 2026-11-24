# Tokscale Sandbox

`tokscale-sandbox` runs [`tokscale`](https://github.com/junhoyeo/tokscale) offline inside an unprivileged gVisor (`runsc`) Docker container to inspect token consumption and cost across local coding agents while keeping credentials, MCP secrets, and network interfaces isolated.

## Security boundary

1. **Build-vs-runtime separation**: Image build installs a pinned `tokscale@4.23.0` release with `--ignore-scripts` and pre-populates the pricing table under `/opt/tokscale-config` before any local user data is present. Runtime execution runs with `--runtime=runsc`, `--network=none`, `--read-only`, `--cap-drop=ALL`, `--security-opt=no-new-privileges`, `--pids-limit=128`, and the caller's unprivileged UID/GID.
2. **Narrow read-only session mounts**: Coding agents store OAuth tokens, API keys, and MCP server secrets in the same parent directories as conversation history. `tokscale-sandbox` mounts only the specific session subdirectories or database files `:ro`:

| `--agent` selector        | Read-only host paths mounted when present                                                       | Sibling files kept outside the container                   |
| ------------------------- | ----------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `agy` / `antigravity-cli` | `~/.gemini/antigravity-cli/conversations`                                                       | `~/.gemini/mcp_config.json`, OAuth tokens, `settings.json` |
| `codex`                   | `~/.codex/sessions`, `~/.codex/archived_sessions`                                               | `~/.codex/auth.json`, `~/.codex/config.toml`               |
| `claude`                  | `~/.claude/projects`, `~/.claude/transcripts`                                                   | `~/.claude/.credentials.json`, `~/.claude/settings.json`   |
| `gemini`                  | `~/.gemini/tmp`                                                                                 | `~/.gemini/oauth_creds.json`, `~/.gemini/mcp_config.json`  |
| `opencode`                | `~/.local/share/opencode/opencode*.db*` (files only), `~/.local/share/opencode/storage/message` | `~/.local/share/opencode/auth.json`                        |

3. **In-memory `tmpfs` staging**: `entrypoint.sh` copies mounted session files into `/tmp` (`tmpfs`, `rw,noexec,nosuid,nodev`) before invoking `tokscale --offline`. SQLite-backed clients (`antigravity-cli` and `opencode`) require `-shm`/`-wal` lock files on open, which read-only `runsc` bind mounts disallow; staging into `tmpfs` satisfies SQLite locking in memory with zero writes to the host.

## Prerequisites and build

Docker on the host must have the `runsc` (gVisor) runtime registered (`tokscale-sandbox` verifies `runsc` in `docker info` and exits `1` if unavailable).

Build the sandbox image once (and rebuild when refreshing the embedded pricing cache):

```bash
docker build -t tokscale-sandbox tools/tokscale-sandbox
```

## CLI and queries

```text
tokscale-sandbox [wrapper-options] [--] [tokscale-args...]
```

Wrapper options:

- `--agent <list>`: comma-separated or repeatable selector (`agy` / `antigravity-cli`, `codex`, `claude`, `gemini`, `opencode`, or `all`). Defaults to `all`, mounting every supported agent whose session data exists on the host and passing the active list via `--client`.
- `--image <tag>`: image tag to run (default `tokscale-sandbox`).
- `--build`: build or rebuild `--image` from `tools/tokscale-sandbox/Dockerfile` before running.
- `--tmpfs-size <size>`: size limit for `/tmp` staging `tmpfs` (default `1g`; raise for multi-gigabyte histories).
- `--memory <size>`: container memory limit (default `2g`).
- `-h`, `--help`: print usage and exit `0`.

Exit status is `0` on success, `2` on invalid wrapper options, and `1` when `docker`, `runsc`, the container image, or host session directories are unavailable (diagnostics go to `stderr`). All remaining arguments pass straight to `tokscale --offline`.

Common queries:

```bash
# Interactive TUI across all detected local agents (Tab switches views; 'b' merges subagents)
./tools/tokscale-sandbox/tokscale-sandbox

# Scope strictly to Antigravity CLI over a date window
./tools/tokscale-sandbox/tokscale-sandbox --agent agy --since 2026-11-01 --until 2026-11-24

# Export hourly structured JSON for Codex and Claude Code
./tools/tokscale-sandbox/tokscale-sandbox --agent codex,claude hourly --since 2026-11-24 --json

# Group usage by session and model
./tools/tokscale-sandbox/tokscale-sandbox --agent agy models --group-by session,model --since 2026-11-20
```
