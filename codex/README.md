# Codex pure-patch workspace

This directory maintains one ordered, reproducible patch set over a pinned
[`openai/codex`](https://github.com/openai/codex) revision. The applied
upstream checkout is ignored runtime state; only the manifests and patch files
are repository inputs.

## Authority and layout

- [`upstream.toml`](upstream.toml) pins the official URL, tag, peeled commit,
  ignored checkout, direct generator sequence, regression commands, and
  cumulative Cargo validation command.
- [`series.toml`](series.toml) defines patch order, behavior, exclusive
  handwritten/generated ownership, and focused tests.
- `scripts/maintain.py` is the sole maintenance CLI. `scripts/workflow.py`
  contains its shared implementation.

The active patch directory is selected only by `upstream.toml`'s `ref`. A
future upgrade may retain an older revision directory for history, but Nix and
the maintenance CLI ignore every directory other than the active one.

```text
codex/
  upstream.toml
  series.toml
  patches/rust-v0.154.0/
    0001-runtime-provider-config.patch
    0002-typed-server-overload.patch
    0003-failure-hook-contract.patch
    0004-failure-hook-runtime.patch
    0005-model-request-recovery.patch
    0006-request-user-input-policy.patch
    0007-generated-contracts.patch
  scripts/
    maintain.py
    workflow.py
  tests/
.work/codex/rust-v0.154.0/src/  # ignored upstream checkout
```

Run public recipes at the repository root. Each recipe enters
`nix develop .#codex` itself and invokes the bottom-level Python tool; never
run Just from inside a Nix development shell.

## Maintained series

| Order | Patch | Responsibility |
| --- | --- | --- |
| 1 | `0001-runtime-provider-config.patch` | Provider endpoint, timeout, overload, and request-input defaults plus stable thread-config transport. |
| 2 | `0002-typed-server-overload.patch` | One strict overload classifier across HTTP, SSE, WebSocket, protocol, CLI, and debug boundaries. |
| 3 | `0003-failure-hook-contract.patch` | Strict event-specific RequestError and AbnormalStop wire, matcher, parser, and schema contracts. |
| 4 | `0004-failure-hook-runtime.patch` | Bounded/coalescing RequestError dispatch, synchronous terminal AbnormalStop handling, and active-goal lifecycle integration. |
| 5 | `0005-model-request-recovery.patch` | Producer-specific bounded or persistent recovery, cancellable waits, and failure-hook ordering. |
| 6 | `0006-request-user-input-policy.patch` | Session-boundary request-input blocking policy, independent of approval, MCP, and yolo behavior. |
| 7 | `0007-generated-contracts.patch` | Cargo's normalized lock plus reproducible protobuf, config, app-server, hook-schema, snapshot, and zstd outputs only. |

Every changed upstream path has exactly one owner. Patches 1–6 own handwritten
source. The final patch has no handwritten owner and is the only patch allowed
to contain generated files or binary hunks. `RecoveryDecision` remains in the
producer/recovery layer; the hook runtime consumes explicit next-action and
retry-budget fields, so patches 3 and 4 do not depend on patch 5.

## Behavioral differences from upstream

`request_user_input_policy` defaults to `always_block`. When omitted, every
emitted request-user-input call is blocking. `preserve_source` restores the
producer's original blocking choice. This policy is enforced once at the
Session emission boundary and is unrelated to `approval_policy`, installed MCP
servers or skills, and `--yolo`.

For model-request failures, the producer first chooses retry, defer, fallback,
or stop. RequestError runs immediately after that decision and before any
retry/defer wait. AbnormalStop runs only for a final stop. Cancelling a wait
does not start another request and does not emit AbnormalStop. Fallback and
terminal tests lock the observable RequestError-before-AbnormalStop order.

## Generated ownership and Cargo.lock

`upstream.toml` directly declares every generator; patches never modify an
upstream generator script or upstream Justfile for repository maintenance. The
workflow runs the full sequence twice and requires identical bytes and digest.
Compressed app-server fixtures must decompress and parse as JSON.

Cargo normalizes `Cargo.lock` offline from the pinned dependency resolution,
then every later Cargo generator and gate uses `--locked`. The final generated
patch may change only local workspace package versions from `0.0.0` to the
pinned release version. External sources, checksums, dependency edges, package
sets, and manifests must remain identical.

The Nix package reuses the pinned external vendor payload while deriving its
Cargo lock check from that generated patch. Package builds therefore validate
the normalized lock without fetching or accepting a different dependency graph.

Never edit `Cargo.lock`, generated Rust/JSON/TypeScript, snapshots, or zstd
hunks by hand. Change authoritative source in the ignored checkout and run the
refresh workflow.

## Public workflow

The six durable entrypoints are:

```sh
just codex-fetch
just codex-apply-check
just codex-apply
just codex-refresh-dry-run
just codex-refresh
just codex-test
```

- `fetch` creates a missing checkout at the exact commit. An existing checkout
  is only validated; dirty work is reported and never overwritten.
- `apply --check` validates cumulative binary application in a disposable
  clone. `apply` requires a clean pinned checkout, applies in place, and keeps
  its `.git` directory and official origin URL unchanged.
- `refresh --dry-run` builds and fully verifies a temporary candidate without
  changing tracked files. Normal `refresh` additionally requires the active
  patch directory to be clean in Git, then replaces only manifest-known patch
  files. Unknown files are rejected, and existing patch contents are
  fingerprinted so a concurrent edit cannot be overwritten during validation.
- `verify` (the `codex-test` recipe) replays the installed series from the
  fixed revision, validates zstd and Cargo.lock semantics, runs generation
  twice, and executes the manifest's cumulative, focused, and regression
  gates.

## Changing the patch set

1. Run `just codex-fetch` and `just codex-apply-check`; confirm the checkout is
   at the exact revision, its real index is clean, and origin is the official
   URL.
2. Run `just codex-apply` when starting from a clean pinned checkout. Edit only
   the ignored checkout and keep every path inside one `series.toml` owner.
3. Run focused diagnostics through the matching Codex Nix shell, invoking the
   bottom-level Cargo, Python, or generator command directly.
4. Run `just codex-refresh-dry-run`, review its hashes, ownership, binary
   classification, replay, double-generation digest, and test evidence.
5. Run `just codex-refresh`, then `just codex-apply-check` and
   `just codex-test`.
6. Review the full patch contents and root repository diff. Stage only explicit
   task paths.

No alternate lock, receipt, backup tree, checkout exchange, or fault-injection
state exists. Temporary candidates are ignored and removed automatically; the
outer Git repository is the recovery boundary for installed patch files.

## Advancing upstream

An upgrade is an explicit manifest change:

1. Verify the target tag and peeled commit, then update `url`, `ref`,
   `revision`, and `worktree` coherently.
2. Create the new active patch directory. Retain an older revision directory
   only as inactive history; it must not participate in current application.
3. Port the seven semantic responsibilities in order and update ownership only
   where the source boundary genuinely changed.
4. Regenerate Cargo.lock through Cargo offline, replay the full series, run the
   two-pass generators and all behavior gates, and inspect the semantic zstd
   diff.
5. Only after those checks, validate the package:

   ```sh
   nix build --no-link --accept-flake-config .#codex
   ```

Do not infer an upgrade from a floating branch or newest-tag query.

## Failure recovery

- Manifest, ownership, revision, or apply failure: correct the manifest or
  handwritten source; do not edit a patch to bypass the gate.
- Generator or behavior failure: classify it, fix the ignored checkout, and
  rerun the narrow failing command before restarting the full workflow.
- Interrupted refresh: no candidate is installed until all validation passes;
  rerun refresh. Git recovers any previously installed tracked patch content.
- Generated drift: fix authoritative source or the manifest generator; never
  hand-edit generated output or Cargo.lock.
- Nix download, cache, or container sandbox failure: record the environment
  limitation and retry through pinned inputs. Do not install host tools or add
  a project workaround for the container.
