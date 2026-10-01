---
name: pure-patch-workflow
description: Maintain ordered, reproducible patches against a pinned upstream revision without tracking the patched source tree. Use for repositories whose product is a patch set; do not use for ordinary forks, vendored source, or direct upstream code maintenance.
---

# Pure Patch Workflow

1. Follow the repository's upstream manifest, patch layout, ordering, and replay commands. Record an exact upstream commit or immutable source digest; do not rely on a movable tag alone.
2. For a new workspace without conventions, use an upstream manifest, ignored `.work/<upstream>/<revision>/src`, and ordered patches under `<upstream>/patches/<revision>/` with a `series` file. Existing equivalent layouts need no conversion.
3. Fetch the required source into a confirmed ignored work directory. Never stage the upstream checkout. Use the established development shell and upstream-native build/test commands.
4. Generate patches from a clean pinned base. Keep ordering explicit and exclude machine paths, incidental timestamps, unrelated formatting, and generated changes not needed by upstream conventions.
5. Choose build parallelism from available CPU and memory, upstream limits, and observed resource use. Use a conservative limit for memory-heavy builds; do not wait for an out-of-memory failure before reducing it.
6. Preserve useful caches and prior patch history. Follow the repository's revision policy during upgrades; avoid full rebuilds or cache deletion without evidence.
7. Validate replay from the clean pinned base and run affected upstream tests or builds. Report the revision, patch changes, replay result, validation, and material resource limitations.
