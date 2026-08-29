# Nix Layout Reference

Read this only when creating or reorganizing Nix, Just, script, check, or formatter layout. Create only files with real behavior.

## Ordinary project

```text
flake.nix
flake.lock
justfile
```

Expose one `devShells.<system>.default` and enter it with `nix develop`. Include the tools needed to build, run, test, format, lint, generate code, and exercise browser flows in that shell. Add `scripts/` only for durable complex orchestration, and add `nix/` only when reusable Nix logic would otherwise obscure the flake's input and output wiring.

Do not add packages, apps, checks, formatter, modules, overlays, or named development shells to an ordinary development-environment flake without an exact user request or an applicable project-wide Nix contract.

## Project-wide Nix interface

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
treefmt.nix
```

Use only the entries that represent actual project interfaces. An applicable project instruction may make Nix the project-wide interface; a pure Nix product has that boundary by definition. A `justfile` remains optional for a pure Nix product. Add module, overlay, template, script, check, and formatter files only when exported or durable behavior exists.

## Output ownership

- `packages`: buildable products.
- `apps`: runnable wrappers.
- `devShells.default`: the ordinary reproducible development environment; named shells require an explicit isolation contract.
- `checks`: stable validation used by `nix flake check`.
- `formatter`: an explicitly adopted `nix fmt` entrypoint; prefer treefmt-nix when one interface coordinates multiple languages.
- `overlays` and modules: public pure-Nix interfaces only when the project provides them.

## Just command surface

Give a recipe only to a stable project-level command that multiple contributors run repeatedly and that benefits from one environment, argument, or semantic entrypoint. A durable script does not automatically need a recipe. Keep one-off diagnostics, CI-internal steps, subsystem-private implementation, and obvious direct commands out of the menu.

```just
# Run focused tests in the default development shell.
test:
  nix develop --command go test ./...

# Build the adopted Nix product interface.
build:
  nix build .#default

# Run the adopted Nix validation interface.
check:
  nix flake check
```

The `build` and `check` recipes apply only when those Nix outputs are project interfaces. Invoke recipes directly, for example with `just test`. When already inside the development shell, run the underlying tool or script instead of Just.

Keep a recipe to at most three simple linear command invocations. Do not create a `dev` alias, call or depend on another recipe, invoke Just through `nix develop`, or place loops, parsing, branching, retries, traps, cleanup, complex quoting, or stateful orchestration in a recipe.

## treefmt-nix

When Nix owns a multi-language formatting interface, evaluate one treefmt-nix module per supported system and export its wrapper as `formatter.<system>`. Add the evaluation check to `checks.<system>` only when formatting validation is also an adopted Nix interface. Configure only formatters the repository uses, and rely on formatter defaults unless product conventions require overrides.
