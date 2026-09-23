# Codex pure-patch workspace

This directory maintains one ordered, reproducible patch set over a pinned
[`openai/codex`](https://github.com/openai/codex) revision. The applied
upstream source tree is runtime state and is never tracked.

## Authority and layout

Two TOML files define the maintenance product:

- [`upstream.toml`](upstream.toml) pins the repository URL, tag, peeled commit,
  ignored worktree, generator sequence, regression commands, and cumulative
  Cargo validation command.
- [`series.toml`](series.toml) defines patch order, intent, behavior, exclusive
  source/generated ownership, and focused behavior tests.

The active patch directory is
`patches/rust-v0.154.0/`. Its contents must exactly match `series.toml`; no
fallback manifest or historical patch directory participates in maintenance.

```text
codex/
  upstream.toml
  series.toml
  patches/rust-v0.154.0/
    0001-provider-network-config.patch
    0002-typed-server-overload.patch
    0003-server-overload-recovery.patch
    0004-failure-hook-contract.patch
    0005-failure-hook-integration.patch
    0006-request-user-input-policy.patch
    0007-generated-contracts.patch
  scripts/
  tests/
.work/codex/rust-v0.154.0/src/  # ignored upstream checkout
```

All public maintenance recipes enter `nix develop .#codex` before invoking
Python, Cargo, or an upstream generator. Use those root Just recipes for daily
work; do not substitute host tools.

## Maintained series

| Order | Patch | Responsibility |
| --- | --- | --- |
| 1 | `0001-provider-network-config.patch` | Shared provider network settings, validated timeout defaults, overload policy, request-input policy, and thread-config transport. |
| 2 | `0002-typed-server-overload.patch` | One strict overload classifier across HTTP, SSE, WebSocket, protocol errors, and retry advice. |
| 3 | `0003-server-overload-recovery.patch` | Producer-specific bounded or persistent recovery with cancellable delays and preserved Guardian/Luna boundaries. |
| 4 | `0004-failure-hook-contract.patch` | Strict event-specific RequestError and AbnormalStop wire, matcher, parser, and source-schema contracts. |
| 5 | `0005-failure-hook-integration.patch` | Bounded/coalescing RequestError dispatch, synchronous AbnormalStop handling, receiver completion, and active-goal continuation. |
| 6 | `0006-request-user-input-policy.patch` | Default blocking at the Session emission boundary while preserving tool exposure, approval, yolo, and source intent. |
| 7 | `0007-generated-contracts.patch` | Reproducible protobuf, config, app-server, hook-schema, and snapshot outputs only. |

Every changed upstream path has exactly one owner. Patches 1–6 own handwritten
source. Patch 7 has no handwritten owner and is the only patch allowed to
contain generated files or binary hunks.

## Generated ownership

The generator sequence is authoritative in `upstream.toml` and runs in this
order:

1. thread-config protobuf binding;
2. config schema;
3. stable app-server fixtures;
4. experimental app-server fixtures;
5. hook schemas and declared UI snapshots.

The workflow runs the complete sequence twice and requires the second pass to
produce the same bytes and digest. Compressed app-server fixtures must also
decompress, parse as JSON, and have a reviewed canonical semantic diff.

Never edit `Cargo.lock`, generated JSON/TypeScript/Rust, snapshots, or zstd
patch hunks by hand. Change authoritative handwritten source in the ignored
checkout and let `codex-refresh` regenerate the declared outputs. The applied
tree's `Cargo.lock` and every `Cargo.toml` must remain byte-identical to the
pinned revision.

## Daily workflow

Validate the manifests and cumulative application without changing the
ignored checkout:

```sh
just codex-manifest-check
just codex-apply-check
```

Create or update the exact pinned checkout, then apply the installed series to
a clean checkout when an editable applied tree is needed:

```sh
just codex-fetch
just codex-apply
```

Run both validation paths:

```sh
just codex-test
just codex-build
```

`codex-test` clones a disposable clean checkout from the pinned revision,
applies all seven patches cumulatively, runs the generator sequence twice,
checks generated drift and compressed fixtures, then runs the manifest Cargo,
focused, consumer, and regression commands. `codex-build` runs the cumulative
Cargo validation and regressions against the already-applied ignored checkout.

## Changing the patch set

1. Run `just codex-manifest-check`, `just codex-fetch`, and
   `just codex-apply-check`. Confirm the ignored checkout is at the exact
   revision and its real Git index is clean.
2. Apply the installed series to a clean checkout with `just codex-apply`, then
   edit only `.work/codex/rust-v0.154.0/src/`. Keep each changed path inside
   exactly one ownership boundary in `series.toml`.
3. Run focused behavior tests through `nix develop .#codex --command ...`.
   Do not add dependencies or change dependency manifests as a side effect.
4. Run `just codex-refresh-dry-run`. Inspect its ignored candidate, patch
   hashes, numstat, binary ownership, clean replay, and generated semantic
   evidence. Remove only the exact reported candidate after review before
   starting another refresh operation.
5. Run `just codex-refresh`. It repeats generation and validation before an
   atomic directory exchange. Then run `just codex-manifest-check`,
   `just codex-apply-check`, `just codex-test`, and `just codex-build`.
6. Review the complete tracked diff and repository status. Stage only explicit
   task paths.

The refresh workflow uses a temporary Git index and ignored maintenance
workspaces. It does not update the real upstream index and does not install a
candidate unless cumulative apply, generation, Cargo, focused behavior, and
consumer validation are all green.

## Advancing upstream

An upgrade is an explicit manifest change:

1. Review the target upstream tag and resolve its peeled commit.
2. Update `ref`, `revision`, and `worktree` together in `upstream.toml`.
3. Create only the new active patch directory and remove the replaced version
   directory in the same task.
4. Port the seven `series.toml` responsibilities in order, changing ownership
   only when the source boundary genuinely changed.
5. Run two-pass generation, clean-base replay, focused/consumer tests, and the
   applied-worktree Cargo gate. Confirm dependency files are unchanged.
6. Perform the package build only after the package-preflight receipt is
   green:

   ```sh
   nix build --no-link --print-out-paths --accept-flake-config .#codex
   ```

Do not infer an upgrade from a floating branch or newest-tag query. A tag name
never substitutes for the pinned commit check.

## Failure recovery

The tools fail closed and report the exact ignored candidate, backup, or
maintenance workspace involved.

- Manifest, ownership, revision, or cumulative-apply failure: correct the
  authoritative TOML or handwritten source; do not edit a patch to bypass the
  gate.
- Generator, Cargo, focused-test, or consumer-test failure: fix the ignored
  checkout and rerun refresh from the first generator. Do not reuse partial
  generated output.
- Preserved candidate: inspect its receipt and files, verify the exact ignored
  real path and contents, then remove only that candidate before retrying.
- Atomic exchange failure: retain the reported candidate and backup. Verify
  both exact paths before restoring the reported backup; never delete both.
- Interrupted refresh: treat the installed directory as valid only if the
  receipt and installed hashes agree. Otherwise follow the reported recovery
  paths and rerun the full operation.
- Generated drift: change the authoritative source or generator and refresh;
  never patch a generated artifact manually.
- Nix download or V8 cache failure: retry through the pinned shell or package
  inputs. Do not install host tools, edit a lockfile, or clear shared caches.

The patched Nix package is a final delivery check, not a development shortcut.
Do not build it until clean replay, two-pass generation, dependency-file
identity, all behavior gates, and the package-preflight receipt are green.
