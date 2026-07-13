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
    codex = codexFor system;
    codexConfig = codexConfigFor system;
    agentsViewer = agentsViewerFor system;
  in
  {
    inherit codex;
    agents-viewer = agentsViewer;
    inherit (codexConfig)
      codexcfg
      codexSchemaRegistry
      codexConfigData
      codexConfigSite
      ;

    default = codex;
  }
)
