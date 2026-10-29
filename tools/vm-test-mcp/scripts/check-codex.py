"""Exercise the production MCP transport with the two supported Codex binaries.

No model calls or real VM resources are used. Takes about eleven minutes.
Run inside the vm-test-mcp development shell from the repository root.
"""

import argparse
import concurrent.futures
import hashlib
import http.client
import http.server
import json
import os
import queue
import shutil
import signal
import subprocess
import sys
import threading
import time
from pathlib import Path
from urllib.parse import urlsplit


def process_group_exists(pid):
    try:
        os.killpg(pid, 0)
        return True
    except ProcessLookupError:
        return False


def stop_process(process, timeout=10):
    if process.stdin:
        try:
            process.stdin.close()
        except BrokenPipeError:
            pass
    try:
        process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        pass
    for action in (signal.SIGTERM, signal.SIGKILL):
        if not process_group_exists(process.pid):
            process.wait(timeout=timeout)
            return
        try:
            os.killpg(process.pid, action)
        except ProcessLookupError:
            pass
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            process.poll()
            if not process_group_exists(process.pid):
                return
            time.sleep(0.05)
    raise RuntimeError(f"process group {process.pid} has not exited")


class AcceptanceRun:
    """Own the fixture and Codex processes, retaining only their reports on exit."""

    def __init__(self, work, report):
        self.work, self.report = work, report
        self.runtime = work / "runtime"
        self.runtime.mkdir()
        (self.runtime / "tmp").mkdir()
        self.processes = []
        self.proxy = None
        self.fixture_info = self.runtime / "fixture.json"

    def __enter__(self):
        return self

    def start(self, label, argv, **kwargs):
        log = (self.work / f"{label}.log").open("w")
        environment = {**os.environ, **kwargs.pop("env", {}), "TMPDIR": str(self.runtime / "tmp")}
        try:
            process = subprocess.Popen(argv, stderr=log, start_new_session=True,
                                       env=environment, **{"stdout": log, **kwargs})
        except BaseException:
            log.close()
            raise
        self.processes.append((label, process, log))
        return process

    def __exit__(self, kind, error, traceback):
        failures = []
        try:
            self.fixture_info.with_suffix(".json.stop").touch()
        except OSError as failure:
            failures.append(f"fixture stop request: {failure}")
        for label, process, log in reversed(self.processes):
            try:
                stop_process(process)
            except (OSError, subprocess.TimeoutExpired, RuntimeError) as failure:
                failures.append(f"{label}: {failure}")
            finally:
                log.close()
        if self.proxy:
            try:
                self.proxy.shutdown()
                self.proxy.server_close()
            except (OSError, RuntimeError) as failure:
                failures.append(f"cancellation proxy: {failure}")
        self.report["processes"] = {label: {"pid": process.pid, "exit_code": process.returncode}
                                    for label, process, _ in self.processes}
        if not failures:
            try:
                shutil.rmtree(self.runtime)
            except OSError as failure:
                failures.append(str(failure))
        self.report["cleanup"] = {"state": "pending" if failures else "complete"}
        if failures:
            self.report["cleanup"].update(runtime=str(self.runtime), errors=failures)
            print(f"Cleanup pending; retain {self.runtime}: {'; '.join(failures)}", file=sys.stderr)
        if error:
            self.report["error"] = f"{kind.__name__}: {error}"
        (self.work / "result.json").write_text(json.dumps(self.report, indent=2) + "\n")
        if failures and error is None:
            raise RuntimeError("acceptance runtime cleanup is pending; inspect result.json")


