# Executing NixOS VM tests

Evaluate in the development container using the tested project's locked nixpkgs.
The prepared VM Test MCP 0.12 worker submits the derivation to the physical host's
Nix daemon; QEMU nodes run directly on host KVM inside its strict sandbox.

## Submission

Use `service_get` and `nix_job_list`, then allocate a cache with
`upload_create(request_id=…, kind="cache")`. Read the transfer ID and upload URL
from `operation.result`; continue a timed-out observation with its `wait.continue`. Locate the prepared `vm-test-submit` executable first and run
`vm-test-submit --version`; this reads no configuration and performs no upload.
Compare its source/build identity with `service_get.build` and the worker identity
under `service_get.backends.nixos.identity.build`. A missing helper is a consumer
environment prerequisite, not a failed Nix test. The administrator supplies a
stable container-visible executable from the service package; do not assume the
host's executable path exists in the container. In this repository the documented
`just --justfile tools/vm-test-mcp/justfile nixos-test` entry selects the packaged
helper, while another consumer project uses its administrator-provided path.
With Nix and that helper available:

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
`derivation`, `system`) through `nix_job_create`. Submission validates/seals the cache
and queues work. `worker_unavailable` before acceptance does not reserve the
request ID; retain the uploaded cache and retry the same request after the worker
starts. Do not submit duplicate jobs to work around an uncertain accepted execution. If the project's wrapper hides standard test metadata/override
interfaces, adapt that project's entrypoint within its existing output policy.

Use an administrator-approved `--signing-key /absolute/private/key` when local
input-addressed objects need signatures. Preserve signature validation. Never make
the worker a trusted Nix user to work around a cache failure.

## Observation and cleanup

The initial `nix_job_create` waits for execution, evidence and cleanup, for at most
600 seconds. Use Codex CLI 0.163.0+ and `tool_timeout_sec=660`. If the window expires,
call `wait` with the returned `continue` targets; expiry is not execution failure.
For example, a 15-minute execution uses ten minutes plus one five-minute continuation.
SSE sends invisible transport heartbeats every 15 seconds. HTTP disconnect, tool
timeout and MCP cancellation stop observation only. Use `nix_job_get`,
`execution_events` and byte-offset `execution_log` for explicit diagnosis.
`nix_job_get` separates `phase`, `result` and `cleanup.state`; completion is
`phase: ended`, with the build conclusion under `result`. Logs are base64 bytes
with `next_offset`. The FIFO queue is shared with VirtualBox
labs. The daemon owns build parallelism; test definitions own node CPUs/RAM.

List completed output files with `evidence_list` and retrieve scoped URLs with
`evidence_get`. The worker reads only registered store output roots, rejects
symlink boundaries, and releases worker copies/GC roots after the controller
verifies sizes and hashes and commits durable evidence references. Imported upload
caches are released when recovery no longer needs them; host Nix GC is independent. Paths printed by test code
do not authorize host file reads. Failed-build directories belong to administrator
diagnosis; task stderr/logs remain available.

Prepare and evaluate inputs before submitting a batch to the shared executor.
`nix_job_list` defaults to compact active tasks; use `phase=ended` or `all` for
history and its task-ID cursor for paging. Queue details identify occupied or
blocked cleanup without exposing another task's command inputs.

`nix_job_cancel` requests cancellation, including abandonment while queued.
Wait for `cleanup.state: complete` before lengthy local edits or reporting;
the next execution rejoins the queue. An interrupted daemon build can have
`cleanup.state: blocked`; this continues to occupy the scheduler until the administrator
verifies teardown and performs local reconciliation. Reconnect to the original
task after HTTP loss. `execution_delete` is accepted only after terminal state and
complete cleanup. Defaults retain successful evidence for 1 day, other evidence
for 7 days, and compact summaries/receipts for 30 days. Use `evidence_pin`/`evidence_unpin`
for longer retention. Pins do not retain the executor. Evidence budgets are 20 GiB
with reclamation to 16 GiB; protected data can block new admission. Input staging
is 16 GiB total and at most 8 GiB per transfer; inactive unbound uploads expire
after one hour. Renew capabilities explicitly with `upload_renew`.

## Acceptance and diagnosis

Use a fresh execution of the linked multi-node smoke module for initial acceptance.
Actual guest startup/assertions on KVM, a sandbox host-marker probe and rejection
of `__noChroot` are required alongside administrator-confirmed daemon policy.
Device presence or a `kvm` feature declaration alone is insufficient evidence.
Backend `available` reports admission prerequisites only. Keep acceptance evidence
with the actual controller/worker/client build IDs, project lock identity, test
derivation and task artifacts. A changed build, daemon policy, KVM permissions or
test framework requires verification of the affected boundary; unknown evidence
remains unverified.

For assertions, collect relevant guest journals/serial output and change the test
before a fresh run. For host daemon, KVM, trust or baseline prerequisites, report
the preparation issue; this runtime workflow does not provision the executor.
