---
id: core.generated-artifacts
kind: core
triggers:
  - "generated artifact"
  - "codegen"
  - "snapshot"
  - "generated docs"
  - "reproducibility"
summary: Classify generated artifacts and commit only reproducible durable outputs.
companions:
  skills:
    - id: generated-artifacts-review
      when: generated artifacts require review or classification
---

# Generated Artifact Rules

This rule covers code generation, metadata, snapshots, generated documentation, bindings, and comparable outputs from project-owned generation workflows.

## Commit generated artifacts

Commit a generated file when it is a durable project asset required by build, tests, runtime, packaging, or downstream users and the generation flow is reproducible and reviewable. An existing project/upstream convention is supporting evidence, not a mandatory extra condition.

Require tracked inputs, a pinned project toolchain or lockfile, a documented project command, stable output paths, and output without unexplained timestamps, absolute paths, random identifiers, or machine-specific data. Confirm the size is appropriate for the repository.

Examples usually committed:

- flutter_rust_bridge generated bridge files needed by Flutter/Rust builds.
- SQLx offline metadata when checked queries must build without a live database.
- Generated API clients/types that are imported by source code.
- Protobuf/OpenAPI/GraphQL bindings when the project convention is to commit generated bindings.
- Parser/lexer output when the project convention expects generated sources.

## Do not commit generated artifacts when they are runtime/build outputs

Examples not committed:

- `target/`
- `dist/`
- `build/`
- `.dart_tool/`
- `.venv/`
- `node_modules/`
- coverage output
- browser traces
- visual-review screenshots and preview captures
- videos
- Playwright reports
- local SQLite databases
- packaged archives
- compiled binaries
- temporary codegen experiments
- minified frontend bundles produced by normal build steps

## Generation commands

Durable generation commands belong in the project's established command system. Use `justfile` recipes that call Nix only when that workflow is already adopted.

Example:

    generate:
      nix develop .#dev --command cargo run -p xtask -- generate

Complex generation orchestration belongs in checked-in scripts, not directly in `justfile`.

## Reproducibility

A generation flow is reproducible when:

- generator binary comes from `flake.nix` or project lockfile;
- generator inputs are tracked;
- command is documented;
- environment variables are documented;
- output path is stable;
- output does not embed timestamps, absolute paths, random IDs, or machine-specific data unless unavoidable and documented.

## Pure patch work

Follow upstream convention.

Do not commit generated files to patch sets unless upstream requires generated files in patches.

When upstream requires generated files, keep generated changes separate or clearly grouped so review remains possible.
