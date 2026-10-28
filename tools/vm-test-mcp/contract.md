# VM Test MCP 0.13 contract

## Transport and identity

`/mcp` implements official SDK Streamable HTTP. Every request requires the configured
Bearer token. Empty or omitted Host/Origin allowlists permit any value; nonempty
lists require exact matches (requests without Origin remain allowed).
JSON is bounded to 1 MiB and depth 64, rejects duplicate fields, and tool arguments
reject unknown fields. YAML configuration rejects unknown/duplicate fields and
multiple documents. Timestamps are UTC RFC3339. All token holders share authority.

`service_get.build`, worker backend identity and guest `build` identify the running
binaries by SHA-256, platform, Go version and linked source ID. The Nix product
links the filtered source identity into controller, worker, client and both guest
binaries; a development build without it reports `source_state: unlinked`.
`--version` on these executables emits the same JSON without configuration or
state access. Linux fingerprints `/proc/self/exe`, preserving the old running
image's identity after an executable path is replaced. A source label or version
alone does not establish deployment or acceptance.

Windows `guest.command_service` reports the resident service's build, protocol,
heartbeat and process creation identity. Readiness requires a fresh privileged
heartbeat from that live process and the same binary hash as the probed guest
executable. Updating only one executable cannot establish readiness. Linux uses
the installed executable for each systemd transient supervisor; its per-command
process identity is retained in command status.

Guest command protocol 2 confirms `started` after process launch, separates durable
stdin delivery from `written_input_sequence` in the child OS pipe, and reports an
OS boot identity for power readiness. Windows command and resident service binaries
must agree on the protocol/build; Linux supervisors use the installed matching
executable. Application consumption remains an application assertion. This protocol
belongs to VirtualBox guest supervision; NixOS tests use their Nix test driver.

Template metadata carries a configured fingerprint. Each cloned node records
`identity` from the resolved source UUID/snapshot and effective observed hardware,
including firmware and graphics settings when reported. Fingerprints omit source
names, UUIDs, private paths and credentials; they identify the selected snapshot
and configuration, not a full disk-content digest. Actual guest builds are
observed separately. VirtualBox guest ACPI S3 sleep and S4 hibernation are
unsupported (known backend limitation). Hypervisor pause/resume is supported and
is a separate operation. New-user Secret Service behavior remains unverified
without scoped execution evidence; input delivery alone is not application success.

Service-generated execution/node/command/upload/evidence/operation IDs are 32 lowercase
hex digits. Caller `request_id` is 1–128 ASCII identifier characters
(`A-Z a-z 0-9 _ . : -`, first character alphanumeric). Every mutation requires it.
Fixed defaults are normalized before hashing; environment-dependent defaults are
frozen at admission. The same ID and normalized parameters return the original
receipt, even after its target has been deleted; changed parameters return
`request_conflict`. Receipts have `pending`, `succeeded`, `failed` or
`uncertain` state, plus `id`, `request_id`, `tool`, `created`, `result` or `error`.
The response contains `operation` and `wait`; `operation_get` reads the same receipt.
Request digest, target ID, receipt and dispatch intent commit together before dispatch.
Receipts retain typed result identifiers and outcomes; credentials, command bodies
and complete resource snapshots are read separately. Dispatch stages are `prepared`,
`dispatched`, `unknown` and `confirmed`. Restart resumes a prepared intent after
execution recovery; an unconfirmed dispatched effect becomes `uncertain`. Native
input is never automatically repeated after an uncertain dispatch.

Errors are MCP tool errors with structured `{"error":{"code":"…","message":"…"}}`
and the same JSON in text content. HTTP errors use that envelope. Errors also carry
`category`, `retry` and `next_action`: retry is `after_change`, `after_recovery`,
`reconcile` or `inspect`, a prerequisite rather than permission to replay a side
effect. Reconcile the existing receipt and live result before issuing a new
mutation; only changed/corrected actions get a new request ID. Schema rejections
occur before allocating a receipt and identify fields without echoing their
contents. Conditional fields, enums, numeric ranges and standard character limits
are validated from the advertised schema; `x-maxBytes` additionally specifies
and enforces UTF-8 byte limits. Messages are safe summaries. Backend errors retain a bounded diagnostic in
`details`: `stage`, `exit_code`, `stderr`, `timed_out`. Configured credentials,
baseline identifiers and the controller state directory are redacted. Node and
cleanup failures can include `node_id`, `cause` or `last_error`. Stable codes include `invalid_argument`,
`not_found`, `invalid_state`, `request_conflict`, `conflict`, `policy_denied`,
`limit_exceeded`, `resource_busy`, `backend_error`, `network_degraded`,
`cleanup_required`, `input_order`, `input_closed`, `invalid_cache`, `immutable`,
`quota_exceeded`, `invalid_path`, `unauthorized`, `expired`, `host_denied`,
`origin_denied`, `worker_unavailable`, `worker_instance_changed`, `storage_pressure`, `gui_conflict`,
`guest_not_ready`, `initialization_failed`, `preparation_cancelled`, `network_lease_expired`, `node_stopped`, `backend_timeout`,
`backend_cancelled`, `backend_protocol_error`, `node_unavailable`, `cleanup_timeout` and `router_unavailable`.
`endpoint_open` distinguishes `network_not_ready` (inspect the network observation,
then recover explicitly when appropriate) from `endpoint_limit` (128 active
endpoints per lab; closing an endpoint releases capacity). `resource_busy` means capacity such as ports or guest
session slots could not be obtained within the call budget.
Nix worker rejections retain their original structured code and details, with
`boundary: nix_worker`, method and HTTP status. An unconfirmed response is an
execution observation failure requiring reconciliation of the existing task;
it is not evidence that the worker rejected or never accepted the request.

