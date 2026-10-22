# VM Test MCP

A Python HTTP MCP service for single-layer VM testing. Windows tasks use registered VirtualBox guests; NixOS test nodes use physical-host KVM through sandboxed Nix builds. The service schedules these paths serially and retains task state across client disconnects.

The controller requires Python 3.11+, uv and VBoxManage for Windows targets. Native mouse input uses a separately built C helper and the host's VirtualBox C API library. Its only direct Python dependency is the MCP SDK pinned in [server.py](server.py)'s inline metadata. The separate [Nix worker](nix_worker.py) and [Windows command supervisor](guest_runner.py) use only the standard library.

## Prepare and start

Follow [host preparation](preparation.md), then copy the [annotated configuration example](config.example.toml) to the component's ignored `config.toml`. Both processes read this one file and can run as the VirtualBox VM owner. Set separate private state directories and enable the desired backend sections; optional settings have commented examples. The Nix worker submits builds to the host daemon as an ordinary account outside `trusted-users`.

Start the worker:

```sh
just worker
```

As the VM owner, start the controller (a graphical session is required for `gui=true` starts):

```sh
just server
```

Run these foreground recipes from the component directory in separate terminals. They provide Python/uv through the existing Nix shell. Direct startup with `python3 nix_worker.py --config config.toml` and `uv run server.py --config config.toml` also works in a prepared environment. The controller and worker CLI take their configuration only from `--config`; stdin is unused, diagnostics go to stderr, and exit status is 0 for normal completion, 2 for argument errors and 1 for startup/runtime errors. Worker administrator reconciliation also writes its result as JSON on stdout.

For a container client, choose a concrete host bridge IP for `service.host`. `service.public_url` defaults to `http://host:port`, and `nix.cache_base_url` inherits that origin. Set either URL explicitly when clients and the host need different routes to the same controller. Upload references cannot select another host URL.

Client example:

```toml
[mcp_servers.vm_test]
url = "http://HOST_BRIDGE_IP:8765/mcp"
# When the service has a nonempty token:
[mcp_servers.vm_test.http_headers]
Authorization = "Bearer YOUR_TOKEN"
```

Set `service.token` directly in the shared TOML. An empty token permits direct MCP access. Real credentials and runtime state belong outside tracked source. Transfer URLs carry a separate random per-transfer capability, so native Nix and ordinary HTTP clients can use them directly.

## Configuration

| Setting                                                                          | Default / purpose                                                                                         |
| -------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `service.state_dir`                                                              | Required absolute private directory for task records, logs and transfers                                  |
| `host`, `port`, `public_url`                                                     | `127.0.0.1`, `8765`, derived `http://host:port`; public URL is the origin returned in transfer URLs       |
| `token`                                                                          | Empty; optional literal Bearer token for MCP requests                                                     |
| `vboxmanage`                                                                     | `VBoxManage` from the service's PATH                                                                      |
| `input_helper`                                                                   | `build/vbox-input-helper`, relative to the configuration file; absolute paths also accepted               |
| `vbox_library_dir`                                                               | Empty; locate `VBoxXPCOMC.so` from the configured VBoxManage installation; absolute override supported    |
| `command_timeout`                                                                | 30 seconds per short VBoxManage operation and each cleanup evidence collection                            |
| `shutdown_timeout`                                                               | 120 seconds per stop observation and for initial desktop readiness                                        |
| `task_timeout`                                                                   | 3600 seconds, also the maximum requested task/command deadline                                            |
| `upload_bytes`                                                                   | 8 GiB per transfer or exported artifact                                                                   |
| `log_bytes`                                                                      | 64 MiB per task/command log; a guest command exceeding its output limit is stopped                        |
| `retention_days`                                                                 | 7 days for completed task records and unreferenced transfers                                              |
| `nix.state_dir`                                                                  | Required when `[nix]` is enabled; private worker directory, separate from controller state                |
| `nix.socket`                                                                     | `/run/vm-test-nix/worker.sock`; one path used by both processes                                           |
| `nix.nix`                                                                        | `nix` from the worker's PATH; optionally set an absolute executable path                                  |
| `nix.cache_base_url`                                                             | Inherits `service.public_url`; controller origin reachable from the host                                  |
| `nix.task_timeout`, `nix.log_bytes`                                              | Inherit the corresponding service limits; optional worker-specific overrides                              |
| `nix.cleanup_timeout`                                                            | 20 seconds of cancellation grace                                                                          |
| `vms.ID`                                                                         | Registered name/UUID, powered-off baseline, guest credentials, Python path, workspace and startup profile |
| `vms.ID.interruptible`                                                           | `false`; whether an idle VM may be stopped for a different VM or NixOS task                               |
| `vms.ID.input_interval_seconds`                                                  | `1.0`; minimum native input spacing, overridden per request; finite nonnegative seconds including `0`     |
| `vms.ID.automation.controls_python`, `vision_python`                             | Optional absolute guest interpreter paths for pywinauto and Airtest environments                          |
| `vms.ID.startup_env`                                                             | Administrator-owned variables passed through `startvm --putenv`                                           |
| `vms.ID.modify_vm`                                                               | Enabled; per-field capabilities and writable ranges                                                       |
| `vms.ID.webcam.enabled`                                                          | `true`; permit attaching and detaching enumerated cameras                                                 |
| `vms.ID.keyboard`, `mouse`, `display`, `network_link`, `audio`, `guest_sessions` | Independent tables with `enabled = true` by default                                                       |
| `vms.ID.metrics`                                                                 | `enabled = true`, `interval_seconds = 1`; collection starts on request                                    |
| `vms.ID.packet_capture`                                                          | `enabled = true`; collection starts on request for an existing virtual NIC                                |

