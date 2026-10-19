# Example: nix-build smoke-test.nix --arg pkgs 'import /path/to/nixpkgs {}'
{
  pkgs ? import <nixpkgs> { },
  name ? "virtualbox-qemu-cpu0-smoke",
}:
pkgs.testers.runNixOSTest {
  inherit name;
  imports = [ ../../.agents/skills/nixos-vm-test/assets/qemu-cpu0.nix ];

  # All three QEMUs share executor CPU 0; compilation keeps the executor's CPUs.
  defaults = {
    virtualisation = {
      cores = 1;
      memorySize = 768;
    };
  };
  nodes = {
    a = { };
    b = { };
    c = { };
  };
  testScript = ''
    start_all()
    for machine in machines:
        machine.wait_for_unit("multi-user.target")
        assert machine.succeed("nproc").strip() == "1"
        machine.succeed("dd if=/dev/zero bs=1M count=64 status=none | sha256sum")
    a.succeed("ping -c 2 b")
    b.succeed("ping -c 2 c")
    c.succeed("ping -c 2 a")
    for machine in machines:
        assert machine.succeed("cat /proc/sys/kernel/tainted").strip() == "0"
  '';
}
