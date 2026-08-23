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

    llm-agents = {
      url = "github:numtide/llm-agents.nix";
    };

    treefmt-nix.follows = "llm-agents/treefmt-nix";
  };

  outputs =
    {
      nixpkgs,
      llm-agents,
      treefmt-nix,
      ...
    }:
    let
      inherit (nixpkgs) lib;

      codexProject = import ./nix/codex.nix {
        inherit lib llm-agents nixpkgs;
      };

      inherit (codexProject)
        codexCheckFor
        codexFor
        patchOrder
        supportedSystems
        upstream
        ;

      codexConfigAtlasFor =
        system:
        let
          codex = codexFor system;
          codexVersion = codex.version or (builtins.parseDrvName codex.name).version;
        in
        import ./nix/codex-config-atlas.nix {
          inherit codexVersion;
          pkgs = import nixpkgs { inherit system; };
        };

      agentsViewerFor =
        system:
        import ./nix/agents-viewer.nix {
          inherit lib;
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
    in
    {
      packages = import ./nix/packages.nix {
        inherit
          lib
          codexConfigAtlasFor
          codexFor
          agentsViewerFor
          supportedSystems
          ;
      };

      apps = import ./nix/apps.nix {
        inherit
          lib
          nixpkgs
          codexConfigAtlasFor
          codexFor
          agentsViewerFor
          supportedSystems
          ;
      };

      checks = import ./nix/checks.nix {
        inherit
          lib
          nixpkgs
          codexCheckFor
          codexConfigAtlasFor
          agentsViewerFor
          supportedSystems
          ;
        formattingCheckFor = system: treefmtProject.checks.${system};
      };

      devShells = import ./nix/dev-shells.nix {
        inherit
          lib
          nixpkgs
          codexFor
          supportedSystems
          ;
      };

      formatter = treefmtProject.formatter;

      overlays.default = final: _prev: {
        agents-misc = {
          codex = codexFor final.stdenv.hostPlatform.system;
          agents-viewer =
            (import ./nix/agents-viewer.nix {
              inherit (final) lib;
              pkgs = final;
            }).package;
        };
      };

      lib.codexMaintenance = {
        inherit patchOrder;
        inherit (upstream) ref revision;
      };
    };
}
