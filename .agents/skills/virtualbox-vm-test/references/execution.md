# Executing VirtualBox labs

Use a prepared VM Test MCP 0.12 executor. `service_get` reports templates,
resource/media policy, backend availability, endpoint address and limits. Its
token holders share task authority. Use the advertised tool schemas; do not infer
host paths, VM UUIDs or executable options from guest data.

## Create and observe

`lab_create` takes a fresh `request_id`, a fixed `nodes` list and optional
`lan`, `dhcp`, `dns`, `internet`, `timeout_seconds`. Each node has a unique lowercase
name and a registered template, optional IPv4/CPU/memory overrides, per-node GUI
and media requests. Using the same Windows template twice creates independent
clones. The private router is not a test-command target.

Each node's `gui` flag selects its host frontend: omitted or `false` uses headless;
`true` opens a host-visible VirtualBox window. A GUI frontend requires the
controller to run in the VM owner's graphical session.

Use Codex CLI 0.163.0+ with `tool_timeout_sec=660`. `lab_create` waits for
readiness and returns `{operation, wait}`. A window expires after 600 seconds;
continue with `wait({targets: response.wait.continue})`. Expiry is not execution
failure. SSE heartbeats every 15 seconds are transport-only. Multiple targets can
be observed together; ordinary waits ignore log growth/healthy samples. Request
condition `output` when output changes matter. Read `lab_inspect` for node IDs. `node_get` exposes preparation stage, expected power state,
capabilities and health observations while the lab prepares. Keep original IDs after disconnects and retry identical
mutations with the original request ID. A changed request requires a new ID.
Task `router` exposes router preparation and health; each preparation record
includes stage/start times and its independent deadline. A cancelled peer retains
the primary preparation failure as its cause.
The queue is shared with NixOS tests. Deadlines begin at execution, default to
one hour and can be extended with a concrete reason to at most 24 hours total.
Waiting and ordinary controls do not extend the deadline. HTTP disconnect, client
timeout and MCP cancellation stop observation only; explicit business cancellation
releases the lab. Choose a deadline
for the continuous test batch. `lab_list` defaults to compact active tasks;
`phase=ended` or `all` selects history. Retain its task-ID `next_cursor` for paging.
`lab_get.queue` identifies the position, blocker and waiting reason without
requiring another user's command payload. Activity counts describe supervised
commands, open endpoints and captures; detached services can still be active.

Finish tests and bounded diagnosis before returning to lengthy local work.
`lab_extend(seconds=…, reason=…)` records concrete continued work and the resulting
deadline; it does not extend node startup budgets. A connection loss does not
release the lab: reconnect and inspect the same task. Use `lab_cancel` when
abandoning a queued/preparing request, then wait for cleanup. Resources that were
never created have `state=never_allocated`, distinct from `released`.

## Commands, files and Compose

Use `upload_create(kind="file", task_id=…)` and PUT locally prepared bytes while
the task is queued or preparing. Once the node is available, use `guest_file_put`
with node ID and an absolute guest path; successful sealing makes the upload
read-only. Reuse the same sealed upload for multiple nodes; the controller keeps
one input object. `upload_objects` pages checksums; `upload_renew` renews URLs. Ending the task interrupts pending writes and retains complete inputs.
`upload_get.can_upload` and `uploading` expose the current upload boundary.
`command_start` takes argv, optional absolute guest cwd/environment and an independent
timeout. Default `mode="finite"` waits for exit, process-group stop and output
collection; choose `mode="service"` or `"interactive"` explicitly to return after
confirmed start. `context` defaults to `service`: Linux root or Windows LocalSystem.
Windows GUI workloads select `context="desktop"`, which requires a logged-in
administrator's elevated token and fails if it is unavailable. The default cwd
is the selected account's home; Linux supplies HOME, USER, LOGNAME and PATH for
each command. Inspect `node_get.guest.command_environment` and the live command's
`execution` identity/cwd; set project cache directories explicitly in its env.
No login shell or host shell is involved. Commands on separate nodes and separate
supervised commands can run concurrently.

Read `command_get` and byte-offset `command_read(stdout|stderr)`. Decode base64
output and retain byte offsets even for Unicode or binary data. `at_end` means
the currently retained prefix ended; `eof` requires complete retention. After
execution is final, wait within `retained_output.deadline` for both streams to
be complete or explicitly incomplete before asserting their contents. An absent
first status is pending until `status_deadline`; a receipt alone does not establish
that the command started. `command_input` starts at sequence 0; retain each
request ID for retries and send EOF explicitly. `command_cancel` affects its
supervised process group. A failed command leaves the lab available for diagnosis.
Input is raw UTF-8 text, at most 64 KiB per packet, 64 MiB and 4096 packets per
command including EOF. Use files for larger inputs. Compare `stdin.next_sequence` (accepted), `delivered_sequence` and
`consumed_sequence`. Consumption means delivery into the guest process stdin pipe,
not application processing; assert the application's result separately.
To inspect retained input, use `command_details(input_from=0)` and its `next_sequence`.

Check the live command's `final`, actual `exit_code` or `exit_signal`, `termination`
and output completeness. A Linux signal exit has no exit code; `exit_signal`
identifies the signal and `core_dumped` records the kernel's core-dump status.
`timed_out` records a deadline separately from user cancellation;
`uncertain` means supervision could not confirm a phase. Preserve known exit codes
even if output drainage failed. `release.source=vm_deleted` confirms resource
release only; it does not supply a missing command result. The mutation receipt
is a historical snapshot, so continue reconciliation through `command_get`.

Probe optional executables through guest PATH and execute version/import checks;
Python, Docker and Compose are not prerequisites for creating a lab. If the test
needs a missing dependency, install it in the disposable node with project tooling,
commands/transfers or offline installers. Do not set interpreter paths in server
configuration or silently alter the source snapshot.

