{
  description = "agents-misc";

  inputs = {
    core.url = "git+https://github.com/hellodword/nixos-config.git?ref=master&dir=infra";
    nixpkgs.follows = "core/nixpkgs";
    treefmt-nix.follows = "core/treefmt-nix";
  };

  outputs =
    {
      nixpkgs,
      treefmt-nix,
      ...
    }:
    let
      inherit (nixpkgs) lib;

      supportedSystems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];

      pkgsBySystem = lib.genAttrs supportedSystems (system: nixpkgs.legacyPackages.${system});
      pkgsFor = system: pkgsBySystem.${system};

      codexConfigAtlasBySystem = lib.genAttrs supportedSystems (
        system:
        import ./nix/codex-config-atlas.nix {
          pkgs = pkgsFor system;
        }
      );
      codexConfigAtlasFor = system: codexConfigAtlasBySystem.${system};

      treefmtProject = import ./nix/formatter.nix {
        inherit
          lib
          pkgsFor
          supportedSystems
          treefmt-nix
          ;
      };

      vmTestX86_64Linux = import ./nix/vm-test-mcp.nix {
        inherit lib nixpkgs;
        pkgs = pkgsFor "x86_64-linux";
      };
      vmTestFor =
        system:
        if system == "x86_64-linux" then
          vmTestX86_64Linux
        else
          throw "vm-test-mcp is only supported on x86_64-linux (requested ${system})";
    in
    {
      packages = import ./nix/packages.nix {
        inherit
          lib
          codexConfigAtlasFor
          supportedSystems
          vmTestFor
          ;
      };

      apps = import ./nix/apps.nix {
        inherit
          lib
          codexConfigAtlasFor
          supportedSystems
          vmTestFor
          ;
      };

      checks = import ./nix/checks.nix {
        inherit
          lib
          codexConfigAtlasFor
          supportedSystems
          vmTestFor
          ;
        formattingCheckFor = system: treefmtProject.checks.${system};
      };

      devShells = import ./nix/dev-shells.nix {
        inherit
          lib
          pkgsFor
          supportedSystems
          ;
      };

      formatter = treefmtProject.formatter;
    };
}
