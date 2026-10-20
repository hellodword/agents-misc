# Executing Windows tests

The service controls a registered VirtualBox guest directly on the physical host. Its configured startup profile owns the frontend and graphics environment.

## Task and desktop

Call `targets` to inspect targets and effective limits, then `task_begin(kind="windows", request_id="PROJECT-RUN-ID", target="REGISTERED-ID")`. The service queues the task, restores its powered-off baseline, starts the VM, waits for Guest Additions desktop readiness and probes the command session. Poll the compact `task_status` until `ready`.

Use the same request ID and identical inputs when retrying a submission. A new task ID creates a new baseline-restored environment. Multiple commands in one task share files, installations and desktop state.

Visible UI testing requires the test account's unlocked console desktop. The service's `ready` state confirms Guest Additions, Python and an interactive command session; that session can exist while Windows is locked. Confirm the visible desktop with a screenshot and the project's input assertions. Resolve login/session setup through the [preparation guide](preparation.md), which covers automatic login, lock/sleep settings and the owner's chosen Defender configuration.

## Files and commands

1. Call `transfer_create(kind="file")` and PUT the file's bytes to its returned URL. The URL is a scoped transfer capability usable by ordinary HTTP clients.
2. Call `guest_upload(task_id, transfer_id, destination)` with an absolute Windows destination path. It creates parent directories and verifies the copied file size, including while a command is running. Upload an archive for a directory, then unpack it inside the guest using a project script or Python.
3. Call `guest_exec` with a unique command `request_id` and an argument array. For example, execute PowerShell explicitly as `["C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe", "-NoProfile", "-File", "C:\\vm-tests\\project\\test.ps1"]`.
4. Poll `task_status(task_id, command_id)` for the selected `command`'s `state`, `exit_code`, `reason` and `cleanup_confirmed`; use `task_log(task_id, command_id, offset)` for incremental output. Read full requests/results through `task_commands` pages when investigating history.

One supervised command runs at a time within a task. The supervisor attaches its child to a Windows Job Object before execution. Completion, cancellation and timeouts include descendant cleanup. Script exit status should reflect assertions, not merely whether an application was launched.

Use the guest's configured Python and project framework for browser and desktop automation. The service provides commands and screenshots; project scripts own application interactions and assertions.

For camera input, call `webcams(target)` and select an available device ID. `vm_webcam(target, action="attach", camera_id=…)` presents the exact VM and device for human approval, then reports `observed_attached`. Confirm that the guest application can receive frames. After use, select the ID from the attached list and approve `action="detach"`; verify the observed result before finishing the task. Camera attachment is runtime state. Microphone and speaker settings come from the baseline.

For a long-lived desktop driver, use `guest_exec(stdin=true)` and send actions with `guest_input(task_id, command_id, request_id, text, eof=false)`. Text is UTF-8; include newlines when the driver reads lines. Reuse the same message ID and content for retries. Observe `command.stdin.written_sequence`, then use the driver's output or screenshot to confirm the application action. Sending `eof=true` closes the stream. The 16 KiB message and 64 KiB pending limits are reported by `targets`; pipe writes are distinct from application acknowledgement.

While executing, input, uploads, screenshots and log queries remain available. A second command or `guest_collect` waits until the current command ends; `task_busy` identifies it. Use the existing driver for additional application actions, then close its input or let it exit before collection. Controller reconnection or restart retains the task and input message identities.

## Evidence and completion

`screenshot(task_id)` returns a PNG artifact URL. `guest_collect(task_id, request_id, guest_path)` starts a supervised archive operation in the guest; its command history entry and `artifacts(task_id)` expose the resulting download URL. Command/artifact pages return `items`, `total`, and `next_offset`; follow that offset until null. Fetch selected URLs into the project's ignored evidence directory.

Capture failures while the relevant application state is still visible. `task_finish` collects available screenshot/VirtualBox log evidence and normally shuts down the guest. `task_cancel` also requests command cancellation. Poll the final cleanup state; an HTTP disconnect leaves the task managed by the service.

The task's `outcome` separates command failures, execution errors, explicit cancellation/task deadline and cleanup errors. Any historical command failure keeps normal completion failed even after a later success. Report that cause alongside `cleanup_confirmed`; a failed test with confirmed cleanup is different from an executor or shutdown failure.

If normal shutdown or guest control fails, the service retains occupancy. Inspect the task and VM log. A separately confirmed `vm_power(poweroff)` can stop the affected VM, after which observed `poweroff` establishes cleanup. Save available evidence before that operation.

Each `modify_vm` request presents typed changes for human confirmation and returns observed differences. Coordinate settings with the administrator's baseline: restoring a snapshot also restores the snapshot's VM configuration.
