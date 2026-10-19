# Executing remote VM tests

Use the consuming project's locked nixpkgs, test definition and authorized SSH executor. For manual executor setup, use [the preparation guide](preparation.md). The runtime workflow uses an already prepared Linux machine.

## Entrypoint and connection

Copy the skill's Python template to `scripts/nix-vm-test.py` when a project needs an entrypoint. The client needs Python 3.11+, OpenSSH, Nix with `nix-command` and `flakes`, and `sshpass` for passwords. The executor needs Python 3.11+ and multi-user Nix. Run the wrapper through the project environment as a normal client process, outside a Nix build sandbox:

```sh
nix develop --command python3 scripts/nix-vm-test.py check \
  --host runner@192.168.1.20 --password-file /path/to/password

nix develop --command python3 scripts/nix-vm-test.py build \
  '.#checks.x86_64-linux.vm-smoke' \
  --host test-vm --identity-file /path/to/id_ed25519 \
  --build-timeout 600 --log-dir tmp/vm-tests
```

Supply exactly one authentication input:

| Argument               | Authentication                                               |
| ---------------------- | ------------------------------------------------------------ |
| `--password TEXT`      | Password, passed to `sshpass` through its child environment. |
| `--password-file FILE` | First line of a readable password file, via `sshpass -f`.    |
| `--identity-file FILE` | Readable private key without a passphrase.                   |

The wrapper uses these inputs directly, independently of `ssh-agent`. Keep credentials and actual machine configuration outside tracked source. Credential files remain owned by the caller.

`--host` accepts an SSH alias or `[user@]hostname`; use an alias for IPv6. `--port` overrides SSH configuration. `--ssh-config FILE` has OpenSSH's `-F` semantics. The wrapper resolves the effective `HostName`: RFC1918, loopback, link-local and IPv6 unique-local addresses accept any fingerprint, including a replacement after snapshot restore. These connections use no persistent host-key database. Public or unresolved destinations use strict checking and `--known-hosts FILE` or the configured database.

Each invocation opens one SSH master and reuses its private control socket for probes, transfer and execution. `NIX_SSHOPTS` is set for that invocation to use the same connection; put additional settings in the selected SSH config. Child commands depend on this master. The wrapper closes its own master on completion and retains its short temporary socket directory if cleanup fails.

## Modes and build placement

```text
nix-vm-test.py MODE [FLAKE_TEST] --host TARGET [options]
```

`FLAKE_TEST` identifies a complete flake test attribute without output selectors. It is required except for `check`; adding it to `check` also validates the test configuration.

| Mode      | Behavior                                                                                                         |
| --------- | ---------------------------------------------------------------------------------------------------------------- |
| `check`   | Probe connection, tools and devices; optionally evaluate test metadata.                                          |
| `build`   | Build the test on the selected executor; a successful cached result may be reused.                               |
| `rebuild` | Rebuild and compare with an existing output. Nondeterministic logs can cause a mismatch even if assertions pass. |
| `run`     | Build `.driver`, then execute it once as the SSH user.                                                           |
| `debug`   | Build `.driver`, then execute it interactively with a terminal.                                                  |

All build modes use one flow: evaluate against the local `auto` store with `--no-update-lock-file`; copy the derivation closure with `nix copy --derivation --substitute-on-destination --to ssh-ng://…`; then run a Nix client on the executor against `--store daemon --builders ''`. Outputs remain in the remote store. This makes per-call build timeout and failed-directory retention effective at the daemon that performs the build, while keeping placement local to the selected executor.

