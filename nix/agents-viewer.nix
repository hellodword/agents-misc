{ lib, pkgs }:

let
  sourceFilter =
    path: _type:
    let
      name = builtins.baseNameOf path;
    in
    !(lib.elem name [
      "node_modules"
      "dist"
      "target"
      "playwright-report"
      "test-results"
    ])
    && !(lib.hasSuffix ".tsbuildinfo" name);

  source = lib.cleanSourceWith {
    src = ../tools/agents-viewer;
    filter = sourceFilter;
  };
  webSource = lib.cleanSourceWith {
    src = ../tools/agents-viewer/web;
    filter = sourceFilter;
  };
  web = pkgs.buildNpmPackage {
    pname = "agents-viewer-web";
    version = "0.1.0";
    src = webSource;
    npmDepsHash = "sha256-gY0vgAhxSTb78pBBklbzUIIs9hx1Jwr7tXXvHM7dqbQ=";
    npmFlags = [ "--ignore-scripts" ];
    buildPhase = ''
      runHook preBuild
      npm run build
      runHook postBuild
    '';
    installPhase = ''
      runHook preInstall
      mkdir -p $out/share/agents-viewer/web
      cp -R dist/. $out/share/agents-viewer/web/
      runHook postInstall
    '';
  };
  webCheck = pkgs.buildNpmPackage {
    pname = "check-agents-viewer-web";
    version = "0.1.0";
    src = webSource;
    npmDepsHash = "sha256-gY0vgAhxSTb78pBBklbzUIIs9hx1Jwr7tXXvHM7dqbQ=";
    npmFlags = [ "--ignore-scripts" ];
    buildPhase = ''
      runHook preBuild
      npm run typecheck
      npm run test
      npm run build
      runHook postBuild
    '';
    installPhase = ''
      runHook preInstall
      mkdir -p "$out"
      touch "$out/typecheck-ok" "$out/unit-ok" "$out/build-ok"
      runHook postInstall
    '';
  };
  rustCheck = pkgs.rustPlatform.buildRustPackage {
    pname = "check-agents-viewer-rust";
    version = "0.1.0";
    src = source;
    cargoLock.lockFile = ../tools/agents-viewer/Cargo.lock;
    nativeBuildInputs = [ pkgs.pkg-config ];
    cargoBuildFlags = [ "--all-targets" ];
    cargoTestFlags = [ ];
    doCheck = true;
    postCheck = ''
      cargo run --offline --bin export_types -- --check
      touch "$TMPDIR/rust-tests-ok" "$TMPDIR/bindings-ok"
    '';
    installPhase = ''
      runHook preInstall
      mkdir -p "$out"
      cp "$TMPDIR/rust-tests-ok" "$out/rust-tests-ok"
      cp "$TMPDIR/bindings-ok" "$out/bindings-ok"
      runHook postInstall
    '';
  };
  package = pkgs.rustPlatform.buildRustPackage {
    pname = "agents-viewer";
    version = "0.1.0";
    src = source;
    cargoLock.lockFile = ../tools/agents-viewer/Cargo.lock;
    nativeBuildInputs = [ pkgs.pkg-config ];
    postPatch = ''
      mkdir -p web/dist
      cp -R ${web}/share/agents-viewer/web/. web/dist/
    '';
    cargoBuildFlags = [
      "--bin"
      "agents-viewer"
      "--features"
      "embedded-ui"
    ];
    doCheck = false;
    postInstall = ''
      find $out/bin -type f ! -name agents-viewer -delete
    '';
    meta = {
      description = "Read-only local viewer for Codex rollout conversations";
      mainProgram = "agents-viewer";
      platforms = lib.platforms.unix ++ lib.platforms.windows;
    };
  };
in
{
  inherit package;
  frontend = web;
  checks = {
    rust = rustCheck;
    web = webCheck;
  };
}
