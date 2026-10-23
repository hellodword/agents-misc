# VM Test MCP 0.10 contract

## Transport and identity

`/mcp` implements official SDK Streamable HTTP. Every request requires the configured
Bearer token. Empty or omitted Host/Origin allowlists permit any value; nonempty
lists require exact matches (requests without Origin remain allowed).
JSON is bounded to 1 MiB and depth 64, rejects duplicate fields, and tool arguments
reject unknown fields. YAML configuration rejects unknown/duplicate fields and
multiple documents. Timestamps are UTC RFC3339. All token holders share authority.

Service-generated task/node/command/transfer/artifact/operation IDs are 32 lowercase
hex digits. Caller `request_id` is 1–128 ASCII identifier characters
(`A-Z a-z 0-9 _ . : -`, first character alphanumeric). Every mutation requires it.
The same ID and decoded parameters return the original receipt; changed parameters
return `request_conflict`. Receipts have `pending`, `succeeded`, `failed` or
`uncertain` state, plus `id`, `request_id`, `tool`, `created`, `result` or `error`.
Poll `operation_get`; reconnecting does not create another execution. A pending
receipt found on controller restart becomes uncertain and is never blindly replayed.

Errors are MCP tool errors with structured `{"error":{"code":"…","message":"…"}}`
and the same JSON in text content. HTTP errors use that envelope. Messages are safe summaries. Backend errors retain a bounded diagnostic in
`details`: `stage`, `exit_code`, `stderr`, `timed_out`. Configured credentials,
baseline identifiers and the controller state directory are redacted. Node and
cleanup failures can include `node_id`, `cause` or `last_error`. Stable codes include `invalid_argument`,
`not_found`, `invalid_state`, `request_conflict`, `conflict`, `policy_denied`,
`limit_exceeded`, `resource_busy`, `backend_error`, `network_degraded`,
`cleanup_required`, `input_order`, `input_closed`, `invalid_cache`, `sealed`,
`quota_exceeded`, `invalid_path`, `unauthorized`, `expired`, `host_denied`,
`origin_denied`, `purged`, `worker_unavailable`, `gui_conflict`,
`guest_not_ready`, `initialization_failed`, `node_stopped`, `backend_timeout`,
`backend_cancelled`, `backend_protocol_error`, `node_unavailable`, `cleanup_timeout` and `router_unavailable`.

## Tools

`tools/list` supplies exact JSON schemas. Fields below are in addition to
`request_id` for mutations. A node or command ID must belong to the specified task.
No public tool accepts a host executable, directory, VM UUID or raw backend option.

