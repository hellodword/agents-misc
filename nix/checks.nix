{
  lib,
  supportedSystems,
  codexConfigFor,
  agentsViewerFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codexConfig = codexConfigFor system;
    agentsViewer = agentsViewerFor system;
  in
  {
    codex-schema-registry = codexConfig.checkSchemaRegistry;
    codex-config-data = codexConfig.checkConfigData;
    codex-config-site = codexConfig.checkConfigSite;
    agents-viewer = agentsViewer;
    agents-viewer-web = agentsViewer.frontend;
  }
)
