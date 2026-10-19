# Executing NixOS VM tests

The Agent evaluates the project in its container. A prepared HTTP MCP service submits the resulting derivation to the physical host's Nix daemon. The test framework starts its nodes directly on host KVM.

## Prepare and submit

Use `targets` to identify Nix availability and existing tasks. Allocate a binary cache with `transfer_create(kind="cache")`; its `id` and `url` identify one input transfer. Supply local Python 3.11+ and Nix through the project environment.

```sh
nix develop --command python3 scripts/nix-vm-test.py \
  '.#checks.x86_64-linux.integration' \
  --run-id project-integration-001 \
  --cache-url 'HTTP_CACHE_URL_FROM_MCP'
```

The helper evaluates with `--no-update-lock-file`, requires `config.qemu.forceAccel = true` and QEMU nodes, and uses `overrideTestDerivation` to give the top-level test a fresh name. Node images and dependencies retain their cache identities. Use a new run ID for a new execution, and the same ID when retrying an interrupted submission of identical inputs.

It copies the derivation closure with `nix copy --derivation` and prints JSON containing `request_id`, `drv` and `system`. Submit these through `task_begin(kind="nixos", request_id=…, cache_id=…, drv=…)`. Submission seals the cache and queues execution. The service's configured deadline is the maximum accepted task timeout.

The helper relies on the standard `runNixOSTest` inspection and override interfaces. If a project wrapper hides those interfaces, adapt that project's entrypoint to expose equivalent metadata and a fresh top-level derivation. Adding an entrypoint does not itself adopt new flake outputs.

## Execution and evidence

Poll `task_status(task_id)` and read `task_log` using `next_offset`. A queued task waits for current virtualization work to finish. The worker imports inputs through ordinary Nix verification and invokes the daemon with fixed build parameters, including local builder selection, `--keep-failed` and a builder timeout.

Successful results expose named output roots through `artifacts`. List a root or subdirectory, then use `artifact_download` for selected regular files. Test scripts should place useful reports, screenshots and diagnostics in their normal test output. Directory listings are limited to 2000 entries; descend into narrower directories when needed.

Text logs remain available for failed builds. Nix retains failed build directories for the administrator; automatic downloads use recorded output roots, not paths printed by arbitrary build output. Successful outputs have task-owned GC roots during retention. Retrieve needed evidence before that retention period expires; failed build directories remain administrator-owned.

`task_cancel` requests cancellation and reports its progress through `task_status`. A normal Nix completion establishes worker teardown. If the client is interrupted during a build, `cleanup_unknown` keeps virtualization occupancy until the worker administrator verifies cleanup and reconciles that task. Reconnect to the same task after an HTTP disconnect; task execution belongs to the service, not the client connection.

## Diagnosis and acceptance

Compilation parallelism belongs to the host daemon; node CPUs and RAM belong to the NixOS test module. Device presence and declared `kvm` features are preliminary evidence. Initial acceptance requires a fresh test that actually starts its nodes and passes assertions in the sandbox.

For assertion failures, inspect serial/journal output, collect relevant application logs in the test, and rerun a fresh derivation. For missing host tools, daemon access or sandbox KVM access, report the preparation requirement; do not provision the executor through this runtime workflow.

For signature failures, compare the path's provenance, NarHash and signatures with an existing trusted cache. Prefer obtaining identical signed content from that cache, or building the producing derivation on the executor. Preserve signature verification. The [preparation guide](preparation.md) covers the administrator's trust and sandbox setup.

The [Nix copy manual](https://nix.dev/manual/nix/2.34/command-ref/new-cli/nix3-copy.html) and [Nix configuration reference](https://nix.dev/manual/nix/2.34/command-ref/conf-file.html) describe the underlying interfaces.
