#!/usr/bin/env python3
"""Local Codex hook notification server.

Listens on 127.0.0.1:8765 by default. It accepts JSON POSTs from
codex_hook_forwarder.py and, according to config, runs notify-send or sends a
JSON POST request to an explicitly configured webhook endpoint.

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
"""

from __future__ import annotations

import argparse
import json
import shutil
import signal
import subprocess
import sys
import threading
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
MAX_BODY_BYTES = 2 * 1024 * 1024
MAX_WEBHOOK_BODY_BYTES = 256 * 1024
DEFAULT_MAX_HANDLER_CONCURRENCY = 8
MAX_HANDLER_CONCURRENCY = 64
FORWARDED_MESSAGE_FIELDS = {
    "protocolVersion",
    "source",
    "sentAt",
    "hookEventName",
    "severity",
    "title",
    "message",
    "summary",
}


class BoundedThreadingHTTPServer(ThreadingHTTPServer):
    """Threading HTTP server with a hard bound on live request handlers."""

    daemon_threads = True

    def __init__(
        self,
        server_address: tuple[str, int],
        request_handler_class: type[BaseHTTPRequestHandler],
        max_handler_concurrency: int,
    ) -> None:
        self.max_handler_concurrency = max_handler_concurrency
        self._handler_slots = threading.BoundedSemaphore(max_handler_concurrency)
        super().__init__(server_address, request_handler_class)

    def process_request(self, request: object, client_address: object) -> None:
        self._handler_slots.acquire()
        try:
            super().process_request(request, client_address)
        except BaseException:
            self._handler_slots.release()
            raise

    def process_request_thread(self, request: object, client_address: object) -> None:
        try:
            super().process_request_thread(request, client_address)
        finally:
            self._handler_slots.release()


def main() -> int:
    args = parse_args()
    try:
        config = load_config(args.config)
        host = (
            args.host
            if args.host is not None
            else str(config.get("listen_host") or DEFAULT_HOST)
        )
        port = (
            args.port
            if args.port is not None
            else int(config.get("listen_port") or DEFAULT_PORT)
        )
        max_body_bytes = positive_int(
            "max_body_bytes",
            args.max_body_bytes
            if args.max_body_bytes is not None
            else config.get("max_body_bytes", MAX_BODY_BYTES),
        )
        max_handler_concurrency = positive_int(
            "max_handler_concurrency",
            args.max_handler_concurrency
            if args.max_handler_concurrency is not None
            else config.get("max_handler_concurrency", DEFAULT_MAX_HANDLER_CONCURRENCY),
            maximum=MAX_HANDLER_CONCURRENCY,
        )
        validate_webhook_config(config)
    except (OSError, RuntimeError, TypeError, ValueError, json.JSONDecodeError) as exc:
        print(
            f"codex hook notify server: invalid configuration: {exc}", file=sys.stderr
        )
        return 2
    server = BoundedThreadingHTTPServer(
        (host, port),
        make_handler(config, args.verbose, max_body_bytes),
        max_handler_concurrency,
    )
    configured_events = list_value(config.get("events"), DEFAULT_EVENTS)

    stop_requested = False

    def request_stop(signum: int, _frame: object) -> None:
        nonlocal stop_requested
        stop_requested = True
        print(
            f"codex hook notify server: signal {signum}, shutting down", file=sys.stderr
        )

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)

    print(
        f"codex hook notify server: listening on http://{host}:{port}/hook",
        file=sys.stderr,
    )
    print(
        f"codex hook notify server: events={format_events(configured_events)}",
        file=sys.stderr,
    )
    server.timeout = 0.5
    try:
        while not stop_requested:
            server.handle_request()
    finally:
        server.server_close()
    return 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--config",
        help="TOML or JSON config path",
    )
    parser.add_argument("--host", help=f"listen host, default {DEFAULT_HOST}")
    parser.add_argument("--port", type=int, help=f"listen port, default {DEFAULT_PORT}")
    parser.add_argument(
        "--max-body-bytes",
        type=int,
        help=f"maximum JSON request bytes, default {MAX_BODY_BYTES}",
    )
    parser.add_argument(
        "--max-handler-concurrency",
        type=int,
        help=(
            "maximum live HTTP handlers, default "
            f"{DEFAULT_MAX_HANDLER_CONCURRENCY}, maximum {MAX_HANDLER_CONCURRENCY}"
        ),
    )
    parser.add_argument(
        "--verbose", action="store_true", help="log requests/actions to stderr"
    )
    return parser.parse_args()


