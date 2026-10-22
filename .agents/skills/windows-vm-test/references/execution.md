# Executing Windows tests

The service controls a registered VirtualBox guest directly on the physical host. Configuration owns its registration, startup environment and capability policy. Requests choose headless (default) or `gui=true` when the user explicitly requests a host window; that choice remains fixed within a task and participates in request deduplication. Standalone `vm_power(action="start", gui=false)` has the same default.

## Task and desktop

Call `targets` to inspect targets, effective capabilities and limits, then `task_begin(kind="windows", request_id="PROJECT-RUN-ID", target="REGISTERED-ID")`. The service queues the task, powers off its target if necessary, restores its powered-off baseline, starts the VM, waits for Guest Additions desktop readiness and probes the command session. Poll the compact `task_status` until `ready`.

An active task owns the service until cleanup is confirmed. `interruptible` controls stopping an idle VM to make way for a different VM or NixOS task. A new Windows task can power off its own target before baseline restoration regardless of that setting.

Use the same request ID and identical inputs when retrying a submission. A new task ID creates a new baseline-restored environment. Multiple commands in one task share files, installations and desktop state.

Visible UI testing requires the test account's unlocked console desktop. The service's `ready` state confirms Guest Additions, Python and an interactive command session; that session can exist while Windows is locked. Confirm the visible desktop with a screenshot and the project's input assertions. Resolve login/session setup through the [preparation guide](preparation.md), which covers automatic login, lock/sleep settings and the owner's chosen Defender configuration.

## Files and commands

1. Call `transfer_create(kind="file")` and PUT the file's bytes to its returned URL. The URL is a scoped transfer capability usable by ordinary HTTP clients.
2. Call `guest_upload(task_id, transfer_id, destination)` with an absolute Windows destination path. It creates parent directories and verifies the copied file size, including while a command is running. Upload an archive for a directory, then unpack it inside the guest using a project script or Python.
3. Call `guest_exec` with a unique command `request_id` and an argument array. For example, execute PowerShell explicitly as `["C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe", "-NoProfile", "-File", "C:\\vm-tests\\project\\test.ps1"]`.
4. Poll `task_status(task_id, command_id)` for the selected `command`'s `state`, `exit_code`, `reason` and `cleanup_confirmed`; use `task_log(task_id, command_id, offset)` for incremental output. Read full requests/results through `task_commands` pages when investigating history.

One supervised command runs at a time within a task. The supervisor attaches its child to a Windows Job Object before execution. Completion, cancellation and timeouts include descendant cleanup. Script exit status should reflect assertions, not merely whether an application was launched.

Use pywinauto and Airtest in project-owned test assets as described in [standalone automation](automation.md). Inspect `targets` for configured interpreter paths and `task_status.automation` for bounded environment probes. The supervisor's configured `python` is independent of these environments. Native VM input handles desktop interactions needing the virtual device, including UAC.

For camera input, check the target's `webcam.enabled`, call `webcams(target)` and select an available device ID. `vm_webcam(target, action="attach", camera_id=…)` applies the device selection and reports `observed_attached`. Confirm that the guest application can receive frames. After use, select the ID from the attached list and call `action="detach"`; verify the observed result before finishing the task. `vm_audio` can independently toggle the baseline's audio input and playback paths within the task.

For a long-lived desktop driver, use `guest_exec(stdin=true)` and send actions with `guest_input(task_id, command_id, request_id, text, eof=false)`. Text is UTF-8; include newlines when the driver reads lines. Reuse the same message ID and content for retries. Observe `command.stdin.written_sequence`, then use the driver's output or screenshot to confirm the application action. Sending `eof=true` closes the stream. The 16 KiB message and 64 KiB pending limits are reported by `targets`; pipe writes are distinct from application acknowledgement.

While executing, input, uploads, screenshots and log queries remain available. A second command or `guest_collect` waits until the current command ends; `task_busy` identifies it. Use the existing driver for additional application actions, then close its input or let it exit before collection. Controller reconnection or restart retains the task and input message identities.

## Native desktop controls

