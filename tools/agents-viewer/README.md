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
- extracts line-delimited `<proposed_plan>` blocks only from assistant messages, leaving non-plan assistant text as an ordinary message;
- resumes from a verified stable prefix when a live rollout is appended.

Codex 0.151 persists internal model input in more than one role. System/developer control material covers permissions, collaboration and plan modes, multi-agent behavior, model/personality changes, skills, plugins, apps, tools, environment and Git state, realtime sessions, budgets, extensions, and hooks. User-role contextual material includes AGENTS instructions, environment snapshots, loaded skills, external and internal model context, shell/interruption/subagent notifications, recommended plugins, hook prompts, and legacy warnings. None of these non-assistant roles are eligible for plan extraction, even when their protocol instructions contain a complete `<proposed_plan>` example. The distinct `turn_context`, `world_state`, and `security_risk_score` envelopes remain Context entries, while inter-agent traffic remains Technical rather than being reclassified as an assistant plan.

Plan tags follow Codex's line parser: each tag must be alone on its line apart from whitespace, CRLF is accepted, an unterminated block closes at the end of the message, and the last recognized block supplies the plan body. Inline lookalikes remain ordinary assistant text. The normalized plan body is deduplicated with adjacent plan presentation and durable `Plan` items; the durable item wins while every contributing raw reference remains inspectable. A plan-only assistant record therefore produces one `plan` entry instead of a duplicate received message.

Session relationships use one normalized `parentThreadId`. Explicit parent and subagent metadata take precedence over `forked_from_id`. A fresh-context plan implementation can also be linked to its planning session when the Codex handoff prefix, normalized plan SHA-256, non-empty working directory, and event ordering match exactly. There is no title, time-window, or similarity fallback; an unresolved parent remains a browsable root.

Codex 0.147 conversation sections and their manual ordering are state-backed metadata rather than rollout message records. The viewer continues to index every local rollout message but does not project section labels or ordering because it deliberately never opens the Codex state database.

### SQLite as a derived cache

SQLite contains normalized sessions, entries, raw-record metadata, diagnostics, and search indexes. It is derived entirely from rollout JSONL and is not user-authored state. This allows the indexer to use atomic staging, append reconciliation, FTS5, and cursor-based APIs without writing beside the source files.

The database is initialized from the single baseline in `schema.sql`. This project is still in early development: schema changes replace that baseline directly and do not add upgrade migrations or schema-version history. The current baseline is intentionally a clean break from earlier Viewer caches; rollout JSONL remains the source of truth.

Catalog records and conversation snapshots have separate checkpoints. Interrupted content work never replaces the last atomically committed snapshot. Its hidden staging rows are detached after the worker stops and reclaimed in bounded background batches, so neither shutdown nor the next startup performs an unbounded delete. A rollout that disappears retains its last readable snapshot with `sourceMissing` freshness instead of being cascade-deleted.

### Synchronization policy

Startup performs one automatic catalog sweep over both rollout roots. For a new or replaced rollout it parses only far enough to obtain the stable session ID, source-relative path, session metadata, and first real non-empty user message. It never hydrates full conversation content automatically. An unchanged rollout causes no JSONL read and no SQLite write. After a first user message is known, an append needs only a bounded 64 KiB prefix guard and observation update; it does not parse the appended conversation.

Watcher events refresh only the affected catalog records. A coalesced safety sweep runs every 60 seconds and retries isolated read races without dropping the previous catalog or mistaking the source for a deletion. One failing rollout does not stop the coordinator. This makes reopening proportional to catalog changes instead of to the total transcript history.

Full content has one explicit owner: the open conversation page. Pressing **Start live sync** opens `POST /api/v1/sessions/{sessionId}/live-sync` as an SSE response. While that response remains open, the session receives interactive priority, its source is checked every second, and complete appended records are committed continuously. Leaving the route, refreshing the page, pressing **Stop**, or losing the connection releases the lease and cancels content work that no other page still owns. Multiple pages use reference-counted leases.

The existing snapshot is returned immediately while a lease catches up. A first snapshot or replacement is built in hidden staging and becomes visible in one transaction; an append resumes from the committed checkpoint after bounded first/tail validation and parses only the stable incomplete suffix plus new bytes. At most two source parsers run concurrently. SQLite remains a single writer with separate interactive, recent, and background queues, and routine reconciliation does not vacuum the database.

### API and Web UI

Axum serves a loopback-only JSON API, an SSE stream for index and conversation updates, and the embedded Web bundle. Public DTOs are defined in Rust and exported deterministically to `web/src/generated/api.ts`, so the React client and service share one checked contract.

