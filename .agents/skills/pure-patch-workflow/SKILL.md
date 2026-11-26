---
name: pure-patch-workflow
description: Maintain ordered, reproducible patches against a pinned upstream revision without tracking the patched source tree. Use for repositories whose product is a patch set; do not use for ordinary forks, vendored source, or direct upstream code maintenance.
---

# Pure Patch Workflow

1. Follow the repository's upstream manifest, patch layout, ordering, and replay commands, pinning an exact upstream commit or immutable archive digest rather than a movable tag.
2. Pin `<revision>` in the upstream manifest, keep the working checkout under ignored `.work/<upstream>/<revision>/src` (never tracked or staged in the outer repository), and store ordered patches under `<upstream>/patches/` with an explicit `series` file.
3. In large upstream source trees (such as Chromium or OS kernels), scope `git status`, `git diff`, `git log`, `git show`, and code searches to explicit subdirectories rather than scanning the entire checkout root.
4. Generate patches from a clean pinned base without machine paths, incidental timestamps, or unrelated formatting. Exclude externally generated headers, bindings, or prebuilt libraries injected into the build tree from exported `.patch` files; when cross-component C/FFI ABIs change, rebuild and sync the producer artifacts before compiling dependent upstream targets.
5. Size build parallelism to available CPU and RAM—using conservative limits proactively on memory-heavy link/compile steps—and preserve valid build caches across iterations unless evidence requires invalidation.
6. Validate patch replay from the clean pinned base alongside affected upstream build/check gates (including header/dependency visibility checks such as `gn check` where applicable); when applying patches onto a shallow checkout with `git apply --3way`, expose the prior base's object store (for example via `GIT_ALTERNATE_OBJECT_DIRECTORIES`) so 3-way merge blobs resolve cleanly. Report the pinned revision, changed patches, replay result, validation, and resource constraints.