Read the per-target `capabilities` entries for configured policy, availability and reasons. Screenshots and native controls can operate during preparation once this task has started the VM. Use a screenshot to identify the current desktop, login screen or UAC prompt; guest-command readiness does not prove that elevation or login completed.

`vm_keyboard(task_id, request_id, chords=[["ALT", "Y"]])` sends physical key combinations. Choose keys from the actual guest language/layout and visible prompt. Use project guest automation for text entry. `vm_mouse` supports moves, clicks, drags and wheel input through the host-prepared helper. Absolute coordinates use the selected screenshot's original pixel dimensions, not a scaled chat image; refresh the screenshot after a layout or resolution change. Relative mode is available when the guest supports relative pointing input.

Each input returns a persistent delivery receipt. Reuse the same request ID to inspect a retry; `unconfirmed` can mean that part of the input arrived. Observe the UI before deciding another action. A delivered receipt acknowledges input submission, while screenshots and application assertions establish its effect.

`vm_keyboard` and `vm_mouse` accept `input_interval_seconds`; an explicit finite nonnegative value (including `0`) overrides the VM default of `1.0`. Native actions are serialized per task. The interval runs from the preceding complete action's end; time already elapsed reduces the wait. Queue and interval waits consume the execution deadline. Receipts retain the adopted interval, actual `waited_seconds` and whether delivery started. Identical retries return that receipt without sending input again. After service recovery, the first new input waits a full adopted interval. Coordinate guest-script input separately; the service cannot pace a library running inside the guest.

Use `vm_display` for a task's resolution change, and inspect the actual observed size. Use `vm_network_link` to disconnect/reconnect an existing virtual NIC during recovery tests. `guest_sessions` reports Guest Control session and process counts without allocating another guest session. A full guest-session table can therefore be diagnosed independently of guest command execution.

## Evidence collection

`vm_packet_capture` and `vm_metrics` start on request. Give start/stop mutations a `request_id`, retain the returned `result.id` as `capture_id`, and use status calls to inspect the same capture. Metrics reads are paged; stop exports JSONL. NIC tracing exports PCAP. Download artifacts before the configured retention period.

Metrics report controller observation times and available VM measurements. Keep absent metrics distinct from zero and collect application-specific GPU/codec evidence separately. Packet capture is scoped to the selected virtual NIC. Start captures only for the part of the flow that needs them.

## Evidence and completion

`screenshot(task_id, screen=0)` returns a PNG artifact URL and actual width/height. `guest_collect(task_id, request_id, guest_path)` starts a supervised archive operation in the guest; its command history entry and `artifacts(task_id)` expose the resulting download URL. Command/artifact pages return `items`, `total`, and `next_offset`; follow that offset until null. Fetch selected URLs into the project's ignored evidence directory.

Capture failures while the relevant application state is still visible. Windows task completion, cancellation and deadline cleanup stop captures, release input, collect bounded screenshot/VirtualBox log evidence and power off the guest. Capture and export failures remain in the capture record and do not prevent poweroff. Controller restart stops owned captures and publishes available evidence. Collect application reports before ending the task. Poll the final cleanup state; an HTTP disconnect leaves the task managed by the service.

The task's `outcome` separates command failures, execution errors, unconfirmed desktop operations, capture evidence errors, explicit cancellation/task deadline and cleanup errors. Any historical command failure keeps normal completion failed even after a later success. Report that cause alongside `cleanup_confirmed`; a failed test with confirmed cleanup is different from an executor or shutdown failure.

Use `vm_power(poweroff)` when the next startup will restore the baseline. A standalone `vm_power(shutdown)` requests normal shutdown and falls back to poweroff after a failed request or bounded wait. Inspect the returned attempts and observed state. An observed `poweroff` or `aborted` state confirms the VM has stopped; an abort during the task remains an executor failure. If termination cannot be established, the service retains occupancy as `cleanup_unknown`; inspect the task and VM log before retrying cleanup when conditions change.

Use `modify_vm` within the target's effective field limits and allowed values. The service checks the complete request before applying it and returns observed differences. A `policy_denied` result identifies the restriction; keep subsequent operations within that policy. Limits apply to requested changes, while snapshot restoration uses the administrator's recorded VM configuration. Coordinate durable settings with that baseline.
