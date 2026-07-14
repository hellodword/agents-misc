{
  pkgs,
  codexVersion,
  minVersion ? "0.129.0",
}:

let
  codexTag = "rust-v${codexVersion}";
  toolSource = ../tools/codex-config-atlas;
  schemasSource = toolSource + "/schemas";
  webSource = toolSource + "/web";

  codexConfigAtlasPackage = pkgs.python3Packages.buildPythonApplication {
    pname = "codex-config-atlas";
    version = "0.1.0";
    pyproject = true;

    src = toolSource;

    build-system = [
      pkgs.python3Packages.hatchling
    ];

    dependencies = [
      pkgs.python3Packages.packaging
      pkgs.python3Packages.rich
      pkgs.python3Packages.tomlkit
    ];

    pythonImportsCheck = [
      "codex_config_atlas"
    ];
  };

  codexConfigAtlas = pkgs.writeShellApplication {
    name = "codex-config-atlas";
    runtimeInputs = [ codexConfigAtlasPackage ];

    text = ''
      exec ${codexConfigAtlasPackage}/bin/codex-config-atlas \
        --current-version "${codexVersion}" \
        --current-tag "${codexTag}" \
        --min-version "${minVersion}" \
        "$@"
    '';
  };

  codexConfigAtlasRegistry =
    pkgs.runCommand "codex-config-atlas-registry-${codexVersion}"
      {
        nativeBuildInputs = [ codexConfigAtlas ];
      }
      ''
        codex-config-atlas check-registry \
          --schemas ${schemasSource} \
          --current-version ${codexVersion} \
          --min-version ${minVersion}

        mkdir -p "$out"
        cp -R ${schemasSource}/. "$out/"
      '';

  codexConfigAtlasData =
    pkgs.runCommand "codex-config-atlas-data-${codexVersion}"
      {
        nativeBuildInputs = [ codexConfigAtlas ];
      }
      ''
        mkdir -p "$out"

        codex-config-atlas build-data \
          --schemas ${codexConfigAtlasRegistry} \
          --current-version ${codexVersion} \
          --min-version ${minVersion} \
          --out "$out"
      '';

  codexConfigAtlasSite =
    pkgs.runCommand "codex-config-atlas-site-${codexVersion}"
      {
        nativeBuildInputs = [ codexConfigAtlas ];
      }
      ''
        mkdir -p "$out"

        codex-config-atlas build-site \
          --static ${webSource} \
          --data ${codexConfigAtlasData} \
          --out "$out"
      '';

  checkConfigAtlasRegistry =
    pkgs.runCommand "check-codex-config-atlas-registry-${codexVersion}"
      {
        nativeBuildInputs = [ codexConfigAtlas ];
      }
      ''
        codex-config-atlas check-registry \
          --schemas ${schemasSource} \
          --current-version ${codexVersion} \
          --min-version ${minVersion}

        mkdir -p "$out"
        touch "$out/ok"
      '';

  checkConfigAtlasData = pkgs.runCommand "check-codex-config-atlas-data-${codexVersion}" { } ''
    test -f ${codexConfigAtlasData}/versions.json
    test -d ${codexConfigAtlasData}/versions
    test ! -e ${codexConfigAtlasData}/current.json
    test ! -e ${codexConfigAtlasData}/diffs

    mkdir -p "$out"
    touch "$out/ok"
  '';

  checkConfigAtlasSite = pkgs.runCommand "check-codex-config-atlas-site-${codexVersion}" { } ''
    test -f ${codexConfigAtlasSite}/index.html
    test -f ${codexConfigAtlasSite}/data/versions.json
    test -d ${codexConfigAtlasSite}/data/versions
    test ! -e ${codexConfigAtlasSite}/data/current.json
    test ! -e ${codexConfigAtlasSite}/data/diffs

    mkdir -p "$out"
    touch "$out/ok"
  '';
in
{
  inherit
    codexConfigAtlas
    codexConfigAtlasRegistry
    codexConfigAtlasData
    codexConfigAtlasSite
    checkConfigAtlasRegistry
    checkConfigAtlasData
    checkConfigAtlasSite
    ;
}