Rust DTO definitions are the authoritative contract input.
`web/src/generated/api.ts` is the only checked-in generated Viewer file and
must be changed only through `just agents-viewer-generate`. `web/dist`, Cargo
`target`, SQLite databases, browser artifacts, and the embedded release bundle
are ignored or Nix-produced runtime/build outputs; they are never source
inputs or commit candidates.

The underlying `export_types` CLI requires exactly one of `--write` or
`--check` plus an explicit `--output PATH`; it never derives an output from the
directory where the binary was compiled.

The React/Vite UI presents conversations in a Telegram-like layout. User messages are right-aligned; assistant messages and normalized plans are left-aligned bubbles. Both reuse sanitized GFM Markdown, full-content copying, timestamps, and Inspector actions. Reasoning and commands appear as compact inspectable activity. Each `request_user_input` question appears as its own default-visible incoming poll message with option labels and descriptions; completed polls mark selected answers and place non-empty per-question notes below the selected option. Command results remain in the inspector.

The sidebar is available as soon as cataloging finds sessions. A catalog-only conversation shows its first real user message and source-root-relative path without triggering a content scan. The conversation header distinguishes never-synchronized, stale, current, source-missing, and live-following states and exposes the explicit Start/Stop control. Global search can find catalog titles and first user messages before a content snapshot exists; hydrated entries extend the same search without duplicating the catalog hit.

`GET /api/v1/sessions/{sessionId}/entries` accepts a comma-separated `displayTypes` set for cursor-safe exact filtering. Supported values are `received`, `sent`, `requestUserInput`, `reasoning`, `exec`, `plan`, `patch`, `mcp`, `webSearch`, `function`, `dynamic`, `terminal`, `viewImage`, `otherTool`, `warning`, `error`, `context`, `marker`, `technicalMessage`, `internalMessage`, and `unknown`. `plan` is the canonical view of plan-only assistant records; `received` does not return a second tagged copy, while assistant text outside a plan block remains `received`. `displayTypes` and `includeTechnical` are mutually exclusive; omitting `displayTypes` preserves the earlier boolean behavior for compatible callers. The Web client always includes `plan` in its exact filter, including when it canonicalizes older saved preferences.

The sidebar uses parent/child trees rather than a flat list. All indexed `parentThreadId` relationships share the same layout, filters match whole trees, pagination never splits a tree, and the newest session in the newest group is the default route. Plan-implementation children use the localized title “Implement · parent title”.

## Supported Codex data

The viewer reads only:

- `<codex-home>/sessions/**/*.jsonl`
- `<codex-home>/archived_sessions/**/*.jsonl`

The compatibility promise is for Codex CLI rollout records. Source metadata produced inside the Codex ecosystem is also classified as interactive CLI, VS Code, `codex exec`, review, subagent, app-server/integration, or unknown so mixed Codex homes remain understandable. This classification is not a compatibility promise for unrelated agent products.

| Persisted concept                                    | Viewer behavior                                                                                    |
| ---------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `session_meta`                                       | Stable session ID, source, cwd, parent/fork, version, provider, Git, and paginated-history data    |
| `turn_context`, `world_state`, `security_risk_score` | Collapsed technical context, excluded from default search                                          |
| `event_msg.item_completed`                           | Codex turn-item families through 0.153.2, including asynchronous questions and function output     |
| other known `event_msg` payloads                     | Messages, reasoning, tool lifecycle, plans, settings, and diagnostics                              |
| known `response_item` payloads                       | Messages, assistant plan blocks, reasoning summaries, inter-agent messages, tools, and attachments |
| `realtime_item`                                      | Session lifecycle markers plus normalized user/assistant transcript segments                       |
| inter-agent communication and delivery metadata      | One collapsed technical message with merged delivery metadata                                      |
| compacted history                                    | Ordered technical/context entry with raw provenance                                                |
| unknown envelope or payload                          | Browsable raw reference plus diagnostic; the session continues                                     |
| malformed JSON, invalid UTF-8, incomplete tail       | Partial-session diagnostic while stable records remain available                                   |
| oversized complete record                            | Bounded metadata/raw reference; the content API refuses an oversized read                          |

For paginated subagent rollouts, records before `subagent_history_start_ordinal` remain available as raw records with the `inherited` status but are not projected into the child's conversation. Ordinal gaps are valid. A non-null `history_base` points at content outside the current rollout, so the viewer marks that session partial instead of pretending the referenced prefix was indexed.

Codex 0.146 command attribution (`plugin_id` and `script_path`) and item lifecycle timing are retained as normalized metadata whether they arrive through legacy execution events, durable completed items, or both.

