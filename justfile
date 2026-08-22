set positional-arguments

# Nix owns each task toolchain; public recipes only enter the matching shell.
codex_nix := "nix develop .#codex --command"
viewer_nix := "nix develop .#agents-viewer --command"
agent_evals_nix := "nix develop .#agent-evals --command"

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

# Validate rule structure, skill assets, and the eval corpus.
check-agent-rules:
  nix develop .#dev --command just _check-agent-rules

[private]
_check-agent-rules:
  @test "${AGENTS_MISC_SHELL:-}" = "dev" || { echo "error: run 'just check-agent-rules' to enter nix develop .#dev" >&2; exit 2; }
  python3 scripts/check-agent-rules.py --root .
  python3 scripts/check-maintenance-docs.py --root .
  python3 -m unittest discover -s tests -p 'test_*.py'

# Validate maintenance links and safely smoke every documented shell command.
check-docs:
  nix develop .#dev --command just _check-docs

[private]
_check-docs:
  @test "${AGENTS_MISC_SHELL:-}" = "dev" || { echo "error: run 'just check-docs' to enter nix develop .#dev" >&2; exit 2; }
  python3 scripts/check-maintenance-docs.py --root . --execute-commands

# Validate GitHub Actions syntax, official major tags, and verification gates.
check-workflows:
  nix develop .#dev --command just _check-workflows

