---
name: generated-artifacts-review
description: Use this when code generation, bindings, SQLx metadata, bridge files, API clients, parser output, snapshots, or generated source files are added or changed.
---

# Generated Artifacts Review

## Purpose

Decide whether generated files should be committed and how to keep generation reproducible.

## Workflow

1. Identify generated files and their generator.
2. Identify tracked inputs.
3. Identify whether the generated files are imported by source, required by build/tests/runtime, or expected by upstream.
4. Verify generator tooling is pinned by the project's reproducible toolchain or lockfile.
5. Verify the generation command is documented through the project's command system or docs.
6. Check for timestamps, absolute paths, random ids, machine-specific data, or nondeterministic ordering.
7. Commit durable generated outputs when required and reproducible; project convention is evidence, not a prerequisite.
8. Do not commit runtime/build outputs.
9. For pure patch projects, follow upstream convention.
10. Report the decision for each generated file group.

## Output

For each group:

- commit or ignore;
- reason;
- generator;
- tracked inputs;
- command;
- reproducibility risks;
- validation performed.
