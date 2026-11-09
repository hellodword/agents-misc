{
  description = "agents-misc";

  nixConfig = {
    extra-substituters = [
      "https://nix-community.cachix.org"
      "https://cache.numtide.com"
    ];
    extra-trusted-public-keys = [
      "nix-community.cachix.org-1:mB9FSh9qf2dCimDSUo8Zy7bkq5CX+/rkCWyvRCYg3Fs="
      "niks3.numtide.com-1:DTx8wZduET09hRmMtKdQDxNNthLQETkc/yaX7M4qK0g="
    ];
  };

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    oranc.url = "github:linyinfeng/oranc";

    treefmt-nix = {
      url = "github:numtide/treefmt-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      nixpkgs,
      oranc,
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
        inherit oranc vmTestFor;
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
