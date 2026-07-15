# Generated Artifacts

- Commit generated output only when build, tests, runtime, packaging, or a downstream consumer requires it and the generation flow is reproducible.
- Require tracked inputs, a generator pinned by the project toolchain or lockfile, a documented command, stable output paths, and deterministic content.
- Check for timestamps, absolute paths, random identifiers, machine-specific values, unstable ordering, and unexpected size.
- Generated Flutter/Rust bridge files, actually imported API clients, required parser output, and required offline database-query metadata may be durable tracked assets.
- Build directories, coverage, browser traces, screenshots, videos, local databases, backups, archives, compiled binaries, and temporary experiments are runtime artifacts and must not be committed.
- Put durable generation behind `just generate` or the repository's established equivalent. Keep complex orchestration in a checked-in script.
- After generation, validate a consuming build or test and run the generator a second time to confirm there is no diff.
- Follow upstream conventions for a pure patch project, and keep required generated changes reviewable.
