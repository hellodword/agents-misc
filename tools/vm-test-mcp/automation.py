"""Bounded guest environment discovery; automation itself belongs to projects."""

import asyncio
import json
import time

from common import Failure


PROBE = """
import contextlib, importlib, importlib.metadata, json, platform, struct, sys
result = dict(python=sys.version.split()[0], executable=sys.executable,
              architecture=platform.machine(), bits=struct.calcsize('P') * 8,
              versions={}, errors={})
for package, module in json.loads(sys.argv[1]):
    try:
        result['versions'][package] = importlib.metadata.version(package)
        with contextlib.redirect_stdout(sys.stderr):
            importlib.import_module(module)
    except Exception as error:
        result['errors'][package] = str(error)
result['available'] = not result['errors']
print(json.dumps(result))
"""

ENVIRONMENTS = {
    "controls_python": [
        ("pywinauto", "pywinauto"),
        ("Pillow", "PIL.ImageGrab"),
        ("psutil", "psutil"),
    ],
    "vision_python": [
        ("airtest", "airtest.core.cv"),
        ("numpy", "numpy"),
        ("opencv-contrib-python", "cv2"),
    ],
}


async def probe(vm, path, packages, seconds):
    if seconds <= 0:
        return {"available": False, "error": "Task deadline reached before probe"}
    try:
        async with asyncio.timeout(seconds):
            output, _ = await vm.guest(
                "run",
                "--exe",
                path,
                "--timeout",
                str(max(1, int(seconds * 1000))),
                "--wait-stdout",
                "--wait-stderr",
                "--",
                "-c",
                PROBE,
                json.dumps(packages),
                timeout=seconds,
            )
        result = json.loads(output)
        if not isinstance(result, dict) or type(result.get("available")) is not bool:
            raise ValueError("Invalid environment probe response")
        return result
    except TimeoutError:
        return {"available": False, "error": "Environment probe exceeded its deadline"}
    except (Failure, OSError, ValueError) as error:
        return {"available": False, "error": str(error)}


async def discover(controller, task, vm):
    configured = vm.profile.get("automation", {})
    task["automation"] = {
        name: {
            "path": configured.get(name),
            "available": None if name in configured else False,
            "error": None if name in configured else "Not configured",
        }
        for name in ENVIRONMENTS
    }
    controller.save(task)
    for name, packages in ENVIRONMENTS.items():
        if name not in configured:
            continue
        task["automation"][name].update(
            await probe(
                vm,
                configured[name],
                packages,
                min(
                    controller.settings["command_timeout"],
                    task["deadline"] - time.time(),
                ),
            )
        )
        controller.save(task)
