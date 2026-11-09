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
    codex-config-atlas = {
      type = "app";
      program = "${codexConfigAtlas.codexConfigAtlas}/bin/codex-config-atlas";
      meta = {
        description = "Codex configuration schema explorer and generator";
      };
    };
  }
  // lib.optionalAttrs (system == "x86_64-linux") {
    vm-test-mcp = {
      type = "app";
      program = "${(vmTestFor system).package}/bin/vm-test-mcp";
      meta.description = "Authenticated VM test controller";
    };
  }
)
