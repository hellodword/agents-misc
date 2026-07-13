{
  lib,
  nixpkgs,
  supportedSystems,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
    devShell = pkgs.mkShell {
      packages = with pkgs; [
        cargo
        coreutils
        diffutils
        git
        gnupatch
        jq
        just
        nixfmt
        pkg-config
        python3
        rustc
      ];

      OPENSSL_INCLUDE_DIR = "${lib.getDev pkgs.openssl}/include";
      OPENSSL_LIB_DIR = "${lib.getLib pkgs.openssl}/lib";
      PKG_CONFIG_PATH = "${lib.getDev pkgs.openssl}/lib/pkgconfig";
    };
    agentsViewerShell = pkgs.mkShell {
      packages =
        with pkgs;
        [
          cargo
          clippy
          just
          nodejs_24
          pkg-config
          rustc
          rustfmt
          sqlite
        ]
        ++ lib.optionals stdenv.isLinux [ strace ];
    };
  in
  {
    dev = devShell;
    default = devShell;
    agents-viewer = agentsViewerShell;
  }
)
