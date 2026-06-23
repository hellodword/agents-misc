{
  lib,
  pkgs,
  codexVersion,
  repoSchemas,
  repoSiteStatic,
  minVersion ? "0.129.0",
}:

let
  codexTag = "rust-v${codexVersion}";

  codexcfgPackage = pkgs.callPackage ../tools/codexcfg/package.nix { };

  codexcfg = pkgs.writeShellApplication {
    name = "codexcfg";
    runtimeInputs = [ codexcfgPackage ];

    text = ''
      exec ${codexcfgPackage}/bin/codexcfg \
        --current-version "${codexVersion}" \
        --current-tag "${codexTag}" \
        --min-version "${minVersion}" \
        "$@"
      '';
  };

  codexcfgApp = codexcfg;

  codexSchemaRegistry = pkgs.runCommand "codex-schema-registry-${codexVersion}" {
    nativeBuildInputs = [ codexcfg ];
  } ''
    codexcfg check-registry \
      --schemas ${repoSchemas} \
      --current-version ${codexVersion} \
      --min-version ${minVersion}

    mkdir -p "$out"
    cp -R ${repoSchemas}/. "$out/"
  '';

  codexConfigData = pkgs.runCommand "codex-config-data-${codexVersion}" {
    nativeBuildInputs = [ codexcfg ];
  } ''
    mkdir -p "$out"

    codexcfg build-data \
      --schemas ${codexSchemaRegistry} \
      --current-version ${codexVersion} \
      --min-version ${minVersion} \
      --out "$out"
  '';

  codexConfigSite = pkgs.runCommand "codex-config-site-${codexVersion}" {
    nativeBuildInputs = [ codexcfg ];
  } ''
    mkdir -p "$out"

    codexcfg build-site \
      --static ${repoSiteStatic} \
      --data ${codexConfigData} \
      --out "$out"
  '';

  checkSchemaRegistry = pkgs.runCommand "check-codex-schema-registry-${codexVersion}" {
    nativeBuildInputs = [ codexcfg ];
  } ''
    codexcfg check-registry \
      --schemas ${repoSchemas} \
      --current-version ${codexVersion} \
      --min-version ${minVersion}

    mkdir -p "$out"
    touch "$out/ok"
  '';

  checkConfigData = pkgs.runCommand "check-codex-config-data-${codexVersion}" { } ''
    test -f ${codexConfigData}/versions.json
    test -f ${codexConfigData}/current.json
    test -d ${codexConfigData}/versions
    test -d ${codexConfigData}/diffs

    mkdir -p "$out"
    touch "$out/ok"
  '';

  checkConfigSite = pkgs.runCommand "check-codex-config-site-${codexVersion}" { } ''
    test -f ${codexConfigSite}/index.html
    test -f ${codexConfigSite}/data/versions.json
    test -f ${codexConfigSite}/data/current.json

    mkdir -p "$out"
    touch "$out/ok"
  '';
in
{
  inherit
    codexcfg
    codexcfgApp
    codexSchemaRegistry
    codexConfigData
    codexConfigSite
    checkSchemaRegistry
    checkConfigData
    checkConfigSite
    ;
}
