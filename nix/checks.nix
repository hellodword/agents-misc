{
  lib,
  supportedSystems,
  codexConfigAtlasFor,
  formattingCheckFor,
  vmTestFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codexConfigAtlas = codexConfigAtlasFor system;
  in
  {
    formatting = formattingCheckFor system;
    codex-config-atlas-registry = codexConfigAtlas.checkConfigAtlasRegistry;
    codex-config-atlas-data = codexConfigAtlas.checkConfigAtlasData;
    codex-config-atlas-site = codexConfigAtlas.checkConfigAtlasSite;
    codex-config-atlas-tests = codexConfigAtlas.checkConfigAtlasTests;
  }
  // lib.optionalAttrs (system == "x86_64-linux") {
    vm-test-mcp = (vmTestFor system).package;
    vm-test-native = (vmTestFor system).native;
    vm-test-router = (vmTestFor system).router;
  }
)
