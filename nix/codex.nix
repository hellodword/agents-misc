{
  lib,
  llm-agents,
  nixpkgs,
}:

let
  upstreamFile = ../codex/upstream.toml;
  seriesFile = ../codex/series.toml;
  upstream = builtins.fromTOML (builtins.readFile upstreamFile);
  series = builtins.fromTOML (builtins.readFile seriesFile);
  expectedUpstreamFields = [
    "generate_commands"
    "ref"
    "revision"
    "url"
    "validation_command"
    "worktree"
  ];
  expectedPatchFields = [
    "behavior"
    "file"
    "generated_files"
    "generated_prefixes"
    "intent"
    "source_files"
    "source_prefixes"
    "tests"
  ];
  unknownUpstreamFields = lib.subtractLists expectedUpstreamFields (builtins.attrNames upstream);
  missingUpstreamFields = lib.subtractLists (builtins.attrNames upstream) expectedUpstreamFields;
  patches = series.patch or (throw "agents-misc: codex/series.toml must define [[patch]] entries");
  validatePatch =
    index: patch:
    let
      unknown = lib.subtractLists expectedPatchFields (builtins.attrNames patch);
      missing = lib.subtractLists (builtins.attrNames patch) expectedPatchFields;
      expectedPrefix = lib.fixedWidthNumber 4 (index + 1) + "-";
    in
    if unknown != [ ] then
      throw "agents-misc: unknown field(s) in codex/series.toml patch: ${lib.concatStringsSep ", " unknown}"
    else if missing != [ ] then
      throw "agents-misc: missing field(s) in codex/series.toml patch: ${lib.concatStringsSep ", " missing}"
    else if !(lib.hasPrefix expectedPrefix patch.file) then
      throw "agents-misc: non-contiguous codex patch number at ${patch.file}; expected ${expectedPrefix}"
    else
      patch;
  validatedPatches = lib.imap0 validatePatch patches;
  patchOrder = map (patch: patch.file) validatedPatches;
  targetedTests = lib.unique (lib.concatMap (patch: patch.tests) validatedPatches);
  patchDir = ../codex/patches + "/${upstream.ref}";
  patchPaths = map (
    filename:
    let
      patch = patchDir + "/${filename}";
    in
    if builtins.pathExists patch then
      patch
    else
      throw "agents-misc: codex patch listed in series.toml does not exist: ${filename}"
  ) patchOrder;

  patchCodex =
    pkgs: codex:
    codex.overrideAttrs (
      old:
      let
        version = old.version or (builtins.parseDrvName old.name).version;
        packageRef = "rust-v${version}";
        gitPatchPhase = ''
          runHook prePatch
          chmod -R u+w ..
          for patch in $patches; do
            echo "applying patch $patch"
            (cd .. && git apply --binary --whitespace=nowarn "$patch")
          done
          runHook postPatch
        '';
      in
      if unknownUpstreamFields != [ ] then
        throw "agents-misc: unknown field(s) in codex/upstream.toml: ${lib.concatStringsSep ", " unknownUpstreamFields}"
      else if missingUpstreamFields != [ ] then
        throw "agents-misc: missing field(s) in codex/upstream.toml: ${lib.concatStringsSep ", " missingUpstreamFields}"
      else if packageRef != upstream.ref then
        throw "agents-misc: llm-agents Codex ${packageRef} does not match pinned ${upstream.ref}"
      else if patchPaths == [ ] then
        throw "agents-misc: empty codex patch series for ${upstream.ref}"
      else
        {
          cargoDeps = pkgs.rustPlatform.fetchCargoVendor {
            inherit (old) pname src;
            inherit version;
            sourceRoot = old.sourceRoot or "source/codex-rs";
            patches = (old.patches or [ ]) ++ patchPaths;
            nativeBuildInputs = [ pkgs.gitMinimal ];
            patchPhase = gitPatchPhase;
            hash = "sha256-TCY5pdWvarEqVo8d9cHt3O7+tHbGSrAilx5q7GnXz8Y=";
          };
          patches = (old.patches or [ ]) ++ patchPaths;

          nativeBuildInputs = (old.nativeBuildInputs or [ ]) ++ [ pkgs.gitMinimal ];

          # llm-agents.nix builds from source/codex-rs, while these patches
          # are generated against the OpenAI Codex repository root. Git apply
          # is required because the generated-contract patch owns binary
          # precomputed schemas in addition to text files.
          patchPhase = gitPatchPhase;

          passthru = (old.passthru or { }) // {
            agentsMiscPatch = builtins.head patchPaths;
            agentsMiscPatches = patchPaths;
            agentsMiscPatchOrder = patchOrder;
            agentsMiscUpstreamRevision = upstream.revision;
          };
        }
    );

  supportedSystems = builtins.attrNames llm-agents.packages;
  codexFor =
    system: patchCodex (import nixpkgs { inherit system; }) llm-agents.packages.${system}.codex;
  codexCheckFor =
    system:
    let
      pkgs = import nixpkgs { inherit system; };
      codex = codexFor system;
      renderCommands = commands: lib.concatMapStringsSep "\n" lib.escapeShellArgs commands;
    in
    codex.overrideAttrs (old: {
      pname = "check-codex-maintenance";

      nativeBuildInputs = (old.nativeBuildInputs or [ ]) ++ [
        pkgs.gitMinimal
        pkgs.just
        pkgs.protobuf
        pkgs.python3
        pkgs.rustfmt
      ];

      doCheck = false;
      doInstallCheck = false;
      dontFixup = true;
      phases = [
        "unpackPhase"
        "patchPhase"
        "configurePhase"
        "buildPhase"
        "installPhase"
      ];
      cargoBuildFlags = [ ];
      cargoTestFlags = [ ];

      buildPhase = ''
        runHook preBuild

        export HOME="$TMPDIR/home"
        export RUST_MIN_STACK=8388608
        mkdir -p "$HOME"

        repo_root="$PWD/.."
        git -C "$repo_root" init --quiet
        git -C "$repo_root" config user.email codex-check@example.invalid
        git -C "$repo_root" config user.name codex-check
        git -C "$repo_root" add -A
        git -C "$repo_root" commit --quiet -m baseline

        cd "$repo_root"
        ${renderCommands upstream.generate_commands}
        if test -n "$(git status --short)"; then
          git status --short
          git diff --stat
          echo "error: tracked Codex generated artifacts drifted" >&2
          exit 1
        fi
        touch "$TMPDIR/generation-ok"

        cd codex-rs
        ${lib.escapeShellArgs upstream.validation_command}
        ${renderCommands targetedTests}
        touch "$TMPDIR/behavior-ok"
        touch "$TMPDIR/hooks-rust-ok"

        runHook postBuild
      '';

      installPhase = ''
        runHook preInstall
        mkdir -p "$out"
        cp "$TMPDIR/generation-ok" "$out/generation-ok"
        cp "$TMPDIR/behavior-ok" "$out/behavior-ok"
        cp "$TMPDIR/hooks-rust-ok" "$out/hooks-rust-ok"
        runHook postInstall
      '';

      preInstall = "";
      postInstall = "";
    });
in
{
  inherit
    codexCheckFor
    codexFor
    patchOrder
    supportedSystems
    upstream
    ;
}
