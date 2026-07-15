# Nix and Just

Apply this rule when Nix/Just is established, explicitly requested, or used as a greenfield default.

- For greenfield work, set `nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable"`; let the project's own `flake.lock` pin the revision.
- Do not align `nixpkgs` to an unrelated container revision.
- Use a flake plus a thin `justfile` for ordinary projects. A pure Nix project does not require Just.
- Keep `flake.nix` focused on inputs and output wiring. Put reusable implementation under `nix/`; do not create empty modules for layout symmetry.
- Expose packages, apps, dev shells, checks, and formatter outputs only when they are real project interfaces.
- Use treefmt-nix for every Nix project and expose it through the flake formatter and checks.
- Put durable imperative orchestration in checked-in scripts and call it from thin documented Just recipes.
- Run project tools through the named development shell. Preserve established shell names and public flake outputs unless the task changes them.
- Do not use a `path:` flake reference to bypass Git source filtering in a Git worktree.
- If a durable, non-secret, non-temporary, non-ignored untracked file is required by a Git-backed flake, use only `git add -N -- <file>`, leave intent-to-add in place, and report it.
- Run focused output validation first; use `nix flake show` for public output changes and `nix flake check` for broad flake/check wiring.
- Treat `nix fmt` as mutating and review its diff.
