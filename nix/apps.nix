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
    codexPackage = codexFor system;
    codexConfig = codexConfigFor system;
    rulesyncConfig = rulesyncFor system;
  in
  rec {
    codex = {
      type = "app";
      program = "${codexPackage}/bin/codex";
      meta = {
        description = "Patched Codex CLI";
      };
    };

    default = codex;

    codexcfg = {
      type = "app";
      program = "${codexConfig.codexcfgApp}/bin/codexcfg";
      meta = {
        description = "Codex config schema tooling wrapper";
      };
    };

    rulesync = {
      type = "app";
      program = "${rulesyncConfig.rulesync}/bin/rulesync";
      meta = {
        description = "Strict Rulesync wrapper";
      };
    };
  }
)
