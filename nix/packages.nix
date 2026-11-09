{
  lib,
  supportedSystems,
  codexConfigAtlasFor,
  vmTestFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codexConfigAtlas = codexConfigAtlasFor system;
  in
  {
    codex-config-atlas = codexConfigAtlas.codexConfigAtlas;
    codex-config-atlas-registry = codexConfigAtlas.codexConfigAtlasRegistry;
    codex-config-atlas-data = codexConfigAtlas.codexConfigAtlasData;
    codex-config-atlas-site = codexConfigAtlas.codexConfigAtlasSite;
  }
  // lib.optionalAttrs (system == "x86_64-linux") {
    vm-test-mcp = (vmTestFor system).package;
    vm-test-submit = (vmTestFor system).submit;
    vm-test-router = (vmTestFor system).router;
    vm-test-all = (vmTestFor system).all;
  }
)
