# Agents Viewer

Agents Viewer is a local, read-only browser for Codex rollout JSONL. It indexes only rollout files into a source-scoped SQLite cache, serves a loopback-only HTTP API, and embeds the React UI in one executable. It does not require a running Codex app-server.

## Quick start

```bash
just agents-viewer-run
```

On first start the viewer creates `~/.agents-viewer/config.toml` and its generated `schema.json`. It then prints the URL exactly once on stdout; open that URL yourself. The first bootstrap index shows live progress in the terminal and page. Later startup checks, watcher updates, and periodic reconciles remain silent unless they fail. An explicit `--rebuild-index` completes atomically before HTTP starts and reports progress only in the terminal. The viewer never opens a browser.

```text
agents-viewer [OPTIONS]

--config PATH           configuration file; a missing file is created with defaults
--rebuild-index         atomically rebuild this source's viewer cache
-h, --help              print help
-V, --version           print version
```

Application settings live in TOML rather than flags or custom environment variables:

```toml
#:schema ./schema.json
source_dir = "~/.codex"
data_dir = "~/.agents-viewer"
initial_index_days = 7
listen = "127.0.0.1:4747"
password = ""
max_event_bytes = "32MiB"
log_level = "warn"
```

The generated schema is refreshed on startup; an existing TOML file is never rewritten during upgrades. Relative paths are resolved from the config directory, and `~` means the current user's home. Hostnames, wildcard/non-loopback listeners, decimal units such as `MB`, and out-of-range event sizes are rejected. Exit codes are `0` for help/version or graceful shutdown, `1` for configuration/source/data/lock/permission/bind/runtime failure, and `2` for invalid CLI syntax.

`password` enables HTTP Basic authentication when it is non-empty. The username is always `agents-viewer`. Authentication covers the page, embedded assets, API, raw content, and event stream, so an unauthenticated browser receives its native credential prompt before the UI loads. Browsers decide how long Basic credentials remain cached and provide no reliable application-controlled logout. Direct clients can use `curl --user agents-viewer URL` and enter the password at curl's prompt.

## Read-only boundary and threat model

The source traversal is deliberately narrow:

- `<codex-home>/sessions/**/*.jsonl`
- `<codex-home>/archived_sessions/**/*.jsonl`

Files are opened read-only, file symlinks are not followed, canonical paths must stay inside the selected root, and identity is checked around reads. Source/data overlap is rejected. Linux `strace` acceptance asserts that the application never creates, writes, truncates, renames, deletes, or changes permissions beneath the source root.

These are application-level guards, not an operating-system sandbox. They reduce accidental writes by this program; they do not confine a compromised process. Use an OS sandbox as an additional boundary when that threat matters.

The viewer does **not** open Codex state SQLite databases, `history.jsonl`, authentication data, config, skills, plugins, logs, or other Codex-home files. It never sends content to a network service; HTTP is loopback-only and the UI has a restrictive CSP. Optional Basic authentication is access control, not encryption: HTTP sends the configured credentials with requests.

The SQLite cache is plaintext and contains message text and searchable derived data. Cache directories/files are restricted to the current user, but this is not encryption. The threat model trusts the signed-in user and host and does not defend against a malicious process already running as the same account.

## Cache and recovery

The default layout is:

```text
~/.agents-viewer/
  config.toml
  schema.json
  sources/<first-16-hex-of-source-sha256>/
    index.sqlite3
    viewer.lock
    index.sqlite3.corrupt-*
    index.sqlite3.incompatible-*
```

`initial_index_days` establishes a fixed cutoff when an index is created: `7` means sessions created in the preceding seven days, `-1` means all history, and `0` means only sessions created at or after that startup. Creation time comes from the first `session_meta` timestamp, then the rollout filename timestamp, then file modification time. The cutoff is persisted per source and continues to apply on every reconcile; it does not move forward each day. The status API and top-bar indicator expose the effective window, cutoff, and excluded file/byte counts.

Changing `initial_index_days` or `max_event_bytes` for an existing index fails closed with an instruction to run `just agents-viewer-run --rebuild-index`. Rebuild creates a new database and only swaps it into place after a complete successful scan.

Only one process may hold a source/cache lock; multiple browser tabs share it. A leftover lock file is harmless because the operating-system lock is authoritative. The database is initialized from the single baseline in `schema.sql`; schema changes replace that baseline rather than adding upgrade migrations. A cache with a different schema signature is preserved as incompatible and rebuilt from rollout JSONL.

- Rebuild: stop other viewer processes and start with `--rebuild-index`.
- Reset: stop the viewer, delete only that source's namespace, and restart. Rollout files are never part of reset.
- Corruption: startup preserves the bad database as `*.corrupt-*` and rebuilds.
- Future/incompatible schema: startup preserves it as `*.incompatible-*` and rebuilds rather than downgrading it.
- Failed staging rebuild: the last complete database remains queryable until an atomic swap succeeds.

