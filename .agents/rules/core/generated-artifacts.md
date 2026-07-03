---
id: core.generated-artifacts
kind: core
triggers:
  - 'generated artifact'
  - 'codegen'
  - 'snapshot'
  - 'generated docs'
  - 'reproducibility'
summary: Classify generated artifacts and commit only reproducible durable outputs.
load_with:
  skills:
    - generated-artifacts-review
---

# Generated Artifact Rules

## Commit generated artifacts when all are true

- The generated files are durable project assets.
- The generated files are required by build, tests, runtime, packaging, or downstream users.
- The generator input files are tracked.
- The generator toolchain is pinned through `flake.nix`, lockfiles, or both.
- The generation command is documented through `justfile` or project docs.
- The output is deterministic enough to review.
- The generated files are not excessively large for the repository.
- The project or upstream convention expects them to be committed.

Examples usually committed:

- flutter_rust_bridge generated bridge files needed by Flutter/Rust builds.
- SQLx offline metadata when checked queries must build without a live database.
- Generated API clients/types that are imported by source code.
- Protobuf/OpenAPI/GraphQL bindings when the project convention is to commit generated bindings.
- Parser/lexer output when the project convention expects generated sources.
- Lockfiles for application projects.

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
- screenshots
- videos
- Playwright reports
- local SQLite databases
- packaged archives
- compiled binaries
- temporary codegen experiments
- minified frontend bundles produced by normal build steps

## Generation commands

Durable generation commands belong in `justfile`.

Recipes should call Nix.

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
