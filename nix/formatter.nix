{
  lib,
  nixpkgs,
  supportedSystems,
  treefmt-nix,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
    treefmtEval = treefmt-nix.lib.evalModule pkgs ../treefmt.nix;
  in
  treefmtEval.config.build.wrapper
)
