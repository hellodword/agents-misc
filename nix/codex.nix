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
  pinnedVersion = lib.removePrefix "rust-v" upstream.ref;
  pinnedSource = builtins.fetchGit {
    url = upstream.url;
    rev = upstream.revision;
    shallow = true;
  };
  expectedUpstreamFields = [
    "generate_commands"
    "ref"
    "regression_commands"
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
  expectedPatchCount = 7;
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
  patchDirectoryEntries = if builtins.pathExists patchDir then builtins.readDir patchDir else { };
  actualPatchFiles = lib.sort (a: b: a < b) (builtins.attrNames patchDirectoryEntries);
  patchFilesAreRegular = lib.all (
    filename: patchDirectoryEntries.${filename} == "regular"
  ) actualPatchFiles;
  generatedOnlyPatchValid =
    if lib.length validatedPatches != expectedPatchCount then
      false
    else
      let
        generatedPatch = lib.last validatedPatches;
      in
      generatedPatch.source_files == [ ]
      && generatedPatch.source_prefixes == [ ]
      && lib.elem "codex-rs/Cargo.lock" generatedPatch.generated_files
      && (generatedPatch.generated_files != [ ] || generatedPatch.generated_prefixes != [ ]);
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
        generatedPatch = lib.last patchPaths;
        patchedCargoLock =
          pkgs.runCommand "codex-${version}-patched-Cargo.lock"
            {
              nativeBuildInputs = [ pkgs.gitMinimal ];
            }
            ''
              work="$TMPDIR/source"
              mkdir -p "$work/codex-rs"
              cp ${pinnedSource}/codex-rs/Cargo.lock "$work/codex-rs/Cargo.lock"
              chmod u+w "$work/codex-rs/Cargo.lock"
              git -C "$work" init --quiet
              git -C "$work" apply --check --binary \
                --include=codex-rs/Cargo.lock ${generatedPatch}
              git -C "$work" apply --binary \
                --include=codex-rs/Cargo.lock ${generatedPatch}
              cp "$work/codex-rs/Cargo.lock" "$out"
            '';
        patchedCargoDeps = pkgs.runCommand "codex-${version}-patched-vendor" { } ''
          mkdir -p "$out"
          cp -a ${old.cargoDeps}/. "$out/"
          chmod -R u+w "$out"
          cp ${patchedCargoLock} "$out/Cargo.lock"
        '';
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
      else if lib.length patches != expectedPatchCount then
        throw "agents-misc: expected exactly ${toString expectedPatchCount} active Codex patches"
      else if actualPatchFiles != patchOrder || !patchFilesAreRegular then
        throw "agents-misc: codex patch directory contains stale or missing patch files"
      else if !generatedOnlyPatchValid then
        throw "agents-misc: final Codex patch must have generated-only ownership"
      else if packageRef != upstream.ref then
        throw "agents-misc: llm-agents Codex ${packageRef} does not match pinned ${upstream.ref}"
      else if patchPaths == [ ] then
        throw "agents-misc: empty codex patch series for ${upstream.ref}"
      else
        {
          patches = (old.patches or [ ]) ++ patchPaths;

          # The generated patch normalizes only local workspace versions in
          # Cargo.lock. Reuse the pinned vendor payload, but give Cargo's
          # consistency hook the same generated lock applied to package source.
          cargoDeps = patchedCargoDeps;

          nativeBuildInputs = (old.nativeBuildInputs or [ ]) ++ [ pkgs.gitMinimal ];

          # llm-agents.nix builds from source/codex-rs, while these patches
          # are generated against the OpenAI Codex repository root. Git apply
          # is required because the generated-contract patch owns binary
          # precomputed schemas in addition to text files.
          patchPhase = gitPatchPhase;
        }
    );

  supportedSystems = builtins.attrNames llm-agents.packages;
  codexBaseFor =
    system:
    llm-agents.packages.${system}.codex.override {
      version = pinnedVersion;
      srcOverride = pinnedSource;
    };
  codexFor = system: patchCodex (import nixpkgs { inherit system; }) (codexBaseFor system);
  codexCheckFor =
    system:
    let
      pkgs = import nixpkgs { inherit system; };
      codex = codexFor system;
      renderGeneratorCommand =
        command:
        let
          isEnvAssignment = value: builtins.match "[A-Za-z_][A-Za-z0-9_]*=.*" value != null;
          dropEnvAssignments =
            values:
            if values != [ ] && isEnvAssignment (builtins.head values) then
              dropEnvAssignments (builtins.tail values)
            else
              values;
          commandWithoutEnv =
            if command != [ ] && builtins.head command == "env" then
              dropEnvAssignments (builtins.tail command)
            else
              command;
        in
        if commandWithoutEnv != [ ] && builtins.head commandWithoutEnv == "cargo" then
          ''(cd "$repo_root/codex-rs" && ${lib.escapeShellArgs command})''
        else
          lib.escapeShellArgs command;
      renderGeneratorCommands = commands: lib.concatMapStringsSep "\n" renderGeneratorCommand commands;
      renderCommands = commands: lib.concatMapStringsSep "\n" lib.escapeShellArgs commands;
      patchPathsForShell = lib.concatMapStringsSep " " lib.escapeShellArg patchPaths;
    in
    codex.overrideAttrs (old: {
      pname = "check-codex-maintenance";

      nativeBuildInputs = (old.nativeBuildInputs or [ ]) ++ [
        pkgs.cacert
        pkgs.gitMinimal
        pkgs.jq
        pkgs.protobuf
        pkgs.python3
        pkgs.ripgrep
        pkgs.rustfmt
        pkgs.zstd
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
        export CODEX_CARGO_JOBS=7
        export CARGO_BUILD_JOBS=7
        export CARGO_TARGET_DIR="$TMPDIR/cargo-target"
        mkdir -p "$HOME"

        repo_root="$PWD/.."
        pinned_root="$TMPDIR/codex-pinned"
        mkdir -p "$pinned_root"
        cp -a ${pinnedSource}/. "$pinned_root/"
        chmod -R u+w "$pinned_root"

        # Prove that the complete manifest-declared series applies from the
        # pinned upstream revision, including binary generated artifacts.
        for patch in ${patchPathsForShell}; do
          git -C "$pinned_root" apply --check --binary "$patch"
          git -C "$pinned_root" apply --binary "$patch"
        done

        for fixture in \
          codex-rs/app-server-protocol/schema/precomputed/app-server-exports-stable.json.zst \
          codex-rs/app-server-protocol/schema/precomputed/app-server-exports-experimental.json.zst; do
          test -f "$pinned_root/$fixture"
          zstd --quiet --decompress --stdout "$pinned_root/$fixture" | jq empty
        done
        test -f "$pinned_root/codex-rs/Cargo.lock"
        test -f "$pinned_root/codex-rs/core/config.schema.json"
        test -d "$pinned_root/codex-rs/hooks/schema/generated"

        generation_digest() {
          local root="$1"
          (
            cd "$root"
            find . -type f -o -type l \
              | sed '/^\.\/\.git\//d; /^\.\/target\//d' \
              | sort \
              | while IFS= read -r path; do
                  if test -L "$path"; then
                    printf '%s\\0%s\\0' "$path" "$(readlink "$path")"
                  else
                    printf '%s\\0' "$path"
                    sha256sum "$path"
                  fi
                done
          ) | sha256sum | cut -d' ' -f1
        }

        generation_baseline="$TMPDIR/codex-generation-baseline"
        cp -a "$repo_root/." "$generation_baseline"
        before_generation="$(generation_digest "$repo_root")"

        cd "$repo_root"
        run_generators() {
          ${renderGeneratorCommands upstream.generate_commands}
        }
        run_generators
        first_generation="$(generation_digest "$repo_root")"
        run_generators
        second_generation="$(generation_digest "$repo_root")"
        test "$first_generation" = "$second_generation" || {
          echo "error: generator sequence is not byte-reproducible" >&2
          exit 1
        }
        test "$before_generation" = "$first_generation" || {
          echo "error: generators changed tracked or source-owned files" >&2
          diff -qr --exclude=.git --exclude=target "$generation_baseline" "$repo_root" || true
          exit 1
        }
        for fixture in $(find "$repo_root" -type f -name '*.json.zst' | sort); do
          relative="''${fixture#"$repo_root/"}"
          baseline_fixture="$generation_baseline/$relative"
          test -f "$baseline_fixture"
          diff -u \
            <(zstd --quiet --decompress --stdout "$baseline_fixture" | jq -S .) \
            <(zstd --quiet --decompress --stdout "$fixture" | jq -S .)
        done
        touch "$TMPDIR/generation-ok"

        cd codex-rs
        ${lib.escapeShellArgs upstream.validation_command}
        ${renderCommands targetedTests}
        ${renderCommands upstream.regression_commands}
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
    codexBaseFor
    codexCheckFor
    codexFor
    patchOrder
    supportedSystems
    upstream
    ;
}
