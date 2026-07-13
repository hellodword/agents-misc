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

  web = pkgs.buildNpmPackage {
    pname = "agents-viewer-web";
    version = "0.1.0";
    src = lib.cleanSourceWith {
      src = ../tools/agents-viewer/web;
      filter = sourceFilter;
    };
    npmDepsHash = "sha256-XItlumtmmsojo+MsjceE2A1Da7pLiIR/9UHdirAmmCs=";
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
in
pkgs.rustPlatform.buildRustPackage {
  pname = "agents-viewer";
  version = "0.1.0";
  src = lib.cleanSourceWith {
    src = ../tools/agents-viewer;
    filter = sourceFilter;
  };
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
  cargoTestFlags = [
    "--features"
    "embedded-ui"
    "--lib"
  ];
  postInstall = ''
    find $out/bin -type f ! -name agents-viewer -delete
  '';
  passthru.frontend = web;
  meta = {
    description = "Read-only local viewer for Codex rollout conversations";
    mainProgram = "agents-viewer";
    platforms = lib.platforms.unix ++ lib.platforms.windows;
  };
}
