# Nix Layout Reference

Use this reference only when the shorter Nix rule is not enough to create or reorganize files.

## Ordinary project layout

```text
.
├── flake.nix
├── flake.lock
├── justfile
├── nix/
│   ├── lib.nix
│   ├── packages.nix
│   ├── apps.nix
│   ├── dev-shells.nix
│   ├── checks.nix
│   └── formatter.nix
├── scripts/
│   └── <durable-script>.<ext>
├── treefmt.nix
├── .prettierrc.json
└── .editorconfig
```

Use only files that are needed. Do not create empty structure for symmetry.

## Pure Nix project layout

```text
.
├── flake.nix
├── flake.lock
├── nix/
│   ├── lib.nix
│   ├── packages.nix
│   ├── apps.nix
│   ├── dev-shells.nix
│   ├── checks.nix
│   ├── formatter.nix
│   ├── overlays/
│   ├── modules/
│   ├── nixos-modules/
│   ├── home-manager-modules/
│   └── templates/
├── tests/
└── scripts/
```

## Output responsibilities

- `packages`: buildable artifacts.
- `apps`: runnable wrappers for `nix run`.
- `devShells`: reproducible development environments.
- `checks`: stable validation for `nix flake check`.
- `formatter`: formatter used by `nix fmt`, preferably treefmt-nix for multi-language projects.
- `templates`: reusable flake templates.
- `overlays`, `nixosModules`, and `homeManagerModules`: public pure-Nix interfaces when the project provides them.

## Ordinary justfile shape

```just
# Run unit tests.
test:
  nix develop .#dev --command go test ./...

# Run race tests.
test-race:
  nix develop .#dev --command go test -race ./...

# Format configured project files through the flake formatter.
fmt:
  nix fmt

# Run stable flake checks.
check:
  nix flake check
```

## Pure Nix optional justfile shape

```just
# Show exported flake outputs.
show:
  nix flake show

# Build the default package.
build:
  nix build .#default

# Run all stable flake checks.
check:
  nix flake check

# Format configured files through the flake formatter.
fmt:
  nix fmt
```

## treefmt-nix shape

Seed shared defaults from:

- `.agents/templates/treefmt.nix`
- `.agents/templates/.prettierrc.json`
- `.agents/templates/.editorconfig`

Prefer one flake `formatter` output that calls treefmt. Keep language-specific formatters as narrower developer commands only when that matches local convention.