## Business waits and observation

Codex CLI **0.163.0 or newer** is required; configure `tool_timeout_sec = 660`.
Tools return structured JSON in SSE. Every 15 seconds the transport sends an SSE
comment, not an MCP notification, model-visible progress item or SQLite event.
Binary content uses the separate HTTP routes below.

Every mutating call returns `{operation, wait}`. `operation.state` describes the
dispatch; `wait.targets` describes the business outcome. Creation waits for lab
readiness or for Nix execution, evidence handoff and cleanup. `command_start`
defaults to `mode="finite"`, waiting for exit, process-group termination and
output retention. Explicit `service` or `interactive` mode waits for a confirmed
start. Finish/cancel/cleanup waits for resource release or a new actionable fault.
`command_input` waits for durable guest delivery. Power operations wait for the
requested power state; start/reboot/reset/resume also wait for guest readiness by
default. Reboot/reset require a new boot identity. `wait_until="power_state"`
selects the narrower power observation. A later power operation supersedes an
earlier pending one. Other mutations wait for their durable receipt to resolve.

Each call observes for at most **600 seconds**, including dispatch time. Early
completion returns immediately. Expiry returns `wait.reason="timeout"` and current
summaries; it is not execution failure. Pass the returned `continue` array to
`wait({targets: …})`. A 15-minute job normally needs the initial ten-minute call
and one five-minute continuation. Waiting and ordinary controls do not extend
execution deadlines.

A target is `{kind,id,condition,seen_revision}`. Kinds are `lab`, `nix_job`,
`command`, `operation`, `upload`; conditions are `ready` (execution), `started`
(command), `complete`, `cleaned` (execution), `changed`, or `output` (execution
or command), and `input_delivered`/`input_written` (command, with `sequence`). Inapplicable conditions are rejected. Up to 64 targets can be
observed together. Return when any target satisfies its condition or reports a
new failure/intervention state; include every target's summary, revision,
`satisfied` and `attention`. Reusing `continue` acknowledges the observed failure.
Ordinary revisions ignore transport heartbeats, healthy sampling and byte growth;
explicit `output` waits use byte `offset` and, for commands, `stream`. A missing
target returns `unavailable` and is excluded from `continue`; other targets remain
observable. State changes and their revisions commit in the same transaction.

HTTP disconnect, tool timeout and MCP cancellation stop only that observation.
Accepted dispatch and execution belong to the service. Use `command_cancel`,
`nix_job_cancel`, `lab_cancel` or `lab_finish` to stop business execution. Native
input with an unconfirmed dispatch becomes `uncertain` and is never replayed
automatically. Retrying the identical `request_id` retrieves its receipt.

## Tools

`tools/list` supplies exact JSON schemas. Fields below are in addition to
`request_id` for mutations. Resource IDs are sufficient to address their owner:
node and command tools do not require a second lab ID. Shared log/event/deletion
tools use `execution_id`, equal to the lab or Nix job ID. No public tool accepts a
host executable, directory, VM UUID or raw backend option.