def make_handler(
    config: dict[str, Any],
    verbose: bool,
    max_body_bytes: int = MAX_BODY_BYTES,
) -> type[BaseHTTPRequestHandler]:
    class Handler(BaseHTTPRequestHandler):
        server_version = "CodexHookNotifyServer/1"

        def do_GET(self) -> None:
            if self.path in {"/", "/health"}:
                self.write_json(200, {"ok": True, "time": int(time.time())})
                return
            self.write_json(404, {"ok": False, "error": "not found"})

        def do_POST(self) -> None:
            if urllib.parse.urlsplit(self.path).path not in {"/", "/hook"}:
                self.write_json(404, {"ok": False, "error": "not found"})
                return

            try:
                payload = self.read_json_body()
                validate_forwarded_message(payload)
                result = handle_message(config, payload, verbose)
                self.write_json(200, {"ok": True, **result})
            except (TypeError, UnicodeError, ValueError) as exc:
                log(verbose, f"request failed: {exc}")
                self.write_json(400, {"ok": False, "error": str(exc)})

        def log_message(self, fmt: str, *args: object) -> None:
            if verbose:
                print("http: " + fmt % args, file=sys.stderr)

        def read_json_body(self) -> dict[str, Any]:
            content_type = self.headers.get_content_type()
            if content_type != "application/json":
                raise ValueError("Content-Type must be application/json")
            length_text = self.headers.get("Content-Length")
            if not length_text:
                raise ValueError("missing Content-Length")
            try:
                length = int(length_text)
            except ValueError as exc:
                raise ValueError("invalid Content-Length") from exc
            if length < 0:
                raise ValueError("invalid Content-Length")
            if length > max_body_bytes:
                raise ValueError("request body too large")
            data = self.rfile.read(length)
            if len(data) != length:
                raise ValueError("incomplete request body")
            payload = json.loads(data.decode("utf-8"))
            if not isinstance(payload, dict):
                raise TypeError("request JSON must be an object")
            return payload

        def write_json(self, status: int, payload: dict[str, Any]) -> None:
            body = json.dumps(
                payload, ensure_ascii=False, separators=(",", ":")
            ).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    return Handler


def validate_forwarded_message(payload: dict[str, Any]) -> None:
    fields = set(payload)
    missing = sorted(FORWARDED_MESSAGE_FIELDS - fields)
    unknown = sorted(fields - (FORWARDED_MESSAGE_FIELDS | {"rawPayload"}))
    if missing:
        raise ValueError(f"forwarded message is missing fields: {', '.join(missing)}")
    if unknown:
        raise ValueError(f"forwarded message has unknown fields: {', '.join(unknown)}")
    if payload.get("protocolVersion") != 1:
        raise ValueError("protocolVersion must be 1")
    if payload.get("source") != "codex_hook_forwarder":
        raise ValueError("source must be codex_hook_forwarder")
    sent_at = payload.get("sentAt")
    if isinstance(sent_at, bool) or not isinstance(sent_at, int) or sent_at < 0:
        raise ValueError("sentAt must be a non-negative integer")
    for field in ["hookEventName", "title", "message"]:
        if not isinstance(payload.get(field), str):
            raise TypeError(f"{field} must be a string")
    if payload.get("severity") not in {"info", "warning", "error"}:
        raise ValueError("severity is invalid")
    summary = payload.get("summary")
    if not isinstance(summary, dict):
        raise TypeError("summary must be an object")
    if summary.get("hookEventName") != payload.get("hookEventName"):
        raise ValueError("summary hookEventName must match the envelope")
    if "rawPayload" in payload and not isinstance(payload["rawPayload"], dict):
        raise TypeError("rawPayload must be an object")


