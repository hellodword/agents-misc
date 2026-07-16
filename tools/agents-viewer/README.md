# Agents Viewer

## Why this exists

Codex CLI is designed first as a live terminal interface, not as a durable conversation browser. Long sessions are routinely truncated on screen, and display bugs can make content disappear even though the rollout still contains it. Finding an earlier decision means scrolling through a large terminal transcript, search is limited, and copying is unreliable. Markdown in particular often gains whitespace or line breaks that were not part of the original message.

Agents Viewer turns the rollout into a local, searchable reading model without changing Codex data. It keeps user and assistant messages prominent, collapses reasoning and tool activity by default, retrieves full content when copying, and preserves raw records for inspection. The goal is not to replace the Codex TUI while a task is running; it is to make completed and long-running conversations practical to read, search, compare, and copy.

This problem is not unique to Codex. Most agent CLIs and TUIs optimize for streaming output and immediate interaction, so they eventually face similar limits around history, truncation, search, structured activity, and exact copying. Agents Viewer currently promises compatibility only with Codex CLI rollouts. Other agent tools can fit the same architecture, but each requires an explicit source adapter and compatibility fixtures rather than an assumption that their persisted formats are interchangeable.

## Architecture and design

The viewer is a local, read-only indexing pipeline:

```text
Codex rollout JSONL (read-only)
  -> bounded tolerant parser
  -> viewer-owned normalized model
  -> source-scoped SQLite cache
  -> Axum HTTP API and SSE
  -> React conversation UI
```

### Why Rust

Codex and its rollout recorder are implemented in Rust. Using Rust for the parser and service keeps filesystem, timestamp, JSON, cancellation, and bounded-memory behavior close to the upstream implementation model. It also makes it practical to review persisted protocol changes against the corresponding Codex Rust source rather than translating them through a different runtime's assumptions.

The viewer deliberately does not import Codex internal crates. Those crates and the persisted records can change together, and linking one internal version would create a false promise that arbitrary rollout versions are compatible. Instead, the viewer declares an upstream compatibility baseline, owns small permissive envelope and payload types, and pins its Rust toolchain and third-party dependencies through `flake.lock`, `Cargo.lock`, and exact Cargo versions. Dependency changes that affect parsing or serialization are reviewed together with the relevant upstream baseline.

### Parser and normalized model

Rollout input is treated as an append-oriented, partially open log rather than a closed schema. The parser:

- reads complete JSONL records with a configured size bound;
- preserves stable raw references and diagnostics for malformed or unknown data;
- ignores additive fields and maps unknown enum-like values to viewer-owned fallbacks;
- deduplicates presentation events and response items that represent the same message or tool lifecycle;
- separates user-visible conversation messages from injected instructions, context, reasoning, and tool activity;
- resumes from a verified stable prefix when a live rollout is appended.

Session relationships use one normalized `parentThreadId`. Explicit parent and subagent metadata take precedence over `forked_from_id`. A fresh-context plan implementation can also be linked to its planning session when the Codex handoff prefix, normalized plan SHA-256, non-empty working directory, and event ordering match exactly. There is no title, time-window, or similarity fallback; an unresolved parent remains a browsable root.

### SQLite as a derived cache

SQLite contains normalized sessions, entries, raw-record metadata, diagnostics, and search indexes. It is derived entirely from rollout JSONL and is not user-authored state. This allows the indexer to use atomic staging, append reconciliation, FTS5, and cursor-based APIs without writing beside the source files.

The database is initialized from the single baseline in `schema.sql`. This project is still in early development: schema changes replace that baseline directly and do not add upgrade migrations or schema-version history. A cache with a different schema signature is rebuilt from rollout JSONL by the existing recovery path.

### API and Web UI

Axum serves a loopback-only JSON API, an SSE stream for index and conversation updates, and the embedded Web bundle. Public DTOs are defined in Rust and exported deterministically to `web/src/generated/api.ts`, so the React client and service share one checked contract.

The React/Vite UI presents conversations in a Telegram-like layout. User messages are right-aligned, assistant messages are left-aligned, Markdown is sanitized and rendered with GFM, and full message content is fetched before copying when the list preview is truncated. Reasoning and commands appear as compact inspectable activity. Each `request_user_input` question appears as its own default-visible incoming poll message with option labels and descriptions; completed polls mark selected answers and place non-empty per-question notes below the selected option. Command results remain in the inspector.