VM IDs are stable service names. VM identities resolve to UUIDs on startup. Each new Windows task authorizes restoring that registration's baseline; use disposable test VMs. Snapshot restore also restores VM settings, so the administrator owns baseline updates after durable configuration changes.

Windows tasks start headless by default. `task_begin(gui=true)` requests a host window; the choice is persisted, included in request deduplication and fixed for that task. `vm_power(action="start", gui=true)` makes the same choice for standalone startup. Recovery observes an already running VM without restarting it or changing its display mode.

The `python` path runs the standard-library guest supervisor. `targets` returns it alongside optional automation registrations and the native input interval. During preparation, each configured automation interpreter gets a bounded import/version probe (at most `command_timeout` and the remaining task time). `task_status.automation` retains Python/package versions, availability and errors. Missing environments do not block a ready task from installing its project dependencies. Controls probe pywinauto, Pillow and psutil; vision probes Airtest, NumPy and OpenCV contrib. Automation libraries run inside the guest, independently of host orchestration.

Enable `[nix]` with its state directory for NixOS tests and `[vms.ID]` registrations for Windows tests. The shared parser resolves defaults for both processes. The worker uses the resolved `[nix]` settings; its Unix socket is mode `0660`. For the same-user setup, set `nix.socket` inside the worker's private state directory as shown in the example. Existing state directories must belong to their process user with mode `0700`; file access uses real directory paths without symlink components.

### VM capabilities

Each registration has an independent `modify_vm` table. Its `enabled` flag controls configuration changes as a whole; each field also has an `enabled` flag, defaulting to `true`. Numeric fields accept `min` and `max`; enum and boolean fields accept `allowed`.

| Field                 | Default writable values                                 | Supported bounds |
| --------------------- | ------------------------------------------------------- | ---------------- |
| `cpus`                | 1–4                                                     | 1–256            |
| `memory_mb`           | 128–16384 MiB (maximum 16 GiB)                          | 128–1048576 MiB  |
| `vram_mb`             | 1–256 MiB                                               | 1–256 MiB        |
| `graphics_controller` | `vmsvga`, `vboxsvga`, `vboxvga`, `none`                 | Listed values    |
| `accelerate_3d`       | `false`, `true`                                         | Listed values    |
| `paravirt_provider`   | `default`, `legacy`, `minimal`, `hyperv`, `kvm`, `none` | Listed values    |
| `nested_paging`       | `false`, `true`                                         | Listed values    |
| `vpid`                | `false`, `true`                                         | Listed values    |

Configuration loads at service startup. Omitted attributes use the defaults above. For example:

```toml
[vms.windows.modify_vm]
cpus = { max = 2 }
memory_mb = { min = 1024, max = 8192 }
accelerate_3d = { allowed = [false] }
vpid = { enabled = false }

[vms.windows.webcam]
enabled = true
```

