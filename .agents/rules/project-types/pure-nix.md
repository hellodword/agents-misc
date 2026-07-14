---
id: project-type.pure-nix
kind: project-type
triggers:
  - "pure Nix"
  - "flake outputs"
  - "NixOS module"
  - "Home Manager module"
  - "overlay"
  - "flake template"
summary: Apply defaults for projects whose primary product is Nix outputs.
companions:
  required_rules:
    - toolchain.nix
  conditional_rules:
    - id: core.compatibility
      when: public flake outputs, modules, overlays, or templates change
    - id: core.scripts
      when: imperative orchestration scripts are added or changed
  skills:
    - id: nix-workflow
      when: designing or restructuring public flake outputs
  references:
    - id: nix-layout
      when: layout or output examples are needed
---

# Pure Nix Project Rules

Use this rule for projects whose core product is Nix itself, not merely projects that use Nix as a build or development environment entrypoint.

Treat a project as pure Nix only when the user explicitly says so or the repository clearly states that its main product is one or more of:

- Nix package sets;
- overlays;
- NixOS modules;
- Home Manager modules;
- flake templates;
- Nix libraries;
- Nix-based development environments;
- Nix-based infrastructure or deployment modules.

## Public interface

For pure Nix projects, flake outputs are the primary public interface.

Expose relevant outputs explicitly:

- `packages.${system}.default` and named packages;
- `apps.${system}.default` and named apps;
- `devShells.${system}.dev` and named shells;
- `checks.${system}.<name>`;
- `formatter.${system}`;
- `templates.<name>`;
- `overlays.<name>`;
- `nixosModules.<name>`;
- `homeManagerModules.<name>`.

Do not expose unstable or experimental outputs unless their status is clear.

## Repository shape

- Keep `flake.nix` as the assembly entrypoint.
- Put reusable Nix product logic under `./nix/`.
- Keep modules, overlays, templates, package definitions, and library functions focused and composable.
- Prefer small module interfaces over passing the entire flake scope everywhere.
- Use imperative scripts only for orchestration that does not belong in product Nix code.

## Justfile

A `justfile` is not required.

When one exists, keep it optional, documented, and flake-native. It must not become the primary interface for public Nix outputs.

## Validation

Default validation:

```sh
nix flake show
nix flake check
```

When a formatter exists, run `nix fmt` as a formatting step and review changed files.

When specific outputs changed, validate them directly with `nix build`, `nix run`, or `nix develop` as appropriate.
