{
  lib,
  supportedSystems,
  codexFor,
  codexConfigAtlasFor,
  agentsViewerFor,
  vmTestFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codex = codexFor system;
    codexConfigAtlas = codexConfigAtlasFor system;
    agentsViewer = agentsViewerFor system;
  in
  {
    codex-patched = codex;
    agents-viewer = agentsViewer.package;
    codex-config-atlas = codexConfigAtlas.codexConfigAtlas;
    codex-config-atlas-registry = codexConfigAtlas.codexConfigAtlasRegistry;
    codex-config-atlas-data = codexConfigAtlas.codexConfigAtlasData;
    codex-config-atlas-site = codexConfigAtlas.codexConfigAtlasSite;

    default = codex;
  }
  // lib.optionalAttrs (system == "x86_64-linux") {
    vm-test-mcp = (vmTestFor system).package;
    vm-test-router = (vmTestFor system).router;
    vm-test-all = (vmTestFor system).all;
  }
)
