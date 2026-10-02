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

Python source under `src/codex_config_atlas/` is authoritative for registry
validation, normalization, semantic diffing, stable TOML, synchronization, and
data generation. `web/` is authoritative for the static application shell and
the browser implementation of the shared golden diff contract. The tracked
cross-language golden cases are the compatibility boundary; temporary data and
site trees are disposable build products.

The unpatched registry here is separate from the patched schema hunk owned by
`0006-generated-contracts.patch`. That hunk is reproduced from the local
patched upstream tree and belongs to patch validation.

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

Run the packaged CLI through its flake app:

```bash
nix run .#codex-config-atlas -- current
nix run .#codex-config-atlas -- check-registry --schemas tools/codex-config-atlas/schemas
nix run .#codex-config-atlas -- diff --schemas tools/codex-config-atlas/schemas --from 0.143.0 --to 0.144.1
nix run .#codex-config-atlas -- diff-defaults --schemas tools/codex-config-atlas/schemas --from 0.143.0 --to 0.144.1
nix run .#codex-config-atlas -- gen-toml --schemas tools/codex-config-atlas/schemas --version 0.144.1 --mode reference
nix run .#codex-config-atlas -- gen-toml --schemas tools/codex-config-atlas/schemas --version 0.160.0 --mode example --out example.config.toml
```

The CLI exposes these subcommands:

- `current`: print the current patched Codex package version injected by Nix.
- `sync-schema`: fetch and register one tagged upstream schema.
- `check-registry`: validate manifest ordering, metadata, paths, and hashes.
- `gen-toml`: render default, reference, or opt-in example TOML for a registered version.
- `diff`: render a schema diff as Markdown or JSON.
- `diff-defaults`: render default-value changes as Markdown or JSON.
- `build-data`: generate normalized version payloads for the site.
- `build-site`: combine tracked Web assets with generated data.

Machine-readable output and generated content are written to stdout or the
requested `--out` path. Diagnostics are written to stderr and failures return a
non-zero exit status. `sync-schema` is the only command that accesses the
network.

Schema diffs omit a `profiles.<name>.<path>` event only when the corresponding
`<path>` event has the same semantic change. Profile-only changes and profile
changes with different values or field metadata remain visible.

### Annotated configuration examples

`gen-toml --mode example` reads the original schema, including referenced
definitions and union branches. Each configuration group contains its upstream
description, type and requiredness, explicit default, allowed values and choice
descriptions, constraints, and an example assignment. Defaults are labelled as
JSON so that `null`, arrays, and object defaults retain their meaning. Array item
schemas and named map entries are documented too. Descriptions remain in the
upstream language.

Optional assignments stay commented, **even when they have defaults**. A required
child of an optional object also stays commented. Only fields required through
the entire parent chain can be active. Dotted keys let a setting be uncommented
without relying on a preceding table header; keys containing dots or other TOML
syntax are quoted. Named entries use `example`, and synthesized values such as
`"..."` are explicitly labelled as examples, not runtime defaults. A default
mentioned only in prose is preserved as prose, not inferred as a schema default.

Choose one union alternative, replace placeholders, and enable the fields you
need. Object alternatives replace the corresponding scalar assignment. TOML
cannot encode `null`: leave these keys omitted to preserve their default.
Recursive references are documented at the recursion boundary instead of being
expanded indefinitely. The existing `default` and `reference` modes retain their
previous behavior.

`build-data` writes `versions/<version>/example.toml` for every registered
version. The site's **配置示例** view previews the complete file by default
(`section=all`) and can be narrowed to a selected group; copy and download always
include the complete example for the selected version. Previewing or downloading
does not write to the user's Codex configuration.

## Following upstream Codex

The registry includes every published, non-draft, non-prerelease GitHub release
whose tag matches `rust-v<major>.<minor>.<patch>`, starting at `rust-v0.129.0`.
Patch releases are included even when their schema bytes match a neighboring
version. The supported inventory is owned by `schemas/manifest.json`; it is
independent of the paused patched Codex package's current version.

