{
  description = "agents-misc";

  nixConfig = {
    extra-substituters = [
      "https://hellodword-codex.cachix.org"
    ];
    extra-trusted-public-keys = [
      "hellodword-codex.cachix.org-1:0URmcnC9aynWh9+FJ2tf+HQloylGgZzPtrz3sttTTiQ="
    ];
  };

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

    llm-agents = {
      url = "github:numtide/llm-agents.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      nixpkgs,
      llm-agents,
      ...
    }:
    let
      inherit (nixpkgs) lib;

      patchForVersion =
        version:
        let
          patch = ./codex/patches/rust-v${version}.patch;
        in
        if builtins.pathExists patch then
          patch
        else
          throw "agents-misc: no codex patch found for rust-v${version}";

      patchCodex =
        codex:
        codex.overrideAttrs (
          old:
          let
            version = old.version or (builtins.parseDrvName old.name).version;
            patch = patchForVersion version;
          in
          {
            patches = (old.patches or [ ]) ++ [ patch ];

            # llm-agents.nix builds from source/codex-rs, while these patches
            # are generated against the OpenAI Codex repository root.
            patchFlags = [
              "-p1"
              "-d"
              ".."
            ];

            passthru = (old.passthru or { }) // {
              agentsMiscPatch = patch;
            };
          }
        );

      supportedSystems = builtins.attrNames llm-agents.packages;

      codexFor = system: patchCodex llm-agents.packages.${system}.codex;

      codexConfigFor =
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          codex = codexFor system;
          codexVersion = codex.version or (builtins.parseDrvName codex.name).version;
        in
        import ./codex/nix {
          inherit lib pkgs codexVersion;
          repoSchemas = ./codex/schemas;
          repoSiteStatic = ./codex/site/static;
          minVersion = "0.129.0";
        };

      rulesyncFor =
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        import ./rulesync/nix {
          inherit lib pkgs;
        };
    in
    {
      packages = lib.genAttrs supportedSystems (
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
          default = codex;
          inherit (rulesyncConfig) rulesync;
        }
      );

      apps = lib.genAttrs supportedSystems (
        system:
        let
          codexConfig = codexConfigFor system;
        in
        {
          codexcfg = {
            type = "app";
            program = "${codexConfig.codexcfgApp}/bin/codexcfg";
            meta = {
              description = "Codex config schema tooling wrapper";
            };
          };
        }
      );

      checks = lib.genAttrs supportedSystems (
        system:
        let
          codexConfig = codexConfigFor system;
        in
        {
          codex-schema-registry = codexConfig.checkSchemaRegistry;
          codex-config-data = codexConfig.checkConfigData;
          codex-config-site = codexConfig.checkConfigSite;
        }
      );

      overlays.default = final: _prev: {
        agents-misc = {
          codex = codexFor final.stdenv.hostPlatform.system;
        };
      };
    };
}
