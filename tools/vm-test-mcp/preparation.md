# Administrator preparation

This guide prepares the host and baselines. Test execution uses the separate
[runtime guide](README.md). Perform setup as the responsible administrator;
controller requests cannot select host programs, directories or devices.

## Execution environment

| Role                  | Requirements                                                                                                                      |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Development container | `vm-test-submit`, Nix with `nix-command` and `flakes`, the tested project's inputs, and HTTP access to the controller             |
| Physical Linux host   | Hardware virtualization, sufficient RAM and disk, VirtualBox for labs, and multi-user Nix with working `/dev/kvm` for NixOS tests |
| MCP controller        | The service package and the VirtualBox owner account when labs are enabled                                                        |
| Nix worker            | An ordinary account, the worker package, Nix CLI, executor socket access and a separate private state directory                   |
| NixOS test nodes      | Driver and guest packages supplied by the tested project's locked nixpkgs                                                         |

## Accounts and shared configuration

Install `.#vm-test-mcp` and an administrator-selected VirtualBox 7.2 runtime.
The service package supplies `vbox-api-helper` and `xorriso`. `VBoxManage` is
found in PATH by default; its runtime library is found beside the real executable
or under its installation prefix. Absolute overrides are optional.

`api_helper` is a small **host** program using the official VirtualBox C SDK
for native mouse events, display geometry, VM session unlock observations and
scoped Guest Control session release. Keyboard input uses VBoxManage.
Neither needs guest Python. `iso_builder` is **host** xorriso: it writes each
lab's credential/configuration seed ISO, not an OS installation image. The router
OS ISO is a separate `.#vm-test-router` build product.

All three processes read the same [annotated YAML](config.example.yaml), outside the
Nix store. There is no configuration-file permission requirement. Local
`config.yaml` is Git-ignored. The mandatory token authenticates MCP requests.
Omitted or empty `service.hosts` / `service.origins` allow any value; nonempty
lists apply exact matching. Configure HTTP and published-service listeners
separately; `virtualbox.network.source_cidrs` limits only TCP/UDP entry clients. Register
host public/NAT addresses and extra management ranges for egress protection.

Run the controller as the VirtualBox owner. Run the worker as an ordinary account
outside Nix `trusted-users`, including trusted groups. Both processes may use the
same account. Top-level `state_dir` is a root with `controller/` and `nix-worker/`
role directories. Optional `nix.state_dir` overrides the worker root; it can equal
the controller root. Each role has its own database and lock in a real,
owner-matched `0700` directory without symlink components. For separate accounts
sharing a root, prepare parent traversal permissions and each role directory
before startup. Final role directories must not overlap or nest. Set `nix.controller_uid` to the controller
account's UID (`id -u` when using the same account); connections are checked using
`SO_PEERCRED`.

With one account, the socket may reside inside the worker's state directory. If
using different accounts, give them a dedicated socket group and place the socket
outside either private state directory. A worker-owned `0750` directory such as
`/run/vm-test-bridge`, with that group also used by the worker process, lets the
controller access the `0660` socket.

## Windows baseline

The administrator installs and activates Windows, disables Windows Update through
group policy, and installs matching Guest Additions. Use a password-bearing local
administrator account for Guest Control. The runtime verifies the
prepared command service; administrator membership alone is not evidence of an
elevated Guest Control token.

Recommend automatic login for repeated desktop work, and disabling sleep,
screensavers and automatic lock. These are recommendations, not service
prerequisites. Native screenshots/keyboard/mouse also work on the login or secure
desktop. An agent can inspect screenshots and log in/unlock; the MCP server has no
vision model. CLI commands remain usable without an interactive login. Python on
Windows/Linux, and Docker Engine plus Compose on Linux, are useful optional
preinstalls. Do not configure executable paths in YAML: test commands probe PATH
and actual versions/imports, then install missing dependencies in the disposable
node, optionally from uploaded offline packages. No fixed scaling, language or
keyboard layout is required.

Copy `share/vm-test-mcp/guest/vm-test-guest-windows-amd64.exe` into the source guest.
From an **elevated administrator terminal**, run:

```powershell
.\vm-test-guest-windows-amd64.exe prepare
# For a configured custom work_dir instead:
# .\vm-test-guest-windows-amd64.exe prepare --root C:\Users\vmtest\vm-tests
```

