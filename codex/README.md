# Codex pure-patch workspace

Maintenance is paused at the currently pinned upstream revision. The patch set,
maintenance commands, and Nix package remain available; the Codex CI workflow
runs only on manual dispatch.

This directory maintains an ordered patch set over one pinned
[`openai/codex`](https://github.com/openai/codex) revision. The upstream checkout
and build cache are ignored runtime state; the durable inputs are
[`upstream.toml`](upstream.toml), [`series.toml`](series.toml), the maintenance
scripts, and the active patch directory.

`upstream.toml` pins the official repository and exact commit, then declares
the upstream generators and focused integration checks. `series.toml` declares
patch order, intent, exclusive path ownership, and patch-specific tests.
`scripts/maintain.py` is the sole maintenance CLI.

Run every public recipe from the repository root. Development recipes enter
`nix develop .#codex` themselves; do not nest Just inside that shell.

## Installing and running

The package and app are named `codex-patched`. Install it alongside the upstream
package from llm-agents.nix, or run the flake app directly:

```sh
nix profile add .#codex-patched github:numtide/llm-agents.nix#codex
nix run .#codex-patched -- --help
```

Use `codex-patched` for this patch set and `codex` for the llm-agents.nix package.
The overlay exposes `pkgs.agents-misc.codex-patched`; the default package and app
also select the patched CLI. The former `codex` package, app, and overlay entry
have been renamed, while `devShells.codex` and the `just codex-*` recipes retain
their names.

The public package exports only `codex-patched` and its Bash, Fish, and Zsh
completions. Helper executables and resources remain in its internal package.
Both CLIs share `~/.codex` by default and honor `CODEX_HOME`; no configuration,
login, or session migration is needed. Help and version text retain the
upstream Codex naming.

## Behavior carried by the patches

All downstream settings are optional and live under one table:

```toml
[codex_patch]
force_request_user_input_blocking = true
responses_compact_timeout_ms = 300000
usage_limit_recovery_limit = 2
transient_request_recovery_limit = 3
server_overload_recovery_limit = "unlimited"
recovery_max_wait_ms = 120000
```

Without recovery limits, retry behavior keeps the pinned upstream default.
`force_request_user_input_blocking = true` changes blocking
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
handled by the transient category. An actual `Retry-After` header takes
precedence over the exact `error.retry_after_ms` field; these take precedence
over an existing upstream stream retry delay. Downstream
recovery uses `min(advice, recovery_max_wait_ms)`, or
`min(local_backoff, recovery_max_wait_ms)` when advice is absent. The cap is a
positive integer in milliseconds and defaults to 120000 (two minutes). For
example, a 10000 cap reduces both a server-requested two-minute wait and a local
30-second backoff to ten seconds. A zero server delay remains zero. The same
rule applies to typed overloads, ordinary transient errors and recoverable
usage limits; it does not alter lower HTTP-client retries. Setting only the cap
does not enable recovery. Every wait remains cancellable.

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
endpoint details. Recovery delivery uses a FIFO queue bounded to 64 pending or
executing notifications and never blocks the model request. Each notification
retains its slot until its handlers finish, including handlers configured with
`async: true`. A full queue drops that notification with an internal trace,
not a user warning. Terminal hooks use normal hook completion semantics.
Matchers are intentionally ignored for these two event types.

With native hooks enabled (the pinned revision's default), add a command to
`~/.codex/hooks.json`:

```json
{
  "hooks": {
    "ModelRequestRecovery": [
      {
        "hooks": [
          { "type": "command", "command": "python3 /absolute/path/observe.py" }
        ]
      }
    ],
    "ModelRequestFailed": [
      {
        "hooks": [
          { "type": "command", "command": "python3 /absolute/path/observe.py" }
        ]
      }
    ]
  }
}
```

Review and trust the hook through Codex's normal hook trust flow. The command
reads one JSON object from stdin; a recovery example is:

```json
{
  "sessionId": "00000000-0000-4000-8000-000000000001",
  "turnId": "turn-1",
  "transcriptPath": null,
  "cwd": "/workspace/example",
  "hookEventName": "ModelRequestRecovery",
  "model": "example-model",
  "provider": "example-provider",
  "operation": "sampling",
  "attempt": 1,
  "nextAction": "retry",
  "error": {
    "category": "serverOverloaded",
    "httpStatus": 503,
    "message": "busy"
  }
}
```

`ModelRequestFailed` omits `nextAction`. The optional
[hook helpers](../tools/codex-hooks/README.md) consume this same payload.

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
- Configured recovery now caps every wait at two minutes by default. Set
  `codex_patch.recovery_max_wait_ms` explicitly if a different cap is needed.
- Split `RequestError` consumers between `ModelRequestRecovery` and
  `ModelRequestFailed`. Replace an `AbnormalStop` consumer with
  `ModelRequestFailed` only when its actual concern was a terminal model
  request; the new hook does not claim that the session or goal stopped.

## Generated files and Cargo.lock

Generators are invoked directly from `upstream.toml`, never through upstream
Just recipes. Refresh and verify run the sequence twice and require byte-stable
output. The sequence owns the config schema, app-server schemas and compressed
exports, hook input schemas, TUI snapshots, and Cargo's normalized
lockfile.

The Cargo lock contract permits only local workspace package versions to move
from upstream's development version to the pinned release version. External
packages, sources, checksums, dependency edges, metadata, and the local package
set must remain unchanged. Never hand-edit generated files or `Cargo.lock`.
If the next upstream release already has correct workspace versions, an
unchanged lockfile is valid and the Nix consumer uses its original vendor lock.

The first Cargo command may access the network to obtain missing dependencies
and normalize workspace versions. Later commands use the resulting locked
resolution. `fetch` only obtains Git source; it does not populate Cargo's cache.
A first run or upstream upgrade therefore needs registry and Git dependency
access. Do not warm this cache by building `.#codex-patched` or the flake's checks.

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
- `codex-check <patch>` copies the current handwritten editing changes into the
  disposable checkout, regenerates its artifacts, and runs that patch's tests;
  `codex-check integration` runs the focused cross-crate compile boundary.
  Both check the editing source and leave that checkout unchanged.
  Each declared `cargo test` selection is also listed after execution and a
  zero-test match is rejected instead of being treated as a passing check.
- `refresh` copies handwritten editing changes into the fixed maintenance
  checkout, discards stale edits to declared generated paths, runs generators
  twice, partitions by declared ownership, replays the candidate, validates
  digests and Cargo.lock semantics, then installs a recoverable update to the patch
  directory. It does not run the Rust test matrix. `--dry-run` performs the
  same candidate work without installing patch files.
  Repeated exports need no intervening commit; an identical export succeeds.
  If the editing checkout changes during generation, rerun refresh with the
  latest source; the stale candidate is rejected before installation.
  Both modes reject patch files manually changed since the last successful
  export (or since the clean Git baseline). Preserve those changes and port
  their intent into the editing source before restoring the recognized baseline.
- `codex-test` first runs the maintenance unit tests, then replays the installed
  series, verifies two-pass generation has no drift, and runs all focused patch
  and integration commands.
- `codex-final-nix` is the only workflow step that builds `.#codex-patched`; use it only
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
4. Run `just codex-refresh`. Use `just codex-refresh-dry-run` only when a preview
   is useful; it repeats generation and replay, so it is not a required gate.
5. Run `just codex-test`, inspect every patch and the outer repository diff,
   then run `just codex-final-nix` as the final packaging boundary.

To advance upstream:

1. Preserve the previous revision's patch directory and finish or save its
   editing changes. Confirm the target tag's peeled commit, for example with
   `nix develop .#codex --command git ls-remote https://github.com/openai/codex 'refs/tags/rust-vX.Y.Z*'`.
2. Update `ref`, `revision`, and `worktree` together in `upstream.toml` (and `url`
   only if the official source changes). Use a new worktree and patch directory.
   `just codex-fetch` verifies the fetched tag against the pinned commit.
3. Compare each patch's intent with the new upstream code. Port the remaining
   behavior into the new editing checkout in series order; remove behavior
   upstream already supplies. Update ownership for moved files and remove or
   renumber entries whose whole intent has been absorbed. Do not copy old
   generated files into the new revision.
4. Check generator entrypoints, schema locations, and test names in the new
   source, then update both manifests. Once the dependent handwritten changes
   compile, use `just codex-check <patch>` to check each behavior; this also works
   before exporting the first patch set.
   Missing or zero-match test selectors fail visibly.
5. Run `just codex-refresh` and `just codex-test`. Review all handwritten,
   generated, and Cargo.lock changes. External dependency changes require
   reviewing the Nix base/vendor input instead of relaxing the lock guard.
6. Run `just codex-final-nix` only after these checks pass. Application and Nix
   consumption select the active `ref`; the previous directory remains history.

## Failure recovery

- A manifest, ownership, or apply failure is fixed in authoritative source or
  the manifest; do not edit a patch to bypass the check.
- A failing Rust check should be rerun through its narrow `codex-check`
  selector before the full verification command.
- Refresh records a receipt immediately before patch installation. Rerunning
  refresh completes or rolls back a recognized interrupted install; ambiguous
  external edits are preserved and reported. Recovery, preflight and install
  share the workspace lock. Backups and last-export hashes live in the ignored
  maintenance directory; retain them until an interrupted operation is resolved.
- Generator drift is fixed in handwritten source or the declared generator,
  never in generated output.
- Environment or cache failures are diagnosed inside `nix develop .#codex`;
  do not install host tools or replace the pinned workflow.
