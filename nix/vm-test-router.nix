{
  pkgs,
  lib,
  nixpkgs,
  routerHelper,
}:
let
  # The locked Guest Additions package also builds out-of-tree video modules.
  # This headless router uses the upstream kernel's vboxguest driver and needs
  # only Guest Control, so reuse its source-built userspace archive directly.
  guestService = pkgs.stdenv.mkDerivation {
    pname = "vm-test-router-vbox-service";
    version = pkgs.linuxPackages.virtualboxGuestAdditions.version;
    src = pkgs.linuxPackages.virtualboxGuestAdditions.src;
    sourceRoot = ".";
    nativeBuildInputs = [ pkgs.autoPatchelfHook ];
    buildInputs = [
      pkgs.stdenv.cc.cc
      pkgs.zlib
      pkgs.libxcrypt
      pkgs.pam
    ];
    # Guest Control loads authentication libraries with dlopen; they do not
    # appear in DT_NEEDED and must survive autoPatchelf's RUNPATH pruning.
    runtimeDependencies = [
      (lib.getLib pkgs.libxcrypt)
      (lib.getLib pkgs.pam)
    ];
    dontBuild = true;
    dontStrip = true;
    installPhase = ''
      install -Dm755 sbin/VBoxService $out/bin/VBoxService
      install -Dm755 bin/VBoxControl $out/bin/VBoxControl
    '';
    meta.license = lib.licenses.gpl3Only;
  };
  machine = nixpkgs.lib.nixosSystem {
    system = "x86_64-linux";
    modules = [
      (nixpkgs + "/nixos/modules/installer/cd-dvd/iso-image.nix")
      (nixpkgs + "/nixos/modules/profiles/minimal.nix")
      ({ ... }: {
        system.stateVersion = "26.05";
        system.nixos.label = "vm-test-router-${routerHelper.version}";
        networking.hostName = "vm-test-router";
        networking.useDHCP = false;
        networking.useNetworkd = false;
        networking.firewall.enable = false;
        boot.loader.grub.enable = false;
        boot.initrd.availableKernelModules = [
          "ahci"
          "sd_mod"
          "sr_mod"
          "vboxguest"
        ];
        boot.kernelModules = [
          "vboxguest"
          "e1000"
        ];
        boot.kernelParams = [
          # The router needs a firmware framebuffer, not a native GPU driver.
          "nomodeset"
          "console=tty0"
          # /dev/console carries initrd and service output to the collector.
          "console=ttyS0,115200n8"
        ];
        boot.consoleLogLevel = 6;
        boot.kernel.sysctl = {
          "net.ipv4.ip_forward" = 1;
          "net.ipv6.conf.all.forwarding" = 0;
          "net.ipv6.conf.all.disable_ipv6" = 1;
        };
        image.baseName = lib.mkForce "vm-test-router";
        isoImage = {
          volumeID = "VMTESTROUTER";
          makeEfiBootable = true;
          makeBiosBootable = false;
          makeUsbBootable = false;
          squashfsCompression = "zstd -Xcompression-level 6";
        };
        boot.loader.timeout = lib.mkForce 0;
        boot.initrd.systemd.enable = false;
        boot.initrd.includeDefaultModules = false;
        swapDevices = [ ];
        users.users.root.initialHashedPassword = "!";
        users.mutableUsers = true;
        services.openssh.enable = false;
        services.getty.autologinUser = null;
        services.journald.settings.Journal = {
          Storage = "volatile";
          RuntimeMaxUse = "8M";
        };
        documentation.enable = false;
        documentation.nixos.enable = false;
        documentation.man.enable = false;
        environment.systemPackages = [ routerHelper ];
        nix.enable = false;
        # VBoxService waits for this misc device. Without the tag, systemd
        # never marks dev-vboxguest.device active even when the driver loaded.
        services.udev.extraRules = ''
          SUBSYSTEM=="misc", KERNEL=="vboxguest", TAG+="systemd"
        '';
        systemd.services.virtualbox = {
          wantedBy = [ "multi-user.target" ];
          requires = [
            "dev-vboxguest.device"
            "vm-test-seed.service"
          ];
          after = [
            "dev-vboxguest.device"
            "vm-test-seed.service"
          ];
          unitConfig.ConditionVirtualization = "oracle";
          serviceConfig.ExecStart = "${guestService}/bin/VBoxService --foreground";
        };
        systemd.services.vm-test-seed = {
          wantedBy = [ "multi-user.target" ];
          wants = [ "systemd-udev-settle.service" ];
          before = [ "virtualbox.service" ];
          after = [
            "local-fs.target"
            "systemd-udev-settle.service"
            "nftables.service"
          ];
          path = [
            pkgs.util-linux
            pkgs.iproute2
            pkgs.nftables
            pkgs.conntrack-tools
            pkgs.shadow
            pkgs.systemd
          ];
          serviceConfig = {
            Type = "oneshot";
            RemainAfterExit = true;
            UMask = "0077";
          };
          script = ''
            mkdir -p /run/vm-test /run/vm-test-seed
            # The seed remains attached across reboots; no writable disk exists.
            test -e /dev/disk/by-label/VMTESTSEED
            mount -o ro,nosuid,nodev,noexec /dev/disk/by-label/VMTESTSEED /run/vm-test-seed
            ${routerHelper}/bin/vm-test-router-helper init --request /run/vm-test-seed/seed.json
            umount /run/vm-test-seed
          '';
        };
        # Guest Control inherits this controlled PATH when invoking the helper.
        systemd.services.virtualbox.path = [
          pkgs.iproute2
          pkgs.nftables
          pkgs.conntrack-tools
          pkgs.shadow
          pkgs.systemd
        ];
        systemd.services.vm-test-dnsmasq = {
          serviceConfig = {
            Type = "simple";
            ExecStart = "${pkgs.dnsmasq}/bin/dnsmasq --keep-in-foreground --conf-file=/run/vm-test/dnsmasq.conf";
            Restart = "on-failure";
            MemoryMax = "32M";
          };
        };
        # No WAN traffic is permitted before seed initialization installs policy.
        networking.nftables.enable = true;
        networking.nftables.ruleset = ''
          table inet vm_test {
            chain input { type filter hook input priority 0; policy drop; iifname lo accept; }
            chain output { type filter hook output priority 0; policy drop; oifname lo accept; }
            chain forward { type filter hook forward priority 0; policy drop; }
          }
        '';
      })
    ];
  };
in
pkgs.runCommand "vm-test-router-${routerHelper.version}"
  {
    nativeBuildInputs = [
      pkgs.python3
      pkgs.xorriso
      pkgs.squashfsTools
      pkgs.mtools
      pkgs.patchelf
    ];
    passthru = { inherit machine; };
    meta = {
      description = "Diskless NixOS Live ISO for the VM Test MCP router (256 MiB acceptance target)";
      platforms = [ "x86_64-linux" ];
      license = lib.licenses.mit;
    };
  }
  ''
    mkdir -p $out
    ln -s ${machine.config.system.build.isoImage}/iso/vm-test-router.iso $out/vm-test-router.iso
    python3 ${../tools/vm-test-mcp/scripts/router-image.py} $out/vm-test-router.iso ${machine.config.system.build.toplevel}
    cp ${../tools/vm-test-mcp/THIRD_PARTY.md} $out/THIRD_PARTY.md
    ln -s ${nixpkgs.outPath} $out/nixpkgs-source
    ln -s ${machine.config.system.build.toplevel} $out/system
    touch $out/static-image-check-passed
  ''
