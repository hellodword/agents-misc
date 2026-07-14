set positional-arguments

# Nix owns the complete viewer toolchain; Just only exposes human-facing commands.
viewer_nix := "nix develop .#agents-viewer --command"

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

# Validate shared agent-rules metadata, routing, schemas, and checker tests.
check-agent-rules:
  nix develop .#dev --command python3 scripts/check-agent-rules.py --root .
  nix develop .#dev --command python3 -m unittest discover -s scripts/tests -p 'test_*.py'

# Build the default patched Codex package.
build:
  nix build .#default

# Build repository tool packages without linking outputs.
build-tools:
  nix build --no-link .#codex-config-atlas .#codex-config-atlas-registry .#codex-config-atlas-data .#codex-config-atlas-site

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
codex-config-atlas-current:
  nix run .#codex-config-atlas -- current

# Validate the checked-in Codex schema registry.
codex-config-atlas-check-registry:
  nix run .#codex-config-atlas -- check-registry --schemas tools/codex-config-atlas/schemas

# Sync an upstream Codex config schema into the registry.
codex-config-atlas-sync-schema version:
  nix run .#codex-config-atlas -- sync-schema --schemas tools/codex-config-atlas/schemas --version {{version}}

# Diff Codex config schemas between two versions.
codex-config-atlas-diff from to:
  nix run .#codex-config-atlas -- diff --schemas tools/codex-config-atlas/schemas --from {{from}} --to {{to}}

# Diff Codex config defaults between two versions.
codex-config-atlas-diff-defaults from to:
  nix run .#codex-config-atlas -- diff-defaults --schemas tools/codex-config-atlas/schemas --from {{from}} --to {{to}}

# Generate Codex config TOML for a version and mode.
codex-config-atlas-gen-toml version mode="reference":
  nix run .#codex-config-atlas -- gen-toml --schemas tools/codex-config-atlas/schemas --version {{version}} --mode {{mode}}

# Run the viewer API with the non-embedded development shell.
agents-viewer-api-dev *args:
  {{viewer_nix}} cargo run --manifest-path tools/agents-viewer/Cargo.toml --bin agents-viewer -- {{args}}

# Run the packaged viewer. Viewer settings come from config.toml.
agents-viewer-run *args:
  nix run .#agents-viewer -- {{args}}

# Run the Vite development server; proxy API requests to the default viewer port.
agents-viewer-web-dev:
  {{viewer_nix}} just _agents-viewer-web-dev

[private]
_agents-viewer-web-dev:
  npm --prefix tools/agents-viewer/web ci
  npm --prefix tools/agents-viewer/web run dev

# Build the web bundle and the single embedded release executable.
agents-viewer-build:
  nix build .#agents-viewer

# Run fast Rust and browserless Web tests.
agents-viewer-test:
  {{viewer_nix}} just _agents-viewer-test

[private]
_agents-viewer-test:
  cargo test --manifest-path tools/agents-viewer/Cargo.toml
  npm --prefix tools/agents-viewer/web ci
  npm --prefix tools/agents-viewer/web run test

# Build the embedded debug binary and run host-browser E2E tests.
agents-viewer-e2e:
  {{viewer_nix}} just _agents-viewer-e2e

[private]
_agents-viewer-e2e:
  npm --prefix tools/agents-viewer/web ci
  npm --prefix tools/agents-viewer/web run build
  cargo build --manifest-path tools/agents-viewer/Cargo.toml --bin agents-viewer --features embedded-ui
  npm --prefix tools/agents-viewer/web run e2e

# Export TypeScript API bindings from Rust DTOs.
agents-viewer-generate:
  {{viewer_nix}} cargo run --manifest-path tools/agents-viewer/Cargo.toml --bin export_types -- --write

# Confirm checked-in TypeScript bindings match Rust DTOs.
agents-viewer-generate-check:
  {{viewer_nix}} cargo run --manifest-path tools/agents-viewer/Cargo.toml --bin export_types -- --check

# Run ignored large gates plus Linux syscall read-only validation.
agents-viewer-acceptance-large:
  {{viewer_nix}} just _agents-viewer-acceptance-large

[private]
_agents-viewer-acceptance-large:
  cargo test --manifest-path tools/agents-viewer/Cargo.toml --test performance -- --ignored --nocapture --test-threads=1
  cargo test --manifest-path tools/agents-viewer/Cargo.toml --test read_only_strace -- --ignored --nocapture

# Browser-independent generation, format, static, unit, integration, and Nix gates.
agents-viewer-verify:
  {{viewer_nix}} just _agents-viewer-verify

[private]
_agents-viewer-verify:
  cargo run --manifest-path tools/agents-viewer/Cargo.toml --bin export_types -- --check
  cargo fmt --manifest-path tools/agents-viewer/Cargo.toml --all -- --check
  cargo clippy --manifest-path tools/agents-viewer/Cargo.toml --all-targets -- -D warnings
  cargo test --manifest-path tools/agents-viewer/Cargo.toml
  npm --prefix tools/agents-viewer/web ci
  npm --prefix tools/agents-viewer/web run typecheck
  npm --prefix tools/agents-viewer/web run test
  npm --prefix tools/agents-viewer/web run build
  cargo clippy --manifest-path tools/agents-viewer/Cargo.toml --bin agents-viewer --features embedded-ui -- -D warnings
  nix build --no-link .#agents-viewer