This explicit preparation command asks for the current local administrator's
credential, installs the `VMTestGuest` LocalSystem service and the command binary
under `C:\ProgramData\vm-test`, grants the selected account access, writes an
unattend file and invokes Sysprep `/generalize /oobe /shutdown`. The answer file
preserves the current language, region and keyboard settings so clones do not
stop at those OOBE prompts. It also preserves the administrator's autologon choice.
If enabled, the prepared account must be the
autologon account. It does not enable autologon when disabled. Each clone specializes
with a new Windows identity/computer name. The specialize hook marks preparation
complete without waiting for login. See Microsoft's [Sysprep answer-file
reference](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/use-answer-files-with-sysprep?view=windows-11).

Commands default to LocalSystem in the service session. Windows desktop workloads
explicitly request `context="desktop"`; the service requires a logged-in
administrator's elevated token and returns a terminal startup failure when it
cannot obtain one. It does not change the requested execution identity.
Thus project commands have guest administrative privileges. The command supervisor
puts Windows children in a kill-on-close Job Object **before** resuming them. The
controller probes the prepared command service and records its version/hash as
diagnostic metadata. Both `vm-test-guest.exe` and the resident
`vm-test-service.exe` are copies of the single executable supplied above;
`prepare` installs both and registers the service. No separate service download
or installation command is needed. A fresh heartbeat must
identify a live service process with the same hash and command protocol as the
probed executable; replacing just the command binary leaves the node unready.
Preparation records the selected local Guest Control account in the service's
`--control-user` argument. The service and each supervisor resolve its current
local SID after specialization and grant that account process identity-query and
wait rights, preserving the remaining process DACL. This permits filtered Guest
Control tokens to verify creation time and termination without granting process
termination, memory access, or control over unrelated processes. Failed identity
queries remain distinct from a confirmed exit or reused PID.
Guest command protocol 2 reports confirmed process startup, distinct stdin delivery/OS
pipe progress and an OS boot identity used to verify reboot readiness. Re-run
preparation when changing the guest protocol or baseline account. No project dependencies are installed
implicitly by the MCP service.

After Sysprep has shut down successfully, create a powered-off snapshot and put
its UUID and the source VM name/UUID in the template. Do not boot the generalized
source again before snapshotting it. Verify the full process on two disposable
clones as real-host acceptance; do not infer Sysprep success from compilation.

## Linux and Compose baseline

Install Linux and Guest Additions. Configure root credentials for Guest Control;
Linux command execution requires **systemd 254 or newer, cgroup v2 with
`cgroup.kill` (Linux 5.14 or newer), and an available `clone3` syscall**.
The guest probe reports these execution requirements and observed support.
Each command has its own system-level transient supervisor unit with delegated
subgroups. The supervisor directly waits for the business process; `clone3`
places that process in its subgroup before execution. Cancellation kills the
entire subgroup, and `cgroup.events` confirms it is empty. Systemd also cleans
up descendants if the supervisor dies. User sessions and linger are unnecessary.
Clone firstboot has a 90-second total budget, 30 seconds per hostname/SSH-key
step and a 100-second systemd startup limit. It durably installs `/etc/hostname`
and sets the kernel hostname directly, independently of D-Bus service activation.
The existing journal records each step, elapsed time and structured errors;
subprocess results include their actual exit code or signal and at most 8 KiB of stderr
with a truncation flag. Failed initialization retains `firstboot-pending`; a prepared marker while
pending does not permit commands. Diagnose the recorded step and retry initialization
inside the disposable clone; no failure symptom alone identifies a D-Bus or CPU cause.

Copy `share/vm-test-mcp/guest/vm-test-guest-linux-amd64` into the source guest and run
as root:

```sh
chmod +x ./vm-test-guest-linux-amd64
./vm-test-guest-linux-amd64 prepare
# An optional --root must match the template's work_dir.
```

Preparation verifies the prerequisites, installs `/opt/vm-test/vm-test-guest` and
the first-boot unit, creates the work directory, clears machine-id and SSH host
keys, and powers off. Take the snapshot before starting the source again. Each
clone generates its own machine-id, hostname and SSH host keys when SSH is installed.
Snapshot a working Guest Additions setup. Recommend Python and, for Compose tests,
Docker Engine/Compose with the daemon enabled at boot; test workflows handle absent
tools without changing the source VM.

