# Agent Rules Kit Maintenance

## Distribution boundary

- Only `AGENTS.md` and `.agents/**` belong to the distributed payload.
- Use real files and directories; the checker rejects every symlink, including links within the payload. Keep local links within the distribution boundary. Do not link to this repository's maintenance overlay, checker, tests, or Just/Nix checks from the payload.

## Rules and skills

- Keep shared rules as flat Markdown files and route every non-index rule and skill directly from `.agents/rules/index.md`.
- Put shared-rule and skill routing decisions in the index. Do not add hidden metadata, recursive loading of shared instruction files, or duplicate inventories.
- Give each skill only `name` and `description` YAML frontmatter. Match the name to its directory and use lowercase letters, digits, and single hyphens.
- Describe the task a skill helps with and any exclusion needed to prevent misrouting. Keep the entrypoint concise and move substantial conditional guidance to owned references.
- Put supporting files under the skill's `assets/`, `references/`, or `scripts/` directory, and link each file directly from `SKILL.md`.
- Keep simple defaults in rules. Use human-readable templates for reports intended for people; add machine contracts only for actual program consumers.
- Keep substantive preferences in one place. Use skills for workflow decisions that benefit from guidance, without repeating rules or basic model capabilities. Route by actual work, and check that incidental keywords do not trigger unrelated workflows.

## Markdown links

Use single-line inline Markdown links with plain paths. Follow the [supported Markdown link syntax](../../README.md#supported-markdown-links) for the distributed payload.

## Validation

- For payload or checker changes, run `just check-agent-rules` or `nix build --no-link .#checks.x86_64-linux.agent-rules`. Do not repeat the same tests through both entrypoints.
- Keep workflow validation separate: run `just check-workflows` or its `github-workflows` Nix check when that boundary changes.
- Format changed files with the repository formatter and review its diff.
- For changed Nix output or check wiring, run `nix flake check --no-build --no-write-lock-file` and build the affected checks. Use the full repository gate when the changed boundaries require it.
- Review skill descriptions and routes together. Check only structural invariants mechanically; do not test prose or source-code spelling.
- For routing or authorization changes, walk through representative tasks to check intended decisions and exclusions. Treat that review as static evidence, not a measured improvement in agent behavior.

Review the final diff and `git status --short --ignored`. Follow the [temporary workspace and retention policy](../../AGENTS.md#temporary-workspace-and-retention) for diagnostics. Preserve and disclose relevant pre-existing ignored work.