Codex 0.147 MCP read-only hints and image-generation transparency hints are retained as normalized metadata. Encrypted function arguments remain opaque and contribute only a count; attempted-tool metadata retains tool names, counts, and omission counts without copying nested arguments into rendered or searchable content.

Codex 0.148 security-risk snapshots remain collapsed and excluded from search. Response-item harness authorship, fractional message creation times, and structured image-generation failures are retained as normalized metadata without rendering opaque image results.

Codex 0.149 asynchronous agent delivery metadata and compaction checkpoints are retained without exposing injected delivery context. Codex 0.150 realtime transcript segments are normalized into ordinary user/assistant messages, while realtime lifecycle and promotion records remain collapsed markers. Interrupted collaboration activity remains explicit.

Codex 0.151 standalone `FunctionCallOutput` records preserve namespace, name, structured output, and source item identity even when no preceding call record exists. `guardian_review` is classified as a first-class session source.

Codex 0.152 response-item fallback token limits and thread-settings owner IDs are retained as normalized metadata. Provider authentication recovery is represented as collapsed lifecycle activity instead of making an otherwise supported rollout partial.

Codex 0.153 token-usage records and root-turn context are retained as collapsed, non-searchable context. Structured asynchronous questions remain attached to their assistant message, while Guardian history inside compaction checkpoints remains available only through the raw record and is never projected into rendered text. Codex 0.153.2 has the same persisted history, protocol, and rollout shapes as 0.153.0 and is the tested compatibility baseline.

Message image and audio attachments are represented only by localized count badges. The transcript does not render attachment URLs, data URIs, ciphertext, or media players; copying a message copies its text only.

Fixtures cover Codex 0.120 and every persisted compatibility boundary from 0.144 through the 0.153.2 baseline, including realtime items, asynchronous delivery and questions, token-usage records, standalone function outputs, guardian review, line-level plan extraction and deduplication, malformed input, source classification, parent/fork metadata, incremental indexing, and plan handoff grouping.

## Following upstream Codex

