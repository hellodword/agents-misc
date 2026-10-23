{
  lib,
  nixpkgs,
  codexBaseFor,
  supportedSystems,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
    codexBasePackage = codexBaseFor system;
    codexPkgConfig = pkgs.writeShellScriptBin "pkg-config" ''
      export PKG_CONFIG_PATH="${lib.getDev pkgs.openssl}/lib/pkgconfig''${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
      exec ${lib.getExe pkgs.pkg-config} "$@"
    '';
    agentRulesPython = pkgs.python3.withPackages (pythonPackages: [
      pythonPackages.pyyaml
    ]);
    devShell = pkgs.mkShell {
      packages = with pkgs; [
        actionlint
        coreutils
        git
        jq
        (lib.getBin nix)
        nixfmt
        openssh
        ruff
        sshpass
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
    atlasShell = pkgs.mkShell (
      {
        packages = [
          pkgs.nodejs_24
          pkgs.ruff
          (pkgs.python3.withPackages (pythonPackages: [
            pythonPackages.packaging
            pythonPackages.tomlkit
            pythonPackages.playwright
          ]))
        ]
        ++ lib.optionals pkgs.stdenv.isLinux [ pkgs.chromium ];
      }
      // lib.optionalAttrs pkgs.stdenv.isLinux {
        PLAYWRIGHT_NIX_BROWSER_PATH = lib.getExe pkgs.chromium;
        PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = "1";
      }
    );
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
  in
  {
    dev = devShell;
    default = devShell;
    codex = codexShell;
    codex-config-atlas = atlasShell;
    agents-viewer = agentsViewerShell;
    vm-test-mcp = pkgs.mkShell {
      packages = with pkgs; [
        go
        gcc
        pkg-config
        sqlite
        python3
        ruff
        unzip
      ];
    };
  }
)