The sidebar uses parent/child trees rather than a flat list. All indexed `parentThreadId` relationships share the same layout, filters match whole trees, pagination never splits a tree, and the newest session in the newest group is the default route. Plan-implementation children use the localized title “Implement · parent title”.

## Supported Codex data

The viewer reads only:

- `<codex-home>/sessions/**/*.jsonl`
- `<codex-home>/archived_sessions/**/*.jsonl`

The compatibility promise is for Codex CLI rollout records. Source metadata produced inside the Codex ecosystem is also classified as interactive CLI, VS Code, `codex exec`, review, subagent, app-server/integration, or unknown so mixed Codex homes remain understandable. This classification is not a compatibility promise for unrelated agent products.

| Persisted concept                              | Viewer behavior                                                                  |
| ---------------------------------------------- | -------------------------------------------------------------------------------- |
| `session_meta`                                 | Stable session ID, source, cwd, parent/fork, version, provider, and Git metadata |
| `turn_context`, `world_state`                  | Collapsed technical context, excluded from default search                        |
| known `event_msg` payloads                     | Messages, reasoning, tool lifecycle, plans, and diagnostics                      |
| known `response_item` payloads                 | Messages, reasoning summaries, tool calls/results, and attachments               |
| compacted history                              | Ordered technical/context entry with raw provenance                              |
| unknown envelope or payload                    | Browsable raw reference plus diagnostic; the session continues                   |
| malformed JSON, invalid UTF-8, incomplete tail | Partial-session diagnostic while stable records remain available                 |
| oversized complete record                      | Bounded metadata/raw reference; the content API refuses an oversized read        |

Fixtures cover Codex 0.120, the 0.144 compatibility baseline, deduplication, malformed input, source classification, parent/fork metadata, and plan handoff grouping.

## Following upstream Codex

