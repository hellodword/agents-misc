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
    "integration_commands"
    "ref"
    "revision"
    "url"
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
  patchDir = ../codex/patches + "/${upstream.ref}";
  patchDirectoryEntries = if builtins.pathExists patchDir then builtins.readDir patchDir else { };
  actualPatchFiles = lib.sort (a: b: a < b) (builtins.attrNames patchDirectoryEntries);
  patchFilesAreRegular = lib.all (
    filename: patchDirectoryEntries.${filename} == "regular"
  ) actualPatchFiles;
  generatedOnlyPatchValid =
    if validatedPatches == [ ] then
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
in
{
  inherit
    codexBaseFor
    codexFor
    patchOrder
    supportedSystems
    upstream
    ;
}
