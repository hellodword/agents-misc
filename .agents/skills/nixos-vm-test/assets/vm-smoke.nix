{ lib, ... }:
{
  name = "remote-kvm-smoke";
  qemu.forceAccel = lib.mkForce true;
  globalTimeout = 900;

  nodes.machine = {
    virtualisation.memorySize = 1024;
    virtualisation.cores = 1;
  };

  testScript = ''
    machine.start()
    machine.wait_for_unit("multi-user.target")
    machine.succeed("test -d /nix/store")
  '';
}
