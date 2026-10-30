# VM Test MCP 0.14.0

One Go module, one public MCP service, with separate VirtualBox and Nix domains
for disposable Windows/Linux VirtualBox labs and strict NixOS sandbox
tests on physical-host KVM. A lab contains independent linked clones and a private
router. Labs and Nix jobs share one persistent FIFO scheduler; commands within a
lab run concurrently. HTTP disconnects do not cancel tasks.

The controller runs as the VirtualBox owner. The Nix worker runs as an
ordinary Nix client outside `trusted-users`, authenticating its controller's UID
on a Unix socket. Both processes may use the same account. A root executor
creates a private Nix daemon endpoint and delegated cgroup for each job, using
the existing host store. Administrator configuration, these processes, VirtualBox
isolation and the Nix sandbox form the trusted boundary. Requests, uploads
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
worker, root executor, container submission helper, Linux/Windows amd64 guest supervisors and
native VirtualBox API helper, and the xorriso seed-ISO builder. The router output is a
diskless NixOS Live ISO, built with ordinary files without mounts or KVM. It reads
squashfs on demand, uses tmpfs for writable state, and targets **1 vCPU / 256 MiB**.
The router uses the firmware framebuffer without native GPU modesetting. Its
primary serial console retains kernel, initrd and service output for diagnostics;
the framebuffer remains available for screenshots.

Follow [administrator preparation](preparation.md). All three services read the same
[annotated YAML](config.example.yaml) outside the Nix store. There is no config-file
permission requirement. Explicit sudo startup authorizes the current build and
the shared file's `executor` policy, which stays fixed for that process lifetime.
Top-level `state_dir` owns the role directories
`controller/` and `nix-worker/`; `nix.state_dir` optionally overrides the worker root.
The roots can be identical while each role retains its own private directory,
lock and database.
Local `config.yaml` is Git-ignored. Optional backend blocks are commented
out until their administrator-owned inputs are ready. Configuring `virtualbox.router.iso`
enables VirtualBox even before templates are registered; `lab_create(nodes=[])`
starts just the router for boot and dynamic policy checks. Templates reference powered-off
snapshots directly; the service never modifies a source VM or creates a preparation
cache.

## Start and use

From the repository root, use separate host terminals. Build as the ordinary
account; the executor recipe explicitly uses sudo for its transient service:

```sh
just --justfile tools/vm-test-mcp/justfile serve       # Host: controller account.
just --justfile tools/vm-test-mcp/justfile nix-executor # Host: sudo, delegated transient service.
just --justfile tools/vm-test-mcp/justfile nix-worker   # Host: ordinary Nix worker account.
```

All commands build the package as needed, remain in the foreground and read
`tools/vm-test-mcp/config.yaml`; override with `--config /absolute/config.yaml`.
Fill in `executor.worker_uid` and `executor.executable` in that shared file.
The recipe passes its absolute path to the transient service. The administrator
trusts this configuration when invoking sudo; edits take effect after restart.
It requires systemd 254+, cgroup v2 and delegated cgroup support. Its recipe uses
`systemd-run --service-type=exec --pty --wait --collect`, `Delegate=yes`,
`DelegateSubgroup=supervisor`, `KillMode=mixed`, `TimeoutStopSec=6min`, `Restart=no`
and `UMask=0077`. No permanent unit is installed. `--collect` removes transient
unit metadata; executor records remain in its private state directory. Ctrl+C
runs bounded session shutdown; abnormal MainPID exit lets systemd terminate the
service's remaining processes. Restart reconciles the durable session receipts.
From `tools/vm-test-mcp`, use `just serve`, `just nix-executor`, `just nix-worker`
and `just nixos-test`.

For an installed package, the equivalent service commands are:

```sh
vm-test-nix-worker --config /etc/vm-test/config.yaml
vm-test-mcp --config /etc/vm-test/config.yaml
```

