{ ... }:
{
  name = "vm-test-acceptance";
  globalTimeout = 900;
  qemu.forceAccel = true;
  defaults = {
    virtualisation.cores = 2;
    virtualisation.memorySize = 1024;
    networking.firewall.enable = false;
  };
  nodes = {
    first = { };
    second = { };
    third = { };
  };
  testScript = ''
    start_all()
    for node in (first, second, third):
        node.wait_for_unit("multi-user.target")
        node.succeed("test $(nproc) -eq 2")
    first.succeed("ping -c 1 second")
    second.succeed("ping -c 1 third")
    third.succeed("ping -c 1 first")
  '';
}