| Tool                                                | Parameters and result                                                                                                                                       |
| --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `service_get`                                       | No parameters; build, templates/policy, backend availability, active execution and limits                                                                   |
| `service_observe`                                   | Optional `seconds` (1..240, default 30); bounded host/controller samples and call timings                                                                   |
| `lab_create`                                        | `nodes`, optional ready `upload_ids`, `lan`, `dhcp`, `dns`, `internet`, `timeout_seconds`; queued lab, waits for readiness                                  |
| `nix_job_create`                                    | Ready `upload_id`, optional `timeout_seconds`; derivation/system come from its manifest, waits for execution and cleanup                                    |
| `lab_list`, `nix_job_list`                          | Optional sequence `cursor`, `limit`, `phase` (`active` default, `all`, `queued`, `preparing`, `running`, `ended`)                                           |
| `lab_get`, `lab_inspect`                            | `lab_id`; compact summary or node inventory, network and resource ledger                                                                                    |
| `nix_job_get`                                       | `job_id`; compact execution, worker result and resource release                                                                                             |
| `lab_finish`                                        | `lab_id`, `conclusion`, `summary`; record the caller's conclusion and await cleanup                                                                         |
| `lab_cancel`, `lab_cleanup`                         | `lab_id`; cancel or retry cleanup and await resource release                                                                                                |
| `nix_job_cancel`, `nix_job_cleanup`                 | `job_id`; cancel or retry evidence handoff/resource cleanup                                                                                                 |
| `lab_extend`, `nix_job_extend`                      | `lab_id` or `job_id`, positive `seconds`, `reason` (1..1024 bytes)                                                                                          |
| `execution_delete`                                  | `execution_id`; delete a cleaned execution and unpinned detail; independent pins/receipts remain                                                            |
| `execution_events`, `execution_log`                 | `execution_id`; event `cursor`/`limit` or byte `offset`/`limit`                                                                                             |
| `operation_get`                                     | `operation_id`; compact durable receipt                                                                                                                     |
| `wait`                                              | `targets`; business observation described above                                                                                                             |
| `node_get`                                          | `node_id`; power, expected transition, preparation, health and capabilities                                                                                 |
| `lab_diagnose`                                      | `lab_id`, optional `node_ids`; bounded diagnostic manifest and evidence IDs                                                                                 |
| `node_exec`                                         | `node_id`, absolute guest `argv`, optional `cwd`, `env`, `timeout_seconds` (10 default, 30 maximum); direct Guest Control                                   |
| `command_start`                                     | `node_id`, `argv`, optional `mode` (`finite`, `service`, `interactive`), `context` (`service`, Windows `desktop`), absolute `cwd`, `env`, `timeout_seconds` |
| `command_list`                                      | `lab_id`, optional `cursor`, `limit`; command summaries                                                                                                     |
| `command_get`, `command_details`                    | `command_id`; compact status or argv/cwd/environment                                                                                                        |
| `command_read`                                      | `command_id`, `stream` (`stdout`/`stderr`), optional byte `offset`, `limit`; base64 output                                                                  |
| `command_input`                                     | `command_id`, `sequence`, optional UTF-8 `data`, `eof`; persist and deliver stdin                                                                           |
| `command_input_read`                                | `command_id`, optional `sequence`, byte `offset`, `byte_limit`; bounded retained stdin                                                                      |
| `command_cancel`                                    | `command_id`; cancel the supervised process group and await completion                                                                                      |
| `upload_create`                                     | `kind` (`file`/`cache`); independent input and scoped manifest/object upload URL                                                                            |
| `upload_get`, `upload_delete`                       | `upload_id`; fresh capability/status, or delete an unbound upload after writes settle                                                                       |
| `upload_list`                                       | Optional `lab_id`, `cursor`, `limit`; upload summaries                                                                                                      |
| `upload_objects`                                    | `upload_id`, optional object-name `cursor`, `limit`; declared sizes/hashes and publication state                                                            |
| `guest_file_put`                                    | `node_id`, ready `upload_id`, absolute guest `path`; bind to one lab and deliver                                                                            |
| `guest_file_get`                                    | `node_id`, absolute guest `path`; guest-created tar evidence                                                                                                |
| `node_control`                                      | `node_id`, `action` (start/shutdown/reboot/poweroff/pause/resume/reset), optional `wait_until` (`ready` default, `power_state`)                             |
| `network_set`                                       | `lab_id`, optional `internet`, `dhcp`, `dns`, `nodes: [{node_id, ipv4}]`; policy result and guest refresh requirement                                       |
| `network_recover`                                   | `lab_id`; explicitly validate router/protection/ports and restore networking                                                                                |
| `node_network_link`                                 | `node_id`, `enabled`; connect/disconnect the lab NIC                                                                                                        |
| `lab_gui_set`                                       | `lab_id`, `max_guest_resolution` (`any`/`auto`); global policy restored during cleanup                                                                      |
| `endpoint_open`                                     | `node_id`, `protocol` (`tcp`/`udp`), `port`; endpoint ID and allocated host port                                                                            |
| `endpoint_list`, `endpoint_close`                   | `lab_id` with optional `cursor`/`limit`, or `endpoint_id` to revoke                                                                                         |
| `node_screenshot`                                   | `node_id`, optional `screen`; PNG evidence                                                                                                                  |
| `node_display`                                      | `node_id`, optional `width`, `height`, `screen`, `auto_resize`; size pair or resize policy                                                                  |
| `node_input`                                        | `node_id`, `action` (move/click/drag/scroll/release/scancodes); screen, coordinates, button, wheel, relative mode or hex scancodes                          |
| `node_media`                                        | `node_id`, `device` (camera/audio_input/audio_output), `enabled`, configured camera alias when applicable                                                   |
| `capture_start`                                     | `node_id`, `kind` (`video`/`nic`/`metrics`); capture ID                                                                                                     |
| `capture_list`, `capture_get`, `capture_stop`       | `lab_id` with optional `cursor`/`limit`, or `capture_id` for status/stop and evidence collection                                                            |
| `evidence_list`                                     | Optional `execution_id`, `cursor`, `limit`; independent retained evidence metadata                                                                          |
| `evidence_get`                                      | `evidence_id`; provenance and a fresh scoped download URL                                                                                                   |
| `evidence_pin`, `evidence_unpin`, `evidence_delete` | `evidence_id`; retain until unpin, or delete unpinned evidence after confirmed cleanup                                                                      |

A node has `name`, `template`, optional `ipv4`, `cpus`, `memory_mib`, `gui` and
`media` booleans (`camera`, `audio_input`, `audio_output`, `accelerate_3d`). Names
are lowercase DNS labels beginning with a letter, up to 31 characters. One lab
has 0–64 test nodes, capped further by subnet address capacity; its router uses the
first host address. Node names, MACs and IPv4 addresses must be unique. Network
and broadcast addresses are unavailable. Media defaults off even if policy permits
it. Set a node `camera` alias when requesting `media.camera`; runtime camera operations also require an allowed alias. 3D is selected before boot. Router IDs are not exposed as test-node targets.

An explicit `nodes: []` creates only the router, using `virtualbox.router.iso` without any
registered templates. This supports boot and dynamic policy checks before guest
baselines are prepared. Missing or null `nodes` is rejected. Router-only labs do
not change the global GUI policy. Client DHCP, LAN forwarding and published
services require test nodes.

List limits default to 50 and cap at 200; log windows default to/cap at 65536 bytes.
Stdin starts at sequence 0 and accepts no new input after EOF. Each UTF-8 packet
is at most 64 KiB, with at most 4096 packets and 64 MiB per command, including EOF.
Larger inputs use file uploads. `stdin.next_sequence` is the controller's durable
acceptance cursor; `delivered_sequence` is durable guest delivery;
`written_sequence` confirms writing to the child process's OS pipe. Each cursor
names the next sequence at that boundary. Application consumption requires an
application-level assertion. EOF can be delivered before the child drains the pipe.

