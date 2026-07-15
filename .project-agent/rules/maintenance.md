# Agent Rules Kit Maintenance

## Distribution boundary

- Only `AGENTS.md` and `.agents/**` belong to the distributed payload.
- Do not reference this repository's maintenance overlay, checker, tests, or Just/Nix checks from the payload.
- Consumers provide project facts and constraints through `.project-agent/project.md` and its direct links.

## Rules

- Keep exactly the flat shared rules named in `.agents/rules/index.md`.
- Write each rule as plain Markdown with exactly one H1 and no YAML frontmatter.
- Link every non-index rule from `index.md`.
- Put all routing decisions in the index. Do not add hidden metadata or recursive loading behavior.

## Skills and owned files

- Give each skill only `name` and `description` YAML frontmatter.
- Match `name` to the directory and follow the Agent Skills lowercase/hyphen naming rules.
- Make each description explain what the skill does, when to use it, and an important exclusion.
- Keep each `SKILL.md` below 500 lines and use imperative workflow language.
- Put supporting material only under that skill's `assets/`, `references/`, or `scripts/` directory.
- Link every supporting file directly from its owning `SKILL.md`; do not leave orphan files.
- Use JSON Schema only for machine-produced or machine-consumed output contracts.

## Routing and evaluation

- Update the relevant JSONL eval cases whenever route behavior or a skill description changes.
- Keep every eval ID globally unique.
- Give every skill at least one positive and one near-miss negative case in the skill corpus.
- CI validates corpus structure and coverage only; it must not use nondeterministic model output as a gate.

## Validation

Run:

```sh
nix fmt
nix develop .#dev --command python3 scripts/check-agent-rules.py --root .
nix develop .#dev --command python3 -m unittest discover -s tests -p 'test_*.py'
nix build --no-link .#checks.x86_64-linux.agent-rules
nix flake check
git status --short --ignored
```

Review formatter changes, the final diff, ignored artifacts, and the distribution boundary before finishing. Do not delete pre-existing ignored artifacts unless the task created them or the user explicitly authorizes their removal. The hygiene requirement is that the task leaves no new temporary artifacts; disclose relevant pre-existing ignored roots in the final report.
