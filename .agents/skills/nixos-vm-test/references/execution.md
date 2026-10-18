# Executing remote VM tests

Use the consuming project's lockfile, test definitions, and authorized executor. This workflow expects a prepared Linux machine; it does not install Nix, change KVM permissions, or configure the virtualization host.

## Entrypoint and connection inputs

Copy the skill's Python template to `scripts/nix-vm-test.py` when the project needs an entrypoint. Invoke it through the project's development shell. The client needs Python 3, Nix with `nix-command` and `flakes`, OpenSSH, and `sshpass` for password authentication. It can run inside or outside a container. Run the wrapper as a normal client process, outside a Nix build sandbox.

```sh
nix develop --command python3 scripts/nix-vm-test.py check \
  --host runner@example.test \
  --password-file /path/to/password

nix develop --command python3 scripts/nix-vm-test.py build \
  '.#checks.x86_64-linux.vm-smoke' \
  --host test-vm --identity-file /path/to/id_ed25519
```

Replace the example target, test attribute, and credential paths with project inputs. Paths refer to the environment executing the client command; no mount convention is required. Supply exactly one of:

| Argument               | Authentication                                                                      |
| ---------------------- | ----------------------------------------------------------------------------------- |
| `--password TEXT`      | Non-interactive account password, passed to `sshpass` through its child environment |
| `--password-file FILE` | Non-interactive password from the file's first line, using `sshpass -f`             |
| `--identity-file FILE` | Readable private key without a passphrase                                           |

The wrapper does not use `ssh-agent`. It accepts an SSH alias or `[user@]hostname`; use an alias for IPv6. `--port` overrides the SSH configuration, otherwise OpenSSH chooses its configured/default port. `--ssh-config FILE` has OpenSSH's `-F` semantics, including its handling of the system configuration. `--known-hosts FILE` selects a user host-key database; otherwise the existing SSH configuration applies. Strict checking requires a previously verified host identity and never asks for confirmation.

Actual passwords, keys, and private machine configuration stay outside the tracked flake source. Reuse an ignored task workspace for temporary password material when needed. User-supplied credential files are inputs and are never deleted by the wrapper. Connection options supplied through ambient `NIX_SSHOPTS` are replaced for this invocation so direct SSH and Nix use the same connection; put required connection settings in the selected SSH config instead.

Each invocation opens one SSH master and reuses its private control socket for probes, Nix, and driver execution. Child connections cannot establish a new network connection if that socket disappears. The wrapper closes its own master on completion or handled interruption. It uses a short system-temporary directory to fit Unix socket path limits; a failed close retains the directory and reports it for recovery.

## Modes and results

```text
nix-vm-test.py MODE [FLAKE_TEST] --host TARGET [connection options]
```

`FLAKE_TEST` is required except for `check`. It must identify one complete flake test attribute, without output selectors. Adding it to `check` reports the test's execution system and checks forced acceleration and absence of nspawn containers.

| Mode      | Meaning                                                                               |
| --------- | ------------------------------------------------------------------------------------- |
| `check`   | Run preliminary environment checks without building a test                            |
| `build`   | Build the test through the remote daemon; an existing successful result may be reused |
| `rebuild` | Use `nix build --rebuild`, including comparison with the previous output              |
| `run`     | Build `.driver` remotely, then execute the test once as the SSH user                  |
| `debug`   | Build the same `.driver`, then run it with `--interactive` and a remote terminal      |

`debug` requires terminal stdin/stdout even though authentication is non-interactive. Using `.driver` rather than `.driverInteractive` keeps the ordinary test configuration. Both direct driver modes run outside the Nix build sandbox; they do not certify a sandbox build. In the interactive Python environment, `start_all()` starts nodes and `test_script()` runs the assertions.

The wrapper uses `ssh-ng://` with an explicit remote `nix-daemon`, `remote-store=daemon`, and one protocol connection through its existing SSH master. Evaluation stays in the local `auto` store. `--no-link` leaves results in the remote store and `--no-update-lock-file` prevents dependency updates.

