t: runID: pythonSource:
let
  pkgs = t.config.hostPkgs;
  hook = pkgs.runCommand "vm-test-driver-observer" { } ''
    mkdir -p $out
    cp ${builtins.toFile "sitecustomize.py" pythonSource} $out/sitecustomize.py
  '';
  fresh = t.overrideTestDerivation (old: {
    name = old.name + "-" + runID;
    vmTestRunID = runID;
    vmTestAdapterVersion = "1";
    allowSubstitutes = false;
    requiredSystemFeatures = builtins.attrNames (
      builtins.listToAttrs (
        map
          (name: {
            inherit name;
            value = true;
          })
          (
            (old.requiredSystemFeatures or [ ])
            ++ [
              "kvm"
              "nixos-test"
            ]
          )
      )
    );
    buildCommand = ''
      vmTestOutput="$out"
      export out="$NIX_BUILD_TOP/vm-test-evidence"
      mkdir -p "$out"
      export VM_TEST_EVIDENCE="$out"
      export VM_TEST_RUN_ID=${pkgs.lib.escapeShellArg runID}
      export PYTHONPATH="${hook}''${PYTHONPATH:+:$PYTHONPATH}"
      ${old.buildCommand}
      mkdir -p "$vmTestOutput"
      cp -a "$out/." "$vmTestOutput/"
    '';
  });
in
{
  derivation = fresh.drvPath;
  system = t.system;
  force_accel = t.config.qemu.forceAccel or false;
  nodes = builtins.attrNames (t.config.nodes or { });
  containers = builtins.attrNames (t.config.containers or { });
}
