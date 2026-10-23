# Preparing a NixOS VM test executor

This manual guide is for the host administrator. Runtime test execution uses an already prepared service; environment setup is a separate authorized task.

## Roles and tools

| Role                | Requirements                                                                          |
| ------------------- | ------------------------------------------------------------------------------------- |
| Agent container     | `vm-test-submit`, Nix with `nix-command` and `flakes`, project inputs and HTTP access |
| Physical Linux host | Multi-user Nix, hardware virtualization, working `/dev/kvm`, sufficient RAM and disk  |
| MCP controller      | Go controller package; the VirtualBox owner account when labs are enabled             |
| Nix worker          | Ordinary account, Go worker package, Nix CLI, daemon socket and private state         |
| Test nodes          | Driver and guest packages supplied by the project's locked nixpkgs                    |

The worker uses the existing host store and daemon. Store contents are readable task inputs. Keep its account outside `trusted-users`; Nix documents trusted users as effectively root-equivalent. The controller reaches the worker through a UID-authenticated Unix socket. Controller and worker may share an account and read one shared YAML file; there is no special configuration-file permission requirement. Their state directories remain separate. When using different accounts, provide socket access through a shared group. The service installation guide owns executable paths, configuration and supervision examples. MCP remains available while the worker is offline.

## Daemon and KVM

On NixOS, merge these settings into the host's existing flake configuration and apply it through the administrator's normal deployment procedure:

```nix
{
  boot.kernelModules = [ "kvm-intel" ]; # Use kvm-amd on AMD systems.
  nix.settings = {
    experimental-features = [ "nix-command" "flakes" ];
    sandbox = true;
    sandbox-fallback = false;
    require-sigs = true;
    extra-system-features = [ "kvm" "nixos-test" ];
  };
}
```

Preserve existing cache and virtualization settings. Confirm the running daemon's actual configuration; a client-side configuration dump alone does not establish daemon policy.

Inspect `/dev/kvm`, the actual Nix build group and its access to the device. Sandbox workers may retain their primary group while dropping supplementary groups. If this installation needs an explicit device mapping or persistent ACL, configure it for the actual build group through the host's managed Nix/udev configuration. Confirm the result after reboot with a fresh sandbox VM test. The low-privilege submission account itself does not need to launch the test nodes.

Choose daemon `max-jobs` and `cores` from available CPU/RAM. They control build scheduling and parallelism hints, not strict per-task resource isolation. Systemd limits on the HTTP service or worker do not constrain separate daemon-owned builders. Limits applied to the shared daemon also affect other host builds. Ordinary Nix builds using KVM must participate in the same administrator scheduling policy as these tests.

The controller serializes its Nix and VirtualBox tasks. Finish manually launched competing virtualization work before switching; the service does not shut down unrelated administrator VMs.

## Cache trust

The service accepts a standard HTTP binary cache scoped to one transfer. Nix verifies imported content using the daemon's existing trust policy. Content-addressed inputs carry their own integrity identity; locally built input-addressed paths may need an administrator-approved signing key or reproduction on the executor.

For a refused path, compare local and trusted-cache metadata:

```sh
nix path-info --json /nix/store/STORE_PATH
nix path-info --json --store https://cache.nixos.org /nix/store/STORE_PATH
```

When NarHash and path match, the administrator can fetch the signed content on the executor:

```sh
nix copy --from https://cache.nixos.org --to daemon /nix/store/STORE_PATH
```

Keep the worker untrusted and signature verification enabled. See the [Nix trust and sandbox settings](https://nix.dev/manual/nix/2.34/command-ref/conf-file.html).

## Acceptance and maintenance

Use the skill's multi-node acceptance module with the consuming project's pinned nixpkgs and a fresh run ID. Retain node startup, CPU-count and network assertions. Then exercise a failing test and cancellation, checking logs, retained evidence and `cleanup.state: complete`.

After interrupted daemon work, inspect the task and its builders from the host. Reconcile only that task after verifying its workers have stopped; the service keeps the resource occupied until then. Failure directories and store garbage collection remain administrator-owned maintenance.

Repeat fresh acceptance after relevant kernel, Nix or test-framework changes. Record actual rendering, decode and encode results separately when a project's node configuration exposes graphics devices; the basic KVM acceptance test does not establish those capabilities.
