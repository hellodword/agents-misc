# Codex pure-patch workspace

This directory maintains a reproducible patch set over one pinned revision of
[`openai/codex`](https://github.com/openai/codex). The patched upstream source
tree is never committed.

## Authority and layout

Two TOML files are the only maintenance manifests:

- [`upstream.toml`](upstream.toml) pins the repository URL, tag, peeled commit,
  ignored worktree, generator commands, mandatory regression commands, and
  cumulative Cargo validation.
- [`series.toml`](series.toml) defines patch order, intent, behavior, exclusive
  source/generated ownership, and focused tests.

The current patch files live below `patches/<upstream-ref>/`. Patch order must
come from `series.toml`; there is no YAML manifest, text `series` file, ref CLI
argument, or historical patch-directory fallback.

```text
codex/
  upstream.toml
  series.toml
  patches/<upstream-ref>/
    0001-*.patch
    ...
  scripts/
  tests/
.work/codex/<upstream-ref>/src/  # ignored checkout
```

All human-facing commands are root Just recipes. They enter the pinned
`codex` development shell before invoking Python, Cargo, or upstream
generators. Do not run repository maintenance with host Python or Cargo.

## Maintained series

The following order mirrors `series.toml`. The manifest remains authoritative
for exact file ownership and commands.

| Order | Patch                                 | Intent and affected behavior                                                                                                     | Focused validation                                    |
| ----- | ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| 1     | `0001-provider-network-config.patch`  | Make the OpenAI sampling/compact endpoint and compact timeout explicit, validated, and precedence-aware.                         | Provider config plus compact timeout behavior.        |
| 2     | `0002-failure-hook-contract.patch`    | Define stable camelCase `RequestError` and `AbnormalStop` payload/output contracts with typed error categories.                  | Hook contract and schema tests.                       |
| 3     | `0003-failure-hook-integration.patch` | Emit one bounded event for each visible retry, fallback, or stop; serialize request hooks and aggregate abnormal-stop decisions. | Hook engine and core integration tests.               |
| 4     | `0004-terminal-wait.patch`            | Match named terminal rules in order and expose checked decision deadlines and completion states.                                 | Config, unified-exec, and terminal-wait behavior.     |
| 5     | `0005-code-mode-wait-control.patch`   | Give terminal-wait decisions explicit one-use Code Mode leases across protocol, host, and runtime boundaries.                    | Code Mode protocol/runtime/host suites.               |
| 6     | `0006-generated-contracts.patch`      | Own every generated schema, protobuf binding, TypeScript contract, and resolved Cargo graph changed by patches 1–5.              | Re-generation plus config/app-server contract suites. |

There is deliberately no Plan auto-resolution patch. Plan-mode input remains
blocking upstream behavior, and no compatibility alias recreates the removed
patch or its former consumer contract.

## Data flow and generated ownership

```text
upstream.toml pinned commit
  -> ignored exact upstream checkout
  -> series.toml cumulative patch application
  -> manifest-declared generators
  -> manifest-declared Cargo and focused tests
  -> atomic replacement of current patch files
```

The first five patches own handwritten behavior. The sixth patch exclusively
owns generated files and prefixes declared in `series.toml`, including the
patched Cargo lock, configuration schema, hooks schemas, app-server schemas,
and protobuf bindings. Never edit those generated hunks by hand. Change their
authoritative source in the ignored checkout, run the declared generators, and
refresh the series.

Atlas's unpatched historical schema registry is a separate product documented
in the [Codex Config Atlas guide](../tools/codex-config-atlas/README.md). Do not
copy the locally patched schema into that registry.

## Daily commands

Validate the manifests without fetching upstream:

```sh
just codex-manifest-check
```

Fetch the exact pinned revision, check cumulative application, apply it to the
ignored checkout, and run the complete Codex patch gate:

```sh
just codex-fetch
just codex-apply-check
just codex-apply
just codex-test
```

`codex-test` checks cumulative application, runs every declared generator
twice, rejects generated drift, runs the cumulative Cargo validation, and runs
the focused and mandatory regression tests in declared order. The regression
commands protect Plan blocking, Default non-blocking behavior, the pending
request round trip, and TUI timer policy. To run only the cumulative Cargo
command:

```sh
just codex-build
```

Build the patched package or its deterministic Nix checks without creating a
result link:

```sh
nix build --no-link --accept-flake-config .#codex
nix build --no-link --accept-flake-config .#checks.x86_64-linux.codex-patch-contract
nix build --no-link --accept-flake-config .#checks.x86_64-linux.codex-behavior
nix build --no-link --accept-flake-config .#checks.x86_64-linux.codex-generation
nix build --no-link --accept-flake-config .#checks.x86_64-linux.codex-hooks
```

The optional local hook transport is maintained separately in the
[hook helper guide](../tools/codex-hooks/README.md).

## Changing a patch

1. Run `just codex-manifest-check`, `just codex-fetch`, and
   `just codex-apply` from a clean repository worktree.
2. Edit only the ignored worktree declared by `upstream.toml`. Keep every
   changed path inside exactly one patch ownership boundary.
3. Run the narrow upstream tests while inside `nix develop .#codex`, then run
   `just codex-refresh-dry-run`.
4. Review the candidate patch bytes and generator/test report. A dry-run must
   not change the current patch directory or the upstream Git index.
5. Run `just codex-refresh` to atomically install the candidate, then repeat
   the dry-run. The second candidate must be byte-identical.
6. Run `just codex-test` and the relevant Nix check/package builds before
   committing current patch files and manifest changes.

`refresh-patches.py` uses a temporary Git index and a sibling candidate
directory. It applies patches cumulatively, checks ownership after each
boundary, runs generation and validation, and replaces the installed patch
directory only after every gate succeeds.

## Advancing upstream

An upstream upgrade is an explicit manifest change, not a positional CLI
argument:

1. Choose a signed or otherwise reviewed upstream tag and resolve its peeled
   commit through Git.
2. Update `ref`, `revision`, and the ref-derived `worktree` together in
   `upstream.toml`.
3. Rename/create only the current patch directory for that ref; do not retain
   historical consumer directories.
4. Fetch the exact revision and port each `series.toml` responsibility in
   order. Update the manifest only when ownership, intent, or validation
   genuinely changes.
5. Refresh generators, compare the patched schema, and complete `codex-test`,
   the four Codex Nix checks, and the patched package build.

Do not infer a target from a floating branch or a newest-tag query. Do not
accept a revision mismatch merely because the tag name exists.

## Failure recovery

The tools fail before installing partial state and print the exact candidate,
backup, or worktree path needed for recovery.

- Manifest, path ownership, revision, or cumulative apply failure: correct the
  authoritative TOML/source. The patch directory, source tree, temporary
  index, and real upstream index remain unchanged.
- Generator, Cargo, or focused-test failure: fix the ignored checkout and run
  the dry-run again. The current patch directory is still the last validated
  version.
- Atomic install failure: keep the reported sibling backup. If automatic
  restoration also fails, move that exact backup back to the printed patch
  path only after verifying both paths; never delete both copies.
- Generated drift after apply: do not edit the generated patch. Re-run the
  manifest-declared generator in `nix develop .#codex`, then refresh.
- Nix V8 download/cache failure: retry through the pinned Codex shell or
  package inputs. Do not install a host Rust/Node toolchain or rewrite the lock
  manually.

Use `just codex-refresh-dry-run` as the recovery proof: success with identical
candidate hashes demonstrates that the installed patch set is complete,
reproducible, and safe to commit.
