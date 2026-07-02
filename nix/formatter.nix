{
  lib,
  nixpkgs,
  supportedSystems,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
  in
  pkgs.writeShellApplication {
    name = "agents-misc-fmt";
    runtimeInputs = [
      pkgs.findutils
      pkgs.nixfmt
    ];
    text = ''
      find flake.nix nix \
        -type f \
        -name '*.nix' \
        -print0 |
        xargs -0 nixfmt
    '';
  }
)