Bodies are immutable files; SQLite stores sequence, length, checksum, EOF and
progress with the state revision. `command_input` waits for delivery; explicit
`wait` condition `input_written` with a packet's `sequence` waits for the pipe.
The outbox retries unconfirmed delivery, and the guest accepts identical sequence
retransmissions, including after exit. `command_input_read` returns base64 byte
windows with continuation coordinates; `command_details` reads only the spec.
Status changes do not rewrite payloads or load historical stdin into memory.
Command logs cap at 64 MiB per stream; the process continues after that limit.
VBox diagnostic logs cap at 1 MiB per collection and are collected at termination when available. Captures use the first NIC or display. Metrics retain the latest 3600 one-second
samples. Video caps at 1 GiB. A lab may have at most 128 active endpoints; closed history does not consume this limit. Guest archives remain opaque on the host.

## Lifecycle and recovery

`lab_list` defaults to tasks that are executing, queued or still require cleanup.
It projects identity, timing, phase, result classification, resource counts,
activity counts and extension reason/deadline, without command inputs, artifacts,
logs or conclusion text. `lab_get` is also compact; `lab_inspect` exposes the
resource ledger; command, endpoint, capture, upload and evidence histories have
separate SQL keyset readers. Sequence cursors remain usable after older rows expire.
Empty items mean no further matching records at observation time; start a fresh
scan to observe changing phases. Completed commands and stopped captures leave
live memory even while their lab remains active.

`lab_get` and list items expose three independent fields:

- `phase`: `queued`, `preparing`, `running`, `ended`. Queued tasks include
  `waiting_reason` and `queue` with position and blocking task ID. Reasons include
  `executor_occupied`, `cleanup_pending`, `cleanup_blocked`, `earlier_submission`,
  `admission_pending`, and backend-specific unavailability. Queue projections
  reflect current state; mutation receipts remain historical snapshots.
- `result`: null until execution ends, then `termination` (`finished`, `failed`,
  `cancelled`, `timed_out`, `interrupted`), `conclusion`, `summary`, `reason`, `error`.
  Lab conclusions come from `lab_finish`; Nix conclusions come from the build.
- `cleanup`: `state` (`pending`, `running`, `complete`, `blocked`), `errors` and
  resource counts. `lab_inspect.cleanup` includes resource records containing `id`, `name`, `state`, `steps`. Resource state is
  `planned`, `allocated` (creation intent may still be in progress),
  `never_allocated`, `released` or `release_failed`. Each step records
  state, attempt count, update time and any error. Task events carry phase, result
  and cleanup state at the event time.

One continuous test batch occupies the scheduler until resource cleanup is
confirmed. Prepare inputs locally, collect bounded evidence, finish/cancel and
wait for complete cleanup before lengthy local work. Queued abandonment uses
`lab_cancel`; no VM deletion is needed for `never_allocated` resources. Activity
counts cover supervised commands, open endpoints and captures; detached guest
services are not inferred from these counts. Empty counts and HTTP disconnects
do not trigger task destruction. `lab_extend` records the continued-work reason
and new deadline in `hold`; independent node startup budgets remain unchanged.

An offline worker prevents fresh storage accounting; new labs, Nix jobs, uploads
and queued starts pause while existing execution/control/cleanup remain usable. A queued Nix job which has not been submitted fails with
complete cleanup if the worker disappears. Once build submission may have occurred,
daemon cleanup must be confirmed locally.

Nix `nix_job_get.execution` records `stage` (`import`, `validate`, `build`, `collect`
or `unknown`), a structured `error`, and the worker's completion time when known.
Its `process` identifies the observed stage, `source: nix_client_wait` (or
`unconfirmed` when no wait result exists), whether
the client exited, and either the actual `exit_code` or `exit_signal`. A policy
rejection can follow a successful metadata command; artifact collection can fail
after a successful build. Neither situation changes that command's exit result.
These observations describe the Nix client, not a QEMU process or confirmed daemon
teardown. Cancellation and task deadlines remain under `result`, independently of
the recovered execution result. An unknown result remains unconfirmed. A terminal storage
failure appears as `persistence_error` and keeps cleanup blocked; failed local
reconciliation cannot release scheduler ownership.

Deadlines start when execution begins, default to one hour and allow explicit
extension up to 24 hours total. `tasks` configures these limits and a cleanup budget
(default 5 minutes). Node operations do not reset task deadlines. Cleanup starts by
closing published entries and cancelling in-flight calls, then collects bounded
evidence and deletes registered resources. Evidence uses at most 30 seconds and
one third of the remaining cleanup budget. Node deletions run independently, with
up to three attempts and at most 30 seconds per attempt. Ownership conflicts stop
that resource's retry. Each step exposes start time, deadline, attempts and error.
After power-off, deletion waits for the SDK's distinct `session_unlock` boundary,
then rechecks VM ownership and power state before detaching media and unregistering.
It does not kill the GUI session owner to force release. Unknown or stalled unlock
retains a blocked cleanup with the last observed session state. Partial progress is persisted; `lab_cleanup` can retry the
remaining VirtualBox resources with a new request ID. Cleanup failure keeps the
scheduler occupied. Restart interrupts active tasks and resumes registered cleanup;
a result already recorded remains unchanged.

### Node observations and command results

