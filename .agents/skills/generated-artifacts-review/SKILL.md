---
name: generated-artifacts-review
description: Decide ownership, tracking, reproducibility, and validation for generated bindings, metadata, clients, parser output, snapshots, or source. Use when generated files are added or changed; do not use for ordinary handwritten source or transient build/runtime output that must remain ignored.
---

# Generated Artifacts Review

1. Group generated files by generator and identify tracked inputs.
2. Identify the build, test, runtime, packaging, or downstream consumer for each group.
3. Mark a group for tracking only when the consumer requires it and generation is reproducible. Mark build/runtime output for ignore.
4. Verify the generator is pinned by the project environment or lockfile and the command is documented through the established command system.
5. Check output for timestamps, absolute paths, random IDs, machine values, unstable ordering, and inappropriate size.
6. Run the generator, validate a real consumer, then run generation again and require no diff.
7. For pure patch work, follow upstream tracking conventions and keep required generated changes reviewable.
8. Report for each group: track or ignore, reason, generator, inputs, command, consumer validation, reproducibility risks, and second-run result.
