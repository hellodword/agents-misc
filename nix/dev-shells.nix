{
  lib,
  nixpkgs,
  codexFor,
  supportedSystems,
}:

lib.genAttrs supportedSystems (
  system:
  let
    pkgs = import nixpkgs { inherit system; };
    codexPackage = codexFor system;
    agentRulesPython = pkgs.python3.withPackages (pythonPackages: [
      pythonPackages.jsonschema
      pythonPackages.pyyaml
    ]);
    devShell = pkgs.mkShell {
      packages = with pkgs; [
        actionlint
        coreutils
        git
        jq
        just
        nixfmt
        agentRulesPython
      ];

      AGENTS_MISC_SHELL = "dev";
    };
    codexShell = pkgs.mkShell {
      packages = with pkgs; [
        cargo
        coreutils
        diffutils
        git
        gnupatch
        jq
        just
        pkg-config
        protobuf
        python3
        ruff
        rustc
        rustfmt
      ];

      AGENTS_MISC_SHELL = "codex";
      OPENSSL_INCLUDE_DIR = "${lib.getDev pkgs.openssl}/include";
      OPENSSL_LIB_DIR = "${lib.getLib pkgs.openssl}/lib";
      PKG_CONFIG_PATH = "${lib.getDev pkgs.openssl}/lib/pkgconfig";
      RUSTY_V8_ARCHIVE = codexPackage.RUSTY_V8_ARCHIVE;
      RUSTY_V8_SRC_BINDING_PATH = codexPackage.RUSTY_V8_SRC_BINDING_PATH;
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

      AGENTS_MISC_SHELL = "agents-viewer";
    };
    agentEvalsShell = pkgs.mkShell {
      packages = [
        codexPackage
        pkgs.coreutils
        pkgs.ruff
        agentRulesPython
      ];

      AGENTS_MISC_SHELL = "agent-evals";
    };
  in
  {
    dev = devShell;
    default = devShell;
    codex = codexShell;
    agents-viewer = agentsViewerShell;
    agent-evals = agentEvalsShell;
  }
)
