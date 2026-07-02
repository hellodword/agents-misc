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

      patchesForVersion =
        version:
        let
          patchDir = ./codex/patches/rust-v${version};
          series = patchDir + "/series";
          parseSeriesLine = line: line != "" && !(lib.hasPrefix "#" line);
          patchForSeriesEntry =
            entry:
            let
              patch = patchDir + "/${entry}";
            in
            if builtins.pathExists patch then
              patch
            else
              throw "agents-misc: codex patch series entry not found for rust-v${version}: ${entry}";
        in
        if builtins.pathExists series then
          map patchForSeriesEntry (
            builtins.filter parseSeriesLine (lib.splitString "\n" (builtins.readFile series))
          )
        else
          throw "agents-misc: no codex patch series found for rust-v${version}";

      patchCodex =
        codex:
        codex.overrideAttrs (
          old:
          let
            version = old.version or (builtins.parseDrvName old.name).version;
            localPatches =
              let
                patches = patchesForVersion version;
              in
              if patches == [ ] then
                throw "agents-misc: empty codex patch series for rust-v${version}"
              else
                patches;
          in
          {
            patches = (old.patches or [ ]) ++ localPatches;

            # llm-agents.nix builds from source/codex-rs, while these patches
            # are generated against the OpenAI Codex repository root.
            patchFlags = [
              "-p1"
              "-d"
              ".."
            ];

            passthru = (old.passthru or { }) // {
              agentsMiscPatch = builtins.head localPatches;
              agentsMiscPatches = localPatches;
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
          rulesyncConfig = rulesyncFor system;
        in
        {
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
      );

      checks = lib.genAttrs supportedSystems (
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
      );

      devShells = lib.genAttrs supportedSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          devShell = pkgs.mkShell {
            packages = with pkgs; [
              cargo
              coreutils
              diffutils
              git
              gnupatch
              just
              pkg-config
              python3
              rustc
            ];

            OPENSSL_INCLUDE_DIR = "${lib.getDev pkgs.openssl}/include";
            OPENSSL_LIB_DIR = "${lib.getLib pkgs.openssl}/lib";
            PKG_CONFIG_PATH = "${lib.getDev pkgs.openssl}/lib/pkgconfig";
          };
        in
        {
          dev = devShell;
          default = devShell;
        }
      );

      overlays.default = final: _prev: {
        agents-misc = {
          codex = codexFor final.stdenv.hostPlatform.system;
        };
      };
    };
}
