# Preparing a remote NixOS VM test executor

This is an administrator reference, outside the distributed Agent Rules Kit. It is not loaded or routed by the runtime skill. The skill uses a prepared executor and reports missing prerequisites; it does not perform the preparation described here.

## Execution roles

| Role                                 | Responsibility                                                                                         |
| ------------------------------------ | ------------------------------------------------------------------------------------------------------ |
| Client                               | Evaluates the project and submits commands over SSH; it may run inside or outside a container.         |
| Remote executor                      | Runs a multi-user Nix daemon and QEMU on Linux with working KVM; it may be a physical machine or a VM. |
| Virtualization host, when applicable | Hosts the executor VM and provides nested hardware virtualization.                                     |
| Test guests                          | NixOS machines created by the project's VM test; their configuration comes from the test definition.   |

The executor may use NixOS or another Linux distribution. No particular hypervisor, account, address, or SSH port is required. Nested virtualization matters when the executor itself is a VM, regardless of how the client runs. The executor must support the driver's execution platform and the guests' hardware-accelerated architecture; the client's architecture can differ.

The client needs Nix with `nix-command` and `flakes`, Python 3, OpenSSH, and `sshpass` for password authentication. Declare these in its project development shell. Remote execution needs no KVM device or privileged container on the client. The test's driver, QEMU, and guest systems come from the project's locked nixpkgs.

## Nix daemon baseline

Manage Nix through the executor's established system configuration. This workflow requires a reachable multi-user daemon, sandboxing without sandbox fallback, and usable KVM for its builds. The following is a configuration example; merge the relevant settings into the managed installation:

```ini
sandbox = true
sandbox-fallback = false
extra-system-features = kvm nixos-test
```

On NixOS, set the corresponding `nix.settings` values. Ensure the running daemon has applied the intended configuration. `nix config show` runs as the SSH user and may report different settings; the wrapper treats them as diagnostics and attempts execution even if they differ or the query is unavailable. Confirm the daemon's actual sandbox policy separately. A successful guest assertion does not establish that sandboxing was enabled.

The `ssh-ng` store does not forward client configuration overrides to the daemon. Execution placement follows the daemon's `builders` configuration. When tests must execute on the selected machine, confirm that its daemon does not delegate elsewhere. A `builders` file reference alone does not establish whether any other builders are configured.

The login account must be allowed to use the multi-user daemon. Ordinary test submission does not require adding it to `trusted-users` or giving it sudo. Keep signature verification enabled; fetch binary dependencies from administrator-approved caches or build them on the executor. Do not fix unsigned binary transfer failures by expanding trust. See the [Nix configuration reference](https://nix.dev/manual/nix/2.34/command-ref/conf-file.html) and [SSH store reference](https://nix.dev/manual/nix/2.34/store/types/experimental-ssh-store).

## KVM and resources

`/dev/kvm` must be present and usable by the actual Nix build identities inside their sandboxes. If the installation needs an explicit sandbox device mapping, configure it administratively, for example with `extra-sandbox-paths = /dev/kvm`. The SSH user also needs read/write device access for direct `run/debug`, which execute outside the build sandbox. Its permissions do not determine build-user permissions. Device ownership, groups, ACLs, and udev policy belong to the executor administrator.

Choose daemon concurrency for the executor's capacity and other workloads. `max-jobs = 1` and `cores = 2` are example starting values, not wrapper requirements. `cores = 0` is valid Nix configuration for automatic CPU selection. Allocate guest memory/vCPUs and test timeouts in the test definition; Nix's `cores` setting is a build hint and does not cap total QEMU resources. See the [Nix configuration reference](https://nix.dev/manual/nix/2.34/command-ref/conf-file.html#conf-cores).

Device listings and declared `kvm` features do not prove that hardware virtualization works. Acceptance needs a fresh test that forces hardware acceleration and actually starts its guests.

## SSH and container inputs

Prepare a normal SSH account using either an account password or a public key corresponding to an unencrypted private key file available to the client. The runtime supports a literal password, a password file, or a private key path without interactive prompts. No SSH agent is required.

Configure the destination, port, and verified host identity in the client's SSH configuration/known-hosts database, or provide the wrapper's explicit file arguments. Keep authentication material and actual machine details outside the tracked flake source. There is no required credential directory.

If the client runs in Docker or a devcontainer, make the chosen credential/configuration files readable there. A read-only mount does not fix mismatched ownership. Bind-mount sources refer to the Docker daemon's machine, which may differ from the desktop running an editor. Maintain these mounts in the environment's own configuration. Clients outside containers can use files directly. See the [Docker bind-mount documentation](https://docs.docker.com/engine/storage/bind-mounts/).

## Acceptance and maintenance

1. Run the wrapper's `check` mode from the actual client environment, including the intended test attribute to report its execution system. Resolve failed connection/device checks and review configuration diagnostics.
2. Verify the selected test forces `qemu.forceAccel = true`. Use the distributed smoke module with the project's locked nixpkgs and a new test name.
3. Run `build` and retain fresh logs showing QEMU startup, guest boot, and successful assertions. A cache hit is not acceptance evidence. Confirm the daemon's sandbox policy separately from its client's visible configuration.
4. Verify password and private-key authentication as used by the deployment, followed by `run`, `debug`, interruption, and artifact retrieval.
5. Repeat fresh acceptance after relevant Nix, kernel, virtualization, or test-framework upgrades.

Avoid GC during test execution and artifact retrieval. Direct runs retain private work directories and logs; their driver root is temporary. Archive needed results or create administrator-owned retention roots before GC. After interrupted connections, inspect reported directories and task-owned processes before cleanup. No automatic deletion of other runs or executor-wide GC belongs in the client workflow.
