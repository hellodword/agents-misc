{
  lib,
  supportedSystems,
  codexFor,
  codexConfigFor,
  rulesyncFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codex = codexFor system;
    codexConfig = codexConfigFor system;
    rulesyncConfig = rulesyncFor system;
  in
  {
    inherit codex;
    inherit (codexConfig)
      codexcfg
      codexSchemaRegistry
      codexConfigData
      codexConfigSite
      ;
    inherit (rulesyncConfig) rulesync;

    default = codex;
  }
)
