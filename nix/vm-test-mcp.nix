{
  pkgs,
  lib,
  nixpkgs,
}:
let
  source = lib.fileset.toSource {
    root = ../tools/vm-test-mcp;
    fileset = lib.fileset.unions [
      ../tools/vm-test-mcp/go.mod
      ../tools/vm-test-mcp/go.sum
      ../tools/vm-test-mcp/config.example.yaml
      ../tools/vm-test-mcp/cmd
      ../tools/vm-test-mcp/internal
    ];
  };
  sdkArchive = pkgs.fetchurl {
    url = "https://download.virtualbox.org/virtualbox/7.2.20/VirtualBoxSDK-7.2.20-175154.zip";
    hash = "sha256-2U5N3dW5nISy4yjooZ+v+QYWitHVHpVFN29jgQjA7cg=";
  };
  sdk = pkgs.runCommand "virtualbox-sdk-7.2.20" { nativeBuildInputs = [ pkgs.unzip ]; } ''
    mkdir -p $out
    unzip -q ${sdkArchive} -d $out
  '';
  native = pkgs.stdenv.mkDerivation {
    pname = "vm-test-api-helper";
    version = "7.2.20";
    src = lib.fileset.toSource {
      root = ../tools/vm-test-mcp;
      fileset = lib.fileset.unions [
        ../tools/vm-test-mcp/native.c
        ../tools/vm-test-mcp/test_native.py
        ../tools/vm-test-mcp/tests/native_fake.c
      ];
    };
    nativeBuildInputs = [ pkgs.python3 ];
    buildPhase = ''
      runHook preBuild
      cc -std=c11 -D_POSIX_C_SOURCE=200809L -Wall -Wextra -Werror -O2 \
        -I${sdk}/sdk/bindings/c/include -I${sdk}/sdk/bindings/c/glue \
        native.c ${sdk}/sdk/bindings/c/glue/VBoxCAPIGlue.c -ldl -o vbox-api-helper
      runHook postBuild
    '';
    doCheck = true;
    checkPhase = ''
      VM_TEST_SDK_DIR=${sdk}/sdk VM_TEST_NATIVE_BINARY="$PWD/vbox-api-helper" \
        python3 -B -m unittest -v test_native
    '';
    installPhase = ''
      mkdir -p $out/bin $out/share/vm-test-mcp/virtualbox-sdk
      cp vbox-api-helper $out/bin/
      cp -r ${sdk}/sdk/bindings/c $out/share/vm-test-mcp/virtualbox-sdk/
      cp native.c $out/share/vm-test-mcp/virtualbox-sdk/
      cp ${sdk}/sdk/bindings/webservice/java/jax-ws/COPYING.LIB $out/share/vm-test-mcp/virtualbox-sdk/
      cp ${../LICENSE} $out/share/vm-test-mcp/virtualbox-sdk/LICENSE.native
    '';
    meta = {
      description = "Native control helper using the independently pinned official VirtualBox C SDK";
      license = [
        lib.licenses.mit
        lib.licenses.lgpl21Only
      ];
      platforms = [ "x86_64-linux" ];
    };
  };
  goBase = {
    pname = "vm-test-mcp";
    version = "0.14.0";
    src = source;
    vendorHash = "sha256-utwEeEeOIhFTZl66lwO+ErGwa1gl+rkJL0qMAlUOx8k=";
    tags = [ "sqlite_omit_load_extension" ];
    ldflags = [
      "-s"
      "-w"
      "-X=github.com/hellodword/agents-misc/vm-test-mcp/internal/wire.SourceID=${builtins.baseNameOf source}"
    ];
    meta = {
      description = "Authenticated VM labs and sandboxed NixOS tests";
      license = lib.licenses.mit;
      mainProgram = "vm-test-mcp";
      platforms = [ "x86_64-linux" ];
    };
  };
  package = pkgs.buildGoModule (
    goBase
    // {
      subPackages = [
        "cmd/vm-test-mcp"
        "cmd/vm-test-nix-worker"
        "cmd/vm-test-nix-executor"
        "cmd/vm-test-submit"
        "cmd/vm-test-guest"
        "cmd/vm-test-router-helper"
      ];
      nativeBuildInputs = [ pkgs.python3 ];
      checkPhase = ''
        runHook preCheck
        go vet -tags sqlite_omit_load_extension ./...
        go test -race -tags sqlite_omit_load_extension ./...
        runHook postCheck
      '';
      postBuild = ''
        GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="${lib.concatStringsSep " " goBase.ldflags}" -o vm-test-guest-windows-amd64.exe ./cmd/vm-test-guest
        GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="${lib.concatStringsSep " " goBase.ldflags}" -o vm-test-guest-linux-amd64 ./cmd/vm-test-guest
      '';
      postInstall = ''
        mkdir -p $out/share/vm-test-mcp/guest $out/share/licenses/vm-test-mcp
        cp vm-test-guest-windows-amd64.exe vm-test-guest-linux-amd64 $out/share/vm-test-mcp/guest/
        cp ${../.agents/skills/nixos-vm-test/assets/vm-smoke.nix} $out/share/vm-test-mcp/vm-smoke.nix
        ln -s ${native}/bin/vbox-api-helper $out/bin/vbox-api-helper
        ln -s ${pkgs.xorriso}/bin/xorriso $out/bin/xorriso
        ln -s ${pkgs.xorriso.src} $out/share/licenses/vm-test-mcp/xorriso-source
        ln -s ${native}/share/vm-test-mcp/virtualbox-sdk $out/share/licenses/vm-test-mcp/virtualbox-sdk
        cp ${../LICENSE} $out/share/licenses/vm-test-mcp/LICENSE
        cp ${../tools/vm-test-mcp/THIRD_PARTY.md} $out/share/licenses/vm-test-mcp/THIRD_PARTY.md
        tar -xOf ${pkgs.go.src} go/LICENSE > $out/share/licenses/vm-test-mcp/LICENSE.Go
        python3 ${../tools/vm-test-mcp/scripts/component-licenses.py} vendor $out/share/licenses/vm-test-mcp/modules
      '';
      passthru = { inherit native sdk; };
    }
  );
  routerHelper = pkgs.buildGoModule (
    goBase
    // {
      pname = "vm-test-router-helper";
      subPackages = [ "cmd/vm-test-router-helper" ];
      doCheck = false;
    }
  );
  router = import ./vm-test-router.nix {
    inherit
      pkgs
      lib
      nixpkgs
      routerHelper
      ;
  };
in
{
  inherit package native router;
  all = pkgs.symlinkJoin {
    name = "vm-test-all-${package.version}";
    paths = [
      package
      router
    ];
    meta = {
      description = "VM Test MCP services, guest supervisors and router Live ISO";
      inherit (package.meta) license platforms;
    };
  };
}