`node_get` returns observed VM `state`, `preparation` (`pending`, `creating`, `starting`,
`guest_control`, `initializing`, `ready`, `failed`, `cancelled`), `expected` power transition,
`health` and `capabilities`. Health separates `virtualbox`, `guest_control` and
`commands`. Observations contain `state`, `checked`, `last_success`, `failure_since`,
consecutive `failures`/`successes` and `error`. An absent observation is `unknown`;
other states are `available`, `suspect`, `unavailable` and `expected`.

Preparation records its start, current stage start, independent startup deadline,
last update and error. The public task's `router` supplies the router's preparation
and health without making it a test-command target. Parallel preparation keeps
the initiating failure's node, stage, elapsed time and budget; cancelled peers
retain that cause instead of reporting a second startup timeout.
During preparation, probes begin two seconds apart and back off with bounded
jitter to at most 30 seconds while the stage remains unchanged. Advancing to a
new stage resets the delay. The router and client nodes expose this schedule in
`polling`; readiness or failure clears it. Waiting never extends the startup
deadline or delays cancellation.

Node `planned_ipv4` is the requested reservation. `network_observation` contains
guest-observed interface names, MACs, link state, address prefixes and observation
time. Its state is `observed`, `partial`, `unavailable` or `unknown` (no compatible
guest observation received). Missing data is not an empty successful observation.
These fields do not prove DHCP origin, DNS, route reachability or application bind
success. Assert those boundaries in the test. The router applies DHCP/DNS and
host-protection policy before starting clients, independently of slow peer readiness.

A probe has a 10-second total deadline and normally runs every 5 seconds without
overlapping another probe of the same node. Failures use exponential retry delay
with bounded jitter, capped at 30 seconds. Node and command `polling` report the
next attempt, interval and reason. Quiet commands gradually poll every 5 seconds;
changed state, acknowledged input or new output returns to a 1-second interval.
When a node's channel is unavailable or intentionally stopped, its recovery probe
owns reconciliation; individual command polls wait for that channel. Output
collection deadlines still expire during an outage. Three actual failures sustained for
`virtualbox.unavailable_after` (default 30 seconds) mark the capability unavailable;
local capacity waits do not count as backend failures. Two successful observations
recover it. Explicit unexpected VM termination or a failed initialization unit is
reported immediately. Intentional pause/shutdown and bounded restart transitions
have expected states. Running faults collect evidence and retain the lab, including
healthy nodes. They do not recreate/restart it. A responsive guest can continue initializing up to the startup deadline. Once
Guest Control is established, sustained control loss or an unresponsive command
service uses the shorter unavailability budget. Initialization failure ends the
whole lab and cleans it after bounded evidence collection; a new run requires a
new `lab_create` request.

Guest Control has four local slots per VM. Every completed VBoxManage invocation
with a confirmed reaped host PID enters a bounded release ledger. Before admitting
more calls after four unconfirmed invocations, the controller inspects sessions
through the native C API, without opening another guest session. Concurrent
completion can leave at most seven ledger entries. Matching requires the exact
invocation name (PID, clone UUID and operation), configured user, task-owned VM
configuration and a presently absent host PID. A live/reused PID, a foreign user
or an unmatched session is retained. The same SDK session object is closed and
its absence is verified. No wildcard or global session close is used.

Node `guest_sessions` reports observation/attempt times, nullable before/after
counts, matching and close results, HRESULT on failure, unconfirmed and in-flight invocations
and actual SDK runtime version. Failed observation preserves previously observed
counts with their timestamp; no initial observation has null counts. Reconciliation
uses three seconds and retries release failures no faster than every five seconds.
Unconfirmed release, 24 observed sessions or increasing unattributed sessions block
new calls with `resource_busy`. When an in-flight invocation could explain a count
increase, `attribution_pending` is true: new calls wait within their own budget
for those invocations to finish, then reconcile their exact identities. The
unattributed baseline is established only when no invocation is in flight.
A forking executable wrapper or a changed session
naming convention is unverified until the observed identities can be reconciled;
it does not authorize closing unknown sessions. Resource release drops the local
VM slot and session ledger. These limits bound control traffic, not OS sleep or
application performance guarantees.

Guest Control is tested independently of the prepared command service. The live
probe must report this controller's `command_protocol`; a mismatch makes the
command capability unavailable. Version/hash identify the deployed build.
`guest.command_environment` describes supported contexts and defaults. Commands
default to `context=service`: Linux root or Windows LocalSystem. Windows desktop
work explicitly requests `desktop`; an unavailable logged-in administrator token
is a terminal startup rejection without a process exit code. Linux desktop
fixtures select their own user and session explicitly. No context uses a login
shell. The default cwd is the selected account's home. Linux sets HOME, USER,
LOGNAME and PATH per command, retaining other system-manager service variables.
Windows inherits the selected token's environment. Explicit env overrides win;
Windows names are case-insensitive and duplicate case variants are rejected.
`execution` records the actual context, account identity, cwd and explicitly
configured variable names without their values. This metadata is evidence about
process setup, not application success. `node_exec` instead inherits Guest Control
defaults and is a bounded diagnostic escape hatch.

Command polling retains the last confirmed execution state and a
separate `observation`. `command_cancel` persists `cancellation: requested` and
retries delivery until a final guest record or VM release. Delivery receipts do
not confirm termination. Commands expose `stage`, `final`, timestamps, optional
actual `exit_code`, `exit_signal`, `termination` and `output` independently.
On Linux, a signal exit has `exit_signal` (for example `SIGKILL`) and no
`exit_code`; `core_dumped` records the kernel's core-dump status when true.
An ordinary Linux exit or Windows process exit retains its actual numeric code.
A final state is
`exited`, `cancelled`, `timed_out`, `failed` or `uncertain`; a nonzero exit is still
`exited`. `termination` records reason, request time, known delivery, process exit,
process-group stop, confirmation source and phase error. Unknown delivery is absent.
`output` reports complete/incomplete drainage, byte counts, truncation flags and
SHA-256 of each completely drained stream. An output failure preserves an
independently observed exit code.

