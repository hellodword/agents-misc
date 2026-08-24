from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import os
import sys
import threading
import time
import unittest
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

HELPER_ROOT = Path(__file__).resolve().parents[1]


def load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {path}")
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


forwarder = load_module(
    "codex_hook_forwarder_test_module",
    HELPER_ROOT / "codex_hook_forwarder.py",
)
server_module = load_module(
    "codex_hook_notify_server_test_module",
    HELPER_ROOT / "codex_hook_notify_server.py",
)


def request_error_payload(**overrides: object) -> dict[str, object]:
    payload: dict[str, object] = {
        "attempt": 2,
        "cwd": "/workspace",
        "endpointPath": "/responses",
        "error": {"category": "transport", "message": "connection reset"},
        "hookEventName": "RequestError",
        "model": "gpt-test",
        "nextAction": "retry",
        "operation": "sampling",
        "provider": "openai",
        "sessionId": "session-1",
        "transcriptPath": None,
        "turnId": "turn-1",
    }
    payload.update(overrides)
    return payload


class ForwarderTests(unittest.TestCase):
    def test_model_request_summary_is_the_stable_camel_case_payload(self) -> None:
        payload = request_error_payload()

        summary = forwarder.normalize_hook_payload(payload, 500)

        self.assertEqual(summary, payload)
        self.assertEqual(forwarder.severity_for(summary), "warning")
        self.assertEqual(
            forwarder.title_for(summary),
            "Codex request error: retry after attempt 2",
        )
        self.assertEqual(
            forwarder.message_for(summary),
            "openai gpt-test sampling /responses - transport: connection reset",
        )

    def test_model_request_rejects_old_unknown_and_invalid_fields(self) -> None:
        cases = [
            request_error_payload(willRetry=True),
            request_error_payload(nextAction="again"),
            request_error_payload(attempt=-1),
            request_error_payload(
                error={"category": "transport", "message": "x", "raw": {}}
            ),
            request_error_payload(endpointPath="relative"),
        ]
        for payload in cases:
            with (
                self.subTest(payload=payload),
                self.assertRaises((TypeError, ValueError)),
            ):
                forwarder.normalize_hook_payload(payload, 500)

        legacy = {"hook_event_name": "RequestError", "will_retry": True}
        self.assertEqual(forwarder.normalize_hook_payload(legacy, 500), {})

    def test_abnormal_stop_requires_its_stable_fields(self) -> None:
        payload = request_error_payload(
            hookEventName="AbnormalStop",
            nextAction="stop",
            goalMode=True,
            approvalPolicy="on-request",
            sandboxMode="workspace-write",
            reason="requestError",
        )

        self.assertEqual(forwarder.normalize_hook_payload(payload, 500), payload)
        self.assertEqual(forwarder.severity_for(payload), "error")
        with self.assertRaises(ValueError):
            forwarder.normalize_hook_payload({**payload, "reason": "legacy"}, 500)

    def test_raw_payload_is_disabled_by_default_and_explicitly_enabled(self) -> None:
        with (
            mock.patch.dict(os.environ, {}, clear=True),
            mock.patch.object(sys, "argv", ["codex_hook_forwarder.py"]),
        ):
            self.assertFalse(forwarder.load_options().include_raw)

        with (
            mock.patch.dict(
                os.environ,
                {"CODEX_HOOK_FORWARDER_INCLUDE_RAW": "1"},
                clear=True,
            ),
            mock.patch.object(sys, "argv", ["codex_hook_forwarder.py"]),
        ):
            self.assertTrue(forwarder.load_options().include_raw)

        raw_text = json.dumps(request_error_payload())
        for include_raw in [False, True]:
            environment = (
                {"CODEX_HOOK_FORWARDER_INCLUDE_RAW": "1"} if include_raw else {}
            )
            with (
                self.subTest(include_raw=include_raw),
                mock.patch.dict(os.environ, environment, clear=True),
                mock.patch.object(sys, "argv", ["codex_hook_forwarder.py"]),
                mock.patch.object(forwarder, "read_stdin", return_value=raw_text),
                mock.patch.object(forwarder, "post_json") as post_json,
            ):
                self.assertEqual(forwarder.main(), 0)
                forwarded = post_json.call_args.args[1]
                self.assertEqual("rawPayload" in forwarded, include_raw)
                self.assertEqual(forwarded["summary"], request_error_payload())

    def test_options_and_forwarded_request_size_fail_closed(self) -> None:
        invalid = {
            "CODEX_HOOK_SERVER_URL": "relative",
            "CODEX_HOOK_FORWARDER_TIMEOUT": "0",
            "CODEX_HOOK_FORWARDER_PREVIEW_LIMIT": "-1",
            "CODEX_HOOK_FORWARDER_MAX_STDIN_BYTES": "0",
            "CODEX_HOOK_FORWARDER_MAX_REQUEST_BYTES": "0",
        }
        for name, value in invalid.items():
            with (
                self.subTest(name=name),
                mock.patch.dict(os.environ, {name: value}, clear=True),
                mock.patch.object(sys, "argv", ["codex_hook_forwarder.py"]),
                self.assertRaises(ValueError),
            ):
                forwarder.load_options()

        with self.assertRaisesRegex(ValueError, "exceeded 8 bytes"):
            forwarder.post_json("http://127.0.0.1/hook", {"too": "large"}, 1, 8)


