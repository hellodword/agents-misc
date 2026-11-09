# Executing NixOS VM tests

Evaluate in the development container using the tested project's locked nixpkgs.
The prepared VM Test MCP 0.15 worker submits the derivation through a task-specific
Nix daemon owned by the host executor; QEMU nodes run directly on host KVM inside its strict sandbox.

## Submission

Read `service_get.protocol`, backend availability and effective limits, and use
`nix_job_list` for the queue. `vm-test-submit --version` reports the client's own
build/source identity. Protocol version and capabilities establish compatibility;
client and service source identities describe independent packages. Use the
project's prepared executable or its pinned flake input's
`packages.x86_64-linux.vm-test-submit`. A direct invocation is:

```sh
nix run github:hellodword/agents-misc#vm-test-submit -- \
  --config /absolute/client.yaml --test '.#checks.x86_64-linux.integration'
```

The client YAML contains `endpoint` and `token`. This call prepares inputs,
submits, observes and downloads registered evidence. `--evidence manifest` selects
inventory-only retrieval. Progress summaries go to stderr; full logs and final
JSON are retained in the printed workspace. Resume with the same `--work-dir`.
The consuming repository supplies its locked test and Nix environment.

For MCP submission, allocate `upload_create(request_id=…, kind="cache")` and read
the upload ID and scoped URL from `operation.result`, then run:

```sh
vm-test-submit --test '.#checks.x86_64-linux.integration' \
  --request-id integration-001 --upload-id UPLOAD_ID --upload-url UPLOAD_URL \
  --work-dir /absolute/ignored/task-workspace --timeout-seconds 3600
```

Submit stdout JSON through `nix_job_create`. The helper refuses lockfile updates,
checks `qemu.forceAccel` and QEMU nodes, and uses the consuming project's original
framework, node configuration and script checks. Its top-level derivation has a
fresh test run identity and disables substitution; dependencies retain their
cache identities. The saved derivation and GC-root link survive checkout changes
and replacement of an expired unaccepted upload. New tests use new workspaces and
request IDs. `--timeout-seconds` applies in both call forms.

The shared local export cache reuses dependency bytes and verified digests. Its
retained budget defaults to 16 GiB; `--cache-dir` and `--cache-bytes` configure it.
The client uploads the missing-object inventory with four concurrent streams.
Server immutable objects are shared across uploads. Keep `derivation` in the
manual workspace until the accepted job ID is recorded, then remove that GC-root
link. Automatic mode journals acceptance before releasing its link. Retain
`submission.json`, useful logs and evidence. Cache reclamation preserves active
uploads.

When the worker is unavailable, retain the request and inputs for recovery. After
an uncertain submission, reconcile that request before allocating another job.
Use an administrator-approved signing key when required by the prepared cache
trust policy. The worker remains an ordinary Nix client with signature validation.

## Observation and cleanup

The initial `nix_job_create` waits for execution, evidence and cleanup, for at most
600 seconds. Use Codex CLI 0.163.0+ and `tool_timeout_sec=660`. If the window expires,
call `wait` with the returned `continue` targets; expiry is not execution failure.
For example, a 15-minute execution uses ten minutes plus one five-minute continuation.
SSE sends invisible transport heartbeats every 15 seconds. HTTP disconnect, tool
timeout and MCP cancellation stop observation only. Use `nix_job_get`,
`execution_events` and byte-offset `execution_log` for explicit diagnosis.
`nix_job_get(job_id=…)` separates `phase`, `result`, `resources`, `collection` and
`evidence`. Query and wait receipts use the same result projection.
`execution.process` records the Nix client exit; `execution.test` records driver
initialization, script outcome and timeout. Failure before the build phase records
`test_started: false`; an unavailable driver receipt records `test_started: null`
and an unconfirmed outcome. Logs are base64 bytes
with `next_offset`. The FIFO queue is shared with VirtualBox
labs. The daemon owns build parallelism; test definitions own node CPUs/RAM.

List registered files with `evidence_list(execution_id=…)` and retrieve scoped URLs
with `evidence_get`. Driver receipts, original logs/output and available final
screenshots survive script failure and timeout. Diagnosis shares 30 seconds across
nodes before release. `collection.state` records complete, partial or unavailable
capture. The executor seals retained build directories after its cgroup is empty
and exposes task-bound ordinary files. Controller size/hash verification precedes
durable handoff and release of build directories, worker copies and GC roots.
Host Nix GC is independent. Automatic downloads verify the same sizes/hashes and
resume partial files; local `downloads` tracks that stage.

Prepare and evaluate inputs before submitting a batch to the shared executor.
`nix_job_list` defaults to compact active tasks; use `phase=ended` or `all` for
history and its sequence cursor for paging. Queue details identify occupied or
blocked cleanup without exposing another task's command inputs.

`nix_job_cancel` requests cancellation, including abandonment while queued.
Wait for `resources.state: released` or `unallocated` before the next execution
batch. `resources.state: blocked` retains scheduler ownership and includes the
cause, session identity and required actor. Report that blocker and continue
independent local edits, analysis or reporting. The executor keeps observing a
known session; later confirmed release updates the original job automatically.
Use `execution_recover(execution_id=…, scope="resources")` after resolving a resource
problem, or `scope="evidence"` for evidence collection/transfer. Neither rebuilds
the test. Evidence failure keeps its files and roots for recovery, independently
of the execution slot. Reconnect to the original job after HTTP loss.

The wait conditions are `resources_released`, `evidence_complete` and `complete`;
`complete` requires the result, released resources and complete evidence. A manual
blocker returns `attention` and is excluded from `continue`. Read its cause before
recovering; repeated waits do not resolve an administrator prerequisite.
`execution_delete` requires a terminal result, released resources and complete
evidence. Defaults retain successful evidence for 1 day, other evidence
for 7 days, and compact summaries/receipts for 30 days. Use `evidence_pin`/`evidence_unpin`
for longer retention. Pins do not retain the executor. Evidence budgets are 40 GiB
with reclamation to 32 GiB; protected data can block new admission. Input staging
reserves 16 GiB of unique object bytes and permits at most 8 GiB per manifest; inactive unbound uploads expire
after one hour. Read a fresh capability with `upload_get(upload_id=…)`. Evidence uses
`evidence_get(evidence_id=…)`; pins remain readable after summary expiry. Worker
receipts expire 30 days after confirmed handoff and copy release.

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
