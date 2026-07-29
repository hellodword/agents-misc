# Codex Pure Patch Workspace

This directory maintains local patches for `https://github.com/openai/codex`
without committing a patched upstream source tree.

## Layout

```text
codex/
  upstream.yaml
  patches/
    <tag>/
      series
      <patch-file>.patch
      config.schema.json
  scripts/
    fetch-upstream.py
    apply-patches.py
    refresh-patches.py
    build.py
    test.py
.work/codex/<tag>/src/
```

`codex/patches/<tag>/series` is the patch order. Patch paths are relative to
that directory. `config.schema.json` is generated from the patched upstream
tree by running `just write-config-schema` in the upstream checkout.
Build caches are kept under `.work/codex/<tag>/target/`.

## Common Commands

Fetch or update a shallow upstream checkout:

```sh
just codex-fetch <tag>
```

Check that the committed patch series applies:

```sh
just codex-apply-check <tag>
```

Apply the patch series:

```sh
just codex-apply <tag>
```

Refresh the patch series and generated schema from the current `.work` checkout:

```sh
just codex-refresh <tag>
```

Run the narrow patch validation:

```sh
just codex-test <tag>
```

The unpatched upstream schema registry, diff site, and configuration generator
are maintained separately in
[`tools/codex-config-atlas`](../tools/codex-config-atlas/README.md).

## Maintained Codex Behavior

### OpenAI Provider Network Overrides

`model_providers.openai` may override only these network-related fields:

- `request_max_retries`
- `stream_max_retries`
- `stream_idle_timeout_ms`
- `websocket_connect_timeout_ms`
- `compact_request_timeout_ms`

If `model_providers.openai` sets any other non-default provider field, config
loading fails. This keeps the built-in OpenAI provider from becoming an
arbitrary custom provider.

Example:

```toml
[model_providers.openai]
request_max_retries = 6
stream_max_retries = 8
stream_idle_timeout_ms = 420000
websocket_connect_timeout_ms = 20000
compact_request_timeout_ms = 900000
```

`compact_request_timeout_ms` controls the total timeout for unary
`/responses/compact` requests. When it is not configured, the existing fallback
is still used:

```text
stream_idle_timeout * COMPACT_REQUEST_TIMEOUT_IDLE_MULTIPLIER
```

This field is wired through `ModelProviderInfo`, the config schema, remote
thread config proto conversion, and existing test struct literals.

`compact_request_timeout_ms` is not OpenAI-only. It is a general
`ModelProviderInfo` field, so it may be set on any user-defined provider under
`model_providers.*`.

Built-in provider IDs in this baseline are:

- `openai`
- `amazon-bedrock`
- `ollama`
- `lmstudio`

Built-in provider override rules are narrower than user-defined provider rules:

- `model_providers.openai` may override the five network fields listed above.
- `model_providers.amazon-bedrock` may override `base_url`, `auth`,
  `http_headers`, `aws.profile`, and `aws.region`; setting
  `compact_request_timeout_ms` there is rejected.
- `model_providers.ollama` and `model_providers.lmstudio` are existing
  built-ins. Define a distinct custom provider ID when customized OSS provider
  settings are needed.

### Terminal Wait Command Rules

Codex accepts root-level `terminal_wait.commands` entries for command-specific
supervised unified exec waits. There is one behavior rather than selectable
wait modes. Profile-scoped `terminal_wait` is not supported in this patch
series.

| Field               | Required | Default         | Notes                                                                 |
| ------------------- | -------- | --------------- | --------------------------------------------------------------------- |
| `pattern`           | yes      | none            | `regex-lite` pattern matched against the original `exec_command.cmd`. |
| `poll_interval_ms`  | yes      | none            | Positive runtime-controlled interval between decision points.         |
| `name`              | no       | none            | Human-readable label for the rule.                                    |
| `enabled`           | no       | `true`          | Disabled rules are skipped.                                           |
| `cwd_pattern`       | no       | none            | `regex-lite` pattern matched against the effective cwd string.        |
| `max_output_tokens` | no       | request/default | Positive model-visible output token cap for this command.             |

Rules are evaluated in TOML order and the first enabled match wins. `pattern`
matches the raw command string from the tool request. `cwd_pattern` matches the
native absolute cwd string when one is available, otherwise the `PathUri`
string. TTY requests never match; managed waits use non-TTY processes only.

For a matching command, `poll_interval_ms` replaces the tool-request
`yield_time_ms` for the initial wait. The runtime returns immediately if the
process exits. If it is still running at the end of the interval, the result
has `terminal_wait_state = "decision_required"` and offers exactly three
actions through `terminal_wait_control`:

- `continue` waits for a fresh, complete `poll_interval_ms`.
- `interrupt` sends a gentle interrupt and waits up to a fixed 10-second grace
  period.
- `terminate` performs confirmed process-tree termination.

The managed process is implicit, so `terminal_wait_control` takes no session or
process ID. For compatibility, empty `write_stdin` on that process acts as
`continue`, and a Ctrl-C write acts as `interrupt`. Other stdin writes are
rejected because managed processes are non-TTY.

