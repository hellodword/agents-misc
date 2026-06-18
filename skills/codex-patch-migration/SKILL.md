---
name: codex-patch-migration
description: Migrate local patches for openai/codex from an existing source tag to a user-specified target upstream ref while preserving behavior, minimizing upstream-file changes, and regenerating the config schema artifact.
---

---

# Codex Patch Migration Skill

## Purpose

Maintain local changes for `https://github.com/openai/codex` and migrate them from an existing upstream Codex release to a user-specified target upstream release.

This skill is used when migrating an existing local patch to a specific target tag, branch, or commit while preserving behavior and keeping the integration small, focused, and maintainable.

## Local Layout

The repository is expected to use this layout:

```text
codex/
  origin/
  patches/
    rust-v0.130.0.patch
    rust-v0.130.0.schema.json
    rust-v0.136.0.patch
    rust-v0.136.0.schema.json
    rust-v0.139.0.patch
    rust-v0.139.0.schema.json
    rust-v0.140.0.patch
    rust-v0.140.0.schema.json
```

`./codex/origin` is a local checkout of:

```text
https://github.com/openai/codex
```

`./codex/patches/<tag>.patch` contains the local changes for the corresponding upstream tag.

`./codex/patches/<tag>.schema.json` is the `codex-rs/core/config.schema.json` file generated after applying the patch for that tag and running:

```sh
just write-config-schema
```

The schema file is a generated artifact for comparison and verification. It must not be treated as a substitute for implementing the actual configuration behavior in source code.

## Required Target Version

The user must provide the target upstream version in the task prompt.

Accepted forms include:

```text
Target upstream tag: rust-v0.141.0
Target upstream ref: rust-v0.141.0
Target upstream ref: main
Target upstream ref: <commit-sha>
```

Do not choose a target version automatically.

If the user does not provide a target tag, branch, or commit, stop before making changes and ask for the target upstream version.

When the target is a tag named `rust-vX.Y.Z`, write outputs as:

```text
./codex/patches/rust-vX.Y.Z.patch
./codex/patches/rust-vX.Y.Z.schema.json
```

When the target is not a version tag, derive a clear filesystem-safe output name from the target ref and report that name before writing output files.

## Core Principles

1. Preserve the existing behavior.
2. Minimize changes to upstream files.
3. Prefer placing extractable logic in new files.
4. Keep wiring changes in existing upstream files as small as practical.
5. Modify tests, snapshots, fixtures, generated files, and unrelated support files only when needed to avoid errors.
6. Do not add broad new tests for the local changes unless required by existing checks.
7. Do not refactor unrelated upstream code.
8. Do not fix unrelated upstream issues.
9. Do not introduce new behavior beyond the intent of the existing local changes.
10. Do not hide failed or skipped validation.

## Prepare the Upstream Checkout

If `./codex/origin` does not exist, create it:

```sh
mkdir -p ./codex
git clone https://github.com/openai/codex ./codex/origin
```

If it already exists, update it:

```sh
cd ./codex/origin
git fetch --all --tags --prune
```

Before switching tags or editing files, confirm the working tree is clean:

```sh
git status --short
```

Do not continue if unrelated local changes are present.

## Identify the Reference Patch

The user may provide a source tag explicitly.

Accepted forms include:

```text
Source patch tag: rust-v0.140.0
Source upstream tag: rust-v0.140.0
Source ref: rust-v0.140.0
```

If the user provides a source tag, use:

```text
./codex/patches/<source-tag>.patch
./codex/patches/<source-tag>.schema.json
```

If the user does not provide a source tag, find existing patch files:

```sh
find ./codex/patches -maxdepth 1 -name 'rust-v*.patch' -print | sort -V
```

Select the newest existing patch as the reference patch, excluding the target output file if it already exists.

Example reference patch:

```text
./codex/patches/rust-v0.140.0.patch
```

The matching reference schema should be:

```text
./codex/patches/rust-v0.140.0.schema.json
```

If the reference patch does not have a matching schema file, continue only if the patch itself is available, and report that the reference schema is missing.

