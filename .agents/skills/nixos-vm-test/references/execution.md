# Executing NixOS VM tests

Evaluate in the development container using the tested project's locked nixpkgs.
The prepared VM Test MCP 0.10 worker submits the derivation to the physical host's
Nix daemon; QEMU nodes run directly on host KVM inside its strict sandbox.

## Submission

Use `service_get` and `task_list`, then allocate a cache with
`transfer_create(request_id=…, kind="cache")`. Poll `operation_get` for its transfer
ID and upload URL. The prepared container provides Nix and `vm-test-submit`:

```sh
vm-test-submit --test '.#checks.x86_64-linux.integration' \
  --request-id integration-001 --cache-id CACHE_ID --cache-url CACHE_URL \
  --work-dir /absolute/ignored/task-workspace
```

The helper refuses lockfile updates, checks `qemu.forceAccel`, QEMU nodes and the
absence of nspawn containers, and creates a fresh top-level test identity through
`overrideTestDerivation`. It saves evaluated metadata in the task workspace so a
retry uses that same derivation even if the checkout changes. Dependencies retain
their cache identities. New executions need new request IDs and workspaces.

It copies the derivation closure into a standard local binary cache, then uploads
cache objects over bounded HTTP. Submit its JSON (`request_id`, `cache_id`,
`derivation`, `system`) through `nixos_run`. Submission validates/seals the cache
and queues work. `worker_unavailable` before acceptance does not reserve the
request ID; retain the uploaded cache and retry the same request after the worker
starts. Do not submit duplicate jobs to work around an uncertain accepted execution. If the project's wrapper hides standard test metadata/override
interfaces, adapt that project's entrypoint within its existing output policy.

Use an administrator-approved `--signing-key /absolute/private/key` when local
input-addressed objects need signatures. Preserve signature validation. Never make
the worker a trusted Nix user to work around a cache failure.

## Observation and cleanup

Follow the operation, then `task_get`, `task_events` and byte-offset `task_log`.
`task_get` separates `phase`, `result` and `cleanup.state`; completion is
`phase: ended`, with the build conclusion under `result`. Logs are base64 bytes
with `next_offset`. The FIFO queue is shared with VirtualBox
labs. The daemon owns build parallelism; test definitions own node CPUs/RAM.

List completed output files with `artifact_list` and retrieve scoped URLs with
`artifact_get`. The worker reads only registered store output roots, rejects
symlink boundaries, and retains GC roots until purge. Paths printed by test code
do not authorize host file reads. Failed-build directories belong to administrator
diagnosis; task stderr/logs remain available.

`task_cancel` requests cancellation. An interrupted daemon build can have
`cleanup.state: blocked`; this continues to occupy the scheduler until the administrator
verifies teardown and performs local reconciliation. Reconnect to the original
task after HTTP loss. `task_purge` is accepted only after terminal state and
complete cleanup. Evidence is retained by default.

## Acceptance and diagnosis

Use a fresh execution of the linked multi-node smoke module for initial acceptance.
Actual guest startup/assertions on KVM, a sandbox host-marker probe and rejection
of `__noChroot` are required alongside administrator-confirmed daemon policy.
Device presence or a `kvm` feature declaration alone is insufficient evidence.

For assertions, collect relevant guest journals/serial output and change the test
before a fresh run. For host daemon, KVM, trust or baseline prerequisites, report
the preparation issue; this runtime workflow does not provision the executor.