Here CPU changes remain enabled with a minimum of 1, and 3D acceleration can be disabled. An empty `allowed` list permits no values. `webcam.enabled = false` prohibits both attachment and detachment. Read `targets.vms` for each target's effective `modify_vm` and `webcam` policy.

The service checks all requested fields before executing a modification. A policy rejection returns `policy_denied` with the operation, target and applicable restriction. The limits apply only to fields present in the request; existing settings, baseline restoration and VM startup are independent of these limits. For example, an administrator's eight-CPU baseline can start under a four-CPU modification limit, and a VRAM-only change leaves those CPUs untouched.

## Tools

| Tool                         | Inputs / result                                                                                                                           |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `targets`                    | Service version, registered IDs, VM UUIDs, Nix availability, effective limits and current task summaries                                  |
| `host_info`, `vm_info`       | Fixed host summary; registered VM configuration/state                                                                                     |
| `vm_power`                   | `target`, `action`: start, shutdown, reboot or poweroff; `gui=false` selects headless on start                                            |
| `wait_for_state`             | `target`, `state`, bounded `timeout`                                                                                                      |
| `modify_vm`                  | `target`, typed `changes`; policy validation followed by observed differences                                                             |
| `webcams`                    | `target`; host video devices and current VM attachments, with selectable device IDs                                                       |
| `vm_webcam`                  | `target`, `action=attach/detach`, `camera_id`; policy validation and attachment readback                                                  |
| `read_log`                   | `target`, optional `index`, `offset`, `limit`, `tail`, `generation`; paged VBox logs                                                      |
| `transfer_create`            | `kind`: file or cache; returns transfer ID and URL                                                                                        |
| `task_begin`                 | `kind`, unique `request_id`; Windows `target` and `gui=false`, or NixOS `cache_id` + `drv`; optional `timeout`                            |
| `task_status`                | `task_id`, optional `command_id`; compact outcome, counts, active/selected command and cleanup                                            |
| `task_commands`              | `task_id`, `offset=0`, `limit=50`; full command requests/results in insertion order                                                       |
| `task_log`                   | `task_id`, optional `command_id`, byte `offset` and `limit`; incremental output                                                           |
| `guest_upload`               | `task_id`, `transfer_id`, absolute guest `destination`                                                                                    |
| `guest_exec`                 | `task_id`, command `request_id`, `argv`; optional guest `cwd`, `env`, `timeout`, `stdin=false`                                            |
| `guest_input`                | `task_id`, `command_id`, message `request_id`, `text=""`, `eof=false`; ordered stdin receipt                                              |
| `guest_collect`              | `task_id`, command `request_id`, `guest_path`; supervised ZIP creation in the guest                                                       |
| `screenshot`                 | `task_id`, `screen=0`; PNG artifact URL, screen ID, width and height                                                                      |
| `vm_keyboard`                | `task_id`, `request_id`, `chords`: ordered lists of physical key names; optional `input_interval_seconds`                                 |
| `vm_mouse`                   | `task_id`, `request_id`, `action=move/click/drag/scroll`; screen, coordinates, button, wheel deltas and optional `input_interval_seconds` |
| `vm_display`                 | `task_id`, `screen=0`; optional `width`, `height`, `request_id` request a resolution change                                               |
| `vm_network_link`            | `task_id`, `adapter=1`; optional `connected`, `request_id` change the cable state                                                         |
| `vm_audio`                   | `task_id`; optional `input`, `output`, `request_id` change audio switches                                                                 |
| `guest_sessions`             | `task_id`, `offset=0`, `limit=50`; session/process IDs, status and counts                                                                 |
| `vm_packet_capture`          | `task_id`, `action=start/stop/status`, `request_id`, `capture_id`, `adapter=1`                                                            |
| `vm_metrics`                 | `task_id`, `action=start/read/stop/status`, `request_id`, `capture_id`; optional `interval_seconds`, `offset`, `limit`                    |
| `artifacts`                  | `task_id`, `offset=0`, `limit=50`; artifact page and Nix roots, or directory listing with `root`/`path`                                   |
| `artifact_download`          | `task_id`, recorded Nix `root`, relative file `path`; download URL                                                                        |
| `task_finish`, `task_cancel` | Begin evidence collection/cleanup; observe completion through task status                                                                 |

