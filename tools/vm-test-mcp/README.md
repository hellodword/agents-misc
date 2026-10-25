# VM Test MCP 0.11.0

Go services for disposable Windows/Linux VirtualBox labs and strict NixOS sandbox
tests on physical-host KVM. A lab contains independent linked clones and a private
router. Labs and Nix jobs share one persistent FIFO scheduler; commands within a
lab run concurrently. HTTP disconnects do not cancel tasks.

The controller runs as the VirtualBox owner. The Nix worker runs as an
ordinary Nix client outside `trusted-users`, authenticating its controller's UID
on a Unix socket. Both processes may use the same account. Administrator
configuration, these processes, VirtualBox
isolation and the Nix daemon sandbox form the trusted boundary. Requests, uploads
and test code are untrusted. Guest escapes through VirtualBox vulnerabilities are
outside this design.

## Build and prepare

```sh
nix build .#vm-test-mcp --out-link result-vm-test-mcp
nix build .#vm-test-router
```

The service package is available through `result-vm-test-mcp`; the router ISO is
`result/vm-test-router.iso`. The component's `just build` and `just router` recipes
use these separate links. The Windows preparation executable is
`result-vm-test-mcp/share/vm-test-mcp/guest/vm-test-guest-windows-amd64.exe`.

To build both packages into one output directory, run from the repository root:

```sh
just --justfile tools/vm-test-mcp/justfile build-all
```

This builds `.#vm-test-all`, prints its Nix store output path and creates
`result-vm-test-all`. The output combines both packages through symlinks: service
executables are under `bin/`, guest supervisors under `share/vm-test-mcp/guest/`,
and the router ISO is `vm-test-router.iso`. It also retains both packages' license
and source references. From `tools/vm-test-mcp`, the same command is `just build-all`.

These outputs target `x86_64-linux`. The service package includes the controller,
worker, container submission helper, Linux/Windows amd64 guest supervisors and
native VirtualBox API helper, and the xorriso seed-ISO builder. The router output is a
diskless NixOS Live ISO, built with ordinary files without mounts or KVM. It reads
squashfs on demand, uses tmpfs for writable state, and targets **1 vCPU / 256 MiB**.
The router uses the firmware framebuffer without native GPU modesetting. Its
primary serial console retains kernel, initrd and service output for diagnostics;
the framebuffer remains available for screenshots.

Follow [administrator preparation](preparation.md). Both services read the same
[annotated YAML](config.example.yaml) outside the Nix store. There is no config-file
permission requirement; their private state directories remain separate.
Local `config.yaml` is Git-ignored. Optional backend blocks are commented
out until their administrator-owned inputs are ready. Configuring `virtualbox.router.iso`
enables VirtualBox even before templates are registered; `lab_create(nodes=[])`
starts just the router for boot and dynamic policy checks. Templates reference powered-off
snapshots directly; the service never modifies a source VM or creates a preparation
cache.

## Start and use

From the repository root, use separate host terminals. Both commands may use the
same ordinary account outside Nix `trusted-users`:

```sh
just --justfile tools/vm-test-mcp/justfile serve       # Host: controller account.
just --justfile tools/vm-test-mcp/justfile nix-worker  # Host: ordinary Nix worker account.
```

Both commands build the package as needed, run in the foreground and read
`tools/vm-test-mcp/config.yaml`. Override with `--config /absolute/config.yaml`.
They use the current account; account/socket/KVM preparation follows the
[administrator guide](preparation.md). They do not switch users or install services.
From `tools/vm-test-mcp`, use `just serve`, `just nix-worker` or `just nixos-test`.

For an installed package, the equivalent service commands are:

```sh
vm-test-nix-worker --config /etc/vm-test/config.yaml
vm-test-mcp --config /etc/vm-test/config.yaml
```

Start each process as its configured account. An offline worker affects only Nix tools; `nixos_run` returns `worker_unavailable` before reserving the request ID or queue slot. For `gui: true`, start the controller
in the VM owner's graphical session. Headless is the node default. Executables
accept `--help`; startup errors exit 1, usage errors exit 2, success exits 0.
Diagnostics go to stderr. Streamable HTTP is at `/mcp`, authenticated with
`Authorization: Bearer TOKEN`, with optional exact Host/Origin allowlists (empty or omitted allows any). All token
holders share authority over all tasks.

1. Prepare scripts and dependencies locally. Call `service_get` for templates,
   backend status and limits, and `task_list` for compact active/queued summaries.
2. Call `lab_create` with a fresh `request_id` and a fixed list of named nodes.
   Select a deadline for one continuous test batch. Reusing a template creates
   independent clones with separate UUIDs, MACs and disks.
3. Poll the operation with `operation_get`, then `task_get`. While queued or
   preparing, upload prepared files through `transfer_create`. Every mutation returns
   a durable receipt. Identical retries reuse the ID; changed parameters conflict.
4. Use `command_start` with guest argv, `command_read` for output and `command_input`
   for numbered stdin packets/EOF. A nonzero exit finishes only that command.
