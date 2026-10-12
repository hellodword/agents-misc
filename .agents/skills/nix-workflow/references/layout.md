# Nix Layout Reference

Use the repository's established layout. The examples below are starting points for interfaces the project has adopted, not a directory checklist.

## Adopted product outputs

| Output               | Purpose                                                         |
| -------------------- | --------------------------------------------------------------- |
| `packages`           | Buildable products                                              |
| `apps`               | Runnable wrappers                                               |
| `devShells`          | Project tools, with named environments when isolation is useful |
| `checks`             | Adopted validation exposed through Nix                          |
| `formatter`          | An adopted `nix fmt` entrypoint                                 |
| Modules and overlays | Nix interfaces that the product supplies                        |

Keep `flake.nix` focused on inputs and output wiring when implementation grows. Extract only useful modules, for example `nix/packages.nix` or `nix/checks.nix`. A pure Nix product does not require Just.

## Just commands

Invoke recipes directly, for example with `just test` or `just report`. Each recipe enters the project shell only when its command needs that environment.

For a project with Go tests and an executable report script that needs no Nix environment:

```just
# Run project tests through the development environment.
test:
  nix develop --command go test ./...

# Run the report script directly.
report:
  ./scripts/report.sh
```

Compose recipes when helpful and move complex orchestration into a reusable script when useful.

Expose `nix build .#<output>` through Just only for an adopted product output. A general `check` recipe need not mean `nix flake check`; prefer focused development-shell commands when those are the project's checks.