`modify_vm` translates typed values to fixed VBoxManage options after policy validation. It returns observed configuration differences, including readback information when the command's result is uncertain. Configuration, power and webcam operations execute directly within the registered target's capabilities and task-ownership rules.

Windows commands execute inside registered guests; Nix builders execute in the host daemon's build sandbox. Host-side orchestration uses fixed programs, registered resources, service-owned paths and recorded Nix output roots. Each VM start checks the actual configuration for host shared folders, including after snapshot restoration.

### Desktop input and runtime controls

Read `targets.vms[].capabilities` for effective policy, availability and any unavailable reason. Once the task has started its VM, screenshot and desktop controls are available during `preparing`, `ready` and `executing`. Guest commands still require their interactive-session readiness. This permits observing and operating a login screen or UAC desktop with native VM input.

Mutations require a fresh `request_id`. A receipt reports `delivered` for native input, `completed` for other operations, or `unconfirmed` with an error. Repeat identical requests with the same ID to retrieve the receipt; an unconfirmed input is not replayed. Inspect the screenshot or application assertion before deciding the next action. Input contents are excluded from task logs and public receipts.

Keyboard and mouse actions share a task lock. `input_interval_seconds` defaults to the VM's `1.0`; an explicit finite nonnegative request value overrides it, including `0`. The minimum interval runs from the end of a complete previous action to the start of the next, subtracting elapsed idle time. One chord sequence, click or complete drag is one action. Both queue and interval waits count against `command_timeout` and the task deadline. Timeout while waiting prevents delivery. Native receipts include the adopted interval, actual `waited_seconds`, `delivery_started` and completion time. Retries preserve the original receipt even if configuration changes. After service recovery the first new input waits a full adopted interval; persisted request identities remain deduplicated. Guest automation libraries need their own project-owned pacing.

`vm_keyboard(chords=[["CTRL", "ALT", "DELETE"]])` sends physical key combinations. Supported names include A–Z, 0–9, F1–F12, modifiers, arrows, navigation keys and named punctuation keys. Each chord is released before the next. Character interpretation follows the guest keyboard layout; use project guest automation for text entry.

Mouse absolute coordinates are zero-based pixels within `screen`, measured from the returned full-resolution screenshot. `relative=true` uses movement deltas; for relative dragging, `end_x`/`end_y` are the total drag delta after the initial movement. Buttons are `left`, `right` or `middle`; `dz` and `dw` are vertical and horizontal wheel steps. The helper translates coordinates to the virtual desktop and releases buttons after each operation. Absolute input requires a guest pointing device that supports it, such as a prepared USB tablet.

Display changes request an existing screen's resolution and report the observed result. Network controls toggle an existing NIC's cable connection. Audio controls independently toggle the existing input and playback paths. `guest_sessions` enumerates Guest Control session/process IDs and statuses without opening another guest session.

### Packet capture and metrics

These tools collect only after `action="start"`. Starts and stops require `request_id`; start returns a receipt whose `result.id` is the `capture_id` for subsequent calls. Status calls can list the task's capture sessions or select one. One metrics collector can run per task; packet captures independently own selected NICs.

Metrics default to a one-second interval, configurable from 1–3600 seconds. Rows contain controller observation time, elapsed collection time, metric names, numeric values and units. Available metrics depend on the running VM and Guest Additions; an absent metric is not a measured zero. `action="read"` uses `offset`/`limit` pagination. Stopping exports JSONL; NIC tracing exports PCAP.

The controller generates private host destinations and records file identities. Collection is bounded by the task deadline and existing file/log limits. File-size observation may stop a capture after it crosses a limit; an oversized artifact is retained locally and reported instead of exported. Stopping verifies backend state before publishing files. Capture errors remain in the session record independently of VM cleanup.

On controller restart, owned captures are reconciled and stopped, with available evidence published. Native input is not repeated. Task cleanup releases input, stops collection, gathers evidence and powers off the VM even when capture or artifact collection fails. Read `cleanup_confirmed` for runtime cleanup and each capture's `errors` for evidence quality.

### Webcam input

Call `webcams(target)` to list `available` host cameras and `attached` cameras on the VM. Each item has an `id`, a display name when available, and the device reference reported by VirtualBox. Select an available ID for `vm_webcam(action="attach")`, or an attached ID for `action="detach"`. The VM must be running or paused; cameras can be changed while a guest command is running.

