#!/usr/bin/env python3
"""Local Codex hook notification server.

Listens on 127.0.0.1:8765 by default. It accepts JSON POSTs from
codex_hook_forwarder.py and, according to config, runs notify-send or sends a
GET request to a webhook endpoint such as https://foo.com/notify?... .

Config may be TOML or JSON. By default, the server reads
~/.codex/hook-notify-server.toml if present.

Example TOML:

    # Empty or omitted events means handle every received event.
    events = ["AbnormalStop", "RequestError"]

    [notify_send]
    enabled = true
    timeout_ms = 0

    [webhook]
    enabled = false
    url = "https://foo.com/notify"
"""

from __future__ import annotations

import argparse
import json
import shutil
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

try:
    import tomllib
except ModuleNotFoundError:  # pragma: no cover - Python < 3.11 fallback
    tomllib = None  # type: ignore[assignment]


DEFAULT_HOST = "127.0.0.1"
DEFAULT_PORT = 8765
DEFAULT_EVENTS: list[str] = []
DEFAULT_WEBHOOK_URL = "https://foo.com/notify"
MAX_BODY_BYTES = 2 * 1024 * 1024


def main() -> int:
    args = parse_args()
    config = load_config(args.config)

    host = args.host or str(config.get("listen_host") or DEFAULT_HOST)
    port = args.port or int(config.get("listen_port") or DEFAULT_PORT)
    server = ThreadingHTTPServer(
        (host, port), make_handler(config, args.verbose))
    server.daemon_threads = True
    configured_events = list_value(config.get("events"), DEFAULT_EVENTS)

    stop_requested = False

    def request_stop(signum: int, _frame: object) -> None:
        nonlocal stop_requested
        stop_requested = True
        print(
            f"codex hook notify server: signal {signum}, shutting down", file=sys.stderr)

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)

    print(
        f"codex hook notify server: listening on http://{host}:{port}/hook", file=sys.stderr)
    print(
        f"codex hook notify server: events={format_events(configured_events)}", file=sys.stderr)
    server.timeout = 0.5
    try:
        while not stop_requested:
            server.handle_request()
    finally:
        server.server_close()
    return 0 if stop_requested else 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--config",
        help="TOML or JSON config path",
    )
    parser.add_argument("--host", help=f"listen host, default {DEFAULT_HOST}")
    parser.add_argument("--port", type=int,
                        help=f"listen port, default {DEFAULT_PORT}")
    parser.add_argument("--verbose", action="store_true",
                        help="log requests/actions to stderr")
    return parser.parse_args()


def make_handler(config: dict[str, Any], verbose: bool) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        server_version = "CodexHookNotifyServer/1"

        def do_GET(self) -> None:  # noqa: N802 - stdlib API name
            if self.path in {"/", "/health"}:
                self.write_json(200, {"ok": True, "time": int(time.time())})
                return
            self.write_json(404, {"ok": False, "error": "not found"})

        def do_POST(self) -> None:  # noqa: N802 - stdlib API name
            if urllib.parse.urlsplit(self.path).path not in {"/", "/hook"}:
                self.write_json(404, {"ok": False, "error": "not found"})
                return

            try:
                payload = self.read_json_body()
                result = handle_message(config, payload, verbose)
                self.write_json(200, {"ok": True, **result})
            except Exception as exc:
                log(verbose, f"request failed: {exc}")
                self.write_json(400, {"ok": False, "error": str(exc)})

        def log_message(self, fmt: str, *args: object) -> None:
            if verbose:
                print("http: " + fmt % args, file=sys.stderr)

        def read_json_body(self) -> dict[str, Any]:
            length_text = self.headers.get("Content-Length")
            if not length_text:
                raise ValueError("missing Content-Length")
            length = int(length_text)
            if length > int(config.get("max_body_bytes") or MAX_BODY_BYTES):
                raise ValueError("request body too large")
            data = self.rfile.read(length)
            payload = json.loads(data.decode("utf-8"))
            if not isinstance(payload, dict):
                raise ValueError("request JSON must be an object")
            return payload

        def write_json(self, status: int, payload: dict[str, Any]) -> None:
            body = json.dumps(payload, ensure_ascii=False,
                              separators=(",", ":")).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    return Handler


