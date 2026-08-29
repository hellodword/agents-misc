# Nix and Just

Apply this rule when Nix/Just is established, explicitly requested, or used as a greenfield default.

## Ownership and compatibility

- Classify Nix as an ordinary development environment, a project-wide product interface, or the implementation language of a pure Nix product.
- Treat an exact user request, an applicable project overlay or project document, or the pure Nix classification as authority for the outputs within its scope. A pre-existing output establishes only its own contract and does not authorize unrelated outputs.
- Preserve established shell names and public flake outputs unless the task explicitly changes them.
- For greenfield work, set `nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable"`; let the project's own `flake.lock` pin the revision. Do not align `nixpkgs` to an unrelated container revision.

## Development environment

- When establishing an ordinary Nix workflow, expose one `devShells.<system>.default` and enter it with `nix develop`. Include every tool needed for normal build, run, test, formatting, lint, code generation, and browser-test work in that shell.
- Add a named development shell only when the user or an applicable project contract requires an isolated environment. Extend the default shell for ordinary tool additions.
- Do not expose packages, apps, checks, formatter, modules, or overlays merely because the repository uses a Nix development shell.

## Product outputs and commands

- When Nix is an explicitly adopted project-wide interface or the project is a pure Nix product, expose only the packages, apps, checks, formatter, modules, and overlays with real consumers.
- Keep `flake.nix` focused on inputs, supported systems, and output wiring. Put reusable implementation under `nix/`; do not create empty modules for layout symmetry.
- Use treefmt-nix when an explicitly Nix-managed multi-language formatting interface needs one formatter entrypoint and check. An ordinary development shell may contain ecosystem formatters without exposing a flake formatter.
- Invoke Just directly, outside `nix develop`. Let a recipe call `nix develop --command <tool-or-script>` or an explicitly adopted Nix output; do not add a `dev` alias, invoke Just through `nix develop`, or compose recipes through other recipes.
- Put durable complex orchestration in checked-in scripts. A script receives a Just recipe only when the command independently qualifies as a stable shared project entrypoint.
- For Playwright and other durable browser E2E, prefer `pkgs.chromium` from the project's locked nixpkgs and pass `lib.getExe` through explicit project configuration. Do not discover a host browser through `PATH` or silently download one. Use `pkgs.google-chrome` only when branded Chrome is required, its supported systems fit the project, and the unfree dependency is explicitly authorized.
- Do not use a `path:` flake reference to bypass Git source filtering in a Git worktree.
- If a durable, non-secret, non-temporary, non-ignored untracked file is required by a Git-backed flake, use only `git add -N -- <file>`, leave intent-to-add in place, and report it.
- Run focused output validation first; use `nix flake show` for public output changes and `nix flake check` for broad flake/check wiring.
- Treat `nix fmt` as mutating and review its diff.