Submission leaves a command `starting` until a guest record confirms execution.
An absent first record is `observation.state: pending`, without a failure count,
until `status_deadline`: 30 seconds after the submission attempt returns, within
the total 120-second submission plus 30-second observation budget. Missing status
after that deadline reports `command_status_missing` and `uncertain` execution;
it does not invent an exit or stop reconciliation. A previously observed record
disappearing reports `command_status_lost`. A later valid record can resolve the
uncertainty. Linux boot/process start identity and Windows process creation time
distinguish an ended supervisor from a live process or a reused PID. An unreadable
identity is unknown, not evidence of death. Confirmed supervisor loss preserves
the last exit evidence and reports a final `uncertain` record; reconciliation
write failure is returned explicitly by the reader.

Each poll returns status and at most 256 KiB from each output stream in one Guest
Control call. Chunks carry base64 bytes, byte offsets and SHA-256; the controller
verifies and appends them, checking replayed prefixes after interrupted writes.
Streams retain at most 64 MiB each. Execution `final` and `retained_output` are
independent: each retained stream is `pending`, `collecting`, `complete` or
`incomplete`, with confirmed bytes and any failure. Final collection has a visible
60-second deadline; catch-up performs at most 16 reads in a 10-second poll budget.
A complete stream matches the supervisor's final size and SHA-256. A missing,
changed, truncated or unconfirmed stream never becomes complete merely because
the current bytes ended. Task release bounds evidence collection and marks any
unfinished stream incomplete without changing the execution result.

`command_read` returns `encoding: base64`, the requested byte `offset`,
`next_offset`, `at_end` of currently retained bytes, `retained` state and
`execution_final`. Its `eof` is true only at the end of a completely retained
stream. During execution an empty read can have `at_end: true, eof: false`.
Offsets remain byte offsets for binary, Unicode and invalid UTF-8 output.
Read both streams until complete or explicitly incomplete before accepting an
assertion that depends on their contents.

Guest startup has a 30-second budget. Termination and exit confirmation each have
five seconds; output and input settlement each have two seconds. These budgets
apply after the command's own deadline/cancellation and do not extend the task's
lease. Adapters which fail to confirm a phase produce a final `uncertain` result.
Late startup is disposed of by its owner. Windows uses finite process waits and
checks Job active-process counts; inherited output handles have a separate drain
deadline. The owning Windows service and identity-aware reader reconcile a
supervisor that ended without a final record, preserving any previously observed exit code. Status replacement
retries sharing conflicts for at most 500 ms and reports write failure.

VM deletion records `release: {source: "vm_deleted", time: …}` separately. When no
final guest result was observed, the execution remains `uncertain` with no invented
exit code; cleanup does not replace it with `cancelled`. A final result and its
failure details survive resource release. Final uncertainty remains a test failure
or an inconclusive assertion to resolve, not successful command execution.

### Diagnostics and direct guest execution

`service_get.performance` describes the passive monitor and its latest sample.
`service_observe` selects a recent window: one sample every two seconds, at most
120 samples and 512 completed calls. Ring eviction is explicit in the window's
truncation flags. A window with no samples is `no_data`; each metric separately
reports `observed`, `unavailable` or `unsupported`. Samples include collection
time/bytes, timer delay, aggregate CPU counters, runnable threads, CPU/memory/I/O
pressure, and the controller's visible cgroup v2 quota/throttling counters and
ancestors (at most 16). Missing counters are absent, not zero. Counter deltas require
matching cgroup identity and monotonic values. The visible cgroup mount may exclude
outer limits; these observations do not describe the separate Nix worker's group.

Call records distinguish local capacity/mutex/dispatch wait from execution time.
Host process CPU times are present when the process runner provides them; omitted
times are unknown. Scopes distinguish queries, asynchronous operations, VirtualBox
and Guest Control. Nested calls overlap and must not be summed as independent work.
No arguments, environment, stdin, output contents or cgroup paths enter telemetry.
Host-call read bytes count captured stdout/stderr, not guest file-transfer volume.

