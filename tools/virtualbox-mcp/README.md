# VirtualBox MCP

A Python Streamable HTTP MCP server that runs on the VirtualBox host and controls one VM. It resolves the configured VM to its UUID at startup, then provides power operations, configuration, logs and host information.

Requires Python 3.11+, `uv` and `VBoxManage`. Run as the user who owns the VM. The only direct Python dependency is the official [MCP SDK](https://github.com/modelcontextprotocol/python-sdk), pinned in [server.py](server.py)'s [inline metadata](https://docs.astral.sh/uv/guides/scripts/#declaring-script-dependencies). uv prepares the cached environment, including the SDK's HTTP runtime.

## Start and connect

From this directory:

```sh
nix-shell -p python3 uv
./server.py --vm test-vm
```

`just shell` enters the same environment. If Python and uv are already available, run the script directly. `uv run server.py --vm test-vm` is equivalent. The endpoint is `http://127.0.0.1:8765/mcp`.

Token authentication is optional. Omit `--token` or pass `--token ''` for direct access. To use a literal token:

```sh
./server.py --vm test-vm --token 'YOUR_TOKEN'
```

Set the client's `Authorization` header to `Bearer YOUR_TOKEN`. Keep the actual value in local configuration.

For a client in a container, bind the service to the host's bridge IP and use that address in the client URL. For example, a project-local `.codex/config.toml` entry for a service bound with `--host 172.17.0.1` is:

```toml
[mcp_servers.virtualbox]
url = "http://172.17.0.1:8765/mcp"
# Include this table only when the server has a nonempty token.
[mcp_servers.virtualbox.http_headers]
Authorization = "Bearer YOUR_TOKEN"
```

The [Codex configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference) describes the `url` and literal `http_headers` fields. Restart or reload the MCP client after updating its configuration. Use `vm_info` to check connectivity. `modify_vm` additionally requires a client that presents MCP form elicitation to the user.

| Parameter                   | Default                | Meaning                                     |
| --------------------------- | ---------------------- | ------------------------------------------- |
| `--vm NAME_OR_UUID`         | Required               | VM controlled by this server instance.      |
| `--host IP`                 | `127.0.0.1`            | Concrete host IP on which to listen.        |
| `--port PORT`               | `8765`                 | HTTP port.                                  |
| `--token TEXT`              | Empty                  | Optional literal Bearer token.              |
| `--vboxmanage PATH`         | `VBoxManage` from PATH | Host CLI executable.                        |
| `--command-timeout SECONDS` | `30`                   | Deadline per CLI command, up to 60 seconds. |

The service uses plain HTTP and accepts MCP clients rather than browser origins. Stdin is unused; responses use HTTP and diagnostics go to stderr. Exit codes are 0 for normal return, 2 for usage errors and 1 for runtime failure. Uvicorn preserves a termination signal after graceful cleanup, for example shell status 143 for SIGTERM.

## Tools

| Tool             | Example arguments                   | Result                                                                                                                                                                                                  |
| ---------------- | ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `vm_info`        | `{}`                                | Machine-readable configuration and current state, including CPU, nested virtualization, paging, VPID, paravirtualization and network fields supplied by this VBox version. Password fields are omitted. |
| `host_info`      | `{}`                                | `virtualbox_version` and native `host` text with CPU, OS and memory information.                                                                                                                        |
| `vm_power`       | `{"action":"start"}`                | Headless start.                                                                                                                                                                                         |
| `vm_power`       | `{"action":"shutdown"}`             | ACPI request for normal shutdown.                                                                                                                                                                       |
| `vm_power`       | `{"action":"reboot"}`               | Normal guest reboot through working Guest Additions.                                                                                                                                                    |
| `vm_power`       | `{"action":"poweroff"}`             | Immediate power cut.                                                                                                                                                                                    |
| `vm_power`       | `{"action":"reset"}`                | Hard reset.                                                                                                                                                                                             |
| `wait_for_state` | `{"state":"poweroff","timeout":30}` | Observe state until reached or deadline; return `reached`, `observed_state` and elapsed seconds.                                                                                                        |
| `read_log`       | `{"tail":true,"limit":65536}`       | Read the end of the current VBox log.                                                                                                                                                                   |
| `modify_vm`      | `{"arguments":["--cpus","4"]}`      | Ask for approval, execute once, then return observed configuration differences.                                                                                                                         |

Power operations report whether VBoxManage accepted the command. Follow shutdown/start with `wait_for_state` or `vm_info`. `running` means the VirtualBox VM process is running; SSH readiness is checked separately. A reboot may remain `running` throughout. Hard poweroff/reset can lose unsaved guest data; graceful requests stay graceful.

Commands use argument arrays and mutations are serialized. `vm_info` exposes the installed VBox version's native keys, so callers can inspect version-specific fields without a hand-maintained allowlist.

## Logs

`read_log` takes optional `index` (0), `offset` (0), `limit` (65536), `tail` (false) and `generation`. Indexes follow [VBoxManage showvminfo](https://docs.oracle.com/en/virtualization/virtualbox/7.2/user/vboxmanage.html): 0 is `VBox.log`, 1 is `VBoxHardening.log`, 2 is `VBox.log.1`, and so on. The server reads these fixed filenames from this VM's `LogFldr`; seeking to the requested range avoids repeatedly draining the entire log.

Results include `text`, actual `offset`, `next_offset`, `total_bytes`, `has_more`, `generation` and `rotated`. To follow a log, pass back its `generation` and `next_offset`. An observed replacement or truncation restarts reading at zero with `rotated: true`. `tail: true` reads the last `limit` bytes. Each result describes that read; appends and rotation can occur between calls. The generation identifies the opened file and its session header, rather than promising a filesystem snapshot.

Indexes are 0–99 and limits are 1–262144 bytes. Offsets count raw bytes; a split UTF-8 character is displayed with a replacement character.

## Configuration confirmation and readback

Every `modify_vm` request presents the VM UUID and exact argument array for human form elicitation. Approval lasts two minutes and authorizes one execution. Accepting with `confirm: true` executes it; decline/cancel returns `applied: false`. Legacy elicitation and `input_required` continuation are supported, with the continuation bound to the original arguments.

Pass options and values separately, excluding the command and VM. VBoxManage validates the options against its version and current VM state. The MCP client presents each request to the user.

After execution, the server reads the VM configuration again. The result includes `applied`, `changes` keyed by native field name with `before`/`after`, and `observed_state`. On a command failure, `applied: null` and `error` express an uncertain outcome; readback still runs. A failed readback appears as `readback_error`. Inspect the resulting state before deciding to retry; retries need their own confirmation.

## Protocol and failure behavior

`/mcp` uses standard MCP JSON-RPC over Streamable HTTP: POST carries messages, GET supports the SDK event stream and DELETE ends a legacy session. Successful tool results include JSON in `structuredContent`. Tool validation and CLI failures use MCP tool errors; protocol failures use MCP protocol errors. `modify_vm` instead returns its execution/readback outcome as described above once an approved command is attempted.

With a nonempty token, missing or invalid credentials return HTTP 401 and `{"error":{"code":"unauthorized","message":"A valid bearer token is required"}}`. The SDK handles Host/Origin checks and body limits. Request bodies are limited to 64 KiB; up to 32 sessions have a five-minute idle timeout.

CLI output and execution time are bounded. Cancellation or timeout stops the server's CLI subprocess. VM state may already have changed, so use readback to assess the outcome. Stopping the MCP service leaves the VM under VirtualBox's control.

## Validate

```sh
just fmt
just test
```

The recipes use `nix-shell -p python3 uv`. Tests load the script's dependencies using `uv run --with-requirements`; Ruff runs through `uv tool run`. Tests exercise a real local HTTP server with a synthetic VBoxManage boundary: optional authentication, power/state transitions, configuration confirmation and readback, log following/rotation, host information and process cancellation.

For nested-KVM integration, [smoke-test.nix](smoke-test.nix) runs three NixOS nodes using the shared [CPU 0 module](../../.agents/skills/nixos-vm-test/assets/qemu-cpu0.nix). The [manual preparation guide](../../.agents/skills/nixos-vm-test/references/preparation.md) owns executor setup, the observed mitigation profile and snapshot acceptance. Supply that test with the project's pinned `pkgs` and a fresh name. Its VM execution is separate from the MCP HTTP tests.

All MCP source, tests, documentation and development commands live here. `tmp/` holds ignored local state; uv manages its own dependency cache.