Attaching makes the selected camera available to guest applications until it is detached or the VM stops. Device IDs come from enumeration and are rechecked before execution; the service passes the resolved device to fixed `VBoxManage controlvm … webcam attach/detach` argument arrays. Camera settings and arbitrary host paths are outside this interface.

The result separates `command_succeeded` from `observed_attached`. `applied` reports whether readback matches the requested state, or null when observation failed. If the CLI times out after changing the device, successful readback can still confirm the effect. Inspect the returned state before retrying. An unplugged camera can remain in the attached list and can still be detached by its listed ID. Runtime attachments are established for each test run; microphone and speaker configuration belongs to the baseline.

Prepare webcam passthrough using the [host guide](preparation.md#webcam-input). The service exposes the device; a guest application must separately verify that it can open the camera and receive frames.

## Task lifecycle

A task starts `queued`, becomes `preparing`, then `ready` for Windows or `executing` for Nix. Windows commands temporarily use `executing` and return to `ready` after their process tree stops and their Guest Control session closes. Windows task completion, cancellation and deadline cleanup enter `finishing`, stop control-side observers, collect bounded screenshot/log evidence and directly power off the VM. Collect application reports before ending the task. Final states are `succeeded`, `failed` or `cancelled`, with `cleanup_confirmed: true`. Cancelling a queued task affects only that task's state.

`cleanup_unknown` retains occupancy when actual VM termination cannot be established. Inspect evidence and retry cleanup after a relevant condition changes. An observed `poweroff` or `aborted` state releases occupancy while preserving the original test outcome. An `aborted` VM that was started by the task records an executor failure; completed cleanup does not make that test successful. Power observations report the accepted `target_states` and the actual `observed_state`; `wait_for_state` observes only its requested state. A Nix worker interruption during a build requires administrator reconciliation after checking daemon workers. Confirmed task cleanup allows the next queued task to start.

Normal shutdown and reboot request the guest operating system through Guest Additions. A standalone `shutdown` uses `VBoxManage controlvm VM shutdown` and observes the actual power state. A failed request or bounded wait triggers a fresh observation followed by `poweroff` if still needed. Each shutdown operation attempts this sequence once. Power results include the requested `action`, actual `attempts`, any `graceful_error` and the final `observation`; task logs record the same details during preparation and cleanup. An already powered-off VM needs no power command.

One active virtualization task owns the service at a time. A new Windows task powers off its own target before restoring the baseline, independently of `interruptible`. Stopping another idle VM for a different VM or NixOS task requires `interruptible = true` and follows the shutdown/poweroff sequence. Standalone power operations act on the current VM without restoring a baseline. The service reports unregistered running VirtualBox VMs for the administrator to handle. KVM workloads started outside this service remain part of administrator scheduling.

Task IDs and command IDs make retries idempotent: identical inputs return the existing operation, changed inputs require a different ID. Task state is atomically persisted before execution. Windows commands use a guest-local supervisor and a Windows Job Object; children start suspended, join the job, then execute. The supervisor reports the child's actual exit code and bounded descendant cleanup independently of VBoxManage's own status.

The controller retains each supervisor's Guest Control session identity and closes that session after guest process cleanup. Command-level `cleanup_confirmed` includes both steps. Restart recovery resumes observation and completes any pending session closure; it checks session identity before closing. An uncertain launch or cleanup retains VM occupancy until task cleanup confirms poweroff.

### Results and polling

`task_begin`, `task_status`, `task_finish` and `task_cancel` return the same compact task view. It includes command/artifact counts, `current_command`, the remaining task deadline and `outcome`. Pass `command_id` to `task_status` for a specific command's exit status and stdin progress. Polling excludes request bodies, transfer records and historical commands.

`outcome.failed_command_count`, `command_failure_kinds` and up to five `failed_command_ids` summarize command failures. Failure kinds distinguish `exit_code`, `stdin`, `timeout`, `output_limit`, `executor`, `cancelled`, `task_timeout` and `task_finished`. `stdin` identifies delivery errors or accepted input that was not acknowledged before the command ended, including an unwritten EOF. It can accompany a real child exit code of zero. An execution error or explicit stop reason takes precedence; stdin details remain available on the command. `outcome.failure` records execution failures with their phase; `stop_reason` identifies explicit cancellation or the task deadline; `cleanup_error` describes unresolved cleanup. Unconfirmed desktop operations and capture evidence errors have their own counts. `cleanup_confirmed` remains independent of the test result. A successful later command does not erase an earlier failure. Normal task completion is failed if a command or execution failed, a desktop operation remains unconfirmed, or capture evidence has errors. Explicit cancellation ends as cancelled after cleanup. Ending a still-running command records `task_finished` rather than inventing its exit code.

The guest supervisor retries file access conflicts for up to two seconds per operation. Running snapshots use `status.json`; the terminal result uses a separate, once-published `result.json`. The controller asks the guest to read the terminal result first, closing each read handle promptly. A persistent snapshot failure stops execution, then terminates and observes the Job Object before publishing the independent terminal record. The last valid snapshot and failed publication file remain available for diagnosis. Cleanup confirmation does not depend on replacing a running snapshot.

Nix status includes the worker phase and exit code. A nonzero build exit is a build failure; importing inputs or starting a client can fail as executor work. Cancellation or deadline interruption may require cleanup reconciliation before another task starts.

`task_commands` and task-level `artifacts` return `items`, `total` and `next_offset` (null at the end). Pages contain at most 100 items; command pages include requests and associated collection artifacts, excluding stdin message text. Follow the returned offset to read all history. `artifacts` also exposes `nix_outputs`; selecting a Nix root/path retains its bounded directory listing.

### Interactive commands

Start a long-lived driver with `guest_exec(stdin=true)`, then send UTF-8 text through `guest_input`. Include newlines explicitly for line-oriented protocols. `eof=true` writes any final text and closes stdin. Input is limited to 16 KiB per message and 64 KiB pending acknowledgement; `targets.limits` exposes these and the configured task/transfer/log limits. Drivers own their input protocol and application acknowledgements.

Each message receives an increasing sequence number. Repeating the same request ID and content returns that message's receipt; changed content returns `request_conflict`. Receipt states are `accepted`, `written` and `unconfirmed` if the command ended before acknowledgement; unconfirmed input may have been partially consumed. The selected command's `stdin` reports accepted/written sequences, pending bytes, closure and errors. `written` means the supervisor wrote to the pipe, not that the application completed the action.

Messages are persisted before acceptance and atomically published in the guest command directory. The supervisor retries temporary input-file access conflicts without advancing the sequence or closing stdin, and consumes messages in order before EOF. A persistent delivery error closes the pipe and is retained in `stdin.error`; terminal results also identify accepted messages left unacknowledged when a process exits early. Controller reconnect/restart resumes observation and publication for the same command. A pipe writer runs independently of supervision, so a driver that stops reading still respects command deadlines, cancellation and Job Object cleanup. This is a pipe interface, not a terminal/PTY.

| Windows task state             | Available operations                                                                          |
| ------------------------------ | --------------------------------------------------------------------------------------------- |
| `preparing` after VM start     | Screenshot, native desktop controls and captures; observe readiness before guest commands     |
| `ready`                        | Start a command or collection; upload; screenshot; query status/logs/artifacts; finish/cancel |
| `executing`                    | Send input to the active stdin-enabled command; upload; screenshot; query; finish/cancel      |
| `finishing`, `cleanup_unknown` | Query retained evidence; capture available screenshots; complete cleanup                      |
| Completed                      | Query retained status, command pages, logs and artifacts                                      |

Starting another command or collection while executing returns `task_busy` with the active command ID. Upload creates the guest destination's parent directories, waits for Guest Control copying and checks the resulting file size; `guest_upload_failed` identifies the destination. Coordinate file replacement with the running application.

A service restart resumes observation of known guest commands and Nix jobs. An interrupted baseline/start/shutdown transition becomes `cleanup_unknown` for inspection and explicit task completion. A worker restart marks its unfinished jobs similarly. Useful evidence remains available; retention applies only to completed, cleaned-up tasks and old unreferenced transfers. Successful Nix outputs have task-owned GC roots until task retention expires. Retained failed-build directories and general store GC remain administrator-owned.

## HTTP and file transfer

`/mcp` uses the SDK's Streamable HTTP JSON-RPC transport. POST carries requests, GET supports its event stream, and DELETE closes a legacy session. Tool results include structured JSON; protocol failures use MCP errors. Unknown tool/config fields are rejected. MCP requests are limited to 64 KiB, with 32 sessions and a 300-second idle timeout.

Operational tool failures use `isError=true` and structured `error: {code, message, details}`. Accepted desktop mutations instead retain delivery/execution errors in their persistent receipt as `state="unconfirmed"`; inspect that state and its error even when the MCP transport succeeded. `task_busy`/`task_state` include the current state and allowed states; `out_of_range` includes the parameter and its minimum/maximum. Task timeouts are validated before allocation, using the smaller controller/worker limit for Nix tasks. Discover limits through `targets` before requesting a long run.

`targets.service` and MCP `serverInfo` identify the same service version. After upgrading the controller, reconnect the MCP client and fetch `tools/list` to refresh names, schemas and descriptions. For a stale declaration, compare that fresh response with the client or gateway's published tool list. See the [MCP tools protocol](https://modelcontextprotocol.io/specification/2026-07-28/server/tools).

`/files/CAPABILITY/blob` supports PUT for file upload and GET/HEAD for download. Cache capabilities instead expose `nix-cache-info`, store-hash `.narinfo` files and `nar/…` objects. PUT streams to a temporary file and publishes it atomically within quota. Submission or first guest use seals the transfer. Artifacts are read-only capabilities.

Cache metadata is bounded during upload: `.narinfo` is at most 64 KiB and `nix-cache-info` contains the service's fixed cache description. Store keys must match, and NAR URLs must be relative objects inside that transfer. Nix performs content/hash/signature validation when importing.

Binary routes are separate from the MCP body limit. They return raw bytes for GET, headers for HEAD and an empty 200 response for successful PUT. Errors use `{"error":{"code":"…","message":"…"}}`: 400 invalid transfer, 404 missing object, 405 unsupported method, 408 upload timeout, 409 sealed transfer, 413 quota. MCP authentication failures return 401. Browser-origin requests return 403. Capability URLs grant access to their transfer only and should remain with the task's local evidence.

Task and artifact file access walks directories through held file descriptors, with ordinary files opened without following symlinks. Writes use exclusive temporary files and atomic publication. VBoxManage exchanges files through fresh private staging directories; completed files are checked before publication. Artifact roots come from service records, and directory listings examine at most 2000 entries before sorting. Windows directories are archived in the guest and downloaded intact. Failed Nix logs are available immediately; failed build directories require administrator inspection because paths printed by test code are not authority to read host files.

## Workflows and validation

The distributed [NixOS skill](../../.agents/skills/nixos-vm-test/SKILL.md), [Windows skill](../../.agents/skills/windows-vm-test/SKILL.md) and [shared conventions](../../.agents/rules/vm-tests.md) own Agent workflow instructions. The Nix input helper evaluates in the Agent container and transfers through the service-issued cache. The host worker imports the derivation and builds with fixed daemon parameters; the project controls its test definition and node configuration.

```sh
just fmt
just test
```

These recipes use `nix-shell -p python3 uv`, the inline script dependency and the pinned Ruff tool. Verification code lives in this component. It exercises real local HTTP/Unix-socket boundaries and subprocesses with synthetic virtualization adapters. Real KVM, VirtualBox Guest Control, desktop and graphics acceptance require the prepared host and guest described in the preparation guides. Record those separately from local adapter tests.

Copy `guest_runner.py` and `test_guest_runner.py` together into the prepared Windows guest and run `python -B -m unittest -v test_guest_runner`. These standard-library tests hold actual Windows file handles to exercise temporary and persistent status/input access conflicts, UTF-8 input order, EOF and Job Object descendant cleanup. The Windows cases are skipped by the Linux suite.

The Windows skill supplies [standalone controls, image and mixed examples](../../.agents/skills/windows-vm-test/references/automation.md) and [environment preparation resources](../../.agents/skills/windows-vm-test/references/preparation.md). The tested project owns its tests, templates, dependencies, assertions and reports. The example runner has its own process deadline, failure capture and Job Object cleanup for direct console execution. UAC secure-desktop input stays with the executor; coordinate it with guest automation and reacquire windows/display state after returning.

Host acceptance covers daemon isolation, a fresh multi-node KVM run, Windows desktop and process cleanup, and switching between the two backends. Local adapter tests establish orchestration behavior; keep actual host results with the task's local evidence.
