{
  lib,
  nixpkgs,
  supportedSystems,
  codexConfigAtlasFor,
  formattingCheckFor,
  vmTestFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
    agentRulesPython = pkgs.python3.withPackages (pythonPackages: [
      pythonPackages.pyyaml
    ]);
    codexConfigAtlas = codexConfigAtlasFor system;
  in
  {
    formatting = formattingCheckFor system;
    agent-rules = pkgs.runCommand "agent-rules-check" { nativeBuildInputs = [ agentRulesPython ]; } ''
      cd ${../.}
      export PYTHONDONTWRITEBYTECODE=1
      python3 scripts/check-agent-rules.py --root . --check-license-copy
      python3 -m unittest tests.test_check_agent_rules
      touch "$out"
    '';
    codex-config-atlas-registry = codexConfigAtlas.checkConfigAtlasRegistry;
    codex-config-atlas-data = codexConfigAtlas.checkConfigAtlasData;
    codex-config-atlas-site = codexConfigAtlas.checkConfigAtlasSite;
    codex-config-atlas-tests = codexConfigAtlas.checkConfigAtlasTests;
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
  // lib.optionalAttrs (system == "x86_64-linux") {
    vm-test-mcp = (vmTestFor system).package;
    vm-test-native = (vmTestFor system).native;
    vm-test-router = (vmTestFor system).router;
  }
)