The declared compatibility baseline is OpenAI Codex tag [`rust-v0.153.2`](https://github.com/openai/codex/tree/rust-v0.153.2). The important boundary is the persisted rollout, not the shape of an internal crate API.

Upstream references for the baseline are:

- [`codex-rs/history/src/lib.rs`](https://github.com/openai/codex/blob/rust-v0.153.2/codex-rs/history/src/lib.rs) for rollout envelopes, response-item harness metadata, compaction payloads, and rollout lines;
- [`codex-rs/protocol/src/protocol.rs`](https://github.com/openai/codex/blob/rust-v0.153.2/codex-rs/protocol/src/protocol.rs) for session metadata, events, token-usage records, realtime items, and inter-agent communication;
- [`codex-rs/protocol/src/items.rs`](https://github.com/openai/codex/blob/rust-v0.153.2/codex-rs/protocol/src/items.rs) for durable `TurnItem` families and asynchronous question metadata;
- [`codex-rs/protocol/src/models.rs`](https://github.com/openai/codex/blob/rust-v0.153.2/codex-rs/protocol/src/models.rs) for response items, standalone function outputs, and structured attachment content;
- [`codex-rs/protocol/src/security_risk.rs`](https://github.com/openai/codex/blob/rust-v0.153.2/codex-rs/protocol/src/security_risk.rs) for durable security-risk snapshots;
- [`codex-rs/utils/stream-parser/src/proposed_plan.rs`](https://github.com/openai/codex/blob/rust-v0.153.2/codex-rs/utils/stream-parser/src/proposed_plan.rs) for line-level plan extraction semantics;
- [`codex-rs/rollout/src/recorder.rs`](https://github.com/openai/codex/blob/rust-v0.153.2/codex-rs/rollout/src/recorder.rs) for `RolloutRecorder`, ordinals, and resume behavior;
- [`codex-rs/state/src/runtime.rs`](https://github.com/openai/codex/blob/rust-v0.153.2/codex-rs/state/src/runtime.rs) for the state boundary that the viewer must not open;
- [`codex-rs/file-watcher/src/lib.rs`](https://github.com/openai/codex/blob/rust-v0.153.2/codex-rs/file-watcher/src/lib.rs) for comparison with the viewer's narrower rollout-root watcher.

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

On first start it creates `~/.agents-viewer/config.toml` and a generated `schema.json`, builds the lightweight session catalog, and prints its URL once. The viewer never opens a browser. Full transcript content is not synchronized until Start live sync is pressed on a conversation page.

```text
agents-viewer [OPTIONS]

--config PATH           configuration file; a missing file is created with defaults
-h, --help              print help
-V, --version           print version
```

Application settings live in TOML:

```toml
#:schema ./schema.json
source_dir = "~/.codex"
data_dir = "~/.agents-viewer"
listen = "127.0.0.1:4747"
password = ""
max_event_bytes = "32MiB"
log_level = "warn"
```

The former `initial_index_days` setting is obsolete. If it remains in an existing TOML file, the loader prints a warning and ignores only that exact key; all other unknown keys remain errors. Changing `max_event_bytes` invalidates content checkpoints automatically. Catalog data stays available and the next explicit live-sync lease rebuilds the affected snapshot with the new bound.

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

Only one process may hold a source/cache lock. The current schema does not migrate older Viewer caches. To reset a disposable cache, stop the viewer, verify the exact source namespace, delete only that namespace, and restart. Rollout files are never part of reset. Corrupt or incompatible cache families are preserved before a replacement is built.

## UI controls

The top bar contains navigation, global search, and Settings. Settings stages session filters, normalized conversation-display types, language, theme, and the optional search shortcut, then applies them together. Received replies, sent messages, `request_user_input`, plans, reasoning, and exec commands are selected by default; the first four are required, while every other normalized message, tool, diagnostic, context, lifecycle, and unknown type remains available as an individual choice. Display choices are remembered in the browser, older preferences are canonicalized to restore required plans, and Reset restores the six defaults. The desktop session sidebar can be resized or collapsed; both its width and collapsed state are remembered. Entry-level Inspect actions open the inspector as a desktop panel or a responsive sheet. The desktop inspector stays between 300 and 600 pixels and cannot be collapsed by dragging; responsive layouts continue to use a sheet. Search defaults to catalog and hydrated user/assistant messages; “Search all activity types” also includes plans, reasoning, commands, results, context, and other hydrated technical entries, and that choice is remembered separately.

Start live sync is intentionally per conversation page and is never remembered as a global preference. Stop releases it immediately. Route changes and page teardown abort the streaming request, so browsing a different session cannot leave a hidden content synchronizer running.

Conversation navigation opens at the latest page and follows appended entries while the viewport remains at the true bottom. Floating controls jump to the first or latest message without downloading the full transcript.

Keyboard controls:

- `Ctrl/Cmd+K` or `/`: open global search.
- `Ctrl+Shift+F`: optionally open global search when enabled in Settings; disabled by default.
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

The development Playwright fixtures locate `target/debug/agents-viewer` through
Cargo metadata; the Nix check instead runs the packaged binary supplied through
`AGENTS_VIEWER_E2E_BINARY`. Running the Web package's `e2e` script directly does
not rebuild either development artifact and can silently test an old Web
bundle. Always use `just agents-viewer-e2e` after frontend changes; it installs
the locked Web dependencies, builds `web/dist`, recompiles the debug binary with
`embedded-ui`, and only then starts Playwright. Pass Playwright arguments
through the same recipe for focused runs:

```bash
just -- agents-viewer-e2e --grep "preserves the reader position"
```

Viewer E2E is provided only on Linux through the named Nix shell and flake
check. The shell supplies the locked `pkgs.chromium` executable through the
required absolute `PLAYWRIGHT_NIX_BROWSER_PATH` and disables Playwright browser
downloads. Tests never search `PATH`, connect to an existing browser, or fall
back to a host browser. Browser profiles, screenshots, traces, databases, build
output, and other runtime artifacts stay in ignored or temporary locations.

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
- stale content after changing `max_event_bytes`: open the conversation and start live sync; its snapshot rebuilds automatically.
- no FTS5: use the Nix package or another build with bundled SQLite and FTS5.
- no E2E browser: enter `nix develop .#agents-viewer`; E2E is intentionally unavailable from non-Linux shells and without the Nix-provided absolute browser path.
- stale UI during E2E: use `just agents-viewer-e2e`, not the Web package's `e2e` script directly; the Just recipe rebuilds the compile-time embedded bundle first.
- UI/API version mismatch: rebuild the embedded binary with `just agents-viewer-build`.
- generated binding drift: change the Rust DTO, run `just agents-viewer-generate`,
  review the public contract diff, then run `just agents-viewer-generate-check`;
  never hand-edit `web/src/generated/api.ts`.

All SQLite recovery commands apply only to Viewer-owned derived caches. Stop
the process and verify the exact source namespace before removing one; never
delete or modify rollout JSONL. A successful `just agents-viewer-verify`
followed by `just agents-viewer-acceptance-large` is the complete local
recovery proof for source, generated, package, bounded-memory, and Linux
read-only syscall boundaries.
