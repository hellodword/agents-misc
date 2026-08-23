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
      cargo run --offline --bin export_types -- \
        --check --output web/src/generated/api.ts

      clone_root="$TMPDIR/export-types-cross-clone"
      clone_a="$clone_root/clone-a"
      clone_b="$clone_root/clone-b"
      shared_target="$clone_root/shared-target"
      mkdir -p "$clone_a" "$clone_b"
      cp -R ${source}/. "$clone_a/"
      cp -R ${source}/. "$clone_b/"
      chmod -R u+w "$clone_a" "$clone_b"
      CARGO_TARGET_DIR="$shared_target" cargo build --offline \
        --manifest-path "$clone_a/Cargo.toml" --bin export_types
      "$shared_target/debug/export_types" \
        --check --output "$clone_b/web/src/generated/api.ts"

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
  e2eCheck = pkgs.buildNpmPackage {
    pname = "check-agents-viewer-e2e";
    version = "0.1.0";
    src = source;
    sourceRoot = "source/web";
    npmDeps = web.npmDeps;
    npmDepsHash = "sha256-gY0vgAhxSTb78pBBklbzUIIs9hx1Jwr7tXXvHM7dqbQ=";
    npmFlags = [ "--ignore-scripts" ];
    PLAYWRIGHT_NIX_BROWSER_PATH = lib.getExe pkgs.chromium;
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD = "1";
    AGENTS_VIEWER_E2E_BINARY = lib.getExe package;
    buildPhase = ''
      runHook preBuild
      npm run e2e
      runHook postBuild
    '';
    installPhase = ''
      runHook preInstall
      mkdir -p "$out"
      touch "$out/e2e-ok"
      runHook postInstall
    '';
  };
in
{
  inherit package;
  frontend = web;
  checks = {
    rust = rustCheck;
    web = webCheck;
  }
  // lib.optionalAttrs pkgs.stdenv.isLinux { e2e = e2eCheck; };
}
