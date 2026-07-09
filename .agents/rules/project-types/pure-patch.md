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
load_with:
  rules:
    - toolchain.nix-just
    - core.repo-hygiene
  skills:
    - pure-patch-workflow
  references:
    - nixpkgs-devcontainer-alignment
---

# Pure Patch Project Rules

## Applicability

Use these defaults only for new projects, greenfield scaffolding, or repositories without a clear existing convention.

Prefer coherent local conventions.

Use this for maintaining patches against upstream projects such as Chromium, Firefox, codex_rs, dnsmasq, and similar projects.

A pure patch repository commits:

- patch files;
- patch series metadata;
- upstream revision metadata;
- documentation;
- Nix development environment;
- just recipes;
- patch apply/refresh scripts;
- minimal build/test helper scripts.

A pure patch repository does not commit the patched upstream source tree.

## Directory convention

Use:

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

Use ignored worktrees:

    .work/
      <upstream-name>/
        <upstream-version-or-tag>/
          src/

## Initial flow

Before editing patches:

1. Determine upstream project name, URL, and exact revision/tag.
2. If the user did not request full history, do not clone all history.
3. Fetch only what is needed for the selected revision/tag when possible.
4. Place upstream checkout under `.work/<upstream>/<rev>/src`.
5. Create or update Nix dev shell for the patch workspace.
6. Before initializing or updating `nixpkgs`, read `.agents/references/nixpkgs-devcontainer-alignment.md`; keep `flake.nix` on `github:NixOS/nixpkgs/nixos-unstable` and use `--override-input` only through `nix flake update`.
7. Add just recipes that call Nix and then upstream-native commands.
8. Verify the narrowest build/test command that can compile or validate the unpatched upstream checkout.
9. Apply existing patches.
10. Verify the narrowest build/test command after patches.

## Fetch strategy

Use the smallest reliable source acquisition method required by the upstream project.

Default choices:

- shallow clone a specific tag/revision when Git metadata is useful;
- use upstream-native fetch tooling for complex projects such as Chromium or Firefox;
- avoid full-history clones unless upstream tooling requires them or the user explicitly asks.

Record the exact upstream revision in `upstream.yaml`.

Do not rely on floating branches for patch directories.

## Build jobs

Build jobs must never exceed:

    max(1, nproc - 1)

When `nproc` is unavailable, use a portable fallback such as Python or `getconf`.

Example Python expression:

    max(1, (os.cpu_count() or 2) - 1)

## Long builds

If the first full upstream build is likely to take a very long time, do not babysit it for hours.

Instead:

- provide the exact command;
- explain expected cache/output directories;
- ask the user to run the first full build manually;
- continue later using the populated build cache.

## Avoid invalidating build cache

When modifying patches:

- do not delete build caches;
- do not clean the whole upstream tree unless necessary;
- avoid commands that force full rebuilds;
- prefer narrow build/test targets;
- preserve generated build directories unless the build system requires cleanup.

## Patch version directories

Do not overwrite older patch sets during upstream upgrades.

Use versioned directories:

    <upstream-name>/patches/<old-version>/
    <upstream-name>/patches/<new-version>/

When upgrading upstream from version A to version B:

1. create a new patch directory for B;
2. copy or regenerate the patch series from A;
3. apply to upstream B;
4. resolve conflicts;
5. refresh patches;
6. run narrow validation;
7. keep A unless explicitly retired.

## Patch format

Prefer `git format-patch` style patches when upstream is Git-based.

Maintain a `series` file for ordering.

Patch filenames should be stable and reviewable.

## Git

Environment scaffolding and patch files may be committed to the pure patch repository.

Patched upstream source under `.work/` must not be committed.

Use one semantic commit for one patch management change.

## Validation

Prefer upstream-native validation inside Nix.

Record:

- upstream revision;
- patch series path;
- command run;
- jobs setting;
- cache reuse status;
- limitations.
