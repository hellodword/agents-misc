{
  lib,
  supportedSystems,
  codexFor,
  codexConfigAtlasFor,
  agentsViewerFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codexPackage = codexFor system;
    codexConfigAtlas = codexConfigAtlasFor system;
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

    codex-config-atlas = {
      type = "app";
      program = "${codexConfigAtlas.codexConfigAtlas}/bin/codex-config-atlas";
      meta = {
        description = "Codex configuration schema explorer and generator";
      };
    };

    agents-viewer = {
      type = "app";
      program = "${agentsViewer}/bin/agents-viewer";
      meta.description = "Read-only local Codex conversation viewer";
    };
  }
)