| Tool                          | Parameters and result                                                                                                                                                          |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `service_get`                 | No parameters; version, templates/policy, backend availability, active task and limits                                                                                         |
| `lab_create`                  | `nodes`, optional `lan`, `dhcp`, `dns`, `internet`, `timeout_seconds`; creates queued lab                                                                                      |
| `nixos_run`                   | `cache_id`, `derivation`, `system`, optional `timeout_seconds`; seals cache and queues job                                                                                     |
| `task_list`                   | Optional `cursor`, `limit`; submission-order items and next cursor                                                                                                             |
| `task_get`                    | `task_id`; task, nodes, commands, entries, captures, artifacts and cleanup                                                                                                     |
| `node_get`                    | `task_id`, `node_id`; power, expected state, preparation, health and capabilities                                                                                              |
| `task_diagnose`               | `task_id`, optional `node_ids`; bounded evidence manifest and artifact IDs                                                                                                     |
| `node_exec`                   | `task_id`, `node_id`, absolute guest `argv`, optional `cwd`, `env`, `timeout_seconds` (default 10, maximum 30); direct Guest Control result and artifact                       |
| `task_cleanup`                | `task_id`; retry resource cleanup of an ended VirtualBox task                                                                                                                  |
| `task_events`                 | `task_id`, optional `cursor`, `limit`; ordered events after cursor                                                                                                             |
| `task_log`                    | `task_id`, optional byte `offset`, `limit`; base64 `data`, `next_offset`, `eof`                                                                                                |
| `operation_get`               | `operation_id`; durable receipt                                                                                                                                                |
| `task_finish`                 | `task_id`, `conclusion`, `summary`; caller conclusion for a running lab                                                                                                        |
| `task_cancel`                 | `task_id`; cancel queued or executing task                                                                                                                                     |
| `task_extend`                 | `task_id`, positive `seconds`; extend within configured total duration                                                                                                         |
| `task_purge`                  | `task_id`; remove data only after terminal state and complete cleanup                                                                                                          |
| `command_start`               | `task_id`, `node_id`, `argv`, optional `cwd`, `env`, `timeout_seconds`                                                                                                         |
| `command_get`                 | `task_id`, `command_id`; independent state, exit code and stdin progress                                                                                                       |
| `command_read`                | `task_id`, `command_id`, `stream` (`stdout`/`stderr`), optional `offset`, `limit`                                                                                              |
| `command_input`               | `task_id`, `command_id`, `sequence`, optional UTF-8 `data`, `eof`                                                                                                              |
| `command_cancel`              | `task_id`, `command_id`; cancel its supervised process group                                                                                                                   |
| `transfer_create`             | `kind` (`file`/`cache`), `task_id` required for files; scoped upload URL                                                                                                       |
| `transfer_get`                | `transfer_id`; object lengths/checksums, state and renewed capability                                                                                                          |
| `guest_file_put`              | `task_id`, `node_id`, `transfer_id`, absolute guest `path`; seals file input                                                                                                   |
| `guest_file_get`              | `task_id`, `node_id`, absolute guest `path`; guest-created tar artifact                                                                                                        |
| `node_control`                | `task_id`, `node_id`, `action`: start, shutdown, reboot, poweroff, pause, resume, reset                                                                                        |
| `network_set`                 | `task_id`, optional `internet`, `dhcp`, `dns` (1..8 public IPv4s), `nodes: [{node_id, ipv4}]`; returns `task` and `guest_network_refresh_required`                             |
| `node_network_link`           | `task_id`, `node_id`, `enabled`; connect/disconnect the lab NIC                                                                                                                |
| `lab_gui_set`                 | `task_id`, `max_guest_resolution` (`any`/`auto`); journal global policy until cleanup                                                                                          |
| `network_recover`             | `task_id`; explicitly check router/protection/ports and restore networking                                                                                                     |
| `endpoint_open`               | `task_id`, `node_id`, `protocol` (`tcp`/`udp`), `port`; allocated endpoint ID/host port                                                                                        |
| `endpoint_close`              | `task_id`, `endpoint_id`; revoke an entry                                                                                                                                      |
| `node_screenshot`             | `task_id`, `node_id`, optional `screen`; PNG artifact                                                                                                                          |
| `node_display`                | `task_id`, `node_id`, optional `width`, `height`, `screen`, `auto_resize`; provide a size pair or auto_resize                                                                  |
| `node_input`                  | `task_id`, `node_id`, `action`: move/click/drag/scroll/release/scancodes; optional screen, x/y/end_x/end_y, button (0/1/2/4), dz/dw, relative, scancodes (two hex digits each) |
| `node_media`                  | `task_id`, `node_id`, `device` (camera/audio_input/audio_output), `enabled`, camera alias when applicable                                                                      |
| `capture_start`               | `task_id`, `node_id`, `kind` (`video`/`nic`/`metrics`)                                                                                                                         |
| `capture_get`, `capture_stop` | `task_id`, `capture_id`; status or stop and collect                                                                                                                            |
| `artifact_list`               | `task_id`, optional `cursor`, `limit`; evidence metadata                                                                                                                       |
| `artifact_get`                | `task_id`, `artifact_id`; read-only scoped download URL                                                                                                                        |

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
Stdin starts at sequence 0, persists in order, and accepts no input after EOF.
Its receipt reports `persisted` and `delivery` (`submitted` or `pending`). Pending
delivery is retried from the durable outbox; guest `next_input` confirms consumption.
The guest accepts identical retransmissions of each sequence, including after exit.
Command logs cap at 64 MiB per stream; the process continues after that limit.
VBox diagnostic logs cap at 1 MiB per collection and are collected at termination when available. Captures use the first NIC or display. Metrics retain the latest 3600 one-second
samples. Video caps at 1 GiB. Guest archives remain opaque on the host.

## Lifecycle and recovery

`task_get` and list items expose three independent fields:

- `phase`: `queued`, `preparing`, `running`, `ended`. Queued tasks include
  `waiting_reason`; backend/resource conflicts keep the first queued task waiting.
- `result`: null until execution ends, then `termination` (`finished`, `failed`,
  `cancelled`, `timed_out`, `interrupted`), `conclusion`, `summary`, `reason`, `error`.
  Lab conclusions come from `task_finish`; Nix conclusions come from the build.
- `cleanup`: `state` (`pending`, `running`, `complete`, `blocked`), `errors` and
  resource records containing `id`, `name`, `released`, `steps`. Each step records
  state, attempt count, update time and any error. Task events carry phase, result
  and cleanup state at the event time.

One task occupies the scheduler until resource cleanup is confirmed. An offline
worker rejects new `nixos_run` calls before allocating a receipt; the request can
be retried after startup. A queued Nix job which has not been submitted fails with
complete cleanup if the worker disappears. Once build submission may have occurred,
daemon cleanup must be confirmed locally.

