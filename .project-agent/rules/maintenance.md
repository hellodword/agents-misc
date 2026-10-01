# Agent Rules Kit Maintenance

## Distribution boundary

- Only `AGENTS.md` and `.agents/**` belong to the distributed payload.
- Use real files and directories; the checker rejects every symlink, including links within the payload. Keep local links within the distribution boundary. Do not link to this repository's maintenance overlay, checker, tests, or Just/Nix checks from the payload.
- Consumers provide project facts and constraints through `.project-agent/project.md` and its direct links.

## Rules and skills

- Keep shared rules as flat Markdown files and route every non-index rule and skill directly from `.agents/rules/index.md`.
- Put routing decisions in the index. Do not add hidden metadata, recursive instruction loading, or duplicate inventories.
- Give each skill only `name` and `description` YAML frontmatter. Match the name to its directory and use lowercase letters, digits, and single hyphens.
- Describe the task a skill helps with and any exclusion needed to prevent misrouting. Keep the entrypoint concise and move substantial conditional guidance to owned references.
- Put supporting files under the skill's `assets/`, `references/`, or `scripts/` directory, and link each file directly from `SKILL.md`.
- Keep simple defaults in rules. Use human-readable templates for reports intended for people; add machine contracts only for actual program consumers.

## Markdown links

- The checker scans every Markdown file in `AGENTS.md` and `.agents/**`. Use single-line `[text](path)` links or `![description](path)` images. Local targets are plain paths, optionally followed by `#fragment`.
- Write paths directly, without whitespace, parentheses, angle brackets, quotes, backslash escapes, percent encoding, query strings, or link titles. Paths are relative to the source document; a leading `/` refers to the repository root. References must point to an existing distributed file or directory.
- Reference-style definitions, full references such as `[text][id]`, and collapsed references such as `[text][]` are errors; rewrite them as inline links.
- Put Markdown examples in backtick-delimited inline code or backtick/tilde fenced code blocks so they are excluded from link checking. Skill frontmatter is validated as metadata rather than scanned for links.
- External URLs are not fetched, fragment identifiers are not checked against headings, and native HTML links are outside the checker scope. `file:` URLs are rejected.

## Validation

- For payload or checker changes, run `just check-agent-rules` or `nix build --no-link .#checks.x86_64-linux.agent-rules`. Do not repeat the same tests through both entrypoints.
- Keep workflow validation separate: run `just check-workflows` or its `github-workflows` Nix check when that boundary changes.
- Format changed files with the repository formatter and review its diff.
- For changed Nix output or check wiring, run `nix flake check --no-build --no-write-lock-file` and build the affected checks. Use the full repository gate when the changed boundaries require it.
- Review skill descriptions and routes together. Check only structural invariants mechanically; do not test prose or source-code spelling.

Review the final diff and `git status --short --ignored`. Keep temporary diagnostics under confirmed ignored paths and remove task-created diagnostics before finishing. Preserve and disclose relevant pre-existing ignored work.