Only one managed process may exist in a Codex Session, across environments and
Code Mode cells. Its gate is reserved before approval and spawning and remains
held until the managed result is consumed. If the process exits naturally after
returning `decision_required`, the next control action consumes its final output
and returns `completed`; it does not lose the implicit target or report
`NoTerminalWaitDecision`. A control action is otherwise permitted only from
`decision_required`. While the gate is held, Codex rejects new built-in terminal
starts through `exec_command` or legacy `shell_command`, and rejects interaction
with other built-in terminal sessions. A normal terminal command already
admitted before the reservation may continue in the background, but cannot be
interacted with until the managed gate clears. Non-terminal tools remain
available.

Turn or Code Mode cell cancellation does not terminate a stored managed
process. It returns the gate to `decision_required`, allowing the next turn to
continue, interrupt, or terminate it. `/stop` remains the hard-stop path: it
terminates all built-in terminal processes and clears the managed gate.

These restrictions are session-local and cover Codex's built-in terminal
tools. They do not govern another Codex Session, a child agent's separate
Session, MCP tools, or external processes.

The same wait and decision semantics apply inside Code Mode. A matching call
holds a Code Mode terminal-wait lease, so outer timer-generated `exec`/`wait`
yields cannot return control early. No separate `codex-code-mode-host`
configuration is required. When at least one rule is enabled,
`terminal_wait_control` is exposed in normal, Code Mode, and guardian tool
plans, even if the current command does not match.

Managed results report `terminal_wait_state` as `decision_required`,
`completed`, or `terminated`, along with `poll_interval_ms`. The
`available_actions` array is present only for `decision_required`.

`max_output_tokens` affects only the model-visible tool result. UI streaming and
terminal transcript events are not truncated by this setting.

Patterns are compiled with `regex-lite` during config load. Use Rust-regex-style
syntax supported by `regex-lite`; avoid look-around, backreferences, and
Unicode property classes.

Example:

```toml
[terminal_wait]

[[terminal_wait.commands]]
name = "workspace cargo tests"
pattern = "^cargo test( |$)"
cwd_pattern = "/workspaces/my-project"
poll_interval_ms = 600000
max_output_tokens = 20000

[[terminal_wait.commands]]
name = "vite dev server"
pattern = "npm run dev"
poll_interval_ms = 60000
```

`mode`, `wait_timeout_ms`, and `allow_tty` are removed and rejected as unknown
fields. When `terminal_wait` is unset, all rules are disabled, or no rule
matches, ordinary terminal wait and background polling behavior is unchanged.

### Model Request Failure Hooks

Codex exposes model request failures through two hook events. `RequestError`
is an observational hook for every failed model request attempt, including
attempts that will be retried. `AbnormalStop` is the final-stop hook for model
request failures that would end the current execution.

Together they support notification, diagnostics, and recovery policy around
provider outages, streaming disconnects, retry exhaustion, context-window
failures, usage limits, sandbox failures, and policy blocks. `AbnormalStop`
also carries the active `/goal` state and the effective permission mode, so a
hook can distinguish yolo sessions and decide whether a failed turn should
deliver turn error lifecycle events to extensions.

#### RequestError Hook

`RequestError` fires whenever a model request fails, including intermediate
retry failures and final failures. On final failure, `willRetry` is `false` and
`nextRetryAttempt` is `null`.

Trigger scope:

- normal sampling: `/responses`
- local compact: `/responses`
- remote compaction v2: `/responses`
- remote compact v1: `/responses/compact`

This hook only emits `HookStarted` / `HookCompleted` notifications. After
`HookStarted` is emitted, the hook command completes in the background. Hook
output does not block retry and does not change the later stop flow.

Example payload:

```json
{
  "sessionId": "00000000-0000-0000-0000-000000000000",
  "turnId": "turn-1",
  "transcriptPath": "/home/user/.codex/sessions/session.jsonl",
  "cwd": "/workspaces/project",
  "hookEventName": "RequestError",
  "model": "gpt-5",
  "provider": "openai",
  "requestType": "sampling",
  "requestSubtype": "normal",
  "endpointPath": "/responses",
  "retryAttempt": 1,
  "maxRetries": 5,
  "willRetry": true,
  "nextRetryAttempt": 2,
  "errorRetryable": true,
  "errorKind": "Stream",
  "errorMessage": "stream disconnected",
  "codexErrorInfo": {
    "message": "stream disconnected"
  }
}
```

#### AbnormalStop Hook

`AbnormalStop` fires once only when a final model request failure causes Codex
to stop the current execution. Codex runs the hook before it emits turn error
lifecycle events, so hook output can control whether that lifecycle is
delivered to extensions.

Included cases:

- final sampling failure in a normal turn
- final model request failure during pre-turn auto compact
- final model request failure during mid-turn auto compact
- final model request failure during a manual compact task

Excluded cases:

- user interrupt
- task replacement
- hook-initiated stop
- review event channel close
- startup prewarm cancellation
- ordinary tool handling errors

Example payload:

