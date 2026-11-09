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
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
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

      codexConfigAtlasFor =
        system:
        import ./nix/codex-config-atlas.nix {
          pkgs = import nixpkgs { inherit system; };
        };

      treefmtProject = import ./nix/formatter.nix {
        inherit
          lib
          nixpkgs
          supportedSystems
          treefmt-nix
          ;
      };
      vmTestFor =
        system:
        import ./nix/vm-test-mcp.nix {
          inherit lib nixpkgs;
          pkgs = import nixpkgs { inherit system; };
        };
    in
    {
      packages = import ./nix/packages.nix {
        inherit vmTestFor;
        inherit
          lib
          codexConfigAtlasFor
          supportedSystems
          ;
      };

      apps = import ./nix/apps.nix {
        inherit vmTestFor;
        inherit
          lib
          codexConfigAtlasFor
          supportedSystems
          ;
      };

      checks = import ./nix/checks.nix {
        inherit vmTestFor;
        inherit
          lib
          nixpkgs
          codexConfigAtlasFor
          supportedSystems
          ;
        formattingCheckFor = system: treefmtProject.checks.${system};
      };

      devShells = import ./nix/dev-shells.nix {
        inherit
          lib
          nixpkgs
          supportedSystems
          ;
      };

      formatter = treefmtProject.formatter;
    };
}