## UI

Desktop uses resizable session and conversation panels; the inspector starts collapsed and opens only when requested. Tablet moves the inspector into a sheet; mobile moves both side panels into sheets. The session panel is a flat, newest-first conversation list with source avatars, friendly update times, previews, and working-directory subtitles. User messages appear as right-aligned chat bubbles and assistant replies as left-aligned bubbles. Messages use sanitized GFM rendering, local clock times, human-readable day separators, and a copy control that retrieves the complete message on demand.

Reasoning and commands appear as compact two-column activity text beginning with `Reasoning:` and `Executing:`; activity rows do not repeat timestamps. Command output is never shown in the conversation; click the activity to inspect its complete input, result, and raw records. Warnings and errors remain visible. Turn/world context, turn markers, token usage, patches, `wait`, plans, other tools, and injected instructions are hidden by default. The top-bar Filter dialog controls source, working directory, archive state, and “Show technical activity”; its technical-activity choice is stored locally in the browser.

Ordinary conversations open on the latest page. Floating controls jump directly to the first or latest page without downloading the entire transcript. Reaching the true bottom enables follow mode so appended entries remain visible; scrolling away disables follow mode and shows a new-item count on the latest-message control. Language (`English`/`简体中文`) and light/dark/system theme are stored locally in the browser.

The source filter always lists every normalized origin: interactive CLI, VS Code, non-interactive `codex exec`, review tasks, child agents, app-server/integration clients, and unknown metadata. Archive filtering has separate active, active-plus-archived, and archived-only modes. Archive state is managed by `codex archive` and `codex unarchive`; the viewer never changes it. Filter changes are drafted in the dialog and issue requests only after Apply.

Global search defaults to active User and Assistant conversation messages. “Search all activity types” additionally searches reasoning, commands and results, context, and other technical entries; this choice is stored in the browser. `GET /api/v1/search` exposes the same scope as the optional `allTypes=true` query parameter, which defaults to `false`. The broad scope uses bounded fallback scanning for text-bearing entries that are deliberately absent from FTS and reports `partial=true` if that bound is reached.

Keyboard controls:

- `Ctrl/Cmd+K`: open global search.
- `/`: open search when an input is not focused.
- `Escape`: close the active search or sheet and restore focus.
- `Enter`: open the selected command-search result.
- `j` / `k`: focus the next/previous visible transcript entry.
- `g g`: scroll the transcript to the top.
- `G`: scroll the transcript to the bottom.

Raw records and long content are chunked; ordinary rendering never automatically reads more than 256 KiB. Explicitly copying a truncated chat bubble reads its remaining chunks before writing the complete Markdown to the clipboard. Markdown raw HTML is disabled and remote images are rendered as attachment metadata.

## Development and validation

Just is the human-facing command menu, and every public viewer recipe enters the pinned Nix environment itself:

```bash
just agents-viewer-api-dev --config /path/to/config.toml
just agents-viewer-web-dev
```

Available project recipes:

```text
agents-viewer-build
agents-viewer-test
agents-viewer-e2e
agents-viewer-generate
agents-viewer-generate-check
agents-viewer-acceptance-large
agents-viewer-verify
```

`agents-viewer-verify` contains only generator, formatting, static, unit/integration, Web, embedded-build, and Nix gates that do not need a host browser. `agents-viewer-e2e` is separate.

Rust, Cargo, Node.js, npm, SQLite, Playwright, and the other tools come from the pinned Nix development shell and lockfiles. E2E never downloads a browser. Set `PLAYWRIGHT_CDP_ENDPOINT`, copy `web/e2e.config.example.json` to the ignored `web/e2e.config.json`, or expose `google-chrome`, `microsoft-edge`, or `chromium` on `PATH`.

```bash
just agents-viewer-e2e
```

Each E2E test creates independent temporary source/cache directories and an actual embedded server on an independent port. Browser routing aborts every non-loopback request. The large acceptance recipe runs ignored performance tests and the Linux syscall audit; it may require substantial time and temporary disk.

Nix outputs:

```bash
nix build .#agents-viewer
nix run .#agents-viewer -- --help
nix flake check
```

The Nix result contains one executable and has no runtime Web download. The repository's existing default Codex package/app is intentionally unchanged.

## Supported source formats