class NotifyServerTests(unittest.TestCase):
    def start_server(
        self,
        handler: type,
        concurrency: int = 2,
    ) -> tuple[object, threading.Thread]:
        server = server_module.BoundedThreadingHTTPServer(
            ("127.0.0.1", 0), handler, concurrency
        )
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        return server, thread

    def stop_server(self, server: object, thread: threading.Thread) -> None:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)

    def test_http_body_and_content_type_limits_are_enforced(self) -> None:
        handler = server_module.make_handler(
            {"notify_send": {"enabled": False}}, False, max_body_bytes=256
        )
        server, thread = self.start_server(handler)
        url = f"http://127.0.0.1:{server.server_port}/hook"
        try:
            envelope = {
                "protocolVersion": 1,
                "source": "codex_hook_forwarder",
                "sentAt": 1,
                "hookEventName": "Test",
                "severity": "info",
                "title": "title",
                "message": "",
                "summary": {"hookEventName": "Test"},
            }
            base_body = json.dumps(envelope, separators=(",", ":")).encode()
            envelope["message"] = "a" * (256 - len(base_body))
            body = json.dumps(envelope, separators=(",", ":")).encode()
            self.assertEqual(len(body), 256)
            request = urllib.request.Request(
                url,
                data=body,
                headers={"Content-Type": "application/json"},
                method="POST",
            )
            with urllib.request.urlopen(request, timeout=2) as response:
                self.assertEqual(response.status, 200)

            for data, content_type in [
                (b"x" * 257, "application/json"),
                (body, "text/plain"),
            ]:
                with self.subTest(content_type=content_type, size=len(data)):
                    request = urllib.request.Request(
                        url,
                        data=data,
                        headers={"Content-Type": content_type},
                        method="POST",
                    )
                    with self.assertRaises(urllib.error.HTTPError) as raised:
                        urllib.request.urlopen(request, timeout=2)
                    self.assertEqual(raised.exception.code, 400)
        finally:
            self.stop_server(server, thread)

    def test_live_handlers_never_exceed_configured_concurrency(self) -> None:
        lock = threading.Lock()
        at_capacity = threading.Event()
        release = threading.Event()
        state = {"active": 0, "maximum": 0}

        class BlockingHandler(server_module.BaseHTTPRequestHandler):
            def do_GET(self) -> None:
                with lock:
                    state["active"] += 1
                    state["maximum"] = max(state["maximum"], state["active"])
                    if state["active"] == 2:
                        at_capacity.set()
                release.wait(timeout=2)
                self.send_response(200)
                self.send_header("Content-Length", "0")
                self.end_headers()
                with lock:
                    state["active"] -= 1

            def log_message(self, _format: str, *_args: object) -> None:
                pass

        server, thread = self.start_server(BlockingHandler, concurrency=2)
        url = f"http://127.0.0.1:{server.server_port}/"
        try:
            with ThreadPoolExecutor(max_workers=4) as executor:
                futures = [
                    executor.submit(urllib.request.urlopen, url, None, 3)
                    for _ in range(4)
                ]
                self.assertTrue(at_capacity.wait(timeout=2))
                time.sleep(0.1)
                self.assertEqual(state["maximum"], 2)
                release.set()
                for future in futures:
                    with future.result() as response:
                        self.assertEqual(response.status, 200)
        finally:
            release.set()
            self.stop_server(server, thread)

    def test_non_loopback_binding_remains_supported(self) -> None:
        handler = server_module.make_handler({}, False, max_body_bytes=256)
        server = server_module.BoundedThreadingHTTPServer(("0.0.0.0", 0), handler, 1)
        try:
            self.assertEqual(server.server_address[0], "0.0.0.0")
        finally:
            server.server_close()

    def test_enabled_webhook_requires_an_absolute_http_url_before_listen(self) -> None:
        for url in [None, "", "relative", "ftp://example.invalid/hook"]:
            webhook = {"enabled": True}
            if url is not None:
                webhook["url"] = url
            with (
                self.subTest(url=url),
                self.assertRaisesRegex(ValueError, "webhook.url"),
            ):
                server_module.validate_webhook_config({"webhook": webhook})

        args = SimpleNamespace(
            config=None,
            host=None,
            port=None,
            max_body_bytes=None,
            max_handler_concurrency=None,
            verbose=False,
        )
        with (
            mock.patch.object(server_module, "parse_args", return_value=args),
            mock.patch.object(
                server_module,
                "load_config",
                return_value={"webhook": {"enabled": True}},
            ),
            mock.patch.object(server_module, "BoundedThreadingHTTPServer") as server,
            contextlib.redirect_stderr(io.StringIO()),
        ):
            self.assertEqual(server_module.main(), 2)
            server.assert_not_called()

    def test_webhook_posts_exact_json_and_preserves_only_configured_query(self) -> None:
        captured: dict[str, object] = {}

        class WebhookHandler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                length = int(self.headers["Content-Length"])
                captured.update(
                    method=self.command,
                    path=self.path,
                    content_type=self.headers.get("Content-Type"),
                    body=self.rfile.read(length),
                )
                self.send_response(204)
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, _format: str, *_args: object) -> None:
                pass

        server, thread = self.start_server(WebhookHandler)
        url = f"http://127.0.0.1:{server.server_port}/notify?configured=value"
        try:
            result = server_module.run_webhook(
                {"url": url}, "Synthetic title", "Synthetic message", False
            )
        finally:
            self.stop_server(server, thread)

        self.assertEqual(result, {"type": "webhook", "ok": True, "status": 204})
        self.assertEqual(captured["method"], "POST")
        self.assertEqual(captured["path"], "/notify?configured=value")
        self.assertEqual(captured["content_type"], "application/json")
        self.assertEqual(
            json.loads(captured["body"]),
            {
                "msgtype": "text",
                "text": {"content": "Synthetic title\nSynthetic message"},
            },
        )

    def test_webhook_rejects_redirects_and_oversize_encoded_bodies(self) -> None:
        requests: list[str] = []

        class RedirectHandler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                requests.append(self.path)
                self.send_response(302)
                self.send_header("Location", "/followed")
                self.send_header("Content-Length", "0")
                self.end_headers()

            def do_GET(self) -> None:
                requests.append(self.path)
                self.send_response(204)
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, _format: str, *_args: object) -> None:
                pass

        server, thread = self.start_server(RedirectHandler)
        try:
            result = server_module.run_webhook(
                {"url": f"http://127.0.0.1:{server.server_port}/start"},
                "title",
                "",
                False,
            )
        finally:
            self.stop_server(server, thread)

        self.assertEqual(result["status"], 302)
        self.assertFalse(result["ok"])
        self.assertEqual(requests, ["/start"])

        with (
            mock.patch.object(server_module, "MAX_WEBHOOK_BODY_BYTES", 8),
            mock.patch.object(server_module.urllib.request, "build_opener") as opener,
        ):
            result = server_module.run_webhook(
                {"url": "https://example.invalid/hook"}, "title", "message", False
            )
        self.assertEqual(result["error"], "request body too large")
        opener.assert_not_called()

    def test_webhook_failure_logs_do_not_disclose_url_or_message(self) -> None:
        class FailureHandler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                self.send_response(500)
                self.send_header("Content-Length", "0")
                self.end_headers()

            def log_message(self, _format: str, *_args: object) -> None:
                pass

        server, thread = self.start_server(FailureHandler)
        secret_query = "configured-secret-token"
        secret_title = "private title"
        secret_message = "private message"
        stderr = io.StringIO()
        try:
            with contextlib.redirect_stderr(stderr):
                result = server_module.run_webhook(
                    {
                        "url": (
                            f"http://127.0.0.1:{server.server_port}/hook"
                            f"?token={secret_query}"
                        )
                    },
                    secret_title,
                    secret_message,
                    True,
                )
        finally:
            self.stop_server(server, thread)

        self.assertEqual(result["status"], 500)
        log_text = stderr.getvalue()
        self.assertNotIn(secret_query, log_text)
        self.assertNotIn(secret_title, log_text)
        self.assertNotIn(secret_message, log_text)

    def test_invalid_resource_limits_are_rejected(self) -> None:
        for value in [0, -1, True, "bad"]:
            with self.subTest(value=value), self.assertRaises((TypeError, ValueError)):
                server_module.positive_int("limit", value)
        with self.assertRaises(ValueError):
            server_module.positive_int("limit", 65, maximum=64)

    def test_forwarded_envelope_rejects_missing_unknown_and_mismatched_fields(
        self,
    ) -> None:
        valid = {
            "protocolVersion": 1,
            "source": "codex_hook_forwarder",
            "sentAt": 1,
            "hookEventName": "RequestError",
            "severity": "warning",
            "title": "title",
            "message": "message",
            "summary": {"hookEventName": "RequestError"},
        }
        server_module.validate_forwarded_message(valid)
        invalid = [
            {key: value for key, value in valid.items() if key != "summary"},
            {**valid, "unknown": True},
            {**valid, "protocolVersion": 2},
            {**valid, "sentAt": True},
            {**valid, "summary": {"hookEventName": "AbnormalStop"}},
            {**valid, "rawPayload": "raw"},
        ]
        for payload in invalid:
            with (
                self.subTest(payload=payload),
                self.assertRaises((TypeError, ValueError)),
            ):
                server_module.validate_forwarded_message(payload)


if __name__ == "__main__":
    unittest.main()
