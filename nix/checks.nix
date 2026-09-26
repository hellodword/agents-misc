{
  lib,
  nixpkgs,
  supportedSystems,
  codexConfigAtlasFor,
  agentsViewerFor,
  formattingCheckFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
    agentRulesPython = pkgs.python3.withPackages (pythonPackages: [
      pythonPackages.pyyaml
    ]);
    codexConfigAtlas = codexConfigAtlasFor system;
    agentsViewer = agentsViewerFor system;
    codexPatchContract =
      pkgs.runCommand "codex-patch-contract-check"
        {
          nativeBuildInputs = [
            pkgs.coreutils
            pkgs.gitMinimal
            pkgs.python3
          ];
        }
        ''
          cd ${../.}
          export PYTHONDONTWRITEBYTECODE=1
          python3 -m unittest discover -s codex/tests -p 'test_*.py'
          touch "$out"
        '';
    codexHooksHelper =
      pkgs.runCommand "codex-hooks-helper-check"
        {
          nativeBuildInputs = [
            pkgs.python3
            pkgs.ruff
          ];
        }
        ''
          cd ${../.}
          export PYTHONDONTWRITEBYTECODE=1
          export RUFF_CACHE_DIR="$TMPDIR/ruff-cache"
          ruff format --check tools/codex-hooks
          ruff check tools/codex-hooks
          python3 -m unittest discover -s tools/codex-hooks/tests -p 'test_*.py'
          touch "$out"
        '';
    checkMarker =
      name: dependency: marker:
      pkgs.runCommand name { } ''
        test -f ${dependency}/${marker}
        touch "$out"
      '';
  in
  {
    formatting = formattingCheckFor system;
    agent-rules = pkgs.runCommand "agent-rules-check" { nativeBuildInputs = [ agentRulesPython ]; } ''
      cd ${../.}
      export PYTHONDONTWRITEBYTECODE=1
      python3 scripts/check-agent-rules.py --root .
      python3 -m unittest tests.test_check_agent_rules
      touch "$out"
    '';
    codex-config-atlas-registry = codexConfigAtlas.checkConfigAtlasRegistry;
    codex-config-atlas-data = codexConfigAtlas.checkConfigAtlasData;
    codex-config-atlas-site = codexConfigAtlas.checkConfigAtlasSite;
    codex-config-atlas-tests = codexConfigAtlas.checkConfigAtlasTests;
    codex-patch-contract = codexPatchContract;
    codex-hooks = pkgs.runCommand "codex-hooks-check" { } ''
      test -e ${codexHooksHelper}
      touch "$out"
    '';
    agents-viewer-rust =
      checkMarker "agents-viewer-rust-check" agentsViewer.checks.rust
        "rust-tests-ok";
    agents-viewer-bindings =
      checkMarker "agents-viewer-bindings-check" agentsViewer.checks.rust
        "bindings-ok";
    agents-viewer-web = agentsViewer.checks.web;
    github-workflows =
      pkgs.runCommand "github-workflows-check"
        {
          nativeBuildInputs = [
            pkgs.actionlint
            agentRulesPython
          ];
        }
        ''
          cd ${../.}
          actionlint .github/workflows/*.yml
          python3 scripts/check-workflows.py --root .
          python3 -m unittest tests.test_check_workflows
          touch "$out"
        '';
  }
  // lib.optionalAttrs pkgs.stdenv.isLinux {
    agents-viewer-e2e = agentsViewer.checks.e2e;
  }
)