Start each process as its configured account. When Nix is configured, worker availability is also needed for evidence accounting.
An offline worker pauses new labs/jobs/uploads and queued starts; existing
execution, controls, diagnosis and cleanup remain usable. For `gui: true`, start the controller
in the VM owner's graphical session. Headless is the node default. Executables
accept `--help`; startup errors exit 1, usage errors exit 2, success exits 0.
Diagnostics go to stderr. Streamable HTTP is at `/mcp`, authenticated with
`Authorization: Bearer TOKEN`, with optional exact Host/Origin allowlists (empty or omitted allows any). All token
holders share authority over all tasks.

Use Codex CLI **0.163.0+** with a 660-second tool timeout:

```toml
[mcp_servers.vm_test]
url = "http://127.0.0.1:8765/mcp"
bearer_token_env_var = "VM_TEST_TOKEN"
tool_timeout_sec = 660
```

The [Codex MCP configuration](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)
defines this timeout setting. SSE carries structured results and 15-second
transport heartbeats. Disconnects, timeout and MCP cancellation end observation;
explicit business cancellation ends execution. Use empty controller, worker and
executor state for initial installation of this release; old schemas/configuration are intentionally incompatible.

1. Prepare scripts and dependencies locally. Allocate file inputs with `upload_create`,
   PUT their immutable manifests and bytes, and wait for automatic `ready` state.
   Call `service_get` for templates,
   backend status and limits, and `lab_list` for compact active/queued summaries.
2. Call `lab_create` with a fresh `request_id`, a fixed list of named nodes and optional ready `upload_ids`.
   Select a deadline for one continuous test batch. Reusing a template creates
   independent clones with separate UUIDs, MACs and disks.
3. Creation waits for readiness and returns `{operation, wait}`. If the 600-second
   window expires, call `wait` with its `continue` targets. Read `lab_inspect` for
   node IDs. Identical retries reuse the receipt; changed parameters conflict.
4. Use `command_start` with guest argv (finite mode waits for completion; explicit
   service/interactive mode waits for start), `command_read` for output and `command_input`
   for numbered stdin packets/EOF. A nonzero exit finishes only that command.
5. Deliver ready inputs through `guest_file_put`; reuse them across nodes in the
   same lab. Read fresh capabilities with `upload_get`. Collect files or guest tar
   archives with `guest_file_get`, and retrieve evidence through `evidence_get`.
6. Submit `passed`, `failed` or `inconclusive` and a summary through `lab_finish`.
   Confirm `cleanup.state: complete` before the next executor batch. Report an
   administrator blocker with its concrete resource cause and continue independent
   local edits, analysis or reporting;
   evidence defaults to 1 day for success or 7 days otherwise; pin needed evidence. Abandon queued/preparing tasks
   with `lab_cancel`. Later batches use a new execution identity. Extend only for
   concrete continued work using `lab_extend(seconds=…, reason=…)`.

The [API contract](contract.md) describes parameters, errors and transfer routes.
The distributed [VirtualBox workflow](../../.agents/skills/virtualbox-vm-test/SKILL.md)
and [NixOS workflow](../../.agents/skills/nixos-vm-test/SKILL.md) describe execution.

Evidence budgets are 40 GiB across controller/worker with reclamation to 32 GiB; input staging is 16 GiB,
with 8 GiB per upload. Protected data can pause admissions while cleanup remains
available. Summary/receipt retention is 30 days; pins survive until unpin.
VirtualBox guest S3 sleep/S4 hibernation are unsupported; hypervisor pause/resume
remains supported.

## Networks and evidence

Each lab has its own Internal Network, DHCP reservations and `NAME.lab.test` DNS.
The router applies reservations and protection before clients boot, so a slow
peer does not consume another guest's DHCP retry window. `planned_ipv4` is a
reservation; guest interface/address observations and application connectivity
assertions establish the actual network boundary separately.
LAN defaults to `10.77.0.0/24`; callers can select an RFC1918 subnet and disable DHCP.
Guest Control remains usable independently of networking. Docker/Compose run
inside Linux nodes, with their privileges scoped to that guest.

