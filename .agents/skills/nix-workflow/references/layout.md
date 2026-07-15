# Nix Layout Reference

Read this only when creating or reorganizing Nix, Just, script, check, or formatter layout. Create only files with real behavior.

## Ordinary project

```text
flake.nix
flake.lock
justfile
nix/
  packages.nix
  apps.nix
  dev-shells.nix
  checks.nix
  formatter.nix
scripts/
treefmt.nix
```

Use Nix for the reproducible environment and Just as a thin human-facing command menu. Keep `flake.nix` to inputs, supported systems, and output wiring.

## Pure Nix project

```text
flake.nix
flake.lock
nix/
  packages.nix
  apps.nix
  dev-shells.nix
  checks.nix
  formatter.nix
  modules/
  overlays/
tests/
scripts/
```

Flake outputs are the product interface. A `justfile` is optional. Add module/overlay/template directories only when exported behavior exists.

## Output ownership

- `packages`: buildable products.
- `apps`: runnable wrappers.
- `devShells`: reproducible development environments.
- `checks`: stable validation used by `nix flake check`.
- `formatter`: the `nix fmt` entrypoint, backed by treefmt-nix.
- `overlays` and modules: public pure-Nix interfaces only when the project provides them.

## Thin Just example

```just
# Run focused tests inside the development shell.
test:
  nix develop .#dev --command go test ./...

# Format configured files through treefmt-nix.
fmt:
  nix fmt

# Run stable flake checks.
check:
  nix flake check
```

Move loops, parsing, branching, retries, cleanup, and stateful orchestration to a checked-in script.

## treefmt-nix

Evaluate one treefmt-nix module per supported system and export its wrapper as `formatter.<system>`. Add the evaluation check to `checks.<system>`. Configure only formatters the repository actually uses, and rely on formatter defaults unless product conventions require overrides.