The remote daemon owns `builders`, `max-jobs`, and `cores`: [Nix's SSH store implementation](https://github.com/NixOS/nix/blob/2.34.9/src/libstore/ssh-store.cc) does not forward local configuration overrides. These settings belong to the executor's managed configuration. If execution must stay on the selected machine, confirm that its daemon does not delegate to other builders. The wrapper reports SSH-user settings as diagnostics; it does not reject valid forms such as `cores = 0` or a `builders` file reference. Test VM memory/vCPU limits belong to the test definition and are not capped by `cores`.

Successful `build/rebuild` writes the remote output path(s) to stdout; diagnostics and Nix build logs use stderr. `check` writes a preliminary-success message to stdout. `run/debug` inherit driver output and report their remote work directory on stderr. Exit codes are 0 for success, 2 for invalid CLI inputs, 1 for runtime failure, and 130 for handled interruption. A command's original failure status is included in diagnostics. A later cleanup failure is reported separately and preserves the main failure/interruption status; cleanup failure alone returns 1 and retains recovery state.

## Preflight and acceptance

Preflight checks SSH, Linux, the remote `nix-daemon` executable, and KVM device presence. It queries the daemon through the exact `ssh-ng` store used for subsequent builds. Tools are discovered independently through the SSH user's PATH, with standard NixOS and multi-user profile paths as fallbacks. `run/debug` additionally require `nix-store` and SSH-user device access; the latter is not required for `build/rebuild`.

When remote `nix` is available, `nix config show` reports selected SSH-user settings for diagnosis. They can differ from the running daemon, so differences or an unavailable configuration query do not block execution. The test's execution system and the executor's kernel architecture are reported; the daemon determines build compatibility. Device existence does not establish build-user access, sandbox device mapping, or working hardware virtualization. Administrators confirm daemon sandbox policy separately, and a fresh test demonstrates VM execution. Neither preflight success nor a guest assertion alone proves that sandboxing is enabled.

Before executing a selected test, inspect its definition and require:

```nix
qemu.forceAccel = true;
```

The wrapper evaluates the standard `runNixOSTest` passthru `config.qemu.forceAccel` and rejects a false or missing value, as well as tests with nspawn containers. It does not rewrite test definitions. If a project's wrapper hides this configuration, preserve the passthru or adapt its existing entrypoint to verify the same requirement. For a project that has adopted flake checks, a smoke module can be connected with `pkgs.testers.runNixOSTest (import ./vm-smoke.nix)`. Otherwise use an isolated synthetic fixture with a lockfile generated by Nix from the project's pinned input; do not silently add a new product interface or bypass Git source filtering.

For initial acceptance, assign the smoke test a new `name` and run `build`. Retain logs showing QEMU startup, the guest reaching its target, and a passing assertion. A cached path alone does not establish acceptance. `--rebuild` also compares outputs: nondeterministic logs can cause an output mismatch even when assertions pass. Use `run` for routine repeated execution rather than interpreting `rebuild` as just a repeat button.

## Logs and failure handling

Direct driver runs create a private `~/nix-vm-test.XXXXXXXX` directory remotely. `log/`, `driver.xml`, and `tmp/` remain for inspection. A `driver` GC root protects the closure during the session; normal completion and handled remote signals remove that root while retaining logs. Connection loss, process kills, or host failure can leave a root or processes behind. Report the directory and inspect it before removing task-owned state; do not perform broad cleanup or GC.

Build outputs remain in the remote store. Retrieve selected artifacts as ordinary files with `scp` using the same target, port, authentication, and host-key configuration, into an ignored local artifact directory. A password connection can be established separately with `sshpass`; the wrapper's socket is closed when it exits. Do not import unsigned local binary closures or change Nix trust settings to work around transfer failures. Keep needed outputs rooted or retrieve them before administrator GC.

| Failure                                             | Response                                                                |
| --------------------------------------------------- | ----------------------------------------------------------------------- |
| Missing local client tool                           | Declare it in the appropriate project shell, then retry                 |
| Unknown/changed host key or failed authentication   | Report the connection prerequisite; do not disable verification         |
| Missing remote Nix, daemon access, or KVM           | Report the executor deficiency; do not provision it                     |
| Daemon or driver rejects the execution architecture | Select a compatible authorized executor/test system                     |
| Sandbox or accelerator launch failure               | Retain logs and report failure; do not switch to local execution or TCG |
| SSH connection loss                                 | Fail the invocation; inspect any reported remote work directory         |

The [Nix SSH store manual](https://nix.dev/manual/nix/2.34/store/types/experimental-ssh-store), [build semantics](https://nix.dev/manual/nix/2.34/command-ref/new-cli/nix3-build), and [OpenSSH configuration manual](https://man.openbsd.org/ssh_config) describe the underlying interfaces. Revalidate a fresh smoke test after relevant Nix, test-framework, kernel, or executor changes; `ssh-ng` is an experimental Nix interface.
