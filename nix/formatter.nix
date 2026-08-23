{
  lib,
  nixpkgs,
  supportedSystems,
  treefmt-nix,
}:

let
  projects = lib.genAttrs supportedSystems (
    system:
    let
      pkgs = import nixpkgs { inherit system; };
      treefmtEval = treefmt-nix.lib.evalModule pkgs ../treefmt.nix;
    in
    {
      formatter = treefmtEval.config.build.wrapper;
      check = treefmtEval.config.build.check ../.;
    }
  );
in
{
  formatter = lib.mapAttrs (_system: project: project.formatter) projects;
  checks = lib.mapAttrs (_system: project: project.check) projects;
}
