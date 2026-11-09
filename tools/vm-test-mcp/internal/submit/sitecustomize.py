"""Record the consuming project's NixOS driver lifecycle and final evidence."""

import atexit
import datetime
import functools
import importlib.abc
import importlib.machinery
import json
import os
import sys
import threading
import time
import traceback
from pathlib import Path


def install(Driver):
    directory = Path(os.environ.pop("VM_TEST_EVIDENCE"))
    run_id = os.environ.pop("VM_TEST_RUN_ID")
    lock = threading.RLock()
    collected = threading.Event()
    capture_started = False
    receipt = {
        "version": 1,
        "test_run_id": run_id,
        "phase": "initializing",
        "test_started": False,
        "outcome": "unconfirmed",
        "collection": {"state": "pending"},
    }

    def save(**changes):
        with lock:
            receipt.update(changes)
            receipt["updated"] = datetime.datetime.now(datetime.timezone.utc).isoformat()
            temporary = directory / "driver-result.json.partial"
            with temporary.open("w") as stream:
                json.dump(receipt, stream)
                stream.flush()
                os.fsync(stream.fileno())
            temporary.replace(directory / "driver-result.json")

    def failed(outcome):
        with lock:
            if receipt["outcome"] != "timed_out":
                save(outcome=outcome)
        with (directory / "driver-error.log").open("a") as stream:
            traceback.print_exc(file=stream)

    def capture(driver):
        nonlocal capture_started
        with lock:
            owner = not capture_started
            capture_started = True
        if not owner:
            collected.wait(30)
            return
        started = time.monotonic()
        results = []
        result_lock = threading.Lock()
        closed = False
        save(collection={"state": "collecting"})

        def node(machine):
            items = []
            process = getattr(machine, "process", None)
            running = process is not None and process.poll() is None
            # Machine methods retain the locked framework's monitor, guest
            # connection and screenshot tools. A stalled node shares the same
            # collection deadline as every other node.
            actions = [
                ("state", lambda: {"running": running, "guest_ready": machine.is_up()}),
                ("screenshot", lambda: machine.screenshot(f"final-{machine.name}.png")),
                (
                    "guest",
                    lambda: machine.execute(
                        "systemctl --failed --no-pager; journalctl -b -n 150 --no-pager",
                        timeout=datetime.timedelta(seconds=10),
                    ),
                ),
            ]
            if not running:
                actions = actions[:1]
            elif not getattr(machine, "booted", False):
                actions = actions[:2]
                items.append({
                    "node": machine.name, "name": "guest", "state": "unavailable",
                    "error": "VM startup has not completed",
                })
            for name, action in actions:
                if time.monotonic() - started >= 30:
                    break
                item = {"node": machine.name, "name": name, "state": "complete"}
                try:
                    item["result"] = action()
                except BaseException as error:
                    item.update(state="unavailable", error=str(error))
                items.append(item)
            with result_lock:
                if not closed:
                    results.extend(items)

        threads = []
        try:
            for machine in driver.machines:
                thread = threading.Thread(target=node, args=(machine,), daemon=True)
                threads.append(thread)
                thread.start()
            for thread in threads:
                thread.join(max(0, 30 - (time.monotonic() - started)))
            with result_lock:
                closed = True
                pending = sum(thread.is_alive() for thread in threads)
                missing = pending + sum(item["state"] != "complete" for item in results)
                state = "partial" if missing else "complete"
                if missing and not any(item["state"] == "complete" for item in results):
                    state = "unavailable"
                report = {"state": state, "unfinished_nodes": pending, "items": results}
                (directory / "driver-diagnostics.json").write_text(json.dumps(report))
                save(collection={"state": state}, phase="finished")
        except BaseException:
            failed(receipt["outcome"])
            save(collection={"state": "unavailable"}, phase="finished")
        finally:
            collected.set()

    save()

    def process_exited():
        if receipt["phase"] != "finished":
            save(phase="finished", outcome="driver_failed", collection={"state": "unavailable"})

    atexit.register(process_exited)
    try:
        for name in ("__init__", "__enter__", "__exit__", "test_script", "run_tests", "terminate_test"):
            if not callable(getattr(Driver, name, None)):
                raise RuntimeError(f"NixOS driver lacks lifecycle method {name}")

        def wrap(name, before=None, success=None, failure=None, after=None):
            original = getattr(Driver, name)

            @functools.wraps(original)
            def observed(driver, *args, **kwargs):
                try:
                    if before:
                        before(driver)
                    value = original(driver, *args, **kwargs)
                    if success:
                        success(driver)
                    return value
                except BaseException:
                    if failure:
                        failure(driver)
                    raise
                finally:
                    if after:
                        after(driver)

            setattr(Driver, name, observed)

        def initialization_failed(driver):
            failed("driver_failed")
            capture(driver)

        def script_started(driver):
            save(phase="script", test_started=True)

        def timeout(driver):
            save(outcome="timed_out")
            capture(driver)

        def before_release(driver):
            timer = getattr(driver, "race_timer", None)
            if timer:
                timer.cancel()
            capture(driver)

        def run_finished(driver):
            with lock:
                if receipt["outcome"] == "unconfirmed":
                    save(outcome="passed")

        def run_failed(driver):
            with lock:
                if receipt["outcome"] == "unconfirmed":
                    failed("driver_failed")

        wrap("__init__", failure=initialization_failed)
        wrap("__enter__", success=lambda _: save(phase="ready"), failure=initialization_failed)
        wrap("test_script", before=script_started, failure=lambda _: failed("failed"))
        wrap("run_tests", success=run_finished, failure=run_failed)
        wrap("terminate_test", before=timeout)
        wrap("__exit__", before=before_release)
    except BaseException:
        failed("driver_failed")
        save(phase="finished", collection={"state": "unavailable"})
        # sitecustomize exceptions are otherwise ignored by Python.
        os._exit(1)


class DriverObserver(importlib.abc.MetaPathFinder):
    def find_spec(self, fullname, path=None, target=None):
        if fullname != "test_driver.driver":
            return None
        spec = importlib.machinery.PathFinder.find_spec(fullname, path, target)
        if spec is None or spec.loader is None:
            return None
        loader = spec.loader

        class ObservedLoader(importlib.abc.Loader):
            def create_module(self, spec):
                return loader.create_module(spec)

            def exec_module(self, module):
                loader.exec_module(module)
                install(module.Driver)

        spec.loader = ObservedLoader()
        sys.meta_path.remove(self)
        return spec


if os.environ.get("VM_TEST_EVIDENCE") and os.environ.get("VM_TEST_RUN_ID"):
    # Nix's Python wrapper adds its locked dependencies after sitecustomize.
    # Attach when that wrapper imports the driver, before constructing it.
    sys.meta_path.insert(0, DriverObserver())