Run Docker/Compose through ordinary commands on a Linux node. Container mounts,
capabilities, host networking and privileged mode apply to that guest. Services
started outside a command process group can persist until lab destruction.

## Network tests

The default LAN is `10.77.0.0/24`; DHCP reservations give `NAME.lab.test` names.
Set node IPv4 values or disable DHCP when the test must configure addresses itself.
Reservations and protection are applied before clients boot. Node `planned_ipv4`
is the declared address; `network_observation` shows actual guest interfaces,
MACs and address prefixes, with `unknown` when no observation is available.
Assert the test's required address, bind, LAN and DNS behavior before interpreting
an application network failure. Guest Control does not depend on LAN reachability.
Use `endpoint_open` with a
node, TCP/UDP and a guest port to get an allocated host entry. The relay enforces
administrator source CIDRs; source addresses may be rewritten. Test original
source-address behavior entirely within the LAN.

`network_set` changes public IPv4 egress, DHCP, public upstream DNS and node
address registrations while the lab's LAN subnet stays fixed. Its result reports
whether guest renewal/reconfiguration is needed. Use privileged commands for guest
addresses, DNS, routes and firewall rules. Disabling DHCP does not remove a guest's
current address. `node_network_link` connects/disconnects its first NIC. Egress-only
changes preserve LAN and published services.
Host, metadata and protected destinations remain denied. `network.state=suspect`
means a late observation within the confirmed policy lease; inspect the retained
stage/error/budget and wait for the next bounded observation. `degraded` suspends
egress/entries while retaining VMs. Inspect task events and explicitly call
`network_recover`; failed recovery must leave networking suspended. Reconcile the
original command after a connection loss; endpoint EOF is not a command exit.
LAN-local
IPv6 communication is possible, with no managed IPv6 egress/publication.

VirtualBox guest ACPI S3 sleep and S4 hibernation are unsupported. Hypervisor
pause/resume is supported and must be described/tested separately.

## Desktop and evidence

Headless nodes can run guest desktop applications. Use `node_screenshot`,
`node_display`, `node_input` and `node_media` only within advertised policy.
Native input is serialized per node; delivery receipts are
independent of application assertions. If input is uncertain, inspect the current
screen before deciding whether another action is appropriate. Media is off unless
requested; 3D is selected before boot.
Automatic login is optional; inspect and log in/unlock through native input if
needed. Use agent vision and assertions, not an assumed MCP vision component.
`node_display(auto_resize=…)` controls per-node automatic resizing (default false).
`lab_gui_set(max_guest_resolution="any"|"auto")` changes the journaled global
resolution policy (default any), restored on cleanup unless an external edit causes
a conflict.

`capture_start` selects `video`, `nic` or `metrics`; keep its ID and stop/collect
with `capture_stop`. `guest_file_get` creates a tar archive inside the guest and
stores it as opaque data. `evidence_list` and `evidence_get` provide evidence and
expiring download capabilities; read a new evidence capability or call `upload_renew` for inputs as needed.

Submit the caller's `passed`, `failed` or `inconclusive` conclusion and a summary
through `lab_finish`. Cancellation and timeout use the same cleanup path. Wait
for `cleanup.state: complete`. An ended task with `cleanup.state: blocked` retains
resource step errors; inspect them and use a new `lab_cleanup` request for a retry. Evidence
and reproduction inputs default to 1 day after success or 7 days after other
terminal outcomes; summaries/receipts last 30 days. `evidence_pin` retains selected
files until `evidence_unpin` without extending VM occupancy. Evidence budgets are
20 GiB with reclamation to 16 GiB, including pins/protected data. Protected pressure
blocks new labs/jobs/uploads while control and cleanup remain available. Input
staging is 16 GiB total and 8 GiB per transfer; unbound idle uploads expire after
one hour. `execution_delete` removes eligible detail early. Enter local edits or reporting only after cleanup is
complete; a later batch uses a new request and task. Keep capability URLs private.

## Fault handling

Read `lab_get.phase`, `result` and `cleanup` separately. An initialization failure
ends the lab after bounded evidence collection. A runtime capability failure keeps
the lab and healthy nodes available until the caller finishes/cancels or its deadline
expires. Collect needed evidence within a stated diagnostic window, then release
the shared executor before local investigation. Do not restart or recreate it
automatically. After reviewing evidence,
explicit node power actions remain available; they record the expected transition.

`node_get.health` separates VirtualBox, Guest Control and command-service
observations, including last success, failure duration and error. `lab_diagnose`
collects available metadata, VBox logs, screenshots and guest initialization/system
logs within a shared 30-second budget. It accepts selected test-node IDs; omission
collects the full lab including its router. Retain partial evidence and the manifest.

For control latency or network lease faults, use `service_observe` and the retained
`performance.json` window to compare host pressure, controller quota/throttling,
capacity wait and execution time. Missing samples or unsupported metrics are not
evidence of idle CPU. Correlate timestamps and preserve alternative causes; do not
attribute historical failures to CPU without concurrent evidence or inject load
on a shared executor. Fault-window collection continues after VM release.

Use `node_exec` for short absolute guest argv when ordinary command supervision is
not ready. It defaults to 10 seconds, caps at 30, and retains bounded base64 output,
exit information and an artifact. Delivery/termination uncertainty remains in its
receipt; inspect it before deciding on another command. Use supervised commands for
ordinary workloads. A command's observation failure does not establish its exit,
and a requested command cancellation requires a guest terminal record with its
termination evidence. VM deletion confirms resource release and cannot supply an
execution result or missing exit code.
