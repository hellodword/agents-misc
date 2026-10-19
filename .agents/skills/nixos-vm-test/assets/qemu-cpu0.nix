# Opt-in workaround for nested KVM on an Intel VirtualBox executor.
# Import into runNixOSTest, alongside nodes/testScript, using the project's pkgs.
#
# Observed on a NixOS host / Intel i5-1135G7 / VirtualBox 7.2.18r175117,
# with a 4-vCPU, 16-GiB Debian 13 executor (Linux 6.12.86+deb13-amd64):
# parallel NixOS guests could freeze the executor's SSH and console, while
# VBox.log reported a lost guest heartbeat. Other runs produced an inner-guest
# double fault, stack corruption panic or KVM emulation error.
#
# Candidate reproduction: enable nested VT-x and nested paging on that 4-vCPU
# executor, leave its kvm_intel vpid=Y, and run QEMU across CPUs 0-3. Give a fresh
# three-node runNixOSTest one vCPU and 768 MiB per node; start_all(), wait for
# multi-user.target, hash 64 MiB and ping between nodes. The failure is
# intermittent; those steps describe the observed workload, not a reliable
# reproducer. Two-vCPU guests also failed with QEMU pinned to CPU 0.
#
# The tested mitigation combines this module (all QEMU threads on CPU 0,
# one vCPU per node), executor kvm_intel vpid=0, and VirtualBox VPID enabled.
# Three fresh tests passed after reboot: QEMU 11.1.0/Linux 6.18.50 with 9p,
# and QEMU 11.1.2/Linux 6.18.56 with virtiofs. This is short-term evidence.
# Direct-driver runs with this profile also showed a QEMU exit with status 0
# and a guest boot stall while executor SSH remained responsive.
# Root cause remains unresolved among VirtualBox, Linux KVM and QEMU. Keep
# this opt-in while awaiting an upstream fix; retest without it after relevant
# upgrades. Nix compilation, the test driver and virtiofsd retain all CPUs.
{ hostPkgs, ... }:
let
  qemu = hostPkgs.qemu_test;
in
{
  defaults =
    { config, ... }:
    {
      assertions = [
        {
          assertion = config.virtualisation.cores == 1;
          message = "qemu-cpu0 uses one vCPU per node; run tests requiring SMP on a suitable executor.";
        }
      ];
    };
  qemu.forceAccel = true;
  qemu.package =
    assert hostPkgs.stdenv.hostPlatform.system == "x86_64-linux";
    hostPkgs.symlinkJoin {
      name = "qemu-test-cpu0";
      paths = [ qemu ];
      nativeBuildInputs = [ hostPkgs.makeWrapper ];
      passthru = (qemu.passthru or { }) // {
        inherit (qemu) stdenv;
      };
      postBuild = ''
        rm "$out/bin/qemu-system-x86_64"
        makeWrapper ${hostPkgs.util-linux}/bin/taskset "$out/bin/qemu-system-x86_64" \
          --add-flags "--cpu-list 0 ${qemu}/bin/qemu-system-x86_64"
      '';
    };
}
