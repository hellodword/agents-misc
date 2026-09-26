# Codex pure-patch workspace

This directory maintains an ordered patch set over one pinned
[`openai/codex`](https://github.com/openai/codex) revision. The upstream checkout
and build cache are ignored runtime state; the durable inputs are
[`upstream.toml`](upstream.toml), [`series.toml`](series.toml), the maintenance
scripts, and the active patch directory.

`upstream.toml` pins the official repository and exact commit, then declares
the upstream generators and focused integration checks. `series.toml` declares
patch order, intent, exclusive path ownership, and patch-specific tests.
`scripts/maintain.py` is the sole maintenance CLI.

Run every public recipe from the repository root. Recipes enter
`nix develop .#codex` themselves; do not nest Just inside that shell.

## Behavior carried by the patches

All downstream settings are optional and live under one table:

```toml
[codex_patch]
force_request_user_input_blocking = true
responses_compact_timeout_ms = 300000
usage_limit_recovery_limit = 2
transient_request_recovery_limit = 3
server_overload_recovery_limit = "unlimited"
```

If the table or an individual value is absent, that behavior keeps the pinned
upstream default. `force_request_user_input_blocking = true` changes blocking
only at the Session emission boundary; missing or `false` preserves the value
chosen by the producer. `responses_compact_timeout_ms` is a positive integer
and applies only to the unary `/responses/compact` call. Without it, that call
retains upstream's `4 × stream_idle_timeout` timeout.

Recovery limits accept either a non-negative integer or the exact string
`"unlimited"`. Zero disables downstream recovery for that category, an integer
allows that many Core recovery rounds, and `"unlimited"` keeps recovering until
success or cancellation. A configured category replaces the producer's Core
budget from its first visible failure; lower HTTP-client retries remain
internal. Budgets are tracked independently by request target and category and
reset when transport fallback changes the target.

The overload classifier is shared by HTTP, raw SSE errors, and WebSocket
errors. `server_is_overloaded` is accepted when the status is absent or 503;
`slow_down` is accepted when the status is absent, 429, or the legacy 503.
Contradictory code/status pairs are ordinary errors, while a bare 429 or 503 is
handled by the transient category. Provider advice means an actual
`Retry-After` header or the exact `error.retry_after_ms` field. Downstream
recovery does not truncate that advice; long waits are chunked so cancellation
remains responsive. Locally computed backoff remains capped at one minute.

Configured recovery covers sampling plus local, legacy remote, and v2 remote
compaction. It deliberately excludes guardian, prewarm, realtime, voice, and
memory requests. Recoverable categories are `UsageLimitReached`, typed server
overload, and the narrowly classified transient transport, I/O, timeout, 408,
rate-limit/429, 5xx, or internal failures. It does not extend recovery to tool
or runtime failures, install compacted history before success, or issue another
request after cancellation.

`ModelRequestRecovery` reports a Core decision to retry or fall back;
`ModelRequestFailed` reports the terminal visible failure. They are
observational model-request hooks, not `AbnormalStop`: a provider failure does
not imply a session or goal lifecycle stop. Both expose the stable request
identity, operation, attempt, and normalized error. Recovery adds only the
next action; hook consumers do not receive wait duration, advice, budget, or
endpoint details. Recovery delivery uses a bounded FIFO queue and never blocks
the model request. A full queue drops that notification with an internal trace,
not a user warning. Terminal hooks use normal hook completion semantics.
Matchers are intentionally ignored for these two event types.

The current series separates configuration, overload typing, hook contracts,
request recovery, forced request-input blocking, and generated contracts. Every
changed upstream path has exactly one owner. Only the final entry may own
generated files; it contains no handwritten source.

### Migration from the previous series

- Replace `request_user_input_policy = "always_block"` with
  `codex_patch.force_request_user_input_blocking = true`; omit the new value or
  set it to `false` for the old `preserve_source` behavior.
- Move provider `compact_request_timeout_ms` to the global
  `codex_patch.responses_compact_timeout_ms` setting.
- Replace `server_overload_policy = "persistent"` with
  `codex_patch.server_overload_recovery_limit = "unlimited"`; omit the new
  value to retain upstream's bounded behavior.
- Split `RequestError` consumers between `ModelRequestRecovery` and
  `ModelRequestFailed`. Replace an `AbnormalStop` consumer with
  `ModelRequestFailed` only when its actual concern was a terminal model
  request; the new hook does not claim that the session or goal stopped.

## Generated files and Cargo.lock

Generators are invoked directly from `upstream.toml`, never through upstream
Just recipes. Refresh and verify run the sequence twice and require byte-stable
output. The sequence owns the config schema, app-server schemas and compressed
exports, hook input schemas, TUI snapshots, and Cargo's offline-normalized
lockfile.

The Cargo lock contract permits only local workspace package versions to move
from upstream's development version to the pinned release version. External
packages, sources, checksums, dependency edges, metadata, and the local package
set must remain unchanged. Never hand-edit generated files or `Cargo.lock`.

## Commands and cache behavior

```sh
just codex-fetch
just codex-apply-check
just codex-apply
just codex-check 0004
just codex-check integration
just codex-refresh-dry-run
just codex-refresh
just codex-test
just codex-final-nix
```

- `fetch` creates or validates the exact upstream checkout without overwriting
  local edits.
- `apply-check` replays all patches in the fixed maintenance checkout without
  touching the editing checkout. `apply` additionally requires a clean editing
  checkout and applies there.
- `codex-check <patch>` runs only that patch's declared tests;
  `codex-check integration` runs the focused cross-crate compile boundary.
  Each declared `cargo test` selection is also listed after execution and a
  zero-test match is rejected instead of being treated as a passing check.
- `refresh` copies handwritten editing changes into the fixed maintenance
  checkout, discards stale edits to declared generated paths, runs generators
  twice, partitions by declared ownership, replays the candidate, validates
  digests and Cargo.lock semantics, then atomically updates only the patch
  directory. It does not run the Rust test matrix. `--dry-run` performs the
  same candidate work without installing patch files.
- `codex-test` first runs the maintenance unit tests, then replays the installed
  series, verifies two-pass generation has no drift, and runs all focused patch
  and integration commands.
- `codex-final-nix` is the only workflow step that builds `.#codex`; use it only
  after development-shell validation passes. The Nix package is a consumer of
  the installed patch set, not part of refresh.

The maintenance checkout is fixed at
`.work/codex/<ref>/maintenance/test-workspace/src` and protected by a lock.
Cargo artifacts are shared at `.work/codex/<ref>/cargo-target`; incremental
compilation is enabled because the maintenance checkout path is stable, so
repeated checks, refreshes, and verification reuse both dependencies and local
crate work. Generator rewrites that reproduce an identical tree keep prior file
and directory modification times, avoiding false Cargo invalidation during the
second determinism pass. Refresh resets only the disposable source checkout; it
does not remove that target directory.

## Editing and upgrading

For an ordinary change:

1. Run `just codex-fetch`, then apply the installed series to a clean editing
   checkout if needed.
2. Edit only the ignored checkout and keep each changed path within one
   `series.toml` owner.
3. Run the narrow `just codex-check <patch>` selector while iterating.
4. Run `just codex-refresh-dry-run`, then `just codex-refresh`.
5. Run `just codex-test`, inspect every patch and the outer repository diff,
   then run `just codex-final-nix` as the final packaging boundary.

To advance upstream, verify the target tag and peeled commit, update `url`,
`ref`, `revision`, and `worktree` coherently, then port the semantic boundaries
in series order. Change ownership only where upstream moved a real source
boundary. Regenerate through the declared tools, replay, run focused checks,
and inspect generated and lockfile changes before the final Nix package build.
An old patch directory may remain as inactive history, but application and Nix
consumption select only the active `ref`.

## Failure recovery

- A manifest, ownership, or apply failure is fixed in authoritative source or
  the manifest; do not edit a patch to bypass the check.
- A failing Rust check should be rerun through its narrow `codex-check`
  selector before the full verification command.
- Refresh records a receipt immediately before patch installation. Rerunning
  refresh completes or rolls back a recognized interrupted install; ambiguous
  external edits are preserved and reported.
- Generator drift is fixed in handwritten source or the declared generator,
  never in generated output.
- Environment or cache failures are diagnosed inside `nix develop .#codex`;
  do not install host tools or replace the pinned workflow.
