set positional-arguments

# Validate the distributed rules, skill metadata, and local references.
check-agent-rules:
  nix develop .#dev --command python3 -B scripts/check-agent-rules.py --root .
  nix develop .#dev --command python3 -B -m unittest tests.test_check_agent_rules

# Validate GitHub Actions syntax, official major tags, and verification gates.
check-workflows:
  nix develop .#dev --command actionlint .github/workflows/*.yml
  nix develop .#dev --command python3 scripts/check-workflows.py --root .
  nix develop .#dev --command python3 -m unittest tests.test_check_workflows

# Fetch or validate the pinned upstream Codex checkout without overwriting edits.
codex-fetch:
  nix develop .#codex --command python3 codex/scripts/maintain.py fetch

# Check the complete binary-capable patch series against the pinned revision.
codex-apply-check:
  nix develop .#codex --command python3 codex/scripts/maintain.py apply --check

# Apply the validated patch series in place to a clean pinned checkout.
codex-apply:
  nix develop .#codex --command python3 codex/scripts/maintain.py apply

# Generate and replay a reproducible candidate patch series without installing it.
codex-refresh-dry-run:
  nix develop .#codex --command python3 codex/scripts/maintain.py refresh --dry-run

# Generate, replay, and install the manifest-owned patch series without Rust tests.
codex-refresh:
  nix develop .#codex --command python3 codex/scripts/maintain.py refresh

# Generate current editing sources, then run the selected patch or integration checks.
codex-check *args:
  nix develop .#codex --command python3 codex/scripts/maintain.py check "$@"

# Replay, regenerate, and run every manifest-declared Codex verification gate.
codex-test:
  nix develop .#codex --command python3 -m unittest discover -s codex/tests -p 'test_*.py'
  nix develop .#codex --command python3 codex/scripts/maintain.py verify

# Build the patched Codex Nix package only after development-shell validation passes.
codex-final-nix:
  nix build --no-link --accept-flake-config .#codex

# Run the Viewer API with the non-embedded development shell.
agents-viewer-api-dev *args:
  nix develop .#agents-viewer --command cargo run --locked --manifest-path tools/agents-viewer/Cargo.toml --bin agents-viewer -- "$@"

# Run the Vite development server and proxy API requests to the Viewer API.
agents-viewer-web-dev:
  nix develop .#agents-viewer --command npm --prefix tools/agents-viewer/web ci
  nix develop .#agents-viewer --command npm --prefix tools/agents-viewer/web run dev

# Run fast Rust and browserless Web tests.
agents-viewer-test:
  nix develop .#agents-viewer --command cargo test --locked --manifest-path tools/agents-viewer/Cargo.toml
  nix develop .#agents-viewer --command npm --prefix tools/agents-viewer/web ci
  nix develop .#agents-viewer --command npm --prefix tools/agents-viewer/web run test

# Regenerate the Rust-to-TypeScript Viewer API contract.
agents-viewer-generate:
  nix develop .#agents-viewer --command cargo run --locked --manifest-path tools/agents-viewer/Cargo.toml --bin export_types -- --write --output tools/agents-viewer/web/src/generated/api.ts

# Build the embedded debug Viewer and run host-browser Playwright tests.
agents-viewer-e2e *args:
  nix develop .#agents-viewer --command python3 tools/agents-viewer/scripts/workflow.py e2e -- "$@"

# Run browser-independent Viewer source and embedded-UI verification.
agents-viewer-verify:
  nix develop .#agents-viewer --command python3 tools/agents-viewer/scripts/workflow.py verify
