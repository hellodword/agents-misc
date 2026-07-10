{
  lib,
  nixpkgs,
  llm-agents,
}:

let
  codeModeHostOverlayVersions = [
    "0.144.0"
    "0.144.1"
  ];

  patchesForVersion =
    version:
    let
      patchDir = ../codex/patches/rust-v${version};
      series = patchDir + "/series";
      parseSeriesLine = line: line != "" && !(lib.hasPrefix "#" line);
      patchForSeriesEntry =
        entry:
        let
          patch = patchDir + "/${entry}";
        in
        if builtins.pathExists patch then
          patch
        else
          throw "agents-misc: codex patch series entry not found for rust-v${version}: ${entry}";
    in
    if builtins.pathExists series then
      map patchForSeriesEntry (
        builtins.filter parseSeriesLine (lib.splitString "\n" (builtins.readFile series))
      )
    else
      throw "agents-misc: no codex patch series found for rust-v${version}";

  patchCodex =
    codex:
    codex.overrideAttrs (
      old:
      let
        version = old.version or (builtins.parseDrvName old.name).version;
        cargoBuildFlags = old.cargoBuildFlags or [ ];
        needsCodeModeHostOverlay =
          (builtins.elem version codeModeHostOverlayVersions)
          && !(builtins.elem "codex-code-mode-host" cargoBuildFlags);
        localPatches =
          let
            patches = patchesForVersion version;
          in
          if patches == [ ] then
            throw "agents-misc: empty codex patch series for rust-v${version}"
          else
            patches;
      in
      {
        patches = (old.patches or [ ]) ++ localPatches;

        # llm-agents.nix builds from source/codex-rs, while these patches
        # are generated against the OpenAI Codex repository root.
        patchFlags = [
          "-p1"
          "-d"
          ".."
        ];

        passthru = (old.passthru or { }) // {
          agentsMiscPatch = builtins.head localPatches;
          agentsMiscPatches = localPatches;
        };
      }
      // lib.optionalAttrs needsCodeModeHostOverlay {
        cargoBuildFlags = cargoBuildFlags ++ [
          "--package"
          "codex-code-mode-host"
        ];
      }
    );

  supportedSystems = builtins.attrNames llm-agents.packages;

  pkgsFor = system: import nixpkgs { inherit system; };

  codexFor = system: patchCodex llm-agents.packages.${system}.codex;

  codexConfigFor =
    system:
    let
      pkgs = pkgsFor system;
      codex = codexFor system;
      codexVersion = codex.version or (builtins.parseDrvName codex.name).version;
    in
    import ../codex/nix {
      inherit lib pkgs codexVersion;
      repoSchemas = ../codex/schemas;
      repoSiteStatic = ../codex/site/static;
      minVersion = "0.129.0";
    };

in
{
  inherit
    codexConfigFor
    codexFor
    supportedSystems
    ;
}
