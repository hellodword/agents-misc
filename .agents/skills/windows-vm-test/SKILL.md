---
name: windows-vm-test
description: Run Windows application, browser and desktop tests in a prepared VirtualBox guest through HTTP MCP, using baseline snapshots, guest scripts, screenshots and artifacts. Windows installation and baseline preparation are manual setup tasks.
---

# Windows VM Tests

1. Identify the registered Windows target, project test command and useful evidence. Apply the [VM test conventions](../../rules/vm-tests.md), then read [execution](references/execution.md).
2. Read the target's effective policy, capabilities and automation paths with `targets`, then use `task_begin` to restore its baseline. Windows tasks start headless; set `gui=true` only when the user requests a visible host window. Observe preparation through screenshots; native VM input can operate login or UAC once the task's VM is running. Inspect environment probes in `task_status` before choosing interpreters. An unavailable baseline prerequisite belongs to [manual preparation](references/preparation.md).
3. Explore with guest Python scripts using pywinauto for Win32/UIA controls and Airtest for image matching, then keep working tests, templates, assertions and dependencies in the tested project. Read [standalone automation](references/automation.md) when establishing or adapting those assets. Upload and run them with `guest_exec`; check exit status and reports. Task-specific dependencies can be installed inside the task.
4. Use screenshots and project assertions to evaluate desktop behavior. Start NIC capture or performance sampling when the test needs that evidence, and retain their capture IDs. Rendering, video decoding and video encoding each need their own application evidence when requested.
5. Collect useful reports and failure artifacts before ending the task, which powers off the VM. Report task ID, results, evidence and cleanup state. A new task restores the baseline; retain useful findings outside that reset boundary.

For a copyable starting point, use the example's [runner](assets/desktop-example/run.py), [scenario](assets/desktop-example/scenario.py), [input and matching helpers](assets/desktop-example/support.py), [vision subprocess](assets/desktop-example/vision.py), [Win32 fixture](assets/desktop-example/fixture.py), [template generator](assets/desktop-example/make_template.py) and [template](assets/desktop-example/target.png). Dependency inputs and generated locks are [controls.in](assets/desktop-example/controls.in), [controls.txt](assets/desktop-example/controls.txt), [vision.in](assets/desktop-example/vision.in) and [vision.txt](assets/desktop-example/vision.txt). The [environment preparation script](scripts/prepare-environments.ps1) is for an explicitly selected baseline setup or task-local environment directory.
