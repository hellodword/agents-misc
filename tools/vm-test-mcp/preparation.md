# Host service preparation

The host administrator performs these steps. The Agent container calls the HTTP service; it does not need a host login or a host shell endpoint.

## Account, directories and configuration

Both processes can run as the current VirtualBox VM owner. Use Python 3.11+, uv, VBoxManage for Windows targets, and the host Nix CLI/daemon for Nix tasks. The worker user must be an ordinary Nix client outside `trusted-users`, including group entries such as `@wheel`. The daemon runs builders under its own build identities; separating the controller and worker processes provides scheduling and recovery, not a separate filesystem identity when both use the same user.

Deploy the component to a stable host-owned directory. Copy [config.example.toml](config.example.toml) to its ignored `config.toml`, mode `0600`, and enable the required backends. Both processes read that same file. Keep literal guest credentials in this host-local file, outside the Nix store.

For the same-user setup, use separate state directories and put the worker socket inside its directory:

```toml
[service]
state_dir = "/home/VM_OWNER/.local/state/vm-test-controller"

[nix]
state_dir = "/home/VM_OWNER/.local/state/vm-test-nix"
socket = "/home/VM_OWNER/.local/state/vm-test-nix/worker.sock"
```

The processes create private state directories. Existing directories must belong to the corresponding process user with mode `0700`; use real paths without symlink components. If startup reports a permission mismatch, inspect that specific directory and set its owner/mode while the services are stopped. Existing task records and artifacts remain usable.

A dedicated worker account is also supported. In that setup, give the controller and worker a dedicated socket group, put the socket in a worker-owned `0750` runtime directory, and make the common configuration readable by both accounts (for example `root:vm-test-bridge`, `0640`). Keep each state directory private to its owner. Both accounts can read the guest credentials in the shared configuration.

For a container client, set `service.host` to the host bridge IP. The public origin defaults to that address and `service.port`; the worker's cache origin inherits it. Override `service.public_url` or `nix.cache_base_url` when the client and worker use different routes to the same controller. Both routes must reach its HTTP listener.

## Start and supervise

