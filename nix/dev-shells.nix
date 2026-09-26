{
  lib,
  nixpkgs,
  codexBaseFor,
  codexFor,
  supportedSystems,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
    codexBasePackage = codexBaseFor system;
    codexPackage = codexFor system;
    codexPkgConfig = pkgs.writeShellScriptBin "pkg-config" ''
      export PKG_CONFIG_PATH="${lib.getDev pkgs.openssl}/lib/pkgconfig''${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
      exec ${lib.getExe pkgs.pkg-config} "$@"
    '';
    agentRulesPython = pkgs.python3.withPackages (pythonPackages: [
      pythonPackages.jsonschema
      pythonPackages.pyyaml
    ]);
    devShell = pkgs.mkShell {
      packages = with pkgs; [
        actionlint
        coreutils
        git
        jq
        nixfmt
        ruff
        agentRulesPython
      ];
    };
    codexShell = pkgs.mkShell {
      inputsFrom = [ codexBasePackage ];

      packages = with pkgs; [
        cargo
        clippy
        coreutils
        diffutils
        git
        gnupatch
        jq
        codexPkgConfig
        protobuf
        python3
        ripgrep
        ruff
        rustc
        rustfmt
      ];
      OPENSSL_INCLUDE_DIR = "${lib.getDev pkgs.openssl}/include";
      OPENSSL_LIB_DIR = "${lib.getLib pkgs.openssl}/lib";
      PKG_CONFIG_PATH = "${lib.getDev pkgs.openssl}/lib/pkgconfig";
      RUSTY_V8_ARCHIVE = codexBasePackage.RUSTY_V8_ARCHIVE;
      RUSTY_V8_SRC_BINDING_PATH = codexBasePackage.RUSTY_V8_SRC_BINDING_PATH;
    };
    agentsViewerShell = pkgs.mkShell (
      {
        packages =
          with pkgs;
          [
            cargo
            clippy
            nodejs_24
            pkg-config
            python3
            ruff
            rustc
            rustfmt
            sqlite
          ]
          ++ lib.optionals stdenv.isLinux [
            chromium
            strace
          ];
        CARGO_TARGET_DIR = "target";
      }
      // lib.optionalAttrs pkgs.stdenv.isLinux {
        PLAYWRIGHT_NIX_BROWSER_PATH = lib.getExe pkgs.chromium;
        PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = "1";
      }
    );
    agentEvalsShell = pkgs.mkShell {
      packages = [
        codexPackage
        pkgs.coreutils
        pkgs.ruff
        agentRulesPython
      ];
    };
  in
  {
    dev = devShell;
    default = devShell;
    codex = codexShell;
    agents-viewer = agentsViewerShell;
    agent-evals = agentEvalsShell;
  }
)