Select durable baseline dependencies explicitly. For the optional controls/vision
example, use its [environment preparation script](../../.agents/skills/virtualbox-vm-test/scripts/prepare-environments.ps1)
and the [automation guide](../../.agents/skills/virtualbox-vm-test/references/automation.md)
for the declared Python version, separate environments and hash-locked dependencies.
Verify actual imports and desktop behavior. These project environments also run
without MCP and are separate from baseline identity and command supervision.

Use VDI disks for both Windows and Linux source VMs. Remove raw disks, host shares,
PCI passthrough, generic USB filters and host
serial/parallel devices from source VMs. The controller also sanitizes and checks
clones before boot. Allowed camera/audio/3D policy is independent of baseline
installation; media stays off unless explicitly requested. Graphics controller,
VRAM, paravirtualization and paging remain baseline choices. Declare any required
host startup environment in the template's `startup_env`.

Clones default to `GUI/AutoresizeGuest=false`. Use `node_display(auto_resize=…)`
for that per-node setting and `lab_gui_set(max_guest_resolution="any"|"auto")`
for the global resolution policy, whose default is `any`. MCP applies these
settings, restores the recorded global value at cleanup and reports conflicts
with external edits; administrator `setextradata` commands are unnecessary.

## Snapshots and host disk use

There is one administrator-owned source and its powered-off snapshot. Each lab
creates linked clones directly from that snapshot. There is no additional full
copy, preparation generation or service-owned baseline cache.

For example, suppose the source's actual VDI/snapshot files occupy **30 GiB** on
the host, irrespective of their advertised virtual capacity. Two linked clones
initially add mostly metadata. If their changed blocks grow to 2 GiB and 3 GiB,
the approximate total is **35 GiB**, plus VM metadata, the shared router ISO,
small seed ISOs and retained evidence. The router adds no persistent virtual disk.
Guest writes, Sysprep specialization and installs all grow the clone's delta.
Deleting the lab removes those 5 GiB of deltas; retained logs/artifacts remain.

Preinstalling a repeatedly used tool in the source can reduce repeated installs
and per-clone growth. Uploading an offline installer only saves downloads; its
installed files still occupy each clone's delta. To update the baseline, finish
using the old snapshot, boot/update the source, run preparation again and create
a new powered-off snapshot. Update `snapshot` explicitly and restart the controller
after active tasks finish. Keep any parent disks still referenced by existing
snapshots/clones. MCP never deletes the source or its snapshots.

Candidate guest releases use this same snapshot workflow. During an administrator
maintenance window, update the existing source VM and take a new powered-off
snapshot, preserving the previous snapshots and their referenced disks. Prepare a
separate full clone when the source VM's current state must also remain unchanged
or other users need that source during preparation. Independent acceptance VMs
are the disposable clones MCP creates from the selected snapshot. Select its UUID
explicitly in the configuration, retain the previous references for rollback, and
record package `--version`, guest probe/service identities and node
template/hardware fingerprints with the actual test results.

## Router Live ISO

Build `nix build .#vm-test-router` in the container and place the resulting
`vm-test-router.iso` at the host path configured by `virtualbox.router.iso`. No VM import,
router account setup, installed disk or router snapshot is needed. Keep the build
output rooted while it is configured. MCP creates a task-owned **1 vCPU / 256 MiB**
EFI VM, attaches the read-only system ISO and a private read-only seed ISO, and
connects Internal Network LAN plus VirtualBox NAT WAN.

The ISO can be checked before preparing Windows/Linux templates. Configure only
`virtualbox.router.iso`, restart the controller and create a lab with `nodes: []`. Wait for
`phase: running` and `network.state: ready`, exercise `network_set` for DHCP, upstream
DNS and public egress, then finish the lab and verify `cleanup.state: complete`.
Client DHCP/DNS traffic, NAT and TCP/UDP publication require a lab with test nodes.

The ISO reads its squashfs store on demand and uses tmpfs for writable state; its
default boot does not copy the ISO into RAM. The seed stays attached until cleanup
so each reboot can reconstruct credentials and interfaces. Both media are detached
before VM deletion; the administrator's boot ISO is never deleted. No permanent
router password or SSH service is needed. Static builds do not prove the 256 MiB
runtime target.