5. Upload through `transfer_create`/`guest_file_put`, collect files or guest tar
   archives with `guest_file_get`, and retrieve evidence through `artifact_get`.
6. Submit `passed`, `failed` or `inconclusive` and a summary through `task_finish`.
   Wait for `cleanup.state: complete` before lengthy local edits or reporting;
   evidence remains until `task_purge` by default. Abandon queued/preparing tasks
   with `task_cancel`. Later batches use a new execution identity. Extend only for
   concrete continued work using `task_extend(seconds=…, reason=…)`.

The [API contract](contract.md) describes parameters, errors and transfer routes.
The distributed [VirtualBox workflow](../../.agents/skills/virtualbox-vm-test/SKILL.md)
and [NixOS workflow](../../.agents/skills/nixos-vm-test/SKILL.md) describe execution.

## Networks and evidence

Each lab has its own Internal Network, DHCP reservations and `NAME.lab.test` DNS.
The router applies reservations and protection before clients boot, so a slow
peer does not consume another guest's DHCP retry window. `planned_ipv4` is a
reservation; guest interface/address observations and application connectivity
assertions establish the actual network boundary separately.
LAN defaults to `10.77.0.0/24`; callers can select an RFC1918 subnet and disable DHCP.
Guest Control remains usable independently of networking. Docker/Compose run
inside Linux nodes, with their privileges scoped to that guest.

Public IPv4 TCP/UDP egress defaults on. The router filters non-public IANA ranges,
host addresses and administrator-protected ranges, including its own connections.
IPv6 is available between LAN guests, without managed egress/publication.
`endpoint_open` publishes TCP/UDP through a source-filtered host relay and
loopback-only VirtualBox NAT forwarding. Source rewriting is allowed.
The router enables NAT loopback access for UDP forwarding replies; its nftables
policy continues to block guest-initiated connections to host addresses.

Policy updates are nftables transactions. Expiring leases require control
heartbeats. A late heartbeat marks policy observation `suspect` while the last
confirmed, unchanged lease remains valid. Router identity/policy failures, changed
host protection and lease expiry degrade networking and close host relays.
`task_get.network` retains stage, cause, budget, lock/execution time, lease deadline
and router identity. `network_recover` validates and restores declared egress/entries.
`service_observe` exposes a bounded recent window of host pressure, controller
quota/throttling and call wait/execution times. Faults retain a before/after window
as an artifact without extending VM occupancy. Missing and unsupported metrics
remain explicit; use concurrent evidence before attributing a fault to CPU load.
`network_set(internet=false)` retains LAN and published access and revokes public
connection state. The same tool can change DHCP, upstream DNS and node address
registrations; adjust guest addresses/routes/firewalls through commands and renew
leases as needed. A lab's LAN subnet stays fixed. `node_network_link` controls its
first NIC's cable. Media omitted from node requests is disabled.

Clones default to `GUI/AutoresizeGuest=false`; `node_display(auto_resize=…)` changes
it. `lab_gui_set(max_guest_resolution="any"|"auto")` manages the global resolution
limit (default `any`), journals its previous value and restores it during cleanup.
An external edit causes a cleanup conflict instead of being overwritten.

Automatic login is optional. Native input/screenshots work before login; CLI
commands use the prepared guest privilege boundary. Python and Docker/Compose are
optional tools discovered in guest PATH, with installation handled by test workflows.
Windows readiness waits for OOBE completion, so commands cannot start on its
temporary setup desktop. It does not require a logged-in user.

Native input is serialized per node. An `uncertain` receipt means delivery could
not be confirmed; inspect a screenshot before choosing another action. Delivery
is separate from application assertions. Video, NIC and metrics captures produce
artifacts. Automatic evidence bundles include available VM logs, screenshots, initialization and system-service observations. Logs use byte offsets and base64 payloads, including for UTF-8 text.

## Faults, diagnostics and cleanup

`task_get` separates `phase` (`queued`, `preparing`, `running`, `ended`), `result`
and `cleanup`. Queue views expose position and blocking task; resource states
distinguish never allocated from released. `task_list` defaults to active work,
including blocked cleanup; request `phase=ended` or `all` for compact history.
Task status omits command payloads. `command_get` reads the spec and input
progress; `input_from` explicitly pages through retained stdin packets.
Use `node_get` for preparation progress, expected power state,
capabilities and the last observation of VirtualBox, Guest Control and command
service health. Each observation records its time, last success and bounded error.
Task status also exposes router preparation. Each preparation record has its own
start, stage start and deadline; peer cancellation retains the initiating cause.

A preparation failure ends the lab after collecting evidence within a shared
30-second budget. Running faults retain the lab and healthy nodes until the caller
finishes/cancels or the task deadline expires. A node's unavailable capability
recovers after two successful observations. Power actions record their expected
transition, so an intentional pause or shutdown does not become a lost-node alarm.

