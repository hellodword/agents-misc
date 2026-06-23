{ python3Packages }:

python3Packages.buildPythonApplication {
  pname = "codexcfg";
  version = "0.1.0";
  pyproject = true;

  src = ./.;

  build-system = [
    python3Packages.hatchling
  ];

  dependencies = [
    python3Packages.packaging
    python3Packages.rich
    python3Packages.tomlkit
  ];

  pythonImportsCheck = [
    "codexcfg"
  ];
}
