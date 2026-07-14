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
    codex = codexFor system;
    codexConfigAtlas = codexConfigAtlasFor system;
    agentsViewer = agentsViewerFor system;
  in
  {
    inherit codex;
    agents-viewer = agentsViewer;
    codex-config-atlas = codexConfigAtlas.codexConfigAtlas;
    codex-config-atlas-registry = codexConfigAtlas.codexConfigAtlasRegistry;
    codex-config-atlas-data = codexConfigAtlas.codexConfigAtlasData;
    codex-config-atlas-site = codexConfigAtlas.codexConfigAtlasSite;

    default = codex;
  }
)
