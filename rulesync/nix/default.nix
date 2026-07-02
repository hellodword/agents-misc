{
  lib,
  pkgs,
}:

let
  version = "9.0.2";

  nodejs = if pkgs ? nodejs_24 then pkgs.nodejs_24 else pkgs.nodejs_22;
  pnpm = pkgs.pnpm_10;

  rulesyncConfigFile = "rulesync.jsonc";
  rulesyncLocalConfigFile = "rulesync.local.jsonc";

  rulesyncSourceDirs = [
    ".rulesync/rules"
    ".rulesync/commands"
    ".rulesync/skills"
    ".rulesync/subagents"
  ];

  rulesyncSourceFiles = [
    rulesyncConfigFile
    rulesyncLocalConfigFile
    ".rulesync/.aiignore"
    ".rulesync/.mcp.json"
    ".rulesync/mcp.json"
    ".rulesync/hooks.json"
    ".rulesync/permissions.json"
    ".rulesyncignore"
  ];

  rulesyncVcsManagedFiles = [
    ".gitattributes"
    ".gitignore"
  ];

  rulesyncNeverWritableDirs = [
    ".claude/memories"
    ".rovodev/.rulesync"
    ".rulesync/skills/.curated"
    ".takt/.cache"
    ".takt/runs"
    ".takt/tasks"
  ];

  rulesyncNeverWritableFiles = [
    ".claude/CLAUDE.local.md"
    ".claude/settings.local.json"
    ".opencode/package-lock.json"
    ".takt/config.yaml"
    "AGENTS.local.md"
    "CLAUDE.local.md"
    rulesyncLocalConfigFile
  ];

  rulesyncNeverWritableGlobs = [
    ".claude/*.lock"
    ".rulesync/rules/*.local.md"
  ];

  parentDirsOf =
    path:
    let
      parent = dirOf path;
    in
    if parent == "." then [ ] else (parentDirsOf parent) ++ [ parent ];

  rulesyncSourceMountDirs = lib.unique (
    (lib.concatMap parentDirsOf (rulesyncSourceDirs ++ rulesyncSourceFiles)) ++ rulesyncSourceDirs
  );

  # Existing shared configs can affect the concrete file Rulesync chooses.
  rulesyncScopeProbeFiles = [
    "opencode.json"
    "opencode.jsonc"
  ];

  rulesyncPolicy = {
    sourceDirs = rulesyncSourceDirs;
    sourceFiles = rulesyncSourceFiles;
    sourceMountDirs = rulesyncSourceMountDirs;
    vcsManagedFiles = rulesyncVcsManagedFiles;
    scopeProbeFiles = rulesyncScopeProbeFiles;
    neverWritableDirs = rulesyncNeverWritableDirs;
    neverWritableFiles = rulesyncNeverWritableFiles;
    neverWritableGlobs = rulesyncNeverWritableGlobs;
    rulesyncWrites = {
      rules = {
        dirs = [ ".rulesync/rules" ];
      };
      ignore = {
        emptyFiles = [ ".rulesync/.aiignore" ];
      };
      mcp = {
        jsonFiles = [ ".rulesync/mcp.json" ];
      };
      commands = {
        dirs = [ ".rulesync/commands" ];
      };
      subagents = {
        dirs = [ ".rulesync/subagents" ];
      };
      skills = {
        dirs = [ ".rulesync/skills" ];
      };
      hooks = {
        jsonFiles = [ ".rulesync/hooks.json" ];
      };
      permissions = {
        jsonFiles = [ ".rulesync/permissions.json" ];
      };
    };
    dynamicFileAlternatives = {
      "opencode.json" = [
        "opencode.jsonc"
        "opencode.json"
      ];
    };
  };

  replaceVars =
    vars: file:
    builtins.replaceStrings (map (name: "@${name}@") (builtins.attrNames vars)) (map (
      name: vars.${name}
    ) (builtins.attrNames vars)) (builtins.readFile file);

  patchedDepsWorkspace = ".nix-pnpm-workspace-with-patches.yaml";

  stripPatchedDeps = ''
    sed -i '/^patchedDependencies:/,/^[^ ]/{/^patchedDependencies:/d;/^  /d;}' pnpm-lock.yaml pnpm-workspace.yaml
  '';

  applyPnpmPatchedDeps = ''
    if [[ ! -f ${patchedDepsWorkspace} ]]; then
      echo "missing saved pnpm workspace file: ${patchedDepsWorkspace}" >&2
      exit 1
    fi

    while IFS=$'\t' read -r dep_ref patch_path; do
      [[ -n "$dep_ref" ]] || continue

      pkg_name="''${dep_ref%@*}"
      if [[ -z "$pkg_name" || "$pkg_name" == "$dep_ref" ]]; then
        echo "invalid patched dependency key: $dep_ref" >&2
        exit 1
      fi

      if [[ "$patch_path" = /* ]]; then
        echo "refusing absolute patched dependency path: $patch_path" >&2
        exit 1
      fi

      patch_file="$(realpath -m -- "$patch_path")"
      case "$patch_file" in
        "$PWD"/*) ;;
        *)
          echo "refusing patched dependency path outside source tree: $patch_path" >&2
          exit 1
          ;;
      esac

      pkg_dir="node_modules/$pkg_name"

      [[ -f "$patch_file" ]] || {
        echo "missing patched dependency patch file: $patch_path" >&2
        exit 1
      }
      [[ -d "$pkg_dir" ]] || {
        echo "missing patched dependency package directory: $pkg_dir" >&2
        exit 1
      }

      echo "applying pnpm patched dependency: $dep_ref -> $patch_path"
      if patch --dry-run -p1 -d "$pkg_dir" < "$patch_file" > /dev/null; then
        patch -p1 -d "$pkg_dir" < "$patch_file"
      elif patch --dry-run -R -p1 -d "$pkg_dir" < "$patch_file" > /dev/null; then
        echo "pnpm patched dependency already applied: $dep_ref"
      else
        echo "failed to apply pnpm patched dependency: $dep_ref -> $patch_path" >&2
        patch --dry-run -p1 -d "$pkg_dir" < "$patch_file" >&2 || true
        exit 1
      fi
    done < <(yq -r '.patchedDependencies // {} | to_entries[] | [.key, .value] | @tsv' ${patchedDepsWorkspace})
  '';

  rulesync-unwrapped = pkgs.stdenvNoCC.mkDerivation (finalAttrs: {
    pname = "rulesync";
    inherit version;

    src = pkgs.fetchFromGitHub {
      owner = "dyoshikawa";
      repo = "rulesync";
      rev = "v${finalAttrs.version}";
      hash = "sha256-YdlEA3U7G7jB8aogFUZuZuKnmcTAZJ5C8VrFjquYF0E=";
    };

    pnpmDeps = pkgs.fetchPnpmDeps {
      inherit (finalAttrs) pname version src;
      inherit pnpm;
      fetcherVersion = 4;
      hash = "sha256-ELWYc/gi/yzcp0YxzHMf3ZWXDw/xx01aWzp9RNrVw5U=";
      prePnpmInstall = stripPatchedDeps;
    };

    postPatch = ''
      cp pnpm-workspace.yaml ${patchedDepsWorkspace}
      ${stripPatchedDeps}
    '';

    __structuredAttrs = true;
    strictDeps = true;

    nativeBuildInputs = [
      nodejs
      pnpm
      pkgs.pnpmConfigHook
      pkgs.pnpmBuildHook
      pkgs.makeWrapper
      pkgs.jq
      pkgs.yq
    ];

    pnpmInstallFlags = [ "--ignore-scripts" ];
    pnpmBuildScript = "build";
    preBuild = applyPnpmPatchedDeps;

    installPhase = ''
      runHook preInstall

      mkdir -p "$out/lib/rulesync" "$out/bin"
      cp -a package.json dist node_modules "$out/lib/rulesync/"

      makeWrapper ${nodejs}/bin/node "$out/bin/rulesync" \
        --add-flags "$out/lib/rulesync/dist/cli/index.js" \
        --set NODE_ENV production

      runHook postInstall
    '';

    meta = with lib; {
      description = "Unified AI rules management CLI tool";
      homepage = "https://github.com/dyoshikawa/rulesync";
      license = licenses.mit;
      platforms = platforms.all;
      mainProgram = "rulesync";
    };
  });

  rulesyncInitTemplate = pkgs.runCommand "rulesync-init-template" { } ''
    mkdir -p "$out" "$TMPDIR/home"
    cd "$out"
    HOME="$TMPDIR/home" \
    XDG_CACHE_HOME="$TMPDIR/home/.cache" \
    XDG_CONFIG_HOME="$TMPDIR/home/.config" \
    XDG_DATA_HOME="$TMPDIR/home/.local/share" \
    PATH=/no-such-path \
      ${rulesync-unwrapped}/bin/rulesync init --silent

    if find "$out" -type l | grep -q .; then
      echo "rulesync init template contains symlinks" >&2
      exit 1
    fi
  '';

  rulesyncPolicyFile = pkgs.writeText "rulesync-jail-policy.json" (builtins.toJSON rulesyncPolicy);

  rulesyncScope = pkgs.writeTextFile {
    name = "rulesync-jail-scope.mjs";
    executable = true;
    text = replaceVars {
      node = "${nodejs}/bin/node";
      rulesyncDistImport = "${rulesync-unwrapped}/lib/rulesync/dist/import-zmpFGK87.js";
      rulesyncPolicy = "${rulesyncPolicyFile}";
    } ./scope.mjs;
  };

  rulesyncClosure = pkgs.closureInfo {
    rootPaths = [
      nodejs
      rulesync-unwrapped
      rulesyncScope
      rulesyncPolicyFile
    ];
  };

  rulesync-jailed = pkgs.writeShellApplication {
    name = "rulesync";

    runtimeInputs = [
      pkgs.bubblewrap
      pkgs.coreutils
      pkgs.jq
    ];

    text = replaceVars {
      bwrap = "${pkgs.bubblewrap}/bin/bwrap";
      jq = "${pkgs.jq}/bin/jq";
      node = "${nodejs}/bin/node";
      rulesyncClosure = "${rulesyncClosure}";
      rulesyncInitTemplate = "${rulesyncInitTemplate}";
      rulesyncPolicy = "${rulesyncPolicyFile}";
      rulesyncScope = "${rulesyncScope}";
      rulesyncUnwrapped = "${rulesync-unwrapped}";
    } ./wrapper.sh;
  };

in
{
  rulesync = rulesync-jailed;
  rulesync-unwrapped = rulesync-unwrapped;
}
