{
  lib,
  nixpkgs,
  supportedSystems,
  codexFor,
  codexConfigAtlasFor,
  agentsViewerFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
    codexPackage = codexFor system;
    codexConfigAtlas = codexConfigAtlasFor system;
    agentsViewer = agentsViewerFor system;
    agentEvals = pkgs.writeShellApplication {
      name = "agent-evals";
      runtimeInputs = [
        codexPackage
        pkgs.python3
      ];
      text = ''
        export PYTHONDONTWRITEBYTECODE=1
        export PYTHONPATH=${../.}
        exec python3 -m tools.agent_evals "$@"
      '';
    };
  in
  rec {
    codex = {
      type = "app";
      program = "${codexPackage}/bin/codex";
      meta = {
        description = "Patched Codex CLI";
      };
    };

    default = codex;

    codex-config-atlas = {
      type = "app";
      program = "${codexConfigAtlas.codexConfigAtlas}/bin/codex-config-atlas";
      meta = {
        description = "Codex configuration schema explorer and generator";
      };
    };

    agents-viewer = {
      type = "app";
      program = "${agentsViewer.package}/bin/agents-viewer";
      meta.description = "Read-only local Codex conversation viewer";
    };

    agent-evals = {
      type = "app";
      program = "${agentEvals}/bin/agent-evals";
      meta.description = "Isolated Codex routing, behavior, and certification evaluations";
    };
  }
)