def handle_message(
    config: dict[str, Any], payload: dict[str, Any], verbose: bool
) -> dict[str, Any]:
    summary = payload.get("summary") if isinstance(payload.get("summary"), dict) else {}
    event_name = str(payload.get("hookEventName") or summary.get("hookEventName") or "")
    title = str(payload.get("title") or f"Codex {event_name or 'hook'}")
    message = str(payload.get("message") or "")
    severity = str(payload.get("severity") or "info")
    actions: list[dict[str, Any]] = []

    global_events = list_value(config.get("events"), DEFAULT_EVENTS)
    log(
        True,
        f"received {event_name or '<unknown>'}: severity={severity}",
    )
    if not event_enabled(event_name, global_events):
        log(
            True,
            f"skip {event_name}: not in global events {format_events(global_events)}",
        )
        return {"event": event_name, "actions": actions, "skipped": "event disabled"}

    notify_config = dict_value(config.get("notify_send"))
    if bool_value(notify_config.get("enabled"), default=True):
        notify_events = list_value(notify_config.get("events"), global_events)
        if event_enabled(event_name, notify_events):
            actions.append(
                run_notify_send(notify_config, title, message, severity, verbose)
            )
        else:
            log(
                True,
                f"skip notify-send for {event_name}: not in events {format_events(notify_events)}",
            )
    else:
        log(True, f"skip notify-send for {event_name}: disabled")

    webhook_config = dict_value(config.get("webhook"))
    if bool_value(webhook_config.get("enabled"), default=False):
        webhook_events = list_value(webhook_config.get("events"), global_events)
        if event_enabled(event_name, webhook_events):
            actions.append(run_webhook(webhook_config, title, message, verbose))
        else:
            log(
                True,
                f"skip webhook for {event_name}: not in events {format_events(webhook_events)}",
            )
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
    except (OSError, subprocess.SubprocessError, ValueError) as exc:
        log(True, f"notify-send failed: {exc}")
        return {"type": "notify-send", "ok": False, "error": str(exc)}

    ok = completed.returncode == 0
    if not ok:
        log(
            True, f"notify-send exit {completed.returncode}: {completed.stderr.strip()}"
        )
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
    title: str,
    message: str,
    verbose: bool,
) -> dict[str, Any]:
    del verbose
    url = webhook_url(config)
    timeout = float(config.get("timeout_sec") or config.get("timeoutSec") or 5)
    content = title if not message else f"{title}\n{message}"
    body = json.dumps(
        {"msgtype": "text", "text": {"content": content}},
        ensure_ascii=False,
        separators=(",", ":"),
    ).encode("utf-8")
    if len(body) > MAX_WEBHOOK_BODY_BYTES:
        log(True, "webhook rejected: encoded request body is too large")
        return {"type": "webhook", "ok": False, "error": "request body too large"}
    request = urllib.request.Request(
        url,
        data=body,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "User-Agent": "codex-hook-notify-server/1",
        },
    )
    opener = urllib.request.build_opener(NoRedirectHandler())
    try:
        with opener.open(request, timeout=timeout) as response:
            ok = 200 <= response.status < 300
            log(True, f"webhook POST completed: status={response.status}")
            return {"type": "webhook", "ok": ok, "status": response.status}
    except urllib.error.HTTPError as exc:
        exc.close()
        log(True, f"webhook POST failed: status={exc.code}")
        return {
            "type": "webhook",
            "ok": False,
            "status": exc.code,
            "error": f"HTTP status {exc.code}",
        }
    except (OSError, ValueError, urllib.error.URLError) as exc:
        log(True, f"webhook POST failed: {type(exc).__name__}")
        return {"type": "webhook", "ok": False, "error": "delivery failed"}


class NoRedirectHandler(urllib.request.HTTPRedirectHandler):
    def redirect_request(
        self,
        req: urllib.request.Request,
        fp: Any,
        code: int,
        msg: str,
        headers: Any,
        newurl: str,
    ) -> None:
        del req, fp, code, msg, headers, newurl
        return None


def validate_webhook_config(config: dict[str, Any]) -> None:
    raw = config.get("webhook")
    if raw is None:
        return
    if not isinstance(raw, dict):
        raise TypeError("webhook must be a table/object")
    if bool_value(raw.get("enabled"), default=False):
        webhook_url(raw)


def webhook_url(config: dict[str, Any]) -> str:
    value = config.get("url")
    if not isinstance(value, str) or not value or value != value.strip():
        raise ValueError("webhook.url is required when webhook is enabled")
    parsed = urllib.parse.urlsplit(value)
    if (
        parsed.scheme not in {"http", "https"}
        or not parsed.netloc
        or not parsed.hostname
    ):
        raise ValueError("webhook.url must be an absolute HTTP(S) URL")
    return value


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
        raise TypeError("config root must be an object/table")
    return data


def default_config_path() -> Path | None:
    path = Path.home() / ".codex" / "hook-notify-server.toml"
    return path if path.exists() else None


def event_enabled(event_name: str, events: list[str]) -> bool:
    return not events or "*" in events or event_name in events


def format_events(events: list[str]) -> str:
    return "*" if not events else ",".join(events)


def format_actions(actions: list[dict[str, Any]]) -> str:
    return ",".join(
        f"{action.get('type', 'unknown')}:{'ok' if action.get('ok') else 'failed'}"
        for action in actions
    )


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


def positive_int(name: str, value: Any, maximum: int | None = None) -> int:
    if isinstance(value, bool):
        raise TypeError(f"{name} must be an integer")
    try:
        parsed = int(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"{name} must be an integer") from exc
    if parsed <= 0:
        raise ValueError(f"{name} must be greater than zero")
    if maximum is not None and parsed > maximum:
        raise ValueError(f"{name} must not exceed {maximum}")
    return parsed


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