| Input                                          | Support                 | Behavior                                                                          |
| ---------------------------------------------- | ----------------------- | --------------------------------------------------------------------------------- |
| `session_meta`                                 | full                    | session identity, source, cwd, version/provider/git metadata                      |
| `turn_context`, `world_state`                  | full                    | collapsed context entries, not searchable by default                              |
| `event_msg`                                    | full for known payloads | messages, reasoning, tool lifecycle, diagnostics; unknown payloads degrade safely |
| `response_item`                                | full for known payloads | messages, reasoning summaries, tool calls/results; duplicates are merged          |
| `compacted`                                    | full                    | collapsed technical/context entry                                                 |
| unknown envelope/payload                       | forward-compatible      | raw reference plus diagnostic; the session continues                              |
| malformed JSON, invalid UTF-8, incomplete tail | diagnostic              | stable complete records remain indexed; incomplete tail waits for append          |
| oversized complete record                      | bounded diagnostic      | metadata/raw reference only; content API returns 413                              |

Fixtures cover Codex 0.120, the 0.144 compatibility baseline, deduplication, malformed data, and subagent/review sources. Exec, review, subagent, VS Code, and app-server origin metadata are normalized into the viewer's own stable source kinds.

## Upstream compatibility

The compatibility baseline is OpenAI Codex tag [`rust-v0.144.1`](https://github.com/openai/codex/tree/rust-v0.144.1). Agents Viewer intentionally does not depend on Codex internal crates: those APIs and persisted forms may change together. The viewer owns small permissive envelope/payload types, preserves raw references, ignores additive fields, and maps unknown values to its own `unknown` variants.

Upstream references checked for this baseline:

- [`codex-rs/protocol/src/protocol.rs`](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/protocol/src/protocol.rs): protocol events and response items.
- [`codex-rs/rollout/src/recorder.rs`](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/rollout/src/recorder.rs): `RolloutRecorder`, `RolloutLine`, `RolloutItem`, and first-`SessionMeta` resume behavior.
- [`codex-rs/state/src/runtime.rs`](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/state/src/runtime.rs): state-runtime boundary that the viewer does not open.
- [`codex-rs/file-watcher/src/lib.rs`](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/file-watcher/src/lib.rs): upstream `FileWatcher`; the viewer uses its own narrow rollout-root watcher.

### Type mapping

| Upstream persisted concept                        | Viewer mapping                                                        | Compatibility rule                                                     |
| ------------------------------------------------- | --------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `RolloutLine { timestamp, item }` / JSON envelope | `RawEnvelope { timestamp, type, payload }`                            | parse the generic envelope first; never require a closed upstream enum |
| `RolloutItem::SessionMeta`                        | session metadata and stable session ID                                | first valid metadata wins; filename/hash fallback remains available    |
| `RolloutItem::TurnContext`, world-state records   | `EntryKind::Context`                                                  | hidden and non-searchable by default                                   |
| `RolloutItem::EventMsg`                           | messages, reasoning, tool lifecycle, diagnostics                      | known payloads normalize; unknown payloads keep a raw ref              |
| `RolloutItem::ResponseItem`                       | messages, reasoning summary, tool calls/results                       | semantic duplicates with presentation events are merged                |
| compacted history                                 | `EntryKind::Context`/technical detail                                 | preserve ordering and raw provenance                                   |
| originator/source metadata                        | `cli`, `vscode`, `exec`, `review`, `subagent`, `appServer`, `unknown` | unknown source names never fail deserialization                        |
| thread parent/spawn metadata                      | `parentThreadId`                                                      | unresolved parents remain browsable as orphans                         |
| state database rows                               | unsupported by design                                                 | never open Codex SQLite state                                          |

The viewer's public DTOs are independent contracts generated from `src/model.rs` with `export_types`; clients must not parse opaque IDs or assume upstream enum exhaustiveness. The entries endpoint uses `includeTechnical=true` to include the activity hidden by the default conversation view. Its `previousCursor` and `nextCursor` independently indicate whether older and newer entries exist. Search uses `allTypes=true` for its broader scope; absent or `false` means formal User/Assistant messages only.

When advancing the compatibility baseline, add the smallest sanitized fixture needed, update the mappings, and run generation, parser/integration, E2E, and large acceptance checks. A parser-version change must force source reindexing.

## Troubleshooting

- `already locked`: use the printed URL from the running instance or stop it; do not delete source files.
- unsafe data/config permissions: restrict them to the current account; the viewer does not silently chmod pre-existing paths.
- source/data overlap: choose `data_dir` outside the canonical Codex home.
- index setting mismatch: run `just agents-viewer-run --rebuild-index` after intentionally changing `initial_index_days` or `max_event_bytes`.
- no FTS5: use the Nix package or a build with bundled SQLite; startup requires `ENABLE_FTS5`.
- no E2E browser: set `PLAYWRIGHT_CDP_ENDPOINT`, copy `web/e2e.config.example.json` to the ignored `web/e2e.config.json`, or expose a supported browser command on `PATH`; do not run a Playwright install/download command.
- UI/API version mismatch: rebuild the embedded binary with `just agents-viewer-build`.