def handle_message(config: dict[str, Any], payload: dict[str, Any], verbose: bool) -> dict[str, Any]:
    summary = payload.get("summary") if isinstance(
        payload.get("summary"), dict) else {}
    event_name = str(payload.get("hookEventName")
                     or summary.get("hookEventName") or "")
    title = str(payload.get("title") or f"Codex {event_name or 'hook'}")
    message = str(payload.get("message") or "")
    severity = str(payload.get("severity") or "info")
    actions: list[dict[str, Any]] = []

    global_events = list_value(config.get("events"), DEFAULT_EVENTS)
    log(True,
        f"received {event_name or '<unknown>'}: severity={severity} title={title!r}")
    if not event_enabled(event_name, global_events):
        log(True,
            f"skip {event_name}: not in global events {format_events(global_events)}")
        return {"event": event_name, "actions": actions, "skipped": "event disabled"}

    notify_config = dict_value(config.get("notify_send"))
    if bool_value(notify_config.get("enabled"), default=True):
        notify_events = list_value(notify_config.get("events"), global_events)
        if event_enabled(event_name, notify_events):
            actions.append(run_notify_send(
                notify_config, title, message, severity, verbose))
        else:
            log(True,
                f"skip notify-send for {event_name}: not in events {format_events(notify_events)}")
    else:
        log(True, f"skip notify-send for {event_name}: disabled")

    webhook_config = dict_value(config.get("webhook"))
    if bool_value(webhook_config.get("enabled"), default=False):
        webhook_events = list_value(
            webhook_config.get("events"), global_events)
        if event_enabled(event_name, webhook_events):
            actions.append(run_webhook(webhook_config, payload,
                           summary, title, message, severity, verbose))
        else:
            log(True,
                f"skip webhook for {event_name}: not in events {format_events(webhook_events)}")
    else:
        log(True, f"skip webhook for {event_name}: disabled")

    if not actions:
        log(True, f"handled {event_name}: no action executed")
    else:
        log(True, f"handled {event_name}: actions={format_actions(actions)}")
    return {"event": event_name, "actions": actions}


def run_notify_send(
    config: dict[str, Any],
    title: str,
    message: str,
    severity: str,
    verbose: bool,
) -> dict[str, Any]:
    binary = str(config.get("command") or "notify-send")
    if shutil.which(binary) is None:
        log(True, f"notify-send skipped: {binary!r} not found")
        return {"type": "notify-send", "ok": False, "skipped": "command not found"}

    timeout_ms = int(config.get("timeout_ms") or config.get("timeoutMs") or 0)
    urgency = str(config.get("urgency") or urgency_for(severity))
    command = [binary, "-t", str(timeout_ms), "-u", urgency, title]
    if message:
        command.append(message)

    try:
        completed = subprocess.run(
            command,
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
            text=True,
            timeout=float(config.get("process_timeout_sec") or 5),
        )
    except Exception as exc:
        log(True, f"notify-send failed: {exc}")
        return {"type": "notify-send", "ok": False, "error": str(exc)}

    ok = completed.returncode == 0
    if not ok:
        log(True,
            f"notify-send exit {completed.returncode}: {completed.stderr.strip()}")
    else:
        log(True, f"notify-send delivered: urgency={urgency}")
    return {
        "type": "notify-send",
        "ok": ok,
        "returnCode": completed.returncode,
        "error": completed.stderr.strip() if completed.stderr and not ok else None,
    }