Use `task_diagnose(task_id, node_ids?)` during preparation or execution. Omitting
node IDs collects the whole lab, including its router. Available evidence is kept
even if other collectors time out. `node_exec` executes a short absolute guest
argv directly through Guest Control, independently of command-service readiness;
its default budget is 10 seconds and maximum is 30 seconds. The receipt retains
bounded stdout/stderr and an artifact. An unconfirmed result stays `uncertain`.
Use `command_start` for ordinary workloads and process-group supervision.

Cleanup closes published entries immediately, limits evidence collection and
records resource steps and retry counts. `task_cleanup` retries remaining
resources of an ended VirtualBox task. A `blocked` cleanup keeps the FIFO occupied;
inspect its resource errors before a new retry. Nix daemon teardown still uses the
administrator's local verification procedure. Task results and evidence remain
available after resource cleanup.

The annotated config groups deadlines under `tasks`, backend/router/network policy
under `virtualbox`, worker settings under `nix`, and retention/upload limits under
`storage`. Timeouts use units such as `30s`, `10m` and `1h`. `service_get` exposes the
effective probe, diagnosis and cleanup limits.

## NixOS submissions

With both host processes running, execute from the repository root in the development container:

```sh
just --justfile tools/vm-test-mcp/justfile nixos-test
```

This authenticates with the shared config, evaluates the bundled smoke module using
this project's locked nixpkgs, allocates/uploads a cache, submits a fresh task and
waits for its result and cleanup. The smoke test starts three KVM nodes, each with
2 vCPUs and 1024 MiB RAM, checks their CPU counts and pings between the nodes. It
does not use VirtualBox or the router ISO. Host daemon sandbox policy still needs
administrator verification.
If VirtualBox or another Nix task is running, submission joins the FIFO queue and
waits for that task's cleanup before execution.

Progress, task ID and live logs go to stderr; stdout is the final task JSON. Evidence
and inputs stay on the server. The printed local workspace, by default
`tmp/agent/vm-test-runs/ID`, retains `run.json`, `submission.json`, the uploaded cache,
`task.log` and `result.json` (including artifact metadata). An unsuccessful test or
unresolved cleanup exits 1; only `passed` with `cleanup.state: complete` exits 0.

```sh
# Resume the original task, including after an interrupted/lost submission reply.
just --justfile tools/vm-test-mcp/justfile nixos-test --work-dir /absolute/printed/workspace
# Run an existing project test; preserve these arguments when resuming it.
just --justfile tools/vm-test-mcp/justfile nixos-test --test '.#checks.x86_64-linux.integration'
# Optional task deadline and administrator-approved cache signing key.
just --justfile tools/vm-test-mcp/justfile nixos-test --timeout-seconds 3600 --signing-key /absolute/key
```

Ctrl-C stops the submitting/observing client; an accepted remote task keeps its
deadline. Resume using the same workspace, or use `task_cancel` through MCP. A new
invocation without a workspace or request ID creates a new execution. `--project`
selects the locked project for the bundled smoke module; `--config` selects the
shared config. No bearer token or transfer capability is written to `run.json`.

The packaged equivalent is `vm-test-submit --config /absolute/config.yaml`.
Without `--config`, the helper retains its upload-only interface for MCP clients:

Allocate a cache with `transfer_create(kind="cache")`, then run inside the
development container with the project's locked Nix environment:

```sh
vm-test-submit --test '.#checks.x86_64-linux.integration' \
  --request-id integration-001 --cache-id CACHE_ID --cache-url CACHE_UPLOAD_URL \
  --work-dir /absolute/ignored/task-workspace
```

The helper checks QEMU nodes and `qemu.forceAccel`, creates a unique top-level
derivation, retains it for retries, copies the closure to a standard local binary
cache and uploads it. Submit stdout JSON to `nixos_run`. Optional
`--signing-key /absolute/private/key` signs content under the administrator's trust
policy; signature checking stays enabled. Build outputs have worker-owned GC roots
and are exported only as ordinary files under registered output roots.

An interrupted daemon build keeps scheduler occupancy until local administrator
[recovery](preparation.md#recovery). Nix daemon cleanup attestation is a local administrator operation.

## Development and verification

```sh
just --justfile tools/vm-test-mcp/justfile test
nix build --no-link .#checks.x86_64-linux.vm-test-native
nix build --no-link .#checks.x86_64-linux.vm-test-router
just check-agent-rules
nix flake check --no-build --no-write-lock-file
```

The default tests inject synthetic virtualization adapters and use local HTTP/Unix
sockets. They run Go vet, the race detector and supervisor cross compilation.
SQLite extension loading is compiled out with `sqlite_omit_load_extension`.
SDK ABI tests load a synthetic C API library. Router builds inspect EFI boot content, squashfs, initialization units and bundled tools. These checks are implementation evidence;
complete [real-host acceptance](preparation.md#acceptance) after setup.

The official VirtualBox SDK 7.2.20 is fetched with its published checksum in a
separate derivation, without `pkgs.virtualbox`; the helper loads the administrator's
installed library. Keep its binary cache separate from VirtualBox product builds.
Packages carry component notices and SDK C source for rebuilding/relinking.
Build recipes do not publish or push artifacts.
