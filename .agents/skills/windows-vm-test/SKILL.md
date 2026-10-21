---
name: windows-vm-test
description: Run Windows application, browser and desktop tests in a prepared VirtualBox guest through HTTP MCP, using baseline snapshots, guest scripts, screenshots and artifacts. Windows installation and baseline preparation are manual setup tasks.
---

# Windows VM Tests

1. Identify the registered Windows target, project test command and useful evidence. Apply the [VM test conventions](../../rules/vm-tests.md), then read [execution](references/execution.md).
2. Read the target's effective capabilities with `targets`, then use `task_begin` to start from its registered baseline. Wait for the guest and interactive desktop to be ready. An unavailable prerequisite belongs to [manual preparation](references/preparation.md), not automatic host provisioning.
3. Upload project inputs, run project-owned scripts with `guest_exec`, and observe their actual exit codes through `task_status`. Commands within the task share guest state; installation of task-specific dependencies belongs inside that task.
4. Use screenshots and project assertions to evaluate desktop behavior. Rendering, video decoding and video encoding each need their own application evidence when requested.
5. Collect useful reports and failure artifacts before ending the task, which powers off the VM. Report task ID, results, evidence and cleanup state. A new task restores the baseline; retain useful findings outside that reset boundary.
