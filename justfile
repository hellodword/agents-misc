set positional-arguments

# Enter the default development shell.
dev:
  nix develop .#dev

# Show flake outputs.
show:
  nix flake show

# Format configured project files through the flake formatter.
fmt:
  nix fmt

# Run stable flake checks.
check:
  nix flake check

# Build the default patched Codex package.
build:
  nix build .#default

# Build repository tool packages without linking outputs.
build-tools:
  nix build --no-link .#codexcfg .#codexSchemaRegistry .#codexConfigData .#codexConfigSite .#rulesync

# Fetch an upstream Codex checkout for a ref.
codex-fetch ref:
  nix develop .#dev --command python3 codex/scripts/fetch-upstream.py --ref {{ref}}

# Check whether Codex patches apply to a ref.
codex-apply-check ref:
  nix develop .#dev --command python3 codex/scripts/apply-patches.py --ref {{ref}} --check

# Apply Codex patches to a fetched ref.
codex-apply ref:
  nix develop .#dev --command python3 codex/scripts/apply-patches.py --ref {{ref}}

# Refresh Codex patches against a ref.
codex-refresh ref:
  nix develop .#dev --command python3 codex/scripts/refresh-patches.py --ref {{ref}}

# Run Codex patch tests against a ref.
codex-test ref:
  nix develop .#dev --command python3 codex/scripts/test.py --ref {{ref}}

# Build patched Codex against a ref.
codex-build ref:
  nix develop .#dev --command python3 codex/scripts/build.py --ref {{ref}}

# Print current Codex config schema metadata.
codexcfg-current:
  nix run .#codexcfg -- current

# Validate the checked-in Codex schema registry.
codexcfg-check-registry:
  nix run .#codexcfg -- check-registry --schemas codex/schemas

# Sync an upstream Codex config schema into the registry.
codexcfg-sync-schema version:
  nix run .#codexcfg -- sync-schema --schemas codex/schemas --version {{version}}

# Diff Codex config schemas between two versions.
codexcfg-diff from to:
  nix run .#codexcfg -- diff --schemas codex/schemas --from {{from}} --to {{to}}

# Diff Codex config defaults between two versions.
codexcfg-diff-defaults from to:
  nix run .#codexcfg -- diff-defaults --schemas codex/schemas --from {{from}} --to {{to}}

# Generate Codex config TOML for a version and mode.
codexcfg-gen-toml version mode="reference":
  nix run .#codexcfg -- gen-toml --schemas codex/schemas --version {{version}} --mode {{mode}}

# Build the jailed Rulesync package.
rulesync-build:
  nix build --no-link .#rulesync

# Print the packaged Rulesync version.
rulesync-version:
  nix run .#rulesync -- --version

# Initialize Rulesync files in the current project.
rulesync-init:
  nix run .#rulesync -- init

# Generate Rulesync target files.
rulesync-generate:
  nix run .#rulesync -- generate

# Check whether Rulesync generated files are current.
rulesync-generate-check:
  nix run .#rulesync -- generate --check

# Update Rulesync ignore files.
rulesync-gitignore:
  nix run .#rulesync -- gitignore

# Import rules into Rulesync format.
rulesync-import target="codexcli" features="rules":
  nix run .#rulesync -- import -t {{target}} -f {{features}}

# Convert Rulesync content between targets.
rulesync-convert from="codexcli" to="opencode" features="rules":
  nix run .#rulesync -- convert --from {{from}} --to {{to}} -f {{features}}