If no usable reference patch exists, stop and report that migration cannot proceed without a source patch.

## Understand the Reference Behavior

Do not migrate by blindly replaying old text changes.

Check the reference patch:

```sh
cd ./codex/origin
git checkout <source-tag>
git apply --stat ../patches/<source-tag>.patch
git apply --check ../patches/<source-tag>.patch
git apply ../patches/<source-tag>.patch
```

Study the resulting source tree and determine:

- Which files changed.
- Which files were added.
- Which configuration fields were introduced or modified.
- Which defaults were introduced or modified.
- Whether environment variables are involved.
- Whether the behavior affects only the OpenAI provider or a wider networking layer.
- Where timeout values are parsed.
- Where timeout values are stored.
- Where timeout values are applied.
- Whether streaming, request, connect, idle, retry, backoff, or cancellation behavior changed.
- Whether `codex-rs/core/config.schema.json` changed.
- Whether the saved reference schema matches the generated schema.

Compare the generated schema with the saved schema:

```sh
diff -u codex-rs/core/config.schema.json ../patches/<source-tag>.schema.json
```

If they differ, record the difference and investigate whether the saved schema is stale, the generation command changed, or the patch no longer reproduces the saved artifact.

After understanding the reference behavior, clean the checkout:

```sh
git reset --hard
git clean -fd
```

## Analyze the Target Upstream Version

Use only the target upstream version provided by the user.

Check that the target ref exists after fetching:

```sh
git rev-parse --verify <target-ref>^{commit}
```

Check out the target ref:

```sh
git checkout <target-ref>
```

Compare the relevant source areas between the source tag and the target ref.

Pay special attention to:

- Moved or renamed crates.
- Moved or renamed modules.
- Changed provider configuration structures.
- Changed OpenAI provider request paths.
- Changed HTTP client construction.
- Changed streaming implementation.
- Changed retry, backoff, cancellation, or timeout behavior.
- Changed schema generation.
- Changed location of `codex-rs/core/config.schema.json`.
- New upstream configuration fields that may overlap with the local behavior.
- New upstream abstractions that should be used instead of old integration points.

If upstream now contains similar functionality, reuse the upstream mechanism when it preserves the reference behavior. Add only the compatibility or behavior still required by the existing local changes.

## Migration Strategy

Implement the reference behavior against the target upstream architecture.

Prefer this structure:

- Put local behavior, parsing helpers, timeout mapping, or compatibility logic in new files when practical.
- Keep changes to existing upstream files limited to small integration points.
- Follow the target version’s current architecture.
- Use current upstream abstractions instead of recreating old ones.
- Preserve upstream naming and style in touched files.
- Avoid broad formatting of untouched code.
- Avoid lockfile changes unless a real dependency change is necessary.
- Avoid changing behavior for unrelated providers.

When the old integration point no longer exists, reconstruct the behavior in the correct target-version location.

When the old code and new code differ significantly, preserve behavior rather than textual structure.

## Schema Handling

After implementing the source changes on the target ref, run:

```sh
just write-config-schema
```

This should generate or update:

```text
codex-rs/core/config.schema.json
```

Save the generated file as:

```text
../patches/<target-output-name>.schema.json
```

For a target tag named `rust-v0.141.0`:

```sh
cp codex-rs/core/config.schema.json ../patches/rust-v0.141.0.schema.json
```

Rules for schema handling:

- The target schema file must be generated after the target patch is applied.
- Do not hand-edit the saved schema as a replacement for source changes.
- Do not manually invent schema fields.
- If the schema path or generation command changed upstream, follow the target version’s current mechanism and report the difference.
- If schema generation fails, report the failure and preserve the source changes separately.
- The saved schema must match the generated `codex-rs/core/config.schema.json`.

Verify:

```sh
diff -u codex-rs/core/config.schema.json ../patches/<target-output-name>.schema.json
```

No output means the saved schema matches the generated schema.

## Test and Support File Policy

Use the minimum-change policy for tests and support files.

Do not add large test coverage for the local behavior unless required by existing checks.

