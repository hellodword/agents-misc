{
  lib,
  supportedSystems,
  codexFor,
  codexConfigFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codex = codexFor system;
    codexConfig = codexConfigFor system;
  in
  {
    inherit codex;
    inherit (codexConfig)
      codexcfg
      codexSchemaRegistry
      codexConfigData
      codexConfigSite
      ;

    default = codex;
  }
)
