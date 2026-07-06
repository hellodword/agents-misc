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
  };

  outputs =
    {
      nixpkgs,
      llm-agents,
      ...
    }:
    let
      inherit (nixpkgs) lib;

      project = import ./nix/codex.nix {
        inherit lib nixpkgs llm-agents;
      };

      inherit (project)
        codexConfigFor
        codexFor
        rulesyncFor
        supportedSystems
        ;
    in
    {
      packages = import ./nix/packages.nix {
        inherit
          lib
          codexConfigFor
          codexFor
          rulesyncFor
          supportedSystems
          ;
      };

      apps = import ./nix/apps.nix {
        inherit
          lib
          codexConfigFor
          codexFor
          rulesyncFor
          supportedSystems
          ;
      };

      checks = import ./nix/checks.nix {
        inherit
          lib
          codexConfigFor
          rulesyncFor
          supportedSystems
          ;
      };

      devShells = import ./nix/dev-shells.nix {
        inherit lib nixpkgs supportedSystems;
      };

      formatter = import ./nix/formatter.nix {
        inherit lib nixpkgs supportedSystems;
      };

      overlays.default = final: _prev: {
        agents-misc = {
          codex = codexFor final.stdenv.hostPlatform.system;
        };
      };
    };
}