Preparation, network, capability and cleanup faults retain 30 seconds before and
up to 30 seconds after the trigger in a `performance.json` artifact. Nearby faults
coalesce per task. Collection does not retain VMs or the scheduler; the pending
deadline is exposed as `lab_get.evidence_pending_until`. Service shutdown shortens
the after-window; explicit task purge discards pending evidence. Diagnostics also
include the available window around collection. These are observations for causal
analysis, not automatic CPU diagnoses. Sampling semantics follow Linux's
[PSI](https://docs.kernel.org/accounting/psi.html),
[cgroup v2](https://docs.kernel.org/admin-guide/cgroup-v2.html) and
[/proc](https://docs.kernel.org/filesystems/proc.html) interfaces.

`lab_diagnose` uses a shared 30-second budget. Windows PowerShell collectors
receive up to 10 seconds per call to accommodate process startup; other items
receive up to 5 seconds. Every call also respects the remaining shared budget. Its
manifest lists each node's observation, selected VM metadata, available VBox log,
screenshot, initialization and service/kernel evidence. An item is `collected`,
`partial`, `unavailable` or `skipped`; successful items survive another item's
failure. Omitting `node_ids` includes the router; selecting nodes accepts ordinary
test-node IDs only. Preparation and running faults use the same collector.

The private router's COM1 feeds a receive-only collector over a task-owned Unix
socket. Other serial ports and all test-node serial ports remain disabled. The
collector starts before VM power-on and retains the first 1 MiB of console bytes
in private runtime storage, continuing to drain excess output. It does not
depend on Guest Additions, the supervisor or LAN readiness. `boot-console.log`
captures the router's primary console, including initrd and service output;
kernel messages also remain on its firmware framebuffer. Native GPU modesetting
is disabled for this headless appliance. The console artifact
is included in router diagnostics and termination evidence, with the same
artifact size/hash verification as other files. Its result reports collector
`state` (`listening`, `connected`, `disconnected`, `stopped` or `interrupted`),
`limit_bytes`, `retained_bytes`, `observed_bytes`, `truncated` and any collection
error. Unknown totals/truncation are null after controller restart; retained
bytes survive for recovery collection. Truncation, unknown coverage or a
collection error makes the manifest item `partial`. Empty output does not prove
that the guest booted. Resource cleanup stops the collector and removes its
socket; controller shutdown closes collectors without deleting retained bytes.

`node_exec` and screenshots are available during preparation and for surviving
resources of an ended, blocked task. Direct commands use configured credentials,
absolute guest executables and argv; they do not depend on the supervisor or lab
LAN. Timeouts include local capacity/lock waits. The receipt contains `state`,
`termination_confirmed`, `exit_code` when known, and `evidence_id`. Retrieve that
evidence through `evidence_get` to read `node-exec.json`: base64 `stdout` and
`stderr`, truncation flags, `backend_exit_code`, `exit_code` when known,
`exit_code_min` for a truncated large exit status, `termination`,
`termination_confirmed`, `state` and `error`. Stdout is bounded to 1 MiB, stderr
to 8 KiB. The stderr stream can include VBoxManage diagnostics.

A completed nonzero guest exit is a completed operation, separate from an
application assertion. VBoxManage maps guest exits 1..93 to host codes 33..125;
host code 126 confirms a guest exit of at least 94. Timeouts can leave termination
unconfirmed, in which case both result and receipt remain `uncertain`; retries
with the original request ID retrieve that receipt. These mappings follow
[VirtualBox's Guest Control implementation](https://github.com/VirtualBox/virtualbox/blob/main/src/VBox/Frontends/VBoxManage/VBoxManageGuestCtrl.cpp).
Use `command_start` for durable output, stdin and process-group supervision.

Endpoint ports default to 20000–29999; each entry uses a public relay port and
a loopback NAT port. Up to 128 declared entries and 256 relay connections/UDP peers
per entry are supported. UDP peer mappings expire after 30 seconds idle. An egress
lease defaults to 15 seconds and is refreshed every five seconds. `network.state`
is `pending`, `ready`, `suspect` or `degraded`; this describes policy observation,
independently of guest addresses. It includes the conservative `lease_expires`,
`policy_updated`, router node ID/boot fingerprint, and the last attempt's stage,
error, total budget, lock wait and execution duration. One heartbeat deadline
covers both waiting and execution. A timeout or occupied control channel can
remain `suspect` only while the unchanged, previously confirmed policy lease is
valid. Existing relays remain open in that window; a subsequent confirmation
returns to `ready`. Host-protection changes, identity/policy errors and expired
leases close relays, suspend entries and require explicit recovery. Degradation
and late-observation events retain the trigger and lease/identity evidence.
Source addresses may be rewritten.
Explicit recovery verifies router ownership, starts or resumes it when needed,
reinitializes its interfaces through Guest Control, then reapplies protection and
declared entries. Heartbeats never restart a router automatically or accept a changed router boot
identity. Lease expiry requires explicit recovery. Live ISO boot has closed egress
and entries; its retained seed reconstructs credentials after reboot. A lab's LAN
subnet cannot be changed. DHCP toggles/address registrations do not configure an
already running guest; use commands to renew or configure it.

`GUI/AutoresizeGuest` defaults false per clone. Global `GUI/MaxGuestResolution`
defaults any for a lab. The controller journals original/expected/pending values
before writes, restores the original on cleanup/restart, and holds cleanup on an
external-value conflict. There is no arbitrary extradata or generic VM-settings API.

## Binary transfers and retention

Capability URLs use `/files/CAPABILITY[/OBJECT]`. HMAC binds resource, read/write
right and expiry independently of the MCP token; Host/Origin checks still apply.
Capabilities last one hour. `upload_get` and `evidence_get` issue fresh URLs without
changing the resource or adding a mutation receipt. GET/HEAD support HTTP ranges.
PUT returns 200 after durable publication. Binary requests have a five-minute
budget including lock wait. Keep capabilities private.

Allocate an upload before acquiring an execution slot. PUT an immutable manifest
to `URL/manifest`, then PUT its named objects. The manifest is:

```json
{
  "upload_id": "ID",
  "objects": [{ "name": "blob", "size": 3, "sha256": "SHA256_HEX" }]
}
```

Cache manifests additionally bind `derivation` and `system`. The declared byte
sum reserves input budget before object writes. Sizes may be zero. Repeating an
identical manifest or object is safe; changed declarations or bytes conflict.
The upload becomes `ready` automatically after all declared objects are durably
published and verified. There is no finalize call. `upload_objects` pages declared
sizes/hashes and publication state. `upload_get` returns `execution_id`, state,
revision, byte counts, `can_upload` and `uploading`.

Ready file inputs bind to one lab through `lab_create.upload_ids` or the first
`guest_file_put`. They can serve multiple nodes without duplicate retained host
copies. Nix inputs bind to one job. `upload_delete` applies to unbound uploads and
waits for active writes to settle. Execution cleanup interrupts outstanding writes
and confirms settlement; complete lab inputs then follow its evidence lifetime.
Unbound uploads expire after one idle hour.

File objects are named `blob`. Cache objects are `nix-cache-info`, Nix store hash
`.narinfo` files and relative `nar/*.nar[.xz|.bz2|.zst]` files. Narinfo is bounded
to 64 KiB; names, sizes, SHA-256, references and relative URLs are verified against
the complete declared closure. Nix verifies NAR content and signatures on import.
Each immutable file is synced before its index row commits. Interrupted uploads
and unreferenced publication files are reclaimable without changing accepted
objects. A manifest holds at most 100000 objects. File access uses directory
handles and rejects symlinks. Guest archives remain opaque on the host.

Top-level `state_dir` is a root: controller data lives in `controller/`, worker data
in `nix-worker/`. Optional `nix.state_dir` overrides the worker's root, retaining
its role suffix. Both roots may be the same path. Each role owns its private 0700
directory, lock, SQLite database and files; overlapping final directories and
symlink boundaries are rejected. Different service accounts need an administrator
prepared parent directory and correctly owned role children. Worker database
identity persists across restarts and is bound into accepted Nix jobs. A replacement
worker database cannot confirm that an old execution was never accepted.

Both roles require fresh **schema 21** databases and guest protocol **2**. Before
initial deployment the user stops old services, confirms resources are released,
backs up needed evidence and provides empty state/new configuration. Baselines and
the host Nix store remain independent; update baseline guest executables through
[administrator preparation](preparation.md#service-supervision). Subsequent recovery
tests retain the new state.

One Go module separates VirtualBox and Nix domain repositories from shared
SQLite/file primitives. SQLite stores indexed state, identity and recovery facts:

| Records                      | Purpose and lifetime                                                                           |
| ---------------------------- | ---------------------------------------------------------------------------------------------- |
| Executions, lab/Nix state    | FIFO sequence, result and cleanup ownership; compact terminal summaries                        |
| Nodes and resource ledger    | Reconcile VM sessions, clones, network entries, captures and display policy                    |
| Commands and input rows      | Command state/spec reference; ordered stdin references and delivery progress                   |
| Operations                   | Request digest, typed receipt, target and dispatch stage/payload reference; independent expiry |
| Uploads and declared objects | Ownership, reservation, expected size/hash and durable publication reference                   |
| Evidence                     | Independent provenance, size/hash, file reference, protection, pin and expiry                  |
| Events                       | Real state changes and faults; no healthy samples or wait history                              |
| Worker jobs/files            | Execution intent/result, release and handoff confirmation; direct store references             |
| Store metadata               | Role and durable instance identity                                                             |

Command bodies, stdin, logs and binary data are files. State changes update changed
entity rows, without rewriting histories or file inventories. Startup loads only
active/recovery objects; historical queries use bounded SQL readers. Healthy
samples remain in bounded memory. Pins retain reading metadata independently of
execution summaries and idempotency receipts.

| Data                                                        | Default retention                               |
| ----------------------------------------------------------- | ----------------------------------------------- |
| Active execution, unresolved cleanup, recovery dependencies | Until confirmed release                         |
| Successful evidence and detailed diagnostics                | 1 day after execution terminal time             |
| Other terminal evidence and detailed diagnostics            | 7 days after execution terminal time            |
| Compact execution summary                                   | 30 days after its terminal time                 |
| Idempotency receipt                                         | 30 days after the operation's own terminal time |
| Worker receipt                                              | 30 days after handoff and local copy release    |
| Pinned evidence and reading metadata                        | Until unpin                                     |
| Unbound inactive upload                                     | 1 idle hour                                     |

Evidence has a **40 GiB budget** and **32 GiB reclamation low watermark**. It includes
both roles, pinned/protected files and temporary physical copies; hard links count
once per inode within each role. An unavailable worker is unknown usage, not zero.
Pressure reclaims the oldest successful evidence before the oldest other evidence.
If protected content prevents meeting the budget, new labs/jobs/uploads and queued
starts pause. Existing execution, control, diagnosis and cleanup continue; admission
reopens after space recovery. Scans, worker accounting and reclamation run outside
the controller's shared control mutex. Input staging reserves **16 GiB total** and
at most **8 GiB per upload**; active dependencies are protected.

Nix inputs use an upload-bound fresh top-level derivation with substitution disabled;
dependencies keep their cache identities. Reliable import releases upload caches
when recovery no longer needs them. Worker evidence metadata is paged and file bytes
stream directly over authenticated Unix HTTP from registered store outputs. The
controller verifies size/hash, syncs files and commits indices before acknowledging
the complete manifest digest. Handoff releases worker logs/copies and GC roots;
interrupted acknowledgements resume physical and index cleanup. Host Nix-store GC
is independent. Clone disks and build work directories are runtime resources.
`nix.keep_failed: true` is an administrator diagnostic option with separate cleanup
responsibility; normal failure retention includes logs and exported files.

TTL collection physically deletes eligible files and rows in bounded batches,
including events and receipt payload references, with WAL checkpoints and incremental
vacuum. Unreferenced bodies and publication remnants have restart/sweep paths.
`execution_delete` removes a cleaned summary and unpinned details immediately;
independent pins and unexpired receipts remain readable. Pinning never extends VM
or execution-slot ownership.
