{
  lib,
  nixpkgs,
  supportedSystems,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
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
  in
  {
    dev = devShell;
    default = devShell;
    codex-config-atlas = atlasShell;
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
