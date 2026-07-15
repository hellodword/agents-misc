# Dependencies

- Prefer the standard library and existing project dependencies when they meet the requirement clearly.
- Add the smallest dependency that solves a current need; do not add a framework for speculative use.
- Verify changing facts through official documentation, the authoritative registry, the project lockfile, and current local tool output.
- Before adding or updating a dependency, verify its exact package name, compatible version, maintenance status, license compatibility, install scripts, telemetry, binary downloads, and material transitive dependencies.
- Preserve the project's package manager and lockfile. Do not hand-edit generated lock data unless the ecosystem defines that workflow.
- Pin through the project's established reproducibility mechanism and validate the consumer build or test.
- Do not use global installs or a host package manager for project dependencies.
- Treat packages, plugins, actions, binary archives, and generated clients as supply-chain inputs. Use trusted sources and minimum permissions.
- Do not create, replace, or recommend a project license as a dependency side effect.
