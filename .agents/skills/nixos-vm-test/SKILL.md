---
name: nixos-vm-test
description: Run and diagnose NixOS QEMU VM tests through a prepared HTTP MCP executor when remote execution is selected or local KVM is unavailable. Evaluate with the project's locked nixpkgs and collect sandbox test results; environment provisioning and ordinary Nix builds have separate workflows.
---

# NixOS VM Tests

1. Identify the project test, locked nixpkgs input, execution system and prepared MCP executor. Preserve a suitable project-local flow. Apply the [VM test conventions](../../rules/vm-tests.md) and read [execution](references/execution.md) for submission and diagnosis.
2. Prepare test changes locally; use `service_get` and the compact active `nix_job_list` to check the executor and queue. An offline worker leaves MCP and VirtualBox available, but Nix execution requires the administrator to start it. This workflow does not provision the host. For a separately authorized setup task, read the [VM Test MCP administrator guide](https://github.com/hellodword/agents-misc/blob/master/tools/vm-test-mcp/preparation.md).
3. Allocate a cache with `upload_create`, then run the prepared `vm-test-submit` helper inside the project environment. It uses the project's locked framework, checks QEMU/KVM requirements, saves a fresh top-level derivation identity for retries, and uploads a standard binary cache. Retain its task workspace.
4. Submit the helper's JSON through `nix_job_create`. Nodes run directly on host KVM inside the test build. The creation call waits for execution, evidence and cleanup; use returned `wait.continue` targets for another window. Read byte-offset logs and evidence separately. Diagnose by changing the test and submitting a fresh execution identity. Preserve signature validation and keep the worker account outside Nix `trusted-users`; it may also run the controller.
5. For initial acceptance, use [the multi-node test module](assets/vm-smoke.nix) with the project's locked nixpkgs. Confirm actual multi-vCPU guest startup and network assertions, alongside administrator-confirmed daemon sandbox policy.
6. Confirm complete cleanup before lengthy local rework or reporting. Cancel an abandoned queued task explicitly; a disconnected observer does not cancel it. A new test batch rejoins the queue. Report the task ID, execution system, assertion result, logs, artifacts and cleanup state. Keep unresolved failures attributable to the test, execution environment or framework.

Use Codex CLI 0.163.0+ with `tool_timeout_sec=660`. Each observation window is 600 seconds; transport heartbeats never require model polling. HTTP/tool cancellation stops observation only. Retention defaults to 1 day for success, 7 days otherwise and 30 days for summaries/receipts; pin needed evidence, then unpin when no longer needed. Protected evidence can pause new admission while control and cleanup remain available.