First start the worker and controller using the commands in [README.md](README.md#prepare-and-start). Requests default to headless. To support explicitly requested `gui=true` starts, run the controller in the VM owner's graphical session with the required display/session variables. Configuration supplies the startup environment and capability policy. Prewarm the uv environment with the controller's `--help` before unattended startup.

Prepare and register optional controls/vision interpreters using the [Windows baseline guide](../../.agents/skills/windows-vm-test/references/preparation.md). Its resources keep automation dependencies separate from the guest supervisor and document GUI autoresize/resolution settings, normal shutdown and snapshot acceptance. Baseline installation is separate from running project-owned tests inside a disposable task.

Once verified, user systemd units can supervise both processes. For example, create `~/.config/systemd/user/vm-test-nix.service`:

```ini
[Unit]
Description=Nix worker for VM tests

[Service]
WorkingDirectory=/path/to/vm-test-mcp
ExecStart=/path/to/python3 /path/to/vm-test-mcp/nix_worker.py --config config.toml
Restart=on-failure
UMask=0077

[Install]
WantedBy=default.target
```

For `~/.config/systemd/user/vm-test-controller.service`:

```ini
[Unit]
Description=VM test HTTP MCP controller
After=graphical-session.target vm-test-nix.service
PartOf=graphical-session.target

[Service]
WorkingDirectory=/path/to/vm-test-mcp
ExecStart=/path/to/uv run --no-project /path/to/vm-test-mcp/server.py --config config.toml
Restart=on-failure
UMask=0077

[Install]
WantedBy=graphical-session.target
```

Replace the executable/component paths with their actual host locations and enable the units with `systemctl --user`. Both processes read configuration at startup; restart them after current tasks finish when changing shared settings. The controller's user manager needs the working graphical session environment.

## Nix daemon isolation

The worker imports submitted derivations and asks the daemon to build them; derivations can contain arbitrary builder code. Configure the daemon for strict sandboxing and dedicated build identities. For NixOS, retain its normal build-user setup and merge the following settings into the host configuration:

```nix
nix.settings = {
  sandbox = true;
  sandbox-fallback = false;
  require-sigs = true;
  extra-system-features = [ "kvm" "nixos-test" ];
};
```

Review the effective host configuration for `trusted-users`, build identities, `sandbox-paths` and `extra-sandbox-paths`. Expose only the resources needed by tests, such as `/dev/kvm`; user homes, credentials, daemon sockets and container-engine sockets belong outside the build sandbox. Keep private data outside `/nix/store`, whose contents are normally readable by local users. Follow the linked executor guide below for builder access to KVM and cache signing.

Apply changes through the host's normal NixOS deployment so the running daemon loads them. `nix config show` describes the client's configuration and is supporting evidence, not proof of the daemon's active sandbox. Fixed-output derivations retain their normal network access. See the [Nix sandbox and configuration reference](https://nix.dev/manual/nix/2.34/command-ref/conf-file.html#conf-sandbox).

After setup, run this small acceptance from a Bash shell on the host as the worker user. It uses the host's existing Nix-store Bash, creates a readable synthetic marker outside the store, and gives each attempt a fresh derivation identity. The marker is passed as a string so Nix does not import it into the build inputs.

```bash
VM_TEST_PROBE_DIR=$(mktemp -d /tmp/vm-test-isolation.XXXXXXXX)
VM_TEST_PROBE_SHELL=$(readlink -f "$(command -v bash)")
VM_TEST_PROBE_UID=$(id -u)
export VM_TEST_PROBE_DIR VM_TEST_PROBE_SHELL VM_TEST_PROBE_UID
chmod 0755 "$VM_TEST_PROBE_DIR"
printf 'synthetic host marker\n' > "$VM_TEST_PROBE_DIR/marker"
chmod 0644 "$VM_TEST_PROBE_DIR/marker"
cat > "$VM_TEST_PROBE_DIR/probe.nix" <<'NIX'
derivation {
  name = builtins.baseNameOf (builtins.getEnv "VM_TEST_PROBE_DIR");
  system = builtins.currentSystem;
  builder = builtins.storePath (builtins.getEnv "VM_TEST_PROBE_SHELL");
  hostMarker = builtins.getEnv "VM_TEST_PROBE_DIR" + "/marker";
  workerUid = builtins.getEnv "VM_TEST_PROBE_UID";
  __noChroot = builtins.getEnv "VM_TEST_NO_CHROOT" == "1";
  args = [ "-c" ''
    if [[ -e "$hostMarker" ]]; then
      printf 'host marker is visible\n' >&2
      exit 1
    fi
    if (( EUID == 0 || EUID == workerUid )); then
      printf 'expected a separate build identity\n' >&2
      exit 1
    fi
    printf 'isolated build identity: %s\n' "$EUID" > "$out"
  '' ];
}
NIX
VM_TEST_NO_CHROOT=0 nix build --store daemon --impure --no-link -L --file "$VM_TEST_PROBE_DIR/probe.nix"
VM_TEST_NO_CHROOT=1 nix build --store daemon --impure --no-link -L --file "$VM_TEST_PROBE_DIR/probe.nix"
```

The first build must succeed; the second must be rejected specifically because strict sandboxing disallows `__noChroot`. An unrelated evaluation, permission or download failure leaves acceptance incomplete. Retain the results with local task evidence, then remove only this probe's temporary directory when finished. This establishes those isolation properties for the tested daemon; pair it with the configuration review and actual multi-node KVM acceptance.

## Executor and baseline

Follow the [Nix executor guide](../../.agents/skills/nixos-vm-test/references/preparation.md) for builder KVM access, cache trust and direct multi-node acceptance. Configure daemon limits according to the host's total workload; controller service limits do not constrain daemon-owned builds.

Follow the [Windows baseline guide](../../.agents/skills/windows-vm-test/references/preparation.md) for installation media, Guest Additions, Python, interactive login, hardware acceleration and the powered-off snapshot. Register only disposable test VMs whose reset boundary is acceptable.

## Native mouse input

Build the Linux amd64 helper against the C bindings from an [official VirtualBox SDK](https://www.virtualbox.org/wiki/Downloads) for native mouse input. Run these commands in the component directory containing `config.toml`. The build locates the SDK within the configured VBoxManage installation. The SDK's generated header and glue retain their own license notices; the glue is compiled from the provided SDK without modification.

```sh
just build-helper
just test-helper
```

If the installation does not include the C SDK, supply its root, containing `bindings/c`, as the optional argument to either command: `just build-helper /path/to/sdk` or `just test-helper /path/to/sdk`.

`service.input_helper` defaults to `build/vbox-input-helper` relative to the configuration file. The controller resolves VBoxManage's executable symlink and looks for `VBoxXPCOMC.so` in that installation's executable and library directories. For a custom layout, set the absolute `service.vbox_library_dir`. These paths are controlled by the host administrator. Run the helper through the controller as the VM owner. The host's VirtualBox hardening configuration remains enabled. Runtime use negotiates the C function table and required object interfaces; a matching product version string alone is not the compatibility criterion. Reported helper failures affect native mouse availability independently of CLI controls.

Prepare a suitable virtual pointing device and verify both a relative move and an absolute click against a screenshot. Test a guest UAC prompt using isolated guest credentials and confirm the resulting application state. The helper addresses the VM's input devices, independently of the host desktop pointer.

For development memory checks, run `python3 build_helper.py SDK_DIR --sanitize --test` in a C compiler environment. These tests use a synthetic C API library; actual host/guest acceptance is separate.

## Webcam input

VirtualBox 7.2.2 and later include webcam passthrough in the base package; consult the installed release's [release notes](https://docs.oracle.com/en/virtualization/virtualbox/7.2/relnotes/ChangeLog.html) for its requirements. The administrator prepares device access and the VM's USB controller.

From the VM owner's host session, enumerate visible cameras:

```sh
VBoxManage list webcams
```

The VM process must have permission to use the chosen camera device. Prepare a USB controller in the Windows baseline and allow camera access for the test application inside Windows. Keep microphone and speaker settings in the baseline; runtime camera attachment uses `controlvm` and does not change the snapshot.

Check the target's effective webcam capability with `targets`. After starting a test task, use `webcams` to choose a device and call `vm_webcam(attach)` for that VM. Confirm it appears in the guest and that the application can receive frames. Call `vm_webcam(detach)` after use, then finish the task and verify poweroff. The attachment result proves the VirtualBox device state; application input needs its own assertion.

## Interrupted Nix work

Read the task state and worker journal, then verify from the host that the affected build's workers have stopped. The worker records task ID, derivation and timestamps under its private state directory. Retained failed-build directories remain available to the administrator.

After verifying cleanup, run reconciliation as the worker identity:

```sh
python3 /path/to/vm-test-mcp/nix_worker.py \
  --config config.toml --reconcile TASK_ID
```

The Unix peer UID must match the worker owner for this operation. The HTTP controller does not expose reconciliation. Read the original task again to confirm its updated state before continuing.

Keep private runtime directories on a filesystem with capacity suitable for input caches, images and logs. Completed evidence expires according to `retention_days`; unresolved cleanup remains retained. Plan periodic administrator cleanup of Nix failed-build directories and normal store GC after useful evidence has been collected.