Lab initialization prepares the router and network first, then test nodes in
request order. `virtualbox.startup_parallelism` defaults to 1 (range 1..64) and
covers cloning through guest readiness. Waiting nodes acquire no resources and
start their individual timeout only when dispatched. The lab's execution deadline
includes this waiting time. Explicit runtime power operations use caller-selected
concurrency.

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
`lab_inspect.network` retains stage, cause, budget, lock/execution time, lease deadline
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

`lab_get` separates `phase` (`queued`, `preparing`, `running`, `ended`), `result`
and `cleanup`. Queue views expose position and blocking task; resource states
distinguish never allocated from released. `lab_list` defaults to active work,
including blocked cleanup; request `phase=ended` or `all` for compact history.
Task status omits histories and command payloads. `command_get` reads status and
stdin progress; `command_details` reads argv/cwd/environment. `command_input_read`
reads retained stdin by sequence and byte window. Command, endpoint, capture and
upload lists have separate pagination. Stdin acceptance, durable guest delivery
and OS pipe writes are distinct; application consumption needs its own assertion.
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

Use `lab_diagnose(lab_id, node_ids?)` during preparation or execution. Omitting
node IDs collects the whole lab, including its router. Available evidence is kept
even if other collectors time out. `node_exec` executes a short absolute guest
argv directly through Guest Control, independently of command-service readiness;
its default budget is 10 seconds and maximum is 30 seconds. The receipt retains
bounded stdout/stderr and an artifact. An unconfirmed result stays `uncertain`.
Use `command_start` for ordinary workloads and process-group supervision.

Cleanup closes published entries immediately, limits evidence collection and
records resource steps and retry counts. `lab_cleanup` retries remaining
resources of an ended VirtualBox task. A `blocked` cleanup keeps the FIFO occupied;
inspect its resource errors before a new retry. Nix jobs expose `result`,
`resources` and `evidence` independently. The executor seals the private endpoint,
interrupts its daemons, uses `cgroup.kill` after the 30-second grace period, and
confirms an empty group within a five-minute resource budget. The worker also
confirms client exit. Resource release frees the execution slot while evidence
indexing and verified handoff continue. `nix_job_recover(scope="resources"|"evidence")`
recovers the original job without rebuilding it. Manual blockers return actionable
`attention` and leave automatic wait continuations.

The annotated config groups deadlines under `tasks`, backend/router/network policy
under `virtualbox`, worker settings under `nix`, privileged session policy under
`executor`, and retention/upload limits under
`storage`. Timeouts use units such as `30s`, `10m` and `1h`. `service_get` exposes the
effective probe, diagnosis and cleanup limits.

## NixOS submissions

With the controller, worker and executor running, execute from the repository root in the development container:

```sh
just --justfile tools/vm-test-mcp/justfile nixos-test
```

This authenticates with the shared config, evaluates the bundled smoke module using
this project's locked nixpkgs, reserves an upload, evaluates its bound derivation, uploads the cache and submits a fresh job and
waits for its result and cleanup. The smoke test starts three KVM nodes, each with
2 vCPUs and 1024 MiB RAM, checks their CPU counts and pings between the nodes. It
does not use VirtualBox or the router ISO. Host daemon sandbox policy still needs
administrator verification.
If VirtualBox or another Nix task is running, submission joins the FIFO queue and
waits for that task's cleanup before execution.

Progress, task ID and live logs go to stderr; stdout is the final task JSON. Evidence
and inputs stay on the server. The printed local workspace, by default
`tmp/agent/vm-test-runs/ID`, retains `run.json`, per-upload `inputs/ID/submission.json`,
`task.log` and `result.json` (including artifact metadata). The helper releases each
local binary cache after the accepted job ID is persisted, including on resume.
Uploading or uncertain acceptance retains the cache; an expired input confirmed
unaccepted releases its cache before a fresh attempt. Local deletion failures are
reported on stderr and retried on resume without changing the job result.
An unsuccessful test or
blocked resources or evidence exit 1; only `passed` with released resources and
`evidence.state: complete` exits 0.

