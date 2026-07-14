# Codex Config Atlas

## Why this exists

Codex configuration changes with each upstream release, but the upstream JSON
Schema is published as a version-specific artifact rather than a browsable
history. Answering practical questions—what was added, what became required,
which default changed, or how to write a complete `config.toml`—otherwise
requires downloading and comparing large schema files by hand.

Codex Config Atlas keeps a reviewed registry of upstream schemas and turns it
into two interfaces: a scriptable CLI for validation, diffs, and TOML
generation, and a static Web site for browsing changes between releases. It is
repository tooling; it does not modify a user's Codex configuration.

## Architecture and data ownership

```text
OpenAI Codex tagged config.schema.json
  -> tracked schema registry and provenance metadata
  -> normalized field model
  -> CLI diffs and generated TOML
  -> generated version data
  -> static browser-side diff site
```

`schemas/` is the durable source registry. Each version contains the exact
upstream `config.schema.json` plus `metadata.json` with its tag, source URL,
SHA-256, and fetch time. `manifest.json` defines the supported version range and
stable ordering. A sync refuses to replace an existing version when upstream
bytes no longer match its recorded hash.

The generated data and assembled site are Nix build outputs. They are not
committed to the repository. The site computes selected-version diffs in the
browser from per-version normalized field payloads.

The unpatched registry here is separate from
`codex/patches/<tag>/config.schema.json`. The latter records the schema produced
by the locally patched upstream tree and belongs to patch validation.

## Layout

```text
tools/codex-config-atlas/
  README.md
  pyproject.toml
  src/codex_config_atlas/
  schemas/
    manifest.json
    rust-v<version>/
      config.schema.json
      metadata.json
  web/
    index.html
    app.js
    style.css
```

Nix packaging and generated-output checks live in
`nix/codex-config-atlas.nix`, matching the repository's other tools.

## CLI

Run the packaged CLI through the root Just menu:

```bash
just codex-config-atlas-current
just codex-config-atlas-check-registry
just codex-config-atlas-diff 0.143.0 0.144.1
just codex-config-atlas-diff-defaults 0.143.0 0.144.1
just codex-config-atlas-gen-toml 0.144.1 reference
```

The CLI exposes these subcommands:

- `current`: print the current patched Codex package version injected by Nix.
- `sync-schema`: fetch and register one tagged upstream schema.
- `check-registry`: validate manifest ordering, metadata, paths, and hashes.
- `gen-toml`: render default or reference TOML for a registered version.
- `diff`: render a schema diff as Markdown or JSON.
- `diff-defaults`: render default-value changes as Markdown or JSON.
- `build-data`: generate normalized version payloads for the site.
- `build-site`: combine tracked Web assets with generated data.

Machine-readable output and generated content are written to stdout or the
requested `--out` path. Diagnostics are written to stderr and failures return a
non-zero exit status. `sync-schema` is the only command that accesses the
network.

## Following upstream Codex

Register an explicit upstream version:

```bash
just codex-config-atlas-sync-schema 0.145.0
```

Review the new schema and metadata together, then compare it with the previous
version and build the registry-backed outputs. Do not infer a target from a
floating branch and do not rewrite older registered schemas.

When a local Codex patch changes configuration, refresh the patch-owned schema
through the workflow documented in the
[Codex patch workspace](../../codex/README.md); do not copy that patched schema
into this unpatched upstream registry.

## Site, build, and validation

Build the individual products without creating result links:

```bash
nix build --no-link .#codex-config-atlas
nix build --no-link .#codex-config-atlas-registry
nix build --no-link .#codex-config-atlas-data
nix build --no-link .#codex-config-atlas-site
```

The GitHub Pages workflow publishes `.#codex-config-atlas-site`. For local
validation, use the documented CLI commands above followed by:

```bash
nix flake check
```

The Python package, dependencies, current Codex version, schema inputs, and Web
assets are all resolved through the repository flake. Runtime build output,
temporary generated data, and packaged sites stay outside the tracked source
tree.