Live operation still supports changing public egress, DHCP, public upstream DNS and
registered node IPv4 addresses using `network_set`. The LAN subnet is fixed for the
lab. DHCP/DNS reservations and endpoint forwarding are regenerated; change guest
addresses/routes/DNS/firewalls with ordinary privileged guest commands and renew
leases as needed. Disabling DHCP does not erase an existing guest address.

Boot and policy failures keep egress/entries closed. Controller-detected restart or
lease expiry requires `network_recover`, which checks the router, protection and
port bindings before restoring declared state. It does not restore a RAM snapshot
of the previous router. The dnsmasq memory cap is 32 MiB and volatile logs are
bounded to 8 MiB.

## Nix executor and cache trust

Use multi-user Nix with dedicated build users, systemd 254+ and cgroup v2.
The root executor starts task-specific `nix daemon --stdio --force-untrusted
--store local` processes inside each job cgroup. Enable `nix-command` and
`daemon-trust-override` support in the selected Nix executable. The executor fixes
strict sandboxing, no fallback, local builds and signature verification; configure
local KVM access for builders and trusted cache keys in the host Nix policy.
The worker uses the existing host store through its task endpoint; store contents
remain readable task inputs. Nix treats trusted users as effectively root-equivalent,
so the worker's ordinary account remains outside `trusted-users`.

For NixOS, merge through the host's managed configuration, preserving existing
cache and virtualization settings:

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

In the shared `config.yaml`, set `executor.worker_uid` to the ordinary worker's
numeric UID and `executor.executable` to the administrator-selected absolute Nix
executable. The explicit sudo launch authorizes the current build and this
configuration. The executor reads it once at startup; later edits require a
restart. Host users who can change the checkout or configuration belong to this
administrative trust boundary. Runtime worker requests cannot change the policy.

The default `executor.state_dir` is `/var/lib/vm-test-nix-executor` and
`executor.socket` is `/run/vm-test-nix-executor/control.sock`. The worker reads
that same socket field. State and socket-directory ancestors must be root-owned real directories
without write access for other accounts. The executor creates its private state
with mode `0700` and its dedicated socket directory with mode `0711`; socket
files grant access only to the configured worker UID. For custom socket locations,
prepare the parent directories and allow the worker to traverse them.
The root service accepts only job/session identities and bounded deadlines,
never caller-supplied commands, PIDs, cgroup paths or environment settings.

Worker startup checks its non-root identity, client sandbox settings and executor
availability. Before importing each job it uses that job's endpoint for
`nix store info --json` and verifies `trusted=false`. Import, derivation inspection
and build all use the same endpoint. The executor uses the local store directly;
it does not forward builds to the shared host daemon. The client's
`build-users-group` may be empty because root daemon defaults own build identities.
Startup does not run a build or start a VM. Verify sandbox isolation, build-user
identity, complete descendant containment and KVM access during acceptance.

Review `trusted-users`, `build-users-group`, `sandbox-paths` and
`extra-sandbox-paths`. Expose `/dev/kvm` only as needed; do not expose home directories,
credentials, daemon sockets or container-engine sockets. Verify KVM permissions for
the actual build identity, including after reboot. Sandbox builders may retain
their primary group while dropping supplementary groups. If explicit device
mapping or a persistent ACL is needed, configure it for the actual build group
through managed Nix/udev settings and verify it with a fresh sandbox VM test.
The submission account itself does not launch the test nodes. A worker client configuration
dump is supporting evidence, not proof of the daemon's active policy.

