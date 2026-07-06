# OpenAI Network Configuration, Model Request Failure Hooks, and Plan Mode Input Timeout

The current patch series lives under `codex/patches/<rust-tag>/`. It is split into three patches: OpenAI provider network overrides, model request failure hooks, and Plan mode `request_user_input` auto-resolution handling.

## OpenAI Provider Network Field Overrides

`model_providers.openai` may override only these network-related fields:

- `request_max_retries`
- `stream_max_retries`
- `stream_idle_timeout_ms`
- `websocket_connect_timeout_ms`
- `compact_request_timeout_ms`

If `model_providers.openai` sets any other non-default provider field, config loading fails. This prevents the built-in OpenAI provider from becoming an arbitrary custom provider.

Example:

```toml
[model_providers.openai]
request_max_retries = 6
stream_max_retries = 8
stream_idle_timeout_ms = 420000
websocket_connect_timeout_ms = 20000
compact_request_timeout_ms = 900000
```

`compact_request_timeout_ms` controls the total timeout for unary `/responses/compact` requests. When it is not configured, the existing fallback is still used:

```text
stream_idle_timeout * COMPACT_REQUEST_TIMEOUT_IDLE_MULTIPLIER
```

This field is wired through `ModelProviderInfo`, the config schema, remote thread config proto conversion, and existing test struct literals.

`compact_request_timeout_ms` is not OpenAI-only. It is a general `ModelProviderInfo` field, so it may be set on any user-defined provider under `model_providers.*`.

Built-in provider IDs in this baseline are:

- `openai`
- `amazon-bedrock`
- `ollama`
- `lmstudio`

Built-in provider override rules are narrower than user-defined provider rules:

- `model_providers.openai` may override the five network fields listed above, including `compact_request_timeout_ms`.
- `model_providers.amazon-bedrock` may only override `aws.profile` and `aws.region`; setting `compact_request_timeout_ms` there is rejected.
- `model_providers.ollama` and `model_providers.lmstudio` are existing built-ins. Defining the same IDs in config does not replace them, so `compact_request_timeout_ms` should be set on a distinct custom provider ID if a customized OSS provider is needed.

## RequestError Hook

`RequestError` fires whenever a model request fails, including intermediate retry failures and final failures. On final failure, `willRetry` is `false` and `nextRetryAttempt` is `null`.

Trigger scope:

- normal sampling: `/responses`
- local compact: `/responses`
- remote compaction v2: `/responses`
- remote compact v1: `/responses/compact`

This hook only emits `HookStarted` / `HookCompleted` notifications. After `HookStarted` is emitted, the hook command completes in the background. Hook output does not block retry and does not change the later stop flow.

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

## AbnormalStop Hook

`AbnormalStop` fires once only when a final model request failure causes Codex to stop the current execution. After `HookStarted` is emitted, the hook command completes in the background and does not block the original stop flow.

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

## Plan Mode Request User Input Auto Resolution

The Plan mode input timeout patch preserves the local `request_user_input`
behavior: Plan mode strips `autoResolutionMs`, while Default mode with the
feature enabled can pass an auto-resolution timeout through to the client.

## Local Hook Helper Scripts

`codex/config` contains two optional helper scripts for forwarding Codex hook
events to local desktop or webhook notifications.

Copy them into `~/.codex`:

```bash
cp codex/config/codex_hook_forwarder.py ~/.codex/
cp codex/config/codex_hook_notify_server.py ~/.codex/
```

Start the receiver server:

```bash
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

Use `codex_hook_forwarder.py` as the Codex hook command. It reads the hook JSON
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

Review and trust command hooks with `/hooks`. `RequestError` and `AbnormalStop`
are emitted only after this patch is applied to Codex. If you test with
`SessionStart`, remember it fires only when a matching session starts or
resumes, not in the middle of an already-running session.

## Apply The Patch

```bash
cd /workspaces/agents-misc
just codex-fetch rust-v0.142.5
just codex-apply rust-v0.142.5
```

You can also run a dry-run apply check first:

```bash
cd /workspaces/agents-misc
just codex-apply-check rust-v0.142.5
just codex-test rust-v0.142.5
```