def run_webhook(
    config: dict[str, Any],
    payload: dict[str, Any],
    summary: dict[str, Any],
    title: str,
    message: str,
    severity: str,
    verbose: bool,
) -> dict[str, Any]:
    base_url = str(config.get("url") or DEFAULT_WEBHOOK_URL)
    timeout = float(config.get("timeout_sec") or config.get("timeoutSec") or 5)
    params = webhook_params(payload, summary, title, message, severity)
    url = append_query(base_url, params)
    request = urllib.request.Request(url, method="GET", headers={
                                     "User-Agent": "codex-hook-notify-server/1"})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            ok = 200 <= response.status < 300
            log(True, f"webhook GET {response.status}: {base_url}")
            return {"type": "webhook", "ok": ok, "status": response.status}
    except urllib.error.HTTPError as exc:
        log(True, f"webhook HTTP {exc.code}: {base_url}")
        return {"type": "webhook", "ok": False, "status": exc.code, "error": str(exc)}
    except Exception as exc:
        log(True, f"webhook failed: {exc}")
        return {"type": "webhook", "ok": False, "error": str(exc)}


def webhook_params(
    payload: dict[str, Any],
    summary: dict[str, Any],
    title: str,
    message: str,
    severity: str,
) -> dict[str, str]:
    params: dict[str, str] = {
        "event": str(payload.get("hookEventName") or summary.get("hookEventName") or ""),
        "severity": severity,
        "title": title,
        "message": message,
    }
    for key in [
        "sessionId",
        "turnId",
        "agentId",
        "agentType",
        "cwd",
        "model",
        "permissionMode",
        "source",
        "trigger",
        "toolName",
        "toolUseId",
        "toolCommand",
        "provider",
        "requestType",
        "requestSubtype",
        "endpointPath",
        "retryAttempt",
        "maxRetries",
        "willRetry",
        "nextRetryAttempt",
        "errorRetryable",
        "errorKind",
        "errorMessage",
        "reason",
        "stopHookActive",
    ]:
        value = summary.get(key)
        if value is not None:
            params[key] = str(value)
    return params


def append_query(url: str, params: dict[str, str]) -> str:
    parsed = urllib.parse.urlsplit(url)
    existing = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True)
    query = urllib.parse.urlencode(existing + list(params.items()))
    return urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, parsed.path, query, parsed.fragment))


def load_config(path_text: str | None) -> dict[str, Any]:
    path = Path(path_text).expanduser() if path_text else default_config_path()
    if path is None or not path.exists():
        return {}

    raw = path.read_bytes()
    if path.suffix.lower() == ".json":
        data = json.loads(raw.decode("utf-8"))
    else:
        if tomllib is None:
            raise RuntimeError("TOML config requires Python 3.11+")
        data = tomllib.loads(raw.decode("utf-8"))
    if not isinstance(data, dict):
        raise ValueError("config root must be an object/table")
    return data


def default_config_path() -> Path | None:
    path = Path.home() / ".codex" / "hook-notify-server.toml"
    return path if path.exists() else None


def event_enabled(event_name: str, events: list[str]) -> bool:
    return not events or "*" in events or event_name in events


def format_events(events: list[str]) -> str:
    return "*" if not events else ",".join(events)


def format_actions(actions: list[dict[str, Any]]) -> str:
    return ",".join(f"{action.get('type', 'unknown')}:{'ok' if action.get('ok') else 'failed'}" for action in actions)


def list_value(value: Any, default: list[str]) -> list[str]:
    if value is None:
        return list(default)
    if isinstance(value, str):
        return split_csv(value)
    if isinstance(value, list):
        return [str(item) for item in value]
    return list(default)


def dict_value(value: Any) -> dict[str, Any]:
    return value if isinstance(value, dict) else {}


def split_csv(value: str) -> list[str]:
    return [item.strip() for item in value.split(",") if item.strip()]


def bool_value(value: Any, default: bool) -> bool:
    if value is None:
        return default
    if isinstance(value, bool):
        return value
    return truthy(str(value))


def truthy(value: str) -> bool:
    return value.strip().lower() in {"1", "true", "yes", "on"}


def urgency_for(severity: str) -> str:
    if severity == "error":
        return "critical"
    if severity == "warning":
        return "normal"
    return "low"


def log(enabled: bool, message: str) -> None:
    if enabled:
        print(f"codex hook notify server: {message}", file=sys.stderr)


if __name__ == "__main__":
    raise SystemExit(main())
