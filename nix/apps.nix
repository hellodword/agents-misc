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
    codex-patched = {
      type = "app";
      program = "${codexPackage}/bin/codex-patched";
      meta = {
        description = "Patched Codex CLI";
      };
    };

    default = codex-patched;

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

  }
)
