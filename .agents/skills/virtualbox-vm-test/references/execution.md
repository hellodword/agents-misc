# Executing VirtualBox labs

Use a prepared VM Test MCP 0.10 executor. `service_get` reports templates,
resource/media policy, backend availability, endpoint address and limits. Its
token holders share task authority. Use the advertised tool schemas; do not infer
host paths, VM UUIDs or executable options from guest data.

## Create and observe

`lab_create` takes a fresh `request_id`, a fixed `nodes` list and optional
`lan`, `dhcp`, `dns`, `internet`, `timeout_seconds`. Each node has a unique lowercase
name and a registered template, optional IPv4/CPU/memory overrides, per-node GUI
and media requests. Using the same Windows template twice creates independent
clones. The private router is not a test-command target.

Poll `operation_get` until the creation receipt resolves, then `task_get` until
`phase` is `running`. `node_get` exposes preparation stage, expected power state,
capabilities and health observations while the lab prepares. Keep original IDs after disconnects and retry identical
mutations with the original request ID. A changed request requires a new ID.
The queue is shared with NixOS tests. Deadlines begin at execution, default to
one hour and can be extended within the configured total limit.

## Commands, files and Compose

Use `transfer_create(kind="file", task_id=…)`, PUT bytes to its capability URL,
then `guest_file_put` with node ID and an absolute guest path. This seals the
upload. `command_start` takes argv, optional guest cwd/environment and an independent
timeout. No host shell is involved. Commands on separate nodes and separate
supervised commands can run concurrently.

Read `command_get` and byte-offset `command_read(stdout|stderr)`. Decode base64
output and retain offsets. `command_input` starts at sequence 0; retain each
request ID for retries and send EOF explicitly. `command_cancel` affects its
supervised process group. A failed command leaves the lab available for diagnosis.

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
Guest Control does not depend on LAN reachability. Use `endpoint_open` with a
node, TCP/UDP and a guest port to get an allocated host entry. The relay enforces
administrator source CIDRs; source addresses may be rewritten. Test original
source-address behavior entirely within the LAN.

`network_set` changes public IPv4 egress, DHCP, public upstream DNS and node
address registrations while the lab's LAN subnet stays fixed. Its result reports
whether guest renewal/reconfiguration is needed. Use privileged commands for guest
addresses, DNS, routes and firewall rules. Disabling DHCP does not remove a guest's
current address. `node_network_link` connects/disconnects its first NIC. Egress-only
changes preserve LAN and published services.
Host, metadata and protected destinations remain denied. Network faults suspend
egress and entries while retaining VMs. Inspect task events and explicitly call
`network_recover`; failed recovery must leave networking suspended. LAN-local
IPv6 communication is possible, with no managed IPv6 egress/publication.

## Desktop and evidence

Use `node_screenshot`, `node_display`, `node_input` and `node_media` only within
advertised policy. Native input is serialized per node; delivery receipts are
independent of application assertions. If input is uncertain, inspect the current
screen before deciding whether another action is appropriate. Media is off unless
requested; 3D is selected before boot. GUI requires the controller's graphical session.
Automatic login is optional; inspect and log in/unlock through native input if
needed. Use agent vision and assertions, not an assumed MCP vision component.
`node_display(auto_resize=…)` controls per-node automatic resizing (default false).
`lab_gui_set(max_guest_resolution="any"|"auto")` changes the journaled global
resolution policy (default any), restored on cleanup unless an external edit causes
a conflict.

`capture_start` selects `video`, `nic` or `metrics`; keep its ID and stop/collect
with `capture_stop`. `guest_file_get` creates a tar archive inside the guest and
stores it as opaque data. `artifact_list` and `artifact_get` provide evidence and
expiring download capabilities; renew through the metadata tool if necessary.

Submit the caller's `passed`, `failed` or `inconclusive` conclusion and a summary
through `task_finish`. Cancellation and timeout use the same cleanup path. Wait
for `cleanup.state: complete`. An ended task with `cleanup.state: blocked` retains
resource step errors; inspect them and use a new `task_cleanup` request for a retry. Evidence
and reproduction inputs remain until explicit `task_purge` unless configured
retention says otherwise. Keep capability URLs private.

## Fault handling

Read `task_get.phase`, `result` and `cleanup` separately. An initialization failure
ends the lab after bounded evidence collection. A runtime capability failure keeps
the lab and healthy nodes available until the caller finishes/cancels or its deadline
expires. Do not restart or recreate it automatically. After reviewing evidence,
explicit node power actions remain available; they record the expected transition.

`node_get.health` separates VirtualBox, Guest Control and command-service
observations, including last success, failure duration and error. `task_diagnose`
collects available metadata, VBox logs, screenshots and guest initialization/system
logs within a shared 30-second budget. It accepts selected test-node IDs; omission
collects the full lab including its router. Retain partial evidence and the manifest.

Use `node_exec` for short absolute guest argv when ordinary command supervision is
not ready. It defaults to 10 seconds, caps at 30, and retains bounded base64 output,
exit information and an artifact. Delivery/termination uncertainty remains in its
receipt; inspect it before deciding on another command. Use supervised commands for
ordinary workloads. A command's observation failure does not establish its exit,
and a requested cancellation is confirmed only by guest status or VM deletion.
