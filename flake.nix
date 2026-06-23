{
  description = "agents-misc";

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

      packages = lib.genAttrs supportedSystems (
        system:
        let
          codex = codexFor system;
        in
        {
          inherit codex;
          default = codex;
        }
      );
    in
    {
      inherit packages;

      overlays.default = final: _prev: {
        agents-misc = {
          codex = codexFor final.stdenv.hostPlatform.system;
        };
      };
    };
}
