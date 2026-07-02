# Codex Pure Patch Workspace

This directory maintains local patches for `https://github.com/openai/codex`
without committing a patched upstream source tree.

## Layout

```text
codex/
  upstream.yaml
  patches/
    rust-v0.142.5/
      series
      0001-agents-misc-codex-overrides.patch
      config.schema.json
  scripts/
    fetch-upstream.py
    apply-patches.py
    refresh-patches.py
    build.py
    test.py
.work/codex/rust-v0.142.5/src/
```

`codex/patches/<ref>/series` is the patch order. Patch paths are relative to
that directory. `config.schema.json` is generated from the patched upstream
tree by running `just write-config-schema` in the upstream checkout.
Build caches are kept under `.work/codex/<ref>/target/`.

`codex/schemas/` is the unpatched upstream config schema registry used by
`codexcfg`. It is separate from the patched schema artifact stored beside each
patch series.

## Common Commands

Fetch or update a shallow upstream checkout:

```sh
just codex-fetch rust-v0.142.5
```

Check that the committed patch series applies:

```sh
just codex-apply-check rust-v0.142.5
```

Apply the patch series:

```sh
just codex-apply rust-v0.142.5
```

Refresh the patch and generated schema from the current `.work` checkout:

```sh
just codex-refresh rust-v0.142.5
```

Run the narrow patch validation:

```sh
just codex-test rust-v0.142.5
```

Run the Codex config schema tooling:

```sh
just codexcfg-current
just codexcfg-check-registry
just codexcfg-sync-schema 0.142.5
just codexcfg-diff 0.142.0 0.142.5
just codexcfg-diff-defaults 0.142.0 0.142.5
just codexcfg-gen-toml 0.142.5 reference
```

## Upgrading To A New Codex Ref

Use an explicit target. Do not infer the target from upstream tags.

Example instruction:

```text
Use pure-patch-workflow for Codex.

Upstream: https://github.com/openai/codex
Source patch ref: rust-v0.142.0
Target upstream ref: rust-v0.143.0

Follow codex/upstream.yaml and codex/README.md.
Use .work/codex/<ref>/src, not codex/origin.
Preserve the behavior described in codex/patch.md.
Create codex/patches/rust-v0.143.0/ with series, patch, and config.schema.json.
Run apply check, just write-config-schema, schema diff, and the narrowest useful
cargo check. Report source, target, patch dir, series, schema, validation, and
limitations.
```

If the source patch ref is omitted, use the newest existing `rust-v*` patch
directory as the source. The target ref must still be provided explicitly.
