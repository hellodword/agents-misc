# Dependencies

- Choose dependencies for fitness, quality, trusted provenance, reliable maintenance, ecosystem reputation, and total maintenance cost. A mature library can be preferable to substantial custom code.
- Reuse suitable existing dependencies and the standard library, without treating dependency count or zero dependencies as a goal. Avoid speculative additions.
- Verify changing facts through official documentation, the authoritative registry, the project lockfile, and current local tool output.
- For an addition or update, check the authoritative source and package name, version compatibility, maintenance signals, and license fit. Low stars or infrequent commits alone do not disqualify a mature project.
- Deepen review of install scripts, binary downloads, telemetry, or transitive dependencies when permissions, provenance, unusual behavior, known advisories, or the deployment context warrant it. Do not turn every routine update into a full supply-chain audit.
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