The executor's `max-jobs` and `cores` control compilation. Local overrides on an `ssh-ng` connection are not forwarded by [Nix's SSH store implementation](https://github.com/NixOS/nix/blob/2.35.3/src/libstore/ssh-store.cc). Test vCPUs and memory belong to the NixOS test definition. The optional CPU 0 module is a specific nested-VirtualBox mitigation described in the preparation guide.

`run/debug` execute outside the build sandbox and require SSH-user KVM access. `debug` needs terminal stdin/stdout; `start_all()` and `test_script()` are available in the driver's Python environment. Using `.driver` preserves the ordinary test configuration.

## Deadlines and logs

| Option                    | Meaning                                                                                                                                      |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `--timeout SECONDS`       | Whole invocation deadline, including connection, evaluation, transfer and execution. Omitted: no extra overall deadline.                     |
| `--build-timeout SECONDS` | Remote Nix timeout per builder. Omitted: executor policy. This does not time a direct driver run.                                            |
| `--keep-failed`           | Retain failed Nix build directories; Nix reports their remote paths. Omitted: text logs are retained, failed build trees are removed by Nix. |
| `--log-dir DIRECTORY`     | Parent for a new private local log directory. Omitted: system temporary directory.                                                           |

Text output streams live and is retained in `run.log`. Standard NixOS test output includes service journal messages forwarded to the serial console; collect project-specific file logs from the test when those matter. Full build trees are useful selectively, especially for disks and detailed artifacts.

Each remote build or driver invocation creates a private `~/nix-vm-test.ID/` with `run.log` and `state.json`. Direct modes also retain `log/`, `driver.xml` and `tmp/`. Both `TMPDIR` and `XDG_RUNTIME_DIR` point to this run's `tmp/`, so the driver's disks and sockets stay with its evidence. Interactive console output is visible through SSH; the driver also writes its structured log. The driver has a GC root for its session; confirmed cleanup releases that root while retaining evidence. Build results need their own retention root or timely retrieval before administrator GC.

Successful `build/rebuild` writes remote output paths to stdout; diagnostics and Nix logs use stderr. `check` writes preliminary success to stdout. Direct modes forward driver output. Exit status is 0 for success, 2 for CLI errors, 1 for execution failure (including a Nix builder timeout), 124 for the wrapper's deadline, and 130 for interruption. Cleanup failure preserves an earlier failure status, or returns 1 when it is the only failure.

## Cancellation and recovery

The wrapper gives its command process group 30 seconds to terminate, then sends SIGKILL to remaining members and verifies for up to 5 seconds. This covers the direct test driver's QEMU children as well as the driver. Cleanup time is additional to the requested execution deadline. Interactive execution gives the driver its own foreground process group.

Nix owns sandbox workers: stopping the remote Nix client requests cancellation through the daemon. The wrapper records and checks its client process group; the daemon's worker teardown remains Nix's responsibility. It leaves the shared daemon running. After a failed build, use the Nix log and retained build path to inspect any worker cleanup problem.

On interruption or connection failure, the client attempts a bounded cleanup/status request through the existing SSH master. The remote supervisor's recorded boot/process identity ties cancellation to this invocation. If the connection is unavailable or cleanup cannot finish, it reports **unconfirmed cleanup**, the remote directory and a local `remote.json` recovery record. Reconnect and inspect that run's state and processes before removing its GC root or work directory. A disconnected client alone does not establish that remote QEMU stopped.

Retrieve selected artifacts through SSH/SCP into an ignored local directory. Keep the same target, port and applicable host-key policy; the wrapper's control socket closes at exit. Signature-transfer failures have a [trusted-cache recovery procedure](preparation.md#signed-content-transfer).

## Preflight and acceptance

Preflight checks Linux, Python, independent Nix tool discovery, daemon reachability through the selected SSH store, `/dev/kvm`, and direct-mode user access. Selected SSH-user Nix settings are diagnostic. The actual daemon policy needs administrator confirmation; device presence does not prove sandbox access or working nested virtualization.

Require the test's standard `runNixOSTest` passthru to expose `config.qemu.forceAccel = true` and QEMU nodes. A project wrapper that hides it needs to preserve that metadata or verify the same requirement through its existing entrypoint. The execution system is reported; the daemon decides build compatibility.

For initial acceptance, use the skill's smoke module with the project's pinned nixpkgs and a fresh test name. If flake checks are already adopted, wire it through that interface; otherwise use an isolated fixture with a Nix-generated lockfile. Retain evidence of actual QEMU startup, guest boot and passing assertions, alongside confirmed daemon sandbox policy. Repeat after relevant executor or framework changes.

| Failure                                                  | Response                                                                                                         |
| -------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Missing client tool                                      | Supply it through the project environment.                                                                       |
| Public target identity or account authentication failure | Resolve the connection input. Private target fingerprints are accepted automatically.                            |
| Missing remote tools, daemon access or KVM               | Report the executor deficiency; do not provision it.                                                             |
| Sandbox, architecture or accelerator failure             | Retain evidence and resolve the executor/test compatibility.                                                     |
| Signature transfer failure                               | Compare path provenance, NarHash and trusted-cache signatures, then substitute matching content on the executor. |
| SSH loss or incomplete cleanup                           | Inspect the recorded run after connection recovery.                                                              |

The [Nix copy manual](https://nix.dev/manual/nix/2.35/command-ref/new-cli/nix3-copy.html), [build manual](https://nix.dev/manual/nix/2.35/command-ref/new-cli/nix3-build.html), and [OpenSSH configuration manual](https://man.openbsd.org/ssh_config) describe the underlying interfaces.
