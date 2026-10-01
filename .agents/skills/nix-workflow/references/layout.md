# Nix Layout Reference

Use the repository's established layout. The examples below are starting points for interfaces the project has adopted, not a directory checklist.

## Development environment

A minimal new setup uses `flake.nix`, its generated `flake.lock`, and a default development shell. Add a `justfile` for useful shared commands. Keep tool setup in the shell; introduce `nix/` or named shells when reuse or isolation needs them.

A development shell does not imply packages, apps, checks, formatter, modules, or overlays. These require an explicit user or project choice.

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

Recipes may compose and may run inside or outside the shell. Make it clear which layer enters the environment, and avoid cycles such as a recipe entering a shell that invokes the same recipe again.

For a command designed to enter the shell itself:

```just
# Run project tests through the development environment.
test:
  nix develop --command go test ./...
```

An environment-independent recipe may instead call the tool directly and be invoked as `nix develop --command just test` or from an entered shell. Follow one understandable convention for each command. Move complex orchestration into a reusable script when useful; do not impose command-count limits.

Expose `nix build .#<output>` through Just only for an adopted product output. A general `check` recipe need not mean `nix flake check`; prefer focused development-shell commands when those are the project's checks.

## Formatting

An ordinary shell can provide ecosystem formatters. When the project explicitly adopts a shared Nix formatting interface, prefer treefmt-nix for multiple languages, exporting its wrapper as `formatter.<system>`. Add its check only if Nix formatting validation is also adopted. Configure the languages present and preserve formatter defaults unless conventions require overrides.
