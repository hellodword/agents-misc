# Dependencies

- Prefer the standard library and existing project dependencies when they meet the requirement clearly.
- Add the smallest dependency that solves a current need; do not add a framework for speculative use.
- Verify changing facts through official documentation, the authoritative registry, the project lockfile, and current local tool output.
- Before adding or updating a dependency, verify its exact package name, compatible version, maintenance status, license compatibility, install scripts, telemetry, binary downloads, and material transitive dependencies.
- Preserve the project's declared dependency tool.
- Apply the `AGENTS.md` dependency-lockfile prohibition to every active dependency lockfile; this rule defines no additional manual-edit exception.
- For a direct dependency change, edit the authoritative manifest or input, such as `Cargo.toml`, `flake.nix`, or `package.json`, then run the repository's documented dependency command or declared dependency tool.
- For a transitive or resolution-only change, use a supported lockfile-only operation of the declared command or tool. Do not invent a text edit because no manifest change is required.
- Resolve dependency-lockfile merge conflicts by resolving authoritative manifests or inputs and regenerating the lockfile. Never edit lockfile conflict hunks.
- Review authoritative-input and generated-lockfile diffs together. If generation changes unrelated resolutions, narrow the inputs or tool operation, or report the unexplained output; never trim the generated lockfile manually.
- Validate the consuming build or focused test after any generated lockfile change.
- Do not use global installs or a host package manager for project dependencies.
- Treat packages, plugins, actions, binary archives, and generated clients as supply-chain inputs. Use trusted sources and minimum permissions.
- Do not create, replace, or recommend a project license as a dependency side effect.