```sh
# Resume the original task, including after an interrupted/lost submission reply.
just --justfile tools/vm-test-mcp/justfile nixos-test --work-dir /absolute/printed/workspace
# Run an existing project test; preserve these arguments when resuming it.
just --justfile tools/vm-test-mcp/justfile nixos-test --test '.#checks.x86_64-linux.integration'
# Optional task deadline and administrator-approved cache signing key.
just --justfile tools/vm-test-mcp/justfile nixos-test --timeout-seconds 3600 --signing-key /absolute/key
```

Ctrl-C stops the submitting/observing client; an accepted remote task keeps its
deadline. Resume using the same workspace, or use `nix_job_cancel(job_id=…)` through MCP. A new
invocation without a workspace or request ID creates a new execution. `--project`
selects the locked project for the bundled smoke module; `--config` selects the
shared config. No bearer token or upload capability is written to `run.json`. The journal
reconciles the original request before regenerating an expired unbound input;
accepted jobs keep their identity across retries.

The packaged equivalent is `vm-test-submit --config /absolute/config.yaml`.
Without `--config`, the helper retains its upload-only interface for MCP clients:

Allocate a cache with `upload_create(kind="cache")`, then run inside the
development container with the project's locked Nix environment:

```sh
vm-test-submit --test '.#checks.x86_64-linux.integration' \
  --request-id integration-001 --upload-id UPLOAD_ID --upload-url UPLOAD_URL \
  --work-dir /absolute/ignored/task-workspace
```

The helper checks QEMU nodes and `qemu.forceAccel`, creates a unique top-level
derivation bound to the upload ID with substitution disabled, retains it for retries, copies the closure to a standard local binary
cache and PUTs the manifest and objects. Submit stdout JSON (`request_id`, `upload_id`) to `nix_job_create`.
In upload-only mode, retain `cache/` until the accepted job identity is recorded,
then remove that directory; retain `submission.json` and needed logs/results for review.
Optional
`--signing-key /absolute/private/key` signs content under the administrator's trust
policy; signature checking stays enabled. Build outputs have worker-owned GC roots
and are exported only as ordinary files under registered output roots. Verified durable handoff releases worker copies
and roots; the host Nix store has its own GC policy.

An interrupted build retains scheduler occupancy until the executor and worker
confirm resource release. Evidence attempts have a separate five-minute budget,
with up to three attempts for transient failures and 1s/5s retry delays. Pending or
blocked evidence remains protected from deletion and retention reclamation. See
[recovery](preparation.md#recovery) for resource identity and handoff recovery.

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
SDK ABI tests load a synthetic C API library. Router builds inspect EFI boot content, squashfs, initialization units and bundled tools. The reproducible Codex transport check uses actual 0.163.0 and 0.165.0 binaries:

```sh
nix develop .#vm-test-mcp --command python3 tools/vm-test-mcp/scripts/check-codex.py \
  --codex-0163 /absolute/codex-0.163.0 --codex-0165 /absolute/codex-0.165.0 \
  --work-dir /absolute/ignored/new-acceptance-directory
```

It takes about eleven minutes, uses the production MCP transport with synthetic
VM adapters, and makes no model API calls. It checks the real 600-second window,
continuation, FIFO, client timeout/cancellation and explicit cleanup. The report
records binary hashes and timings. Logs and `result.json` are retained in the output
directory; the fixture executable and isolated Codex profiles live under `runtime/`
and are removed after process-group exit confirmation, including on failure or
interruption. Unconfirmed exit retains `runtime/` and records the pending resource
for recovery. Supplied Codex executables remain caller-owned. These checks are implementation evidence;
complete [real-host acceptance](preparation.md#acceptance) after setup.

The official VirtualBox SDK 7.2.20 is fetched with its published checksum in a
separate derivation, without `pkgs.virtualbox`; the helper loads the administrator's
installed library. Keep its binary cache separate from VirtualBox product builds.
Packages carry component notices and SDK C source for rebuilding/relinking.
Build recipes do not publish or push artifacts.
