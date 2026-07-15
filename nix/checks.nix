{
  lib,
  nixpkgs,
  supportedSystems,
  codexConfigAtlasFor,
  agentsViewerFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
    agentRulesPython = pkgs.python3.withPackages (pythonPackages: [
      pythonPackages.jsonschema
      pythonPackages.pyyaml
    ]);
    codexConfigAtlas = codexConfigAtlasFor system;
    agentsViewer = agentsViewerFor system;
  in
  {
    agent-rules = pkgs.runCommand "agent-rules-check" { nativeBuildInputs = [ agentRulesPython ]; } ''
      cd ${../.}
      python3 scripts/check-agent-rules.py --root .
      python3 -m unittest discover -s tests -p 'test_*.py'
      touch "$out"
    '';
    codex-config-atlas-registry = codexConfigAtlas.checkConfigAtlasRegistry;
    codex-config-atlas-data = codexConfigAtlas.checkConfigAtlasData;
    codex-config-atlas-site = codexConfigAtlas.checkConfigAtlasSite;
    agents-viewer = agentsViewer;
    agents-viewer-web = agentsViewer.frontend;
  }
)
