{
  lib,
  supportedSystems,
  codexConfigAtlasFor,
  agentsViewerFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codexConfigAtlas = codexConfigAtlasFor system;
    agentsViewer = agentsViewerFor system;
  in
  {
    codex-config-atlas-registry = codexConfigAtlas.checkConfigAtlasRegistry;
    codex-config-atlas-data = codexConfigAtlas.checkConfigAtlasData;
    codex-config-atlas-site = codexConfigAtlas.checkConfigAtlasSite;
    agents-viewer = agentsViewer;
    agents-viewer-web = agentsViewer.frontend;
  }
)
