{
  lib,
  supportedSystems,
  codexFor,
  codexConfigFor,
  agentsViewerFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codexPackage = codexFor system;
    codexConfig = codexConfigFor system;
    agentsViewer = agentsViewerFor system;
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

    codexcfg = {
      type = "app";
      program = "${codexConfig.codexcfgApp}/bin/codexcfg";
      meta = {
        description = "Codex config schema tooling wrapper";
      };
    };

    agents-viewer = {
      type = "app";
      program = "${agentsViewer}/bin/agents-viewer";
      meta.description = "Read-only local Codex conversation viewer";
    };
  }
)