When refreshing, enumerate the upstream
[release list](https://github.com/openai/codex/releases) with pagination, exclude
drafts and prereleases, and compare the resulting stable tags to the manifest.
Do not infer missing patch numbers: a tag must have an official release. Resolve
annotated tags to their peeled commit when supplying `--expected-commit`.

Register an explicit upstream version:

```bash
nix run .#codex-config-atlas -- sync-schema --schemas tools/codex-config-atlas/schemas --version 0.148.0
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
inspection, each selected comparison also writes a copyable `新增` / `移除` /
`变更` diff to the browser developer console. For local validation, use the
documented CLI commands above followed by:

```bash
nix build --no-link --accept-flake-config .#checks.x86_64-linux.codex-config-atlas-tests
nix build --no-link --accept-flake-config .#checks.x86_64-linux.codex-config-atlas-registry
nix build --no-link --accept-flake-config .#checks.x86_64-linux.codex-config-atlas-data
nix build --no-link --accept-flake-config .#checks.x86_64-linux.codex-config-atlas-site
```

The Python package, dependencies, current Codex version, schema inputs, and Web
assets are all resolved through the repository flake. Runtime build output,
temporary generated data, and packaged sites stay outside the tracked source
tree.

### Source development and browser review

The static UI has separate comparison and example views. Comparisons support
field/description search, category filters, expandable details, reversing the
selected versions, and copyable differences. URLs contain only the active view's
parameters: `view=diff&from=…&to=…` with optional `q` and `category`, or
`view=example&version=…` with optional `section` (default `all`, omitted from URLs).
Switching views keeps the other view's selections in memory; browser history
and reloading restore the active view from its URL. Older links containing both
views' parameters still open and are normalized. Narrow screens stack
before/after values while keeping the version labels visible.

Use the Atlas development shell for source checks:

```bash
nix develop .#codex-config-atlas --command env PYTHONPATH=tools/codex-config-atlas/src python3 -m unittest discover -s tools/codex-config-atlas/tests -p 'test_*.py'
nix develop .#codex-config-atlas --command node --test tools/codex-config-atlas/tests/test_web_diff.mjs
```

On Linux, the shell supplies Playwright and Chromium from the locked Nix inputs.
The project-owned browser smoke flow launches that explicit browser, serves the
selected site locally, and checks comparison, filtering, keyboard use, widths
from 320 to 1440 px, preview, exact clipboard/download content, URL restoration,
stale requests, and recoverable fetch failures. To capture screenshots of the
packaged site in the ignored task workspace:

```bash
nix build --no-link --print-out-paths .#codex-config-atlas-site
# Pass the printed store path as SITE below.
nix develop .#codex-config-atlas --command python3 tools/codex-config-atlas/tests/browser_smoke.py --site SITE --screenshots tmp/agent/codex-config-atlas/review
```

`captures.json` records each screenshot's route, viewport, and browser version.
Screenshots and AI review notes are temporary review evidence, not tracked
golden images. Inspect the captures using the `ai-visual-review` rubric when
visual review is requested; the smoke test itself does not perform AI review.
Keep the schema registry unchanged while running reproducibility checks.

## Failure recovery

- Sync timeout, size, tag, commit, hash, path, JSON, or provenance failure:
  correct the explicit version/expectation or upstream record and retry. The
  installed registry tree remains byte-identical.
- Interrupted or failed installation: preserve the sibling backup path printed
  by the command. The tool restores the previous tree automatically; if that
  restoration fails, verify the candidate and backup before moving the exact
  backup into place.
- Cross-language golden mismatch: fix the shared normalization semantics, not
  one language's expected output. Run the tests check before regenerating data.
- TOML output changes with input order: treat it as a determinism bug. Keep the
  old registry and reduce the schema pair to a golden case.
- Data/site generation failure: discard only the reported temporary candidate
  output. The tracked registry and previous output remain unchanged.

Recovery is complete only after registry validation and two consecutive
data/site builds produce identical file inventories and hashes.
