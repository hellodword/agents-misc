---
id: project-type.pure-patch
kind: project-type
triggers:
  - "pure patch"
  - "upstream patch"
  - "patch series"
  - "refresh patch"
  - "apply patch"
summary: Maintain reproducible patch sets against upstream projects without committing worktrees.
companions:
  required_rules:
    - toolchain.nix
    - core.repo-hygiene
  conditional_rules:
    - id: core.scripts
      when: patch helper scripts change
  skills:
    - id: pure-patch-workflow
      when: fetching, applying, refreshing, or validating patch series
  references:
    - id: nixpkgs-devcontainer-alignment
      when: initializing, updating, or aligning nixpkgs inputs
---

# Pure Patch Project Rules

## Applicability

Use this for repositories that maintain patches against upstream projects such as Chromium, Firefox, codex_rs, dnsmasq, or similar projects.

A pure patch repository commits patch files, series metadata, upstream revision metadata, documentation, Nix environment, just recipes, and patch helper scripts.

It does not commit the patched upstream source tree.

## Directory convention

```text
<upstream-name>/
  upstream.yaml
  README.md
  patches/
    <upstream-version-or-tag>/
      series
      0001-*.patch
      0002-*.patch
  scripts/
    fetch-upstream.py
    apply-patches.py
    refresh-patches.py
    build.py
    test.py
.work/
  <upstream-name>/
    <upstream-version-or-tag>/
      src/
```

Keep `.work/**` ignored.

## Upstream source

- Record the exact upstream revision in `upstream.yaml`.
- Use the smallest reliable source acquisition method.
- Use a shallow clone of a specific tag or revision when patch generation, history inspection, submodules, or other Git metadata is required.
- Use an immutable source archive when Git metadata is not required and the archive's revision/integrity can be recorded.
- Use upstream-native fetch tooling for complex projects when required.
- Avoid full-history clones unless upstream tooling requires them or the user explicitly asks.
- Do not rely on floating branches for patch directories.

## Patch sets

Use versioned patch directories and do not overwrite older sets during upstream upgrades.

When upgrading from version A to version B:

1. create a new patch directory for B;
2. copy or regenerate the patch series from A;
3. apply to upstream B;
4. resolve conflicts;
5. refresh patches;
6. run narrow validation;
7. keep A unless explicitly retired.

Prefer `git format-patch` style patches when upstream is Git-based. Maintain a `series` file for ordering.

## Build cache

- Do not delete build caches by default.
- Do not clean the whole upstream tree unless necessary.
- Avoid commands that force full rebuilds.
- Prefer narrow build/test targets.
- Preserve generated build directories unless the build system requires cleanup.

Default build jobs to `max(1, nproc - 1)`. Lower the value only for an upstream/project limit or after an observed resource or stability failure. When `nproc` is unavailable, use a portable fallback such as `getconf`.

## Validation

Prefer upstream-native validation inside Nix.

Record upstream revision, patch series path, command run, jobs setting, cache reuse status, and limitations.