[private]
_check-workflows:
  @test "${AGENTS_MISC_SHELL:-}" = "dev" || { echo "error: run 'just check-workflows' to enter nix develop .#dev" >&2; exit 2; }
  actionlint .github/workflows/*.yml
  python3 scripts/check-workflows.py --root .
  python3 -m unittest tests.test_check_workflows

# Seed the independent Agent eval ChatGPT credential vault.
agent-evals-auth-init *args:
  {{agent_evals_nix}} just -- _agent-evals-auth-init "$@"

[private]
_agent-evals-auth-init *args:
  @test "${AGENTS_MISC_SHELL:-}" = "agent-evals" || { echo "error: run 'just agent-evals-auth-init' to enter nix develop .#agent-evals" >&2; exit 2; }
  python3 -m tools.agent_evals auth-init "$@"

# Verify Agent eval prompt sources and the versioned no-execution-tool surface.
agent-evals-preflight *args:
  {{agent_evals_nix}} just -- _agent-evals-preflight "$@"

[private]
_agent-evals-preflight *args:
  @test "${AGENTS_MISC_SHELL:-}" = "agent-evals" || { echo "error: run 'just agent-evals-preflight' to enter nix develop .#agent-evals" >&2; exit 2; }
  python3 -m tools.agent_evals preflight "$@"

# Run isolated Codex route, behavior, judge, and certification evals.
agent-evals *args:
  {{agent_evals_nix}} just -- _agent-evals "$@"

[private]
_agent-evals *args:
  @test "${AGENTS_MISC_SHELL:-}" = "agent-evals" || { echo "error: run 'just agent-evals' to enter nix develop .#agent-evals" >&2; exit 2; }
  python3 -m tools.agent_evals run "$@"

# Build the default patched Codex package.
build:
  nix build .#default

# Build repository tool packages without linking outputs.
build-tools:
  nix build --no-link .#codex-config-atlas .#codex-config-atlas-registry .#codex-config-atlas-data .#codex-config-atlas-site

# Validate the declarative Codex maintenance manifests.
codex-manifest-check:
  {{codex_nix}} just _codex-manifest-check

[private]
_codex-manifest-check:
  @test "${AGENTS_MISC_SHELL:-}" = "codex" || { echo "error: run 'just codex-manifest-check' to enter nix develop .#codex" >&2; exit 2; }
  python3 -m unittest codex.tests.test_manifest

# Fetch the one pinned upstream Codex checkout.
codex-fetch:
  {{codex_nix}} just _codex-fetch

[private]
_codex-fetch:
  @test "${AGENTS_MISC_SHELL:-}" = "codex" || { echo "error: run 'just codex-fetch' to enter nix develop .#codex" >&2; exit 2; }
  python3 codex/scripts/fetch-upstream.py

# Check whether the pinned Codex patches apply cumulatively.
codex-apply-check:
  {{codex_nix}} just _codex-apply-check

[private]
_codex-apply-check:
  @test "${AGENTS_MISC_SHELL:-}" = "codex" || { echo "error: run 'just codex-apply-check' to enter nix develop .#codex" >&2; exit 2; }
  python3 codex/scripts/apply-patches.py --check

# Apply the pinned Codex patches.
codex-apply:
  {{codex_nix}} just _codex-apply

[private]
_codex-apply:
  @test "${AGENTS_MISC_SHELL:-}" = "codex" || { echo "error: run 'just codex-apply' to enter nix develop .#codex" >&2; exit 2; }
  python3 codex/scripts/apply-patches.py

# Fully validate candidate patches without changing the checked-in patch set.
codex-refresh-dry-run:
  {{codex_nix}} just _codex-refresh-dry-run

[private]
_codex-refresh-dry-run:
  @test "${AGENTS_MISC_SHELL:-}" = "codex" || { echo "error: run 'just codex-refresh-dry-run' to enter nix develop .#codex" >&2; exit 2; }
  python3 codex/scripts/refresh-patches.py --dry-run

# Atomically refresh the pinned Codex patch set.
codex-refresh:
  {{codex_nix}} just _codex-refresh

[private]
_codex-refresh:
  @test "${AGENTS_MISC_SHELL:-}" = "codex" || { echo "error: run 'just codex-refresh' to enter nix develop .#codex" >&2; exit 2; }
  python3 codex/scripts/refresh-patches.py

# Run cumulative apply, generation-drift, Cargo, and targeted Codex tests.
codex-test:
  {{codex_nix}} just _codex-test

[private]
_codex-test:
  @test "${AGENTS_MISC_SHELL:-}" = "codex" || { echo "error: run 'just codex-test' to enter nix develop .#codex" >&2; exit 2; }
  python3 codex/scripts/test.py

# Run the manifest-defined Codex Cargo validation command.
codex-build:
  {{codex_nix}} just _codex-build

[private]
_codex-build:
  @test "${AGENTS_MISC_SHELL:-}" = "codex" || { echo "error: run 'just codex-build' to enter nix develop .#codex" >&2; exit 2; }
  python3 codex/scripts/build.py

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
  {{viewer_nix}} just -- _agents-viewer-api-dev "$@"

[private]
_agents-viewer-api-dev *args:
  @test "${AGENTS_MISC_SHELL:-}" = "agents-viewer" || { echo "error: run 'just agents-viewer-api-dev' to enter nix develop .#agents-viewer" >&2; exit 2; }
  cargo run --manifest-path tools/agents-viewer/Cargo.toml --bin agents-viewer -- "$@"

# Run the packaged viewer. Viewer settings come from config.toml.
agents-viewer-run *args:
  nix run .#agents-viewer -- {{args}}

# Run the Vite development server; proxy API requests to the default viewer port.
agents-viewer-web-dev:
  {{viewer_nix}} just _agents-viewer-web-dev

[private]
_agents-viewer-web-dev:
  @test "${AGENTS_MISC_SHELL:-}" = "agents-viewer" || { echo "error: run 'just agents-viewer-web-dev' to enter nix develop .#agents-viewer" >&2; exit 2; }
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
  @test "${AGENTS_MISC_SHELL:-}" = "agents-viewer" || { echo "error: run 'just agents-viewer-test' to enter nix develop .#agents-viewer" >&2; exit 2; }
  cargo test --manifest-path tools/agents-viewer/Cargo.toml
  npm --prefix tools/agents-viewer/web ci
  npm --prefix tools/agents-viewer/web run test

# Rebuild Web into the embedded debug binary before E2E; forward optional Playwright arguments.
agents-viewer-e2e *args:
  {{viewer_nix}} just -- _agents-viewer-e2e "$@"

[private]
_agents-viewer-build-embedded-debug:
  @test "${AGENTS_MISC_SHELL:-}" = "agents-viewer" || { echo "error: run 'just agents-viewer-e2e' to enter nix develop .#agents-viewer" >&2; exit 2; }
  npm --prefix tools/agents-viewer/web ci
  npm --prefix tools/agents-viewer/web run build
  cargo build --manifest-path tools/agents-viewer/Cargo.toml --bin agents-viewer --features embedded-ui

[private]
_agents-viewer-e2e *args:
  @test "${AGENTS_MISC_SHELL:-}" = "agents-viewer" || { echo "error: run 'just agents-viewer-e2e' to enter nix develop .#agents-viewer" >&2; exit 2; }
  just _agents-viewer-build-embedded-debug
  npm --prefix tools/agents-viewer/web run e2e -- "$@"

# Export TypeScript API bindings from Rust DTOs.
agents-viewer-generate:
  {{viewer_nix}} just _agents-viewer-generate

[private]
_agents-viewer-generate:
  @test "${AGENTS_MISC_SHELL:-}" = "agents-viewer" || { echo "error: run 'just agents-viewer-generate' to enter nix develop .#agents-viewer" >&2; exit 2; }
  cargo run --manifest-path tools/agents-viewer/Cargo.toml --bin export_types -- --write

# Confirm checked-in TypeScript bindings match Rust DTOs.
agents-viewer-generate-check:
  {{viewer_nix}} just _agents-viewer-generate-check

[private]
_agents-viewer-generate-check:
  @test "${AGENTS_MISC_SHELL:-}" = "agents-viewer" || { echo "error: run 'just agents-viewer-generate-check' to enter nix develop .#agents-viewer" >&2; exit 2; }
  cargo run --manifest-path tools/agents-viewer/Cargo.toml --bin export_types -- --check

# Run ignored large gates plus Linux syscall read-only validation.
agents-viewer-acceptance-large:
  {{viewer_nix}} just _agents-viewer-acceptance-large

[private]
_agents-viewer-acceptance-large:
  @test "${AGENTS_MISC_SHELL:-}" = "agents-viewer" || { echo "error: run 'just agents-viewer-acceptance-large' to enter nix develop .#agents-viewer" >&2; exit 2; }
  cargo test --manifest-path tools/agents-viewer/Cargo.toml --test performance -- --ignored --nocapture --test-threads=1
  cargo test --manifest-path tools/agents-viewer/Cargo.toml --test read_only_strace -- --ignored --nocapture

# Browser-independent generation, format, static, unit, integration, and Nix gates.
agents-viewer-verify:
  {{viewer_nix}} just _agents-viewer-verify

[private]
_agents-viewer-verify:
  @test "${AGENTS_MISC_SHELL:-}" = "agents-viewer" || { echo "error: run 'just agents-viewer-verify' to enter nix develop .#agents-viewer" >&2; exit 2; }
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
