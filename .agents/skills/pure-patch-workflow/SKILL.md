---
name: pure-patch-workflow
description: Maintain ordered, reproducible patches against a pinned upstream revision without tracking the patched source tree. Use for repositories whose product is a patch set; do not use for ordinary forks, vendored source, or direct upstream code maintenance.
---

# Pure Patch Workflow

1. Record the upstream name, URL, and exact immutable revision or tag in `<upstream>/upstream.yaml`.
2. Fetch only the required source into `.work/<upstream>/<revision>/src`, using shallow Git when metadata is needed or a recorded immutable archive otherwise.
3. Confirm `.work/` is ignored and never stage the upstream checkout.
4. Use the repository's Nix development shell and thin Just commands when already adopted. Call upstream-native build and test commands inside that environment.
5. Keep patches under `<upstream>/patches/<revision>/` and record order in a `series` file.
6. Generate patches from a clean, pinned upstream base and avoid machine paths, timestamps, unrelated formatting, or generated files not required by upstream convention.
7. Default parallel build jobs to `max(1, nproc - 1)`. Lower the limit only for an upstream constraint or observed resource/stability failure.
8. Preserve caches while iterating; do not trigger full rebuilds or delete caches without evidence.
9. On an upstream upgrade, create a new revision directory and preserve the previous patch set.
10. Validate patch application from the clean pinned base, then run upstream-native focused tests/builds.
11. Report revision, worktree, patch directory, series order, environment, job limit, cache state, commands, and limitations.
