# Preparing a Linux VM test executor

This is a manual guide for the person administering the executor. The runtime skill uses a prepared machine and reports missing prerequisites. These setup steps belong to a separately authorized environment-preparation task.

## Roles and prerequisites

| Role                | Requirements                                                                                                                       |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| Client              | Python 3.11+, Nix with `nix-command` and `flakes`, OpenSSH; `sshpass` for password authentication. It may run in a container.      |
| Executor            | Linux, Python 3.11+, OpenSSH server, multi-user Nix daemon, `nix`, `nix-daemon`, `nix-store`, working KVM and sufficient RAM/disk. |
| Virtualization host | Hardware virtualization exposed to the executor; nested virtualization when the executor is a VM.                                  |
| Test guests         | QEMU and NixOS from the consuming project's locked nixpkgs.                                                                        |

Declare the client tools in its project development shell. On Debian 13, an administrator can install the executor's base tools with:

```sh
sudo apt-get update
sudo apt-get install python3 openssh-server ca-certificates curl xz-utils acl
sudo systemctl enable --now ssh
python3 --version
```

Debian provides [Python 3](https://packages.debian.org/trixie/python3) and [ACL utilities](https://packages.debian.org/trixie/acl). QEMU and the test driver are supplied by Nix, so a system-wide QEMU installation is unnecessary.

Prepare a normal login account with a password or an authorized public key. For key authentication the client uses a readable, unencrypted private key. Ordinary test submission needs daemon access, not sudo or membership in `trusted-users`.

## Multi-user Nix

Use the established installation if it already supplies a multi-user daemon. For a fresh Debian system with systemd, the [official installer](https://nix.dev/manual/nix/2.35/installation/installing-binary.html) supports `--daemon`. Download and inspect it, then run it as the administrator's chosen installation procedure:

```sh
curl --fail --location https://nixos.org/nix/install --output nix-install.sh
less nix-install.sh
sh nix-install.sh --daemon
```

Start a new login shell afterward. Make `/nix/var/nix/profiles/default/bin` available to the account; the runtime also probes that path and `/run/current-system/sw/bin` independently for each Nix executable.

Merge these settings into `/etc/nix/nix.conf`, preserving the installation's existing cache and trust configuration:

```ini
experimental-features = nix-command flakes
sandbox = true
sandbox-fallback = false
extra-system-features = kvm nixos-test
require-sigs = true
```

Apply the configuration with `sudo systemctl restart nix-daemon.service`. On NixOS, own these settings through `nix.settings` and the normal system rebuild. Confirm the daemon's actual configuration and service environment; `nix config show` as the SSH user is a useful diagnostic, but can differ from the running daemon.

Choose `max-jobs` and `cores` for compilation and available memory. For the tested 4-CPU/16-GiB executor, `max-jobs = 2` and `cores = 4` preserved parallel compilation. Those are example capacity choices. `cores = 0` is also valid. QEMU's vCPUs and memory are controlled by the test definition, independently of Nix's `cores` hint.

The runtime evaluates locally, copies derivations through `ssh-ng`, and starts a Nix client on the executor against `--store daemon`. Its per-invocation `--builders ''` keeps that build on the selected executor. Per-build timeout and failed-directory retention are likewise passed to that remote client. See the [Nix configuration reference](https://nix.dev/manual/nix/2.35/command-ref/conf-file.html).

## KVM access inside the sandbox

Confirm that `/dev/kvm` exists and that the hypervisor exposes hardware acceleration. On Intel Linux, `kvm_intel` provides the device; on AMD, use `kvm_amd`. The SSH account needs device access for direct `run/debug`. For a normal Debian account, add that account to `kvm`, then reconnect:

```sh
sudo usermod -aG kvm TEST_ACCOUNT
```

Nix's sandbox build identity is a separate boundary. Inspect the actual `build-users-group` and its users' primary GID, for example:

```sh
nix config show build-users-group
getent group nixbld
id nixbld1
getfacl /dev/kvm
```

Use the names from this installation. The sandbox may retain the primary build group while dropping supplementary groups: adding build users to `kvm` alone did not give the tested sandbox access. With the confirmed build group `nixbld`, the following Debian udev rule preserves access after device recreation and reboot. Put it in `/etc/udev/rules.d/70-nix-kvm.rules`:

```udev
KERNEL=="kvm", SUBSYSTEM=="misc", RUN+="/usr/bin/setfacl -m g:nixbld:rw /dev/kvm"
```

Apply and inspect it:

```sh
sudo udevadm control --reload-rules
sudo udevadm trigger --action=change --subsystem-match=misc --sysname-match=kvm
getfacl /dev/kvm
```

If the installation requires an explicit sandbox device mapping, add `extra-sandbox-paths = /dev/kvm` to its managed Nix configuration. Confirm device access with a fresh sandbox VM test after reboot. A device listing, SSH-user access or declared `kvm` feature alone does not demonstrate this boundary.

To retire this ACL setup, remove its task-owned udev rule, run `sudo setfacl -x g:nixbld /dev/kvm`, reload udev rules and verify the resulting access. Preserve other administrators' ACL entries and rules.

## SSH connection inputs

The runtime resolves OpenSSH's effective `HostName`, including aliases and DNS. RFC1918, loopback, link-local and IPv6 unique-local targets accept new or changed fingerprints without a prompt or persistent known-host entry. Public or unresolved targets use strict host checking with the selected SSH configuration/known-hosts file. Account authentication is the same in both cases.

Keep real account credentials and machine configuration in local files outside tracked source. Container clients need these files readable in their own filesystem. A Docker bind-mount source belongs to the Docker daemon's machine, which may differ from the desktop; follow the [bind-mount documentation](https://docs.docker.com/engine/storage/bind-mounts/).

## Signed content transfer

If a copied store path is refused for missing signatures, identify its source and compare the local path's `narHash` with the same path in an already trusted cache:

```sh
nix path-info --json /nix/store/STORE_PATH
nix path-info --json --store https://cache.nixos.org /nix/store/STORE_PATH
```

Inspect `narHash` and `signatures`, then let the executor fetch the matching content using its existing trusted cache:

```sh
nix copy --from https://cache.nixos.org --to daemon /nix/store/STORE_PATH
```

Run the last command on the executor, then retry the test transfer. The runtime already requests destination substitution. If no approved cache has matching signed content, build the producing derivation on the executor or resolve the source/cache discrepancy. Keep signature verification and the established trust policy in place.

## Optional Intel VirtualBox mitigation

Use this section for an executor showing the nested-KVM failures described in [the CPU 0 module](../assets/qemu-cpu0.nix). The module's comments record the observed versions, symptoms and candidate reproduction workload. The upstream cause is unresolved; this is an opt-in mitigation pending a VirtualBox/Linux KVM/QEMU fix.

Keep multiple CPUs on the outer VM for compilation. The tested profile used nested VT-x, nested paging, the KVM paravirtualization provider and VirtualBox VPID enabled. With that VM shut down, the corresponding [VirtualBox 7.2 options](https://docs.oracle.com/en/virtualization/virtualbox/7.2/user/vboxmanage.html) are:

```sh
VBoxManage modifyvm TEST_VM --nested-hw-virt on --nested-paging on --paravirt-provider kvm --x86-vtx-vpid on
```

The paravirtualization provider exposes interfaces to the Linux executor; it does not install KVM or pin QEMU. Large pages and virtio networking can be evaluated separately against the workload; the earlier comparison did not establish a VM-test speedup from those changes.

On the Intel Debian executor, put this separate guest-kernel setting in `/etc/modprobe.d/nixos-vm-tests.conf`:

```conf
options kvm_intel vpid=0
```

Regenerate the running kernel's initramfs and reboot after active tests finish:

```sh
sudo update-initramfs -u -k "$(uname -r)"
sudo reboot
```

After reconnecting, `/sys/module/kvm_intel/parameters/vpid` should read `N`; `ept` and `unrestricted_guest` remain `Y`. This guest KVM setting is distinct from VirtualBox VPID. To revert it, remove that task-owned setting, regenerate the initramfs and reboot.

Copy the single `qemu-cpu0.nix` asset into the project and import it at the test level:

```nix
pkgs.testers.runNixOSTest {
  name = "my-test";
  imports = [ ./qemu-cpu0.nix ];
  nodes.machine = { };
  testScript = ''
    machine.start()
    machine.wait_for_unit("multi-user.target")
  '';
}
```

It uses Nix's `util-linux` `taskset` to pin QEMU and all its threads to executor CPU 0. Each node uses one vCPU; a test that explicitly needs SMP requires a suitable executor. Builders, drivers and independently launched virtiofsd retain their normal CPU availability. For a direct QEMU invocation, the equivalent prefix is `taskset --cpu-list 0 /path/to/qemu-system-x86_64 -enable-kvm -smp 1 ...`.

## Acceptance and snapshot

1. Confirm the daemon sandbox policy, then use `check` with the project's test attribute.
2. Give the smoke test a fresh name, force KVM, and run `build`. Retain QEMU startup, guest boot and assertion logs as execution evidence.
3. Exercise the authentication and direct driver modes used by the project. Confirm that interruption releases this run's QEMU processes and preserves diagnostic logs.
4. Reboot and repeat a fresh sandbox test to verify persistent device access and kernel settings. Inspect QEMU thread affinity when using the optional module, alongside the unrestricted builder/driver affinity.
5. Shut down cleanly, then save the prepared snapshot. Repeat fresh acceptance after relevant kernel, Nix, test-framework or hypervisor upgrades.

Useful results and failed work directories remain available for investigation. Archive needed artifacts before administrator GC, and retain any state whose cleanup has not been confirmed.
