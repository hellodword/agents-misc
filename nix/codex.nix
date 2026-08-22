{
  lib,
  llm-agents,
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
    codex:
    codex.overrideAttrs (
      old:
      let
        version = old.version or (builtins.parseDrvName old.name).version;
        packageRef = "rust-v${version}";
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
          patches = (old.patches or [ ]) ++ patchPaths;

          # llm-agents.nix builds from source/codex-rs, while these patches
          # are generated against the OpenAI Codex repository root.
          patchFlags = [
            "-p1"
            "-d"
            ".."
          ];

          passthru = (old.passthru or { }) // {
            agentsMiscPatch = builtins.head patchPaths;
            agentsMiscPatches = patchPaths;
            agentsMiscPatchOrder = patchOrder;
            agentsMiscUpstreamRevision = upstream.revision;
          };
        }
    );

  supportedSystems = builtins.attrNames llm-agents.packages;
  codexFor = system: patchCodex llm-agents.packages.${system}.codex;
in
{
  inherit
    codexFor
    patchOrder
    supportedSystems
    upstream
    ;
}
