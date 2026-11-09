{ pkgs, lib }:
let
  source = lib.fileset.toSource {
    root = ../tools/vm-test-mcp;
    fileset = lib.fileset.unions [
      ../tools/vm-test-mcp/go.mod
      ../tools/vm-test-mcp/go.sum
      ../tools/vm-test-mcp/cmd/vm-test-submit
      ../tools/vm-test-mcp/internal/submit
      ../tools/vm-test-mcp/internal/safefs
      ../tools/vm-test-mcp/internal/wire
      ../tools/vm-test-mcp/tests/test_driver_observer.py
    ];
  };
in
pkgs.buildGoModule {
  pname = "vm-test-submit";
  version = "0.15.0";
  src = source;
  vendorHash = "sha256-H3zVpMTG/crvB5DxpauMXMQacFCZPVwyKGViFlTuWNY=";
  subPackages = [ "cmd/vm-test-submit" ];
  ldflags = [
    "-s"
    "-w"
    "-X=github.com/hellodword/agents-misc/vm-test-mcp/internal/wire.SourceID=${builtins.baseNameOf source}"
  ];
  checkPhase = ''
    runHook preCheck
    go vet ./...
    VM_TEST_DRIVER_INTEGRATION=1 go test -race ./...
    runHook postCheck
  '';
  nativeBuildInputs = [ pkgs.python3 ];
  nativeCheckInputs = [
    pkgs.nix
    pkgs.nixos-test-driver
  ];
  postInstall = ''
    mkdir -p $out/share/vm-test-mcp $out/share/licenses/vm-test-submit
    cp ${../.agents/skills/nixos-vm-test/assets/vm-smoke.nix} $out/share/vm-test-mcp/vm-smoke.nix
    cp ${../tools/vm-test-mcp/client.example.yaml} $out/share/vm-test-mcp/client.example.yaml
    cp ${../LICENSE} $out/share/licenses/vm-test-submit/LICENSE
    tar -xOf ${pkgs.go.src} go/LICENSE > $out/share/licenses/vm-test-submit/LICENSE.Go
    python3 ${../tools/vm-test-mcp/scripts/component-licenses.py} vendor $out/share/licenses/vm-test-submit/modules
  '';
  meta = {
    description = "Submit NixOS tests, recover executions and retrieve their evidence";
    license = lib.licenses.mit;
    mainProgram = "vm-test-submit";
    platforms = [ "x86_64-linux" ];
  };
}