The helper uploads a standard binary cache. The daemon verifies imported content
and signatures. For locally produced input-addressed objects, configure an
administrator-approved cache signing key with the submission environment and its
public key in the daemon. The helper's `--signing-key` applies the local binary
cache `secret-key` setting. Do not mark the worker trusted or disable signatures
to resolve an import failure. Review the
[Nix sandbox settings](https://nix.dev/manual/nix/2.34/command-ref/conf-file.html#conf-sandbox)
and [binary cache settings](https://nix.dev/manual/nix/2.34/store/types/local-binary-cache-store.html).

Content-addressed inputs carry their own integrity identity. Locally built
input-addressed paths require an accepted signature or reproduction on the executor.
For a refused path, compare local metadata with an administrator-trusted cache:

```sh
nix path-info --json /nix/store/STORE_PATH
nix path-info --json --store https://cache.nixos.org /nix/store/STORE_PATH
```

When the store path and NarHash match, fetch the signed content on the executor:

```sh
nix copy --from https://cache.nixos.org --to daemon /nix/store/STORE_PATH
```

Choose daemon `max-jobs` and `cores` from available CPU/RAM. They control build
scheduling and parallelism hints, not strict per-task resource isolation.
Controller/worker process limits do not constrain daemon-owned builders; limits
on the shared daemon also affect unrelated host builds. Ordinary KVM builds must
participate in the administrator's scheduling policy. Finish competing manually
started virtualization before using this scheduler; MCP leaves unrelated VMs
under their administrator's control.

## Service supervision

Enter `tools/vm-test-mcp` and use separate host terminals:

```sh
just nix-executor
just nix-worker
just serve
```

`just nix-executor` builds as the current ordinary account, then explicitly runs
`sudo systemd-run` with the complete transient-service properties documented in
[Start and use](README.md#start-and-use). Its PTY stays attached. Ctrl+C starts
executor shutdown, bounded by its five-minute release budget plus five seconds
for shutdown completion. `systemctl stop vm-test-nix-executor.service` uses the
service's independent six-minute stop budget and mixed termination policy.
No permanent unit or automatic restart policy is installed. A direct bare root
executor invocation cannot provide the delegated supervisor boundary.

The three recipes use this directory's `config.yaml`; `just nix-executor` passes
its absolute path through sudo to the service. `just nix-worker` and `just serve`
run as ordinary foreground processes. The worker's socket must be accessible to the controller; separate
accounts need a socket outside the worker's private directory. Run `just nixos-test`
in the container after the host services are available. An offline worker makes
storage accounting unknown and pauses new admission; control and recovery remain
available for accepted jobs.

For GUI support, use a user service in the VM owner's graphical session with its
display/session environment. Its command is
`/opt/vm-test-mcp/bin/vm-test-mcp --config /etc/vm-test/config.yaml`, also with
`UMask=0077` and `Restart=on-failure`. A headless-only controller can use ordinary
account supervision. Use `--check-config --config /etc/vm-test/config.yaml` on
each of the three programs to validate configuration values without starting
services or creating state. The executor checks host directory ownership and
cgroup delegation at startup, before creating state or sockets. Configuration
is loaded at startup. Restart after tasks finish when changing policy or paths.

This release requires new configuration and empty controller, worker and executor state
with fresh schema-22 databases. The user must stop the old services, confirm all
old VM/daemon/port resources are released, and clear or retire the old state before
starting. Preserve any required old evidence in private offline backups first.
Do not run the new binary against a live old state or attempt an in-place schema
migration. Guest protocol remains 2; prepared baselines with the configured matching
supervisor identities can continue to use that protocol.
Keep the new state for subsequent restart/recovery acceptance. VirtualBox baselines and host Nix-store contents remain independent.
Old private backups can contain credentials and command inputs; keep them outside
version control and the Nix store.

## Recovery

The worker database has a durable instance identity. A replacement database cannot
attest release of jobs owned by the previous one; inspect and reconcile their actual
resources instead of using an empty store as cleanup evidence.

Inspect `node_get`, `execution_events` and `lab_diagnose` for node failures. `node_exec`
provides a short Guest Control command even when first-boot initialization or the
command service is unavailable. A running lab retains its nodes until the caller
finishes/cancels or the task deadline expires. Startup failure collects what is
available within its evidence budget and cleans the lab.

For an ended VirtualBox task with `cleanup.state: blocked`, inspect each resource's
steps and error, resolve the reported host problem, then call `lab_cleanup` with
a new request ID. It retries recorded ownership and verifies deletion before
releasing the scheduler. If MCP is unavailable, stop the controller before local
reconciliation; its private-directory lock prevents two owners. Review the SQLite
task/resource records and VirtualBox state under the VM owner's account. Verify
every recorded clone UUID, its task-owned configuration path, attached differencing
disks, absence of active entries and remaining VM processes. Resolve the concrete
failure, then retry registered cleanup:

```sh
vm-test-mcp --config /etc/vm-test/config.yaml --reconcile TASK_ID
```

This command cannot waive the ownership/path checks. It also restores the recorded global `GUI/MaxGuestResolution`; if another application changed that value, inspect the recorded original/expected values and resolve the conflict locally before retrying. Preserve baselines and unrelated
VMs; never substitute names or paths printed by untrusted test code as deletion authority.

For Nix jobs, inspect `result`, `resources`, `evidence` and their structured
causes separately. The executor closes admission before signalling, waits the
configured grace period, kills remaining descendants, and persists release only
after `cgroup.events` reports `populated 0`. The worker confirms its client exit.
The executor's deadline remains authoritative while the worker is disconnected.
Known groups remain observed after a release-budget blocker; later release can
free the slot without another job submission.

Restart the executor or worker with the same state. Boot identity, systemd
invocation, durable executor/worker identities and cgroup inode bind recovery to
the original resources. A new boot or disappearance of the previous invocation's
recorded cgroup can prove release. A mismatched or unknown identity requires
administrator inspection; a blank database cannot attest to a previous session.
After resolving the reported resource problem, use
`nix_job_recover(job_id=…, scope="resources")`. The worker has no blanket cleanup
attestation command.

For evidence, use `scope="evidence"`. Indexing and handoff run independently of
resource release. Each attempt is bounded; transient errors retry at most three
times with 1s/5s delays. Permission, storage-capacity, integrity and identity
failures report the actor and required action without repeated automatic replay.
The manifest and acknowledgement are durable, so retries reuse verified objects.
Worker copies and GC roots remain until controller publication and acknowledgement
are complete. Pending handoff remains protected from TTL, pressure reclaim and
explicit deletion. Default builds omit `--keep-failed`; explicitly retained failed
build directories remain administrator-managed.

A manual blocker is a reportable outcome. Retain its job/session identities and
cause, continue independent local work, and confirm runtime release before another
executor batch. Initial rollout may retire old state only after the administrator
has stopped old physical resources and retained any required evidence. Normal
restart recovery keeps all three state directories.

## Acceptance

Run the reproducible [Codex transport check](README.md#development-and-verification)
with CLI 0.163.0 and 0.165.0. Record these real-host results separately from the
adapter/transport suites:

1. Two nodes from one generalized Windows snapshot have independent identities,
   MACs and disk state; the baseline remains unchanged.
2. Windows and a Linux Compose app communicate over LAN DNS; the development
   container reaches published TCP and UDP services; disallowed sources cannot.
3. Public IPv4 works while host, metadata and protected ranges are blocked. Egress
   disable terminates public flows while retaining LAN/entries. Router and controller
   heartbeat failures close networking; explicit recovery restores declared state.
4. CLI commands work without desktop login. Requested GUI login/unlock, native
   input, screenshots, concurrent commands, ordered UTF-8 stdin, EOF, descendant
   cleanup and each explicitly enabled media device work. Verify actual guest
   identity and elevation when the application assertions depend on them.
5. Cancellation, timeout, partial creation and controller restart remove all clones,
   NAT rules, relays and temporary resources while preserving evidence.
6. The router boots and sustains DHCP, DNS, NAT and interactive traffic at 256 MiB.
7. Use the [multi-node acceptance module](../../.agents/skills/nixos-vm-test/assets/vm-smoke.nix)
   with the tested project's locked nixpkgs and a fresh run ID. Confirm guest startup,
   CPU counts and network assertions on host KVM. A synthetic readable host marker
   is inaccessible inside the sandbox; a request with `__noChroot = true` is rejected
   specifically for isolation policy; build identities differ from the worker and
   root. Exercise a failing test and cancellation, retaining logs, evidence and
   cleanup state. Interrupt a build and verify the cleanup hold/recovery procedure.
8. VirtualBox guest ACPI S3 sleep and S4 hibernation are unsupported. Verify
   hypervisor pause/resume separately. Classify new-user Secret Service behavior
   using the observed template/kernel/desktop versions. For a new account, initialize
   its native keyring through the login flow before starting the Secret Service;
   verify collection calls, real item read/write, locking and persistence. An
   advertised alias or a prepared guest alone does not establish that behavior.
   Preserve encrypted keyring bytes when diagnosing an owned daemon restart.

Record rendering, decoding and encoding results from actual application behavior
when graphics or media devices are exposed; device availability and the basic KVM
smoke test do not establish those capabilities. Keep application assertions
separate from evidence collection and resource cleanup.

Repeat fresh acceptance after relevant host kernel, Nix, VirtualBox, Guest Additions,
baseline or test-framework changes. Failed Nix build directories and store garbage
collection remain administrator-owned maintenance.
