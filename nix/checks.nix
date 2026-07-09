{
  lib,
  supportedSystems,
  codexConfigFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codexConfig = codexConfigFor system;
  in
  {
    codex-schema-registry = codexConfig.checkSchemaRegistry;
    codex-config-data = codexConfig.checkConfigData;
    codex-config-site = codexConfig.checkConfigSite;
  }
)
