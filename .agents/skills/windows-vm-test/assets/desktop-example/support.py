"""Project-owned input pacing, state waits and a separate image matcher."""

import json
import math
import subprocess
import threading
import time
from pathlib import Path


class Inputs:
    def __init__(self, interval=1.0):
        if not math.isfinite(interval) or interval < 0:
            raise ValueError("Input interval must be finite and nonnegative")
        self.interval, self.finished = interval, None
        self.lock = threading.Lock()

    def perform(self, action, *args, **kwargs):
        with self.lock:
            if self.finished is not None:
                time.sleep(max(0, self.finished + self.interval - time.monotonic()))
            try:
                return action(*args, **kwargs)
            finally:
                self.finished = time.monotonic()


def wait_for(condition, timeout=10):
    deadline = time.monotonic() + timeout
    while True:
        result = condition()
        if result:
            return result
        if time.monotonic() >= deadline:
            raise TimeoutError("Application condition did not become true")
        time.sleep(min(0.1, max(0, deadline - time.monotonic())))


def locate(python, screenshot, template, timeout=10):
    result = subprocess.run(
        [str(python), str(Path(__file__).with_name("vision.py"))],
        input=json.dumps({"screenshot": str(screenshot), "template": str(template)}),
        text=True,
        encoding="utf-8",
        capture_output=True,
        timeout=timeout,
    )
    if result.returncode:
        raise RuntimeError(result.stderr or result.stdout)
    response = json.loads(result.stdout)
    if not response["found"]:
        raise AssertionError("Template was not found in the current screenshot")
    return tuple(response["point"])
