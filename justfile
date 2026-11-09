set positional-arguments

# Validate the distributed rules, skill metadata, and local references.
check-agent-rules:
  nix develop .#dev --command python3 -B scripts/check-agent-rules.py --root . --check-license-copy
  nix develop .#dev --command python3 -B -m unittest tests.test_check_agent_rules

# Validate GitHub Actions syntax, official major tags, and verification gates.
check-workflows:
  nix develop .#dev --command actionlint .github/workflows/*.yml
  nix develop .#dev --command python3 scripts/check-workflows.py --root .
  nix develop .#dev --command python3 -m unittest tests.test_check_workflows