Deadlines start when execution begins, default to one hour and allow explicit
extension up to 24 hours total. `tasks` configures these limits and a cleanup budget
(default 5 minutes). Node operations do not reset task deadlines. Cleanup starts by
closing published entries and cancelling in-flight calls, then collects bounded
evidence and deletes registered resources. Evidence uses at most 30 seconds and
one third of the remaining cleanup budget. Node deletions run independently, with
up to three attempts and at most 30 seconds per attempt. Ownership conflicts stop
that resource's retry. Partial progress is persisted; `task_cleanup` can retry the
remaining VirtualBox resources with a new request ID. Cleanup failure keeps the
scheduler occupied. Restart interrupts active tasks and resumes registered cleanup;
a result already recorded remains unchanged.

### Node observations and command results

`node_get` returns observed VM `state`, `preparation` (`pending`, `creating`, `starting`,
`guest_control`, `initializing`, `ready`, `failed`), `expected` power transition,
`health` and `capabilities`. Health separates `virtualbox`, `guest_control` and
`commands`. Observations contain `state`, `checked`, `last_success`, `failure_since`,
consecutive `failures`/`successes` and `error`. An absent observation is `unknown`;
other states are `available`, `suspect`, `unavailable` and `expected`.

A probe has a 10-second total deadline and runs every 5 seconds without overlapping
another probe of the same node. Three actual failures sustained for
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

Guest Control is tested independently of the prepared command service. Guest
version/hash are diagnostic metadata; readiness depends on the required live
capabilities. Command polling retains the last confirmed execution state and a
separate `observation`. `command_cancel` persists `cancellation: requested` and
retries delivery until the guest confirms cancellation or task cleanup confirms
VM deletion; a delivery receipt alone is not termination evidence.

### Diagnostics and direct guest execution

`task_diagnose` uses a shared 30-second budget. Windows PowerShell collectors
receive up to 10 seconds per call to accommodate process startup; other items
receive up to 5 seconds. Every call also respects the remaining shared budget. Its
manifest lists each node's observation, selected VM metadata, available VBox log,
screenshot, initialization and service/kernel evidence. An item is `collected`,
`partial`, `unavailable` or `skipped`; successful items survive another item's
failure. Omitting `node_ids` includes the router; selecting nodes accepts ordinary
test-node IDs only. Preparation and running faults use the same collector.

`node_exec` and screenshots are available during preparation and for surviving
resources of an ended, blocked task. Direct commands use configured credentials,
absolute guest executables and argv; they do not depend on the supervisor or lab
LAN. Timeouts include local capacity/lock waits. Results contain base64 `stdout`
and `stderr`, truncation flags, `backend_exit_code`, `exit_code` when known,
`exit_code_min` for a truncated large exit status, `termination`,
`termination_confirmed`, `state`, `error` and `artifact_id`. Stdout is bounded to
1 MiB, stderr to 8 KiB. The stderr stream can include VBoxManage diagnostics.

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
lease defaults to 15 seconds and is refreshed every five seconds. Network faults
suspend entries and require explicit recovery. Source addresses may be rewritten.
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

Capability URLs use `/files/CAPABILITY[/OBJECT]`. They are authenticated by HMAC
and bind object, read/write right and expiry, independently of the MCP token.
Host/Origin checks still apply. Client credentials last one hour and can be renewed.
Read URLs support GET/HEAD and HTTP ranges; upload URLs accept PUT, returning 200.
Errors include 401 expired/invalid capability, 403 disallowed access, 404 absent
object, 405 method, 409 sealed transfer and 413 quota. Binary requests have a
five-minute HTTP deadline. Do not publish capability URLs as public evidence.

File objects are named `blob`. Cache objects are `nix-cache-info`, 32-character
Nix store hash `.narinfo` files and relative `nar/*.nar[.xz|.bz2|.zst]` files. The
cache description is service-owned. Narinfo is bounded to 64 KiB; object names,
store paths, sizes, SHA-256, declared references and relative URLs are checked
before sealing. Nix then verifies NAR structure/content and signatures on import.
The complete declared closure is required. Uploads are atomic; an in-progress
upload prevents sealing. The default transfer limit is 8 GiB. File access uses
directory handles and rejects symlinks; host archive extraction is not provided.

Artifacts include length and SHA-256. Inputs/evidence are retained until explicit
purge unless `storage.retention_days` is positive. Purge retains task summary, conclusions,
receipt identity/hash/status, events and cleanup records. Old receipt payloads are replaced by a purged marker. The worker drops GC roots only
on purge. Failed build directories remain administrator-managed.
