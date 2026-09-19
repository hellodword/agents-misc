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
  expectedPatchCount = 4;
  expectedPatchOrder = [
    "0001-provider-network-config.patch"
    "0002-failure-hook-contract.patch"
    "0003-failure-hook-integration.patch"
    "0004-generated-contracts.patch"
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
  patchDirectoryEntries = if builtins.pathExists patchDir then builtins.readDir patchDir else { };
  actualPatchFiles = lib.sort (a: b: a < b) (
    lib.filter (name: lib.hasSuffix ".patch" name) (builtins.attrNames patchDirectoryEntries)
  );
  generatedOnlyPatchValid =
    if lib.length validatedPatches != expectedPatchCount then
      false
    else
      let generatedPatch = builtins.elemAt validatedPatches 3;
      in generatedPatch.source_files == [ ] && generatedPatch.source_prefixes == [ ]
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
      else if patchOrder != expectedPatchOrder then
        throw "agents-misc: active Codex patch order must be 0001..0004 with generated contracts in 0004"
      else if actualPatchFiles != expectedPatchOrder then
        throw "agents-misc: codex patch directory contains stale or missing patch files"
      else if !generatedOnlyPatchValid then
        throw "agents-misc: 0004-generated-contracts.patch must have generated-only ownership"
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
            hash = "sha256-5kwpOFDCVeurNtaZhH6FWz3rtl2QqGyR1N2c5suIL1M=";
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
    system:
    let
      pkgs = import nixpkgs { inherit system; };
      codex = llm-agents.packages.${system}.codex.override {
        version = pinnedVersion;
        srcOverride = pinnedSource;
      };
    in
    patchCodex pkgs codex;
  codexCheckFor =
    system:
    let
      pkgs = import nixpkgs { inherit system; };
      codex = codexFor system;
      renderGeneratorCommand = command:
        if command != [ ] && builtins.head command == "cargo" then
          "run_cargo_generator ${lib.escapeShellArgs (builtins.tail command)}"
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
        pkgs.just
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

        # Prove that the complete four-patch series applies from the pinned
        # upstream revision, including the binary generated-contract patch.
        for patch in ${patchPathsForShell}; do
          git -C "$pinned_root" apply --check --binary "$patch"
          git -C "$pinned_root" apply --binary "$patch"
        done

        # Negative terminal marker gate and static live-producer coverage.
        if rg -n --hidden --glob '!.git/**' --glob '!target/**' \
          'terminal_wait|TerminalWait|TerminalWaitDecision|terminal_wait_control|TerminalWaitToml|YieldReason' \
          "$pinned_root"; then
          echo "error: terminal-only marker found in patched pinned source" >&2
          exit 1
        fi
        for producer in \
          codex-rs/core/src/session/turn.rs \
          codex-rs/core/src/compact.rs \
          codex-rs/core/src/compact_remote.rs \
          codex-rs/core/src/compact_remote_v2.rs \
          codex-rs/core/src/guardian/review.rs \
          codex-rs/ext/guardian-v2/src/async_scorer/sampler.rs \
          codex-rs/core/src/hook_runtime.rs; do
          test -f "$pinned_root/$producer"
          rg -q 'model_request_failure_policy|record_model_request_failure' "$pinned_root/$producer"
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
        run_cargo_generator() (
          # Nix's cargo vendor hook supplies pinned git sources and deliberately
          # rejects `cargo generate-lockfile` until a lockfile already exists.
          # The lockfile itself is generated by the declared command in the
          # codex shell; here metadata --locked proves that exact generated
          # lockfile is consumable without network access.
          if test "''${1-}" = generate-lockfile; then
            (cd "$repo_root/codex-rs" && cargo metadata --locked --offline --no-deps --format-version 1 >/dev/null)
          else
            (cd "$repo_root/codex-rs" && cargo "$@")
          fi
        )
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
    codexCheckFor
    codexFor
    patchOrder
    supportedSystems
    upstream
    ;
}