```json
{
  "sessionId": "00000000-0000-0000-0000-000000000000",
  "turnId": "turn-1",
  "transcriptPath": "/home/user/.codex/sessions/session.jsonl",
  "cwd": "/workspaces/project",
  "hookEventName": "AbnormalStop",
  "model": "gpt-5",
  "provider": "openai",
  "goalMode": true,
  "approvalPolicy": "never",
  "sandboxMode": "danger-full-access",
  "reason": "request_error",
  "requestType": "compact",
  "requestSubtype": "remote",
  "endpointPath": "/responses/compact",
  "retryAttempt": 4,
  "maxRetries": 4,
  "errorKind": "Timeout",
  "errorMessage": "request timed out",
  "codexErrorInfo": {
    "message": "request timed out"
  }
}
```

`goalMode` is true when the failed turn is executing the active `/goal`.
`approvalPolicy` and `sandboxMode` describe the effective permissions for the
turn; a hook can treat `approvalPolicy == "never"` and
`sandboxMode == "danger-full-access"` as yolo mode.

`errorKind` is the concrete Codex error category, such as `ServerOverloaded`,
`ConnectionFailed`, `ResponseStreamFailed`, `InternalServerError`,
`RetryLimit`, `ContextWindowExceeded`, `CyberPolicy`, `UsageLimitReached`, or
`Sandbox`.

Hook output may include:

```json
{
  "suppressTurnErrorLifecycle": true
}
```

When this field is absent, Codex suppresses turn error lifecycle delivery by
default for `/goal` turns unless `errorKind` is `CyberPolicy`. Other turns
default to normal turn error lifecycle delivery. Setting the field explicitly
overrides the default for that hook run.

This keeps active goals from being ended by transient provider, transport,
retry, context-window, usage-limit, or sandbox failures unless a hook chooses
normal lifecycle delivery. Policy blocks still use normal lifecycle delivery by
default.

### Plan Mode Request User Input Auto Resolution

Plan mode strips `autoResolutionMs` from `request_user_input`, while Default
mode with the feature enabled can pass an auto-resolution timeout through to
the client.

## Local Hook Helper Scripts

`tools/codex-hooks` contains two optional helper scripts for forwarding Codex
hook events to local desktop or webhook notifications.

Copy them into `~/.codex`:

```sh
cp tools/codex-hooks/codex_hook_forwarder.py ~/.codex/
cp tools/codex-hooks/codex_hook_notify_server.py ~/.codex/
```

Start the receiver server:

```sh
python3 ~/.codex/codex_hook_notify_server.py --host 0.0.0.0 --port 8765 --verbose
```

The server reads CLI flags and `~/.codex/hook-notify-server.toml`; it does not
read environment variables. Empty or omitted `events` means handle every
received event:

```toml
events = []

[notify_send]
enabled = true
timeout_ms = 0

[webhook]
enabled = false
url = "https://foo.com/notify"
```

Use `codex_hook_forwarder.py` as the Codex hook command. It reads hook JSON
from stdin and posts to the notification server. The forwarder does not accept
CLI arguments; configure it only with environment variables such as
`CODEX_HOOK_SERVER_URL`, `CODEX_HOOK_FORWARDER_EVENTS`,
`CODEX_HOOK_FORWARDER_TIMEOUT`, `CODEX_HOOK_FORWARDER_VERBOSE`,
`CODEX_HOOK_FORWARDER_STRICT`, `CODEX_HOOK_FORWARDER_INCLUDE_RAW`,
`CODEX_HOOK_FORWARDER_PREVIEW_LIMIT`, and
`CODEX_HOOK_FORWARDER_MAX_STDIN_BYTES`.

Minimal `~/.codex/hooks.json` example:

```json
{
  "hooks": {
    "RequestError": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "env CODEX_HOOK_SERVER_URL=http://172.17.0.1:8765/hook python3 ~/.codex/codex_hook_forwarder.py",
            "timeout": 5,
            "statusMessage": "Forwarding request error"
          }
        ]
      }
    ],
    "AbnormalStop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "env CODEX_HOOK_SERVER_URL=http://172.17.0.1:8765/hook python3 ~/.codex/codex_hook_forwarder.py",
            "timeout": 5,
            "statusMessage": "Forwarding abnormal stop"
          }
        ]
      }
    ]
  }
}
```

Review and trust command hooks with `/hooks`. If you test with `SessionStart`,
remember it fires only when a matching session starts or resumes, not in the
middle of an already-running session.

## Upgrading To A New Codex Ref

Use an explicit target. Do not infer the target from upstream tags.

Example instruction:

```text
Use pure-patch-workflow for Codex.

Upstream: https://github.com/openai/codex
Source patch ref: <source-tag>
Target upstream ref: <target-tag>

Follow codex/upstream.yaml and codex/README.md.
Use .work/codex/<tag>/src, not codex/origin.
Preserve the behavior described in codex/README.md.
Create codex/patches/<target-tag>/ with series, patch files, and config.schema.json.
Run apply check, just write-config-schema, schema diff, and the narrowest useful
cargo check. Report source, target, patch dir, series, schema, validation, and
limitations.
```

If the source patch ref is omitted, use the newest existing `rust-v*` patch
directory as the source. The target ref must still be provided explicitly.