The declared compatibility baseline is OpenAI Codex tag [`rust-v0.144.1`](https://github.com/openai/codex/tree/rust-v0.144.1). The important boundary is the persisted rollout, not the shape of an internal crate API.

Upstream references for the baseline are:

- [`codex-rs/protocol/src/protocol.rs`](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/protocol/src/protocol.rs) for protocol events and response items;
- [`codex-rs/rollout/src/recorder.rs`](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/rollout/src/recorder.rs) for `RolloutRecorder`, `RolloutLine`, `RolloutItem`, and resume behavior;
- [`codex-rs/state/src/runtime.rs`](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/state/src/runtime.rs) for the state boundary that the viewer must not open;
- [`codex-rs/file-watcher/src/lib.rs`](https://github.com/openai/codex/blob/rust-v0.144.1/codex-rs/file-watcher/src/lib.rs) for comparison with the viewer's narrower rollout-root watcher.

Advancing the baseline is an evidence-driven maintenance task:

1. Compare upstream protocol and recorder changes, with particular attention to session metadata, messages, tool lifecycle, compaction, and handoff behavior.
2. Capture the smallest sanitized rollout fixture that demonstrates each new or changed persisted shape.
3. Update viewer-owned normalization without importing internal Codex crates or closing permissive unknown-field handling.
4. Regenerate the TypeScript contract and review it as a public API change.
5. Run parser, index, API, read-only, browser, and large bounded-memory checks before changing the documented baseline.

Additive upstream fields should normally require no change. Unknown records degrade to raw data and diagnostics instead of failing the file. A changed meaning, ordering rule, or deduplication rule requires an explicit parser update and source reindex. The viewer never reads the Codex state database as a shortcut for missing rollout relationships; if a relationship is not present or safely derivable from the rollout, it remains unresolved.

## Read-only and security boundaries

Source files are opened read-only. File symlinks are not followed, canonical paths must remain inside the selected rollout root, and file identity is checked around reads. Source/data overlap is rejected. Linux syscall acceptance verifies that the application never creates, writes, truncates, renames, deletes, or changes permissions beneath the source root.

The viewer does not open Codex state SQLite databases, `history.jsonl`, authentication data, config, skills, plugins, logs, or other files in the Codex home. It sends no content to a network service. HTTP listeners are restricted to loopback addresses and the embedded page uses a restrictive content security policy.

These are application guards, not an operating-system sandbox. The local SQLite cache is plaintext and contains message text and derived search data. Cache directories and files are restricted to the current user, but another process running as that user is outside the threat model.

## Running Agents Viewer

Run the packaged application through the root Just menu:

```bash
just agents-viewer-run
```

On first start it creates `~/.agents-viewer/config.toml` and a generated `schema.json`, indexes the configured rollout window, and prints its URL once. The viewer never opens a browser. A large bootstrap or explicit rebuild reports progress; routine watcher and reconciliation scans remain silent unless they fail.

```text
agents-viewer [OPTIONS]

--config PATH           configuration file; a missing file is created with defaults
--rebuild-index         atomically rebuild this source's viewer cache
-h, --help              print help
-V, --version           print version
```

Application settings live in TOML:

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

`initial_index_days` establishes a fixed cutoff when the index is created: `7` indexes the preceding seven days, `-1` indexes all history, and `0` indexes only sessions created at or after startup. The cutoff does not move forward each day. Changing the index window or event-size bound for an existing cache requires `just agents-viewer-run --rebuild-index`.

A non-empty `password` enables HTTP Basic authentication for the page, assets, API, raw content, and event stream. The username is always `agents-viewer`. Browsers control how long credentials remain cached; direct clients can use `curl --user agents-viewer URL` and enter the password at the prompt.

The cache layout is source-scoped:

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

Only one process may hold a source/cache lock. To reset a disposable cache, stop the viewer, delete only that source namespace, and restart. Rollout files are never part of reset. Corrupt or incompatible cache families are preserved before a replacement is built.

## UI controls

The top bar contains global search, session filters, technical-activity visibility, language, theme, and the initially collapsed inspector. Filter changes are applied together. Reasoning, commands, warnings, errors, and `request_user_input` questionnaires remain visible when other technical activity is hidden. Search defaults to user and assistant messages; “Search all activity types” also includes reasoning, commands, results, context, and other technical entries. Both choices are remembered in the browser.

Conversation navigation opens at the latest page and follows appended entries while the viewport remains at the true bottom. Floating controls jump to the first or latest message without downloading the full transcript.

Keyboard controls:

- `Ctrl/Cmd+K` or `/`: open global search.
- `Escape`: close the active dialog or sheet and restore focus.
- `j` / `k`: focus the next or previous visible transcript entry.
- `g g`: jump to the first message.
- `G`: jump to the latest message.

## Development, build, and test

Nix is the reproducible environment and Just is the human-facing command menu. Public recipes enter the pinned development environment themselves:

```bash
just agents-viewer-api-dev --config /path/to/config.toml
just agents-viewer-web-dev
just agents-viewer-build
```

The durable validation entrypoints are:

```bash
just agents-viewer-generate       # regenerate Rust -> TypeScript DTOs
just agents-viewer-generate-check # verify the checked-in generated contract
just agents-viewer-test           # Rust and browserless Web tests
just agents-viewer-verify         # formatting, Clippy, tests, Web, embedded and Nix builds
just agents-viewer-e2e            # embedded server plus host-browser Playwright tests
just agents-viewer-acceptance-large
```

E2E does not download a browser. Set `PLAYWRIGHT_CDP_ENDPOINT`, copy the ignored `web/e2e.config.example.json` to `web/e2e.config.json`, or expose `google-chrome`, `microsoft-edge`, or `chromium` on `PATH`. Browser profiles, screenshots, traces, databases, build output, and other runtime artifacts stay in ignored locations.

The Nix package contains one executable with the Web UI embedded:

```bash
nix build .#agents-viewer
nix run .#agents-viewer -- --help
nix flake check
```

Common failures:

- `already locked`: use the running instance's printed URL or stop that process.
- unsafe config/cache permissions: restrict them to the current account.
- source/data overlap: choose a data directory outside the canonical Codex home.
- index setting mismatch: intentionally rebuild with `just agents-viewer-run --rebuild-index`.
- no FTS5: use the Nix package or another build with bundled SQLite and FTS5.
- no E2E browser: configure CDP or expose a supported Chromium-family executable; do not install a browser from the test command.
- UI/API version mismatch: rebuild the embedded binary with `just agents-viewer-build`.