class Client:
    def __init__(self, run, label, binary, endpoint, timeout):
        self.messages = queue.Queue()
        self.sequence = 0
        directory = run.runtime / label
        directory.mkdir()
        settings = {"acceptance": {"url": endpoint, "http_headers": {
            "Authorization": "Bearer synthetic-test-token"}, "tool_timeout_sec": timeout}}
        self.process = run.start(label,
            [binary, "app-server", "--stdio", "-c", "analytics.enabled=false"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True,
            cwd=directory, env={**os.environ, "CODEX_HOME": str(directory)},
        )
        threading.Thread(target=self.read, daemon=True).start()
        self.call("initialize", {"clientInfo": {"name": "vm-test-acceptance", "version": "1"},
                                 "capabilities": {"experimentalApi": True}})
        self.send({"method": "initialized", "params": {}})
        result = self.call("thread/start", {"ephemeral": True, "cwd": str(directory),
                                          "approvalPolicy": "never", "config": {"mcp_servers": settings}})
        self.thread = result["thread"]["id"]

    def read(self):
        try:
            for line in self.process.stdout:
                self.messages.put(json.loads(line))
        finally:
            self.process.stdout.close()
            self.messages.put({"closed": True})

    def send(self, value):
        self.process.stdin.write(json.dumps(value) + "\n")
        self.process.stdin.flush()

    def call(self, method, params):
        self.sequence += 1
        self.send({"id": self.sequence, "method": method, "params": params})
        deadline = time.monotonic() + 700
        while time.monotonic() < deadline:
            message = self.messages.get(timeout=max(0.1, deadline - time.monotonic()))
            if message.get("closed"):
                raise RuntimeError("Codex app-server closed; inspect its retained log")
            if message.get("id") == self.sequence:
                if "error" in message:
                    raise RuntimeError(message["error"])
                return message["result"]
        raise TimeoutError(method)

    def tool(self, name, args):
        result = self.call("mcpServer/tool/call", {"server": "acceptance", "threadId": self.thread,
                                                "tool": name, "arguments": args})
        result = result.get("result", result)
        if result.get("isError"):
            raise RuntimeError(result)
        if "structuredContent" in result:
            return result["structuredContent"]
        return json.loads(result["content"][0]["text"])


def cancellation_proxy(endpoint, records):
    target = urlsplit(endpoint)

    class Proxy(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            body = self.rfile.read(int(self.headers.get("Content-Length", 0)))
            message = json.loads(body)
            if message.get("method") == "notifications/cancelled":
                records.append(message)
            connection = http.client.HTTPConnection(target.hostname, target.port, timeout=700)
            try:
                headers = {k: v for k, v in self.headers.items() if k.lower() not in ("host", "connection")}
                connection.request("POST", "/mcp", body, headers)
                response = connection.getresponse()
                self.send_response(response.status)
                for key, value in response.getheaders():
                    if key.lower() not in ("transfer-encoding", "connection"):
                        self.send_header(key, value)
                self.end_headers()
                while data := response.read1(8192):
                    self.wfile.write(data)
                    self.wfile.flush()
            except (BrokenPipeError, ConnectionResetError, http.client.IncompleteRead):
                pass
            finally:
                connection.close()

        def do_GET(self):
            self.send_response(405)
            self.end_headers()

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Proxy)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def check_long_wait(client, report, started):
    start = time.monotonic()
    started.set()
    created = client.tool("lab_create", {"request_id": "long-window", "nodes": []})
    elapsed = time.monotonic() - start
    assert created["wait"]["reason"] == "timeout" and 598 <= elapsed < 650, (elapsed, created)
    report["first_window"] = {"elapsed": elapsed, "reply": created}
    print("0.163.0: 600-second observation window returned a continuation", flush=True)
    completed = client.tool("wait", {"targets": created["wait"]["continue"]})
    assert completed["reason"] == "satisfied", completed
    report["continued"] = {"elapsed": time.monotonic() - start, "reply": completed}
    lab = created["operation"]["execution_id"]
    ended = client.tool("lab_cancel", {"request_id": "long-cleanup", "lab_id": lab})
    assert ended["wait"]["targets"][0]["satisfied"], ended
    print("0.163.0: continuation and cleanup completed", flush=True)


def main():
    def interrupted(*_):
        raise KeyboardInterrupt("termination requested")
    signal.signal(signal.SIGTERM, interrupted)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--codex-0163", required=True, type=Path)
    parser.add_argument("--codex-0165", required=True, type=Path)
    parser.add_argument("--work-dir", required=True, type=Path, help="new ignored output directory")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[3]
    work = args.work_dir.resolve()
    work.mkdir(parents=True, exist_ok=False)
    report = {"binaries": {}}
    for version, binary in [("0.163.0", args.codex_0163), ("0.165.0", args.codex_0165)]:
        output = subprocess.check_output([str(binary.resolve()), "--version"], text=True).strip()
        assert output == "codex-cli " + version, output
        report["binaries"][version] = {"version": output, "sha256": hashlib.sha256(binary.read_bytes()).hexdigest()}
    with AcceptanceRun(work, report) as run:
        executable = run.runtime / "fixture"
        build = run.start("build", ["go", "test", "-c", "-tags", "sqlite_omit_load_extension", "-o", str(executable),
                                    "./internal/control"], cwd=root / "tools/vm-test-mcp")
        if build.wait() != 0:
            raise RuntimeError("fixture compilation failed; inspect build.log")
        report["fixture_sha256"] = hashlib.sha256(executable.read_bytes()).hexdigest()
        info = run.fixture_info
        fixture = run.start("fixture", [str(executable), "-test.run=^TestCodexTransportFixture$", "-test.timeout=25m"],
                            env={**os.environ, "VM_TEST_CODEX_FIXTURE": str(info)})
        deadline = time.monotonic() + 30
        while not info.exists():
            if fixture.poll() is not None or time.monotonic() >= deadline:
                raise RuntimeError("fixture did not start; inspect fixture.log")
            time.sleep(0.1)
        endpoint = json.loads(info.read_text())["url"]
        records = []
        run.proxy = cancellation_proxy(endpoint, records)
        observed = f"http://127.0.0.1:{run.proxy.server_port}/mcp"
        clients = []
        for label, binary, timeout in [("minimum", args.codex_0163, 660),
                                        ("cancel", args.codex_0165, 2),
                                        ("continue", args.codex_0165, 660)]:
            clients.append(Client(run, label, str(binary.resolve()), observed, timeout))
        minimum, cancelled, observer = clients
        started = threading.Event()
        pool = concurrent.futures.ThreadPoolExecutor(max_workers=1)
        try:
            future = pool.submit(check_long_wait, minimum, report, started)
            started.wait()
            deadline = time.monotonic() + 15
            while not (known := observer.tool("lab_list", {})["items"]):
                assert time.monotonic() < deadline, "first submission was not accepted"
                time.sleep(0.1)
            try:
                cancelled.tool("lab_create", {"request_id": "cancel-observer", "nodes": []})
            except RuntimeError:
                pass
            else:
                raise AssertionError("0.165.0 tool timeout was not observed")
            labs = observer.tool("lab_list", {})["items"]
            accepted = [lab for lab in labs if lab["id"] not in {item["id"] for item in known}]
            assert len(accepted) == 1 and accepted[0]["phase"] == "queued", accepted
            assert records, "0.165.0 sent no MCP cancellation notification"
            print("0.165.0: cancellation notification observed; queued execution retained", flush=True)
            lab = accepted[0]["id"]
            ready = observer.tool("wait", {"targets": [{"kind": "lab", "id": lab, "condition": "ready"}]})
            while ready["reason"] == "timeout":
                ready = observer.tool("wait", {"targets": ready["continue"]})
            assert ready["targets"][0]["satisfied"], ready
            ended = observer.tool("lab_cancel", {"request_id": "cancel-cleanup", "lab_id": lab})
            assert ended["wait"]["targets"][0]["satisfied"], ended
            report["cancellation"] = {"notifications": records, "accepted": accepted, "ready": ready, "cleanup": ended}
            future.result()
        finally:
            pool.shutdown(wait=False, cancel_futures=True)
        print("0.165.0: accepted execution became ready and cleaned up", flush=True)
    assert fixture.returncode == 0, "fixture failed; inspect fixture.log"


if __name__ == "__main__":
    main()