Do not rewrite snapshots, fixtures, or expected outputs unless they are directly affected.

Do not update unrelated tests.

Do not fix unrelated upstream test failures.

Do not perform broad formatting just because a file was touched.

Only modify support files when needed for:

- Compilation.
- Existing tests.
- Existing snapshots directly affected by the local behavior.
- Schema consistency.
- Required generated artifacts.

## Validation

Run the lowest-cost useful validation for the touched code.

Prefer repository-provided commands. At minimum, consider:

```sh
just write-config-schema
git status --short
git diff --stat
```

When practical, also run the smallest relevant formatter, build, type check, or test command for the touched Rust crates or packages.

Avoid expensive full-suite validation unless necessary.

If the environment lacks required tools, do not install global tools or make unrelated environment changes. Record the limitation clearly.

For each validation command, record:

- Command.
- Result.
- Whether the result is related to the local changes.
- Any skipped checks and the reason.

## Generate the Target Patch

After implementing and validating the target changes, generate the new patch file:

```sh
git diff --binary > ../patches/<target-output-name>.patch
```

For a target tag named `rust-v0.141.0`:

```sh
git diff --binary > ../patches/rust-v0.141.0.patch
```

The target output should include:

```text
./codex/patches/<target-output-name>.patch
./codex/patches/<target-output-name>.schema.json
```

The patch file must contain only the changes required to preserve the local behavior on the target upstream ref.

Do not include:

- Build artifacts.
- Temporary files.
- Editor files.
- Unrelated formatting.
- Unrelated lockfile changes.
- Unrelated test updates.
- Manual notes.
- Unrelated upstream fixes.

## Patch Applicability Check

Before finishing, verify that the generated patch applies cleanly to the target ref:

```sh
git reset --hard
git clean -fd
git checkout <target-ref>
git apply --check ../patches/<target-output-name>.patch
git apply ../patches/<target-output-name>.patch
just write-config-schema
diff -u codex-rs/core/config.schema.json ../patches/<target-output-name>.schema.json
```

If the final diff has output, regenerate or update the saved schema so it matches the generated file.

## Final Quality Checklist

Confirm all of the following before reporting completion:

- The user provided the target upstream version.
- The reference behavior was understood before migration.
- The target implementation preserves the reference behavior.
- The target implementation follows the target upstream architecture.
- Local behavior is isolated into new files where practical.
- Existing upstream files contain only necessary wiring or minimal direct changes.
- Schema was generated with `just write-config-schema`.
- The saved target schema matches the generated schema.
- The target patch applies cleanly to the target upstream ref.
- Tests, snapshots, fixtures, and generated files were modified only when necessary.
- No unrelated files are included.
- Validation results are accurately reported.
- Any remaining risks are clearly stated.

## Final Report Format

Provide a concise final report with:

```text
Source patch:
Source schema:
Target upstream:
New patch:
New schema:

Behavior summary:
Changed files:
New files:
Upstream wiring:
Schema generation:
Validation:
Skipped or failed checks:
Remaining risks:
```

## Decision Rules

When no target version is provided:

- Stop before making changes.
- Ask the user to provide `Target upstream tag`, `Target upstream ref`, or a commit SHA.
- Do not infer the target from upstream tags.

When the old patch does not apply cleanly:

- Treat this as normal.
- Recreate the behavior against the target architecture.

When upstream files moved:

- Follow the new upstream structure.
- Do not recreate old paths.

When upstream added overlapping functionality:

- Reuse it if it preserves the reference behavior.
- Add only missing compatibility or behavior.
- Avoid creating a parallel mechanism unless required for compatibility.

When schema changes:

- Generate schema from source.
- Save the generated schema beside the patch.
- Do not maintain schema manually.

When tests or snapshots fail:

- Fix only failures caused by the local behavior.
- Do not expand the scope to unrelated upstream failures.

When unsure whether a change is necessary:

- Prefer not to make it.
- Make the change only when needed to preserve behavior, compile successfully, keep schema consistent, or pass relevant existing checks.
