{
  lib,
  supportedSystems,
  codexConfigFor,
  rulesyncFor,
}:

lib.genAttrs supportedSystems (
  system:
  let
    codexConfig = codexConfigFor system;
    rulesyncConfig = rulesyncFor system;
  in
  {
    codex-schema-registry = codexConfig.checkSchemaRegistry;
    codex-config-data = codexConfig.checkConfigData;
    codex-config-site = codexConfig.checkConfigSite;
    rulesync-build = rulesyncConfig.rulesync;
  }
)
