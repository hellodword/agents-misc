# Rulesync strict wrapper

This directory packages [Rulesync](https://github.com/dyoshikawa/rulesync) as a
Nix flake package with a strict runtime wrapper.

Rulesync is useful because it can keep AI-agent project instructions, MCP
configuration, hooks, skills, and related files in sync across multiple tools.
The wrapper in this repository exists to make that useful in local development
without giving the upstream CLI broad access to the user's home directory,
network, or unrelated project files.

## Motivation

The upstream CLI supports many commands and many target tools. That flexibility
is too broad for this repository's default use case:

- generated files should stay inside the current project;
- network-capable commands should not run from the wrapper;
- the real `$HOME` directory should never be used as the project root;
- writes should be limited to the paths needed by the current command;
- `rulesync init` should work directly, with no separate bootstrap command;
- `.cursorrules` should not be created as a compatibility mirror unless an
  upstream Rulesync target factory explicitly asks for that path.

The result is a jailed `rulesync` command that preserves the local, file-based
workflow while making the writable surface predictable.

## Usage

Build or enter the package from the repository flake:

```sh
nix build .#rulesync
nix shell .#rulesync
```

You can also run commands directly through the flake:

```sh
nix run .#rulesync -- init
nix run .#rulesync -- generate
```

The repository `justfile` exposes the same wrapper:

```sh
just rulesync-build
just rulesync-version
just rulesync-init
just rulesync-generate
just rulesync-generate-check
just rulesync-gitignore
just rulesync-import
just rulesync-convert
```

Supported commands:

```sh
rulesync init
rulesync generate
rulesync gitignore
rulesync import -t codexcli -f rules
rulesync convert --from codexcli --to opencode -f rules
```

The wrapper intentionally rejects:

```sh
rulesync fetch
rulesync install
rulesync update
rulesync mcp
rulesync generate --global
rulesync generate -g
rulesync generate --input-root ..
```

Run the command from the project root, or set `RULESYNC_PROJECT_ROOT` to an
existing project directory. The project root must not resolve to the real
`$HOME` directory.

Runtime commands require Bubblewrap and a container or host that allows
unprivileged user namespaces. If that kernel or container capability is not
available, `nix build .#rulesync` and `just rulesync-build` can still validate
the package build, but `nix run .#rulesync -- ...` and the runtime `just`
recipes will fail when Bubblewrap tries to create the jail.

Typical first-time setup:

```sh
rulesync init
rulesync generate
rulesync gitignore
```

`rulesync init` copies a build-time upstream initialization template into the
project. Existing regular files are left in place. Symlinked inputs or outputs
are rejected.

## Design

The implementation is split into three files:

- `nix/default.nix` builds upstream Rulesync, creates the init template, defines
  the policy JSON, and exposes the jailed `rulesync` package.
- `nix/wrapper.sh` enforces the command allowlist, project-root checks,
  symlink checks, Bubblewrap jail, read-only mounts, and writable output binds.
- `nix/scope.mjs` loads Rulesync's config resolver and target factories to
  compute the read/write scope for the current command.

The runtime jail uses Bubblewrap with:

- `--unshare-all` and `--cap-drop ALL`;
- a synthetic `HOME=/tmp/home`;
- `PATH=/no-such-path`;
- a read-only Nix store closure for Node.js, Rulesync, and the scope helper;
- read-only mounts for Rulesync source inputs;
- read/write mounts only for dynamically computed command outputs.

`generate`, `import`, `convert`, and `gitignore` use the scope helper before
running upstream Rulesync. The helper resolves the effective Rulesync config,
validates that `global` is disabled, requires `inputRoot` to be the project
root, validates all `outputRoots`, and asks upstream target factories which
paths may be touched.

`init` does not run upstream Rulesync at runtime. The Nix build creates a clean
template by running upstream `rulesync init --silent` with an isolated home and
empty `PATH`. Runtime `init` copies that template into the project after the
same project-relative and symlink checks.

Dry-run and check-style executions are treated as preview mode. The wrapper
provides temporary in-jail placeholders where upstream Rulesync expects files,
but it does not materialize generated host outputs.

## Upgrade notes

When upgrading Rulesync:

1. Update `version`, the GitHub source hash, and the pnpm dependency hash in
   `nix/default.nix`.
2. Rebuild `.#rulesync`.
3. Check whether the upstream built `dist/import-*.js` filename changed. If it
   did, update the `rulesyncDistImport` path in `nix/default.nix`.
4. Re-check the minified export aliases imported by `nix/scope.mjs`. These are
   upstream build artifacts and may change between releases.
5. Review new or changed target factories. If upstream adds output paths,
   update the policy, denylist, or repository `.gitignore` entries as needed.
6. Re-run behavior checks for `init`, `generate`, `gitignore`, `import`,
   `convert`, dry-run behavior, rejected network commands, rejected global
   mode, rejected external `inputRoot`, rejected external `outputRoots`, and
   the absence of the legacy `rulesync-bootstrap` flake attribute.

Useful validation commands:

```sh
nixfmt rulesync/nix/default.nix flake.nix
bash -n rulesync/nix/wrapper.sh
node --check rulesync/nix/scope.mjs
git diff --check -- rulesync/nix/default.nix rulesync/nix/wrapper.sh rulesync/nix/scope.mjs flake.nix
nix build --no-link --print-out-paths .#rulesync
nix flake show --all-systems | rg rulesync-bootstrap
nix build .#rulesync-bootstrap
```

The `rg rulesync-bootstrap` command should produce no matches in the flake
output, and `nix build .#rulesync-bootstrap` should fail because the compatibility
bootstrap package is intentionally not exposed.
