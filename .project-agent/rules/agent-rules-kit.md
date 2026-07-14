# Agent Rules Kit Maintenance

## Distribution boundary

- `AGENTS.md` and `.agents/**` are the read-only payload consumed by other projects.
- Consumers place project facts, rules, and workflows under `.project-agent/**`; they do not edit the shared payload.
- Do not reference this repository's `.project-agent/**`, `scripts/**`, `schemas/agent-rules/**`, Just recipes, Nix checks, or release process from the distributed payload.
- Keep repository-only maintenance instructions in this overlay, `README.md`, or repository scripts.

## Maintenance contracts

- Central maintenance schemas live under `schemas/agent-rules/`.
- The consumer-facing shared-rules lock schema lives under `.agents/templates/`.
- Keep `.agents/manifest.json`, its central schema, the lock schema, and project-overlay documentation aligned.
- Use a kit patch version for non-semantic corrections and a minor version for consumer-visible routing, default, authorization, or loading changes while the kit remains `0.x`.
- Increment `manifest_schema_version` only when manifest shape or field semantics change.
- Increment `rules_schema_version` when rule metadata or runtime rule semantics change.
- Increment `skills_schema_version` when skill metadata or loading semantics change.
- Increment `overlay_discovery_version` when overlay paths or loading order change.
- Increment `companion_metadata_version` when companion fields or loading semantics change.
- Increment the lock schema version only when `.project-agent/shared-rules.lock` shape changes.

## Validation

Run:

```sh
just check-agent-rules
nix build --no-link .#checks.x86_64-linux.agent-rules
nix flake check
```

The structural checker must reject maintenance-only references in `AGENTS.md` or `.agents/**`.
