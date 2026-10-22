import asyncio
import json
import time
import unittest
from unittest.mock import AsyncMock, patch

from automation import ENVIRONMENTS, probe
from common import Failure
from desktop import Backend
from server import Tools
from tasks import Controller
import test_desktop


class AutomationTests(unittest.IsolatedAsyncioTestCase):
    asyncSetUp = test_desktop.DesktopTests.asyncSetUp
    asyncTearDown = test_desktop.DesktopTests.asyncTearDown

    async def test_gui_task_start_and_concurrent_input_retry(self):
        await self.ctl.finish("run")
        await self.ctl.runners["run"]
        task = await self.ctl.begin("windows", "visible", "windows", gui=True)
        task.update(state="preparing", deadline=time.time() + 120)
        await self.ctl.prepare(task)
        self.assertTrue(self.ctl.status("visible")["gui"])
        starts = [call for call in self.vm.calls if call[0] == "startvm"]
        self.assertEqual(starts[-1][2:4], ["--type", "gui"])
        entered, release = asyncio.Event(), asyncio.Event()

        async def action(chords):
            entered.set()
            await release.wait()
            return {}

        with patch.object(Backend, "keyboard", side_effect=action) as send:
            first = asyncio.create_task(
                self.tools.vm_keyboard("visible", "same", [["A"]])
            )
            await entered.wait()
            second = asyncio.create_task(
                self.tools.vm_keyboard("visible", "same", [["A"]])
            )
            await asyncio.sleep(0)
            release.set()
            a, b = await asyncio.gather(first, second)
        self.assertEqual(a, b)
        self.assertEqual(a["state"], "delivered")
        send.assert_awaited_once()

    async def test_start_choice_is_recorded_and_deduplicated(self):
        starts = [call for call in self.vm.calls if call[0] == "startvm"]
        self.assertEqual(starts[-1][2:4], ["--type", "headless"])
        self.assertFalse(self.ctl.status("run")["gui"])
        with self.assertRaises(Failure):
            await self.ctl.begin("windows", "run", "windows", gui=True)
        self.assertIs(await self.ctl.begin("windows", "run", "windows"), self.task)
        # Pre-0.8 persisted requests still deduplicate with the default choice.
        self.task["request"].pop("gui")
        self.assertIs(await self.ctl.begin("windows", "run", "windows"), self.task)
        await self.vm.power("start", gui=True)
        starts = [call for call in self.vm.calls if call[0] == "startvm"]
        self.assertEqual(starts[-1][2:4], ["--type", "gui"])

    async def test_conflicting_retry_rejected_when_queue_times_out(self):
        entering, allow_live, release_action = (
            asyncio.Event(),
            asyncio.Event(),
            asyncio.Event(),
        )
        live = self.ctl.desktop.live

        async def slow_live(*args):
            entering.set()
            await allow_live.wait()
            return await live(*args)

        async def action(*args):
            await release_action.wait()
            return {}

        with (
            patch.object(self.ctl.desktop, "live", side_effect=slow_live),
            patch.object(Backend, "keyboard", side_effect=action),
        ):
            self.ctl.settings["command_timeout"] = 1
            first = asyncio.create_task(
                self.tools.vm_keyboard("run", "conflict", [["A"]])
            )
            await entering.wait()
            self.ctl.settings["command_timeout"] = 0.02
            second = asyncio.create_task(
                self.tools.vm_keyboard("run", "conflict", [["B"]])
            )
            await asyncio.sleep(0)
            allow_live.set()
            try:
                with self.assertRaisesRegex(Failure, "different inputs"):
                    await second
            finally:
                release_action.set()
            self.assertEqual((await first)["state"], "delivered")

    async def test_missing_or_broken_environments_do_not_block_project_setup(self):
        self.vm.profile["automation"] = {
            "controls_python": r"C:\controls\python.exe",
            "vision_python": r"C:\vision\python.exe",
        }
        with patch.object(
            self.vm, "guest", new=AsyncMock(side_effect=Failure("Missing interpreter"))
        ):
            await self.ctl.prepare(self.task)
        self.assertEqual(self.task["state"], "ready")
        for result in self.ctl.status("run")["automation"].values():
            self.assertFalse(result["available"])
            self.assertIn("Missing interpreter", result["error"])
        targets = await self.tools.targets()
        self.assertEqual(targets["vms"][0]["automation"], self.vm.profile["automation"])

    async def test_native_actions_are_serial_and_interval_follows_completion(self):
        self.vm.profile["input_interval_seconds"] = 0.03
        events = []

        async def action(*args, **kwargs):
            events.append(("start", time.monotonic()))
            await asyncio.sleep(0.02)
            events.append(("end", time.monotonic()))
            return {}

        with (
            patch.object(Backend, "keyboard", side_effect=action),
            patch.object(Backend, "helper", side_effect=action),
        ):
            results = await asyncio.gather(
                self.tools.vm_keyboard("run", "one", [["ENTER"]]),
                self.tools.vm_mouse("run", "two", "drag", end_x=50, end_y=50),
                self.tools.vm_keyboard(
                    "run", "three", [["A"]], input_interval_seconds=0
                ),
            )
        self.assertEqual([e[0] for e in events], ["start", "end"] * 3)
        self.assertGreaterEqual(events[2][1] - events[1][1], 0.029)
        self.assertEqual(results[1]["input_interval_seconds"], 0.03)
        self.assertGreaterEqual(results[1]["waited_seconds"], 0.03)
        self.assertEqual(results[2]["input_interval_seconds"], 0)
        self.assertTrue(all(r["state"] == "delivered" for r in results))

    async def test_wait_timeout_never_delivers_and_receipt_survives_restart(self):
        await self.tools.vm_keyboard("run", "first", [["ENTER"]])
        self.ctl.settings["command_timeout"] = 0.02
        with patch.object(Backend, "keyboard", new=AsyncMock()) as action:
            started = time.monotonic()
            result = await self.tools.vm_keyboard("run", "timeout", [["A"]])
            elapsed = time.monotonic() - started
        action.assert_not_awaited()
        self.assertFalse(result["delivery_started"])
        self.assertEqual(result["state"], "unconfirmed")
        # VM checks and receipt persistence also consume the command budget.
        self.assertGreaterEqual(elapsed, 0.019)
        self.assertGreater(result["waited_seconds"], 0)
        self.assertLessEqual(result["waited_seconds"], elapsed)
        await self.ctl.close()
        self.ctl = Controller(self.config, {"windows": self.vm})
        self.tools = Tools(self.ctl)
        await self.ctl.start()
        self.assertEqual(
            await self.tools.vm_keyboard("run", "timeout", [["A"]]), result
        )
        self.ctl.settings["command_timeout"] = 1
        recovered = await self.tools.vm_keyboard(
            "run", "recovered", [["A"]], input_interval_seconds=0.03
        )
        self.assertGreaterEqual(recovered["waited_seconds"], 0.029)
        with self.assertRaises(Failure):
            await self.tools.vm_keyboard(
                "run", "recovered", [["A"]], input_interval_seconds=0
            )

    async def test_lock_wait_counts_towards_deadline_and_idle_time_reduces_wait(self):
        self.ctl.settings["command_timeout"] = 0.02
        async with self.ctl.desktop.lock("run"):
            result = await self.tools.vm_keyboard("run", "queued", [["A"]])
        self.assertFalse(result["delivery_started"])
        self.assertGreaterEqual(result["waited_seconds"], 0.019)
        self.ctl.settings["command_timeout"] = 1
        self.ctl.desktop.input_finished["run"] = time.monotonic() - 2
        result = await self.tools.vm_keyboard("run", "idle", [["A"]])
        self.assertLess(result["waited_seconds"], 0.1)
        for value in (-1, float("nan"), float("inf"), True):
            with self.subTest(value=value), self.assertRaises(Failure):
                await self.tools.vm_keyboard(
                    "run", "invalid", [["A"]], input_interval_seconds=value
                )


class ProbeTests(unittest.IsolatedAsyncioTestCase):
    async def test_versions_and_failed_import_are_preserved(self):
        expected = {
            "available": False,
            "python": "3.11.17",
            "versions": {"pywinauto": "0.6.9"},
            "errors": {"pywinauto": "COM unavailable"},
        }
        vm = AsyncMock()
        vm.guest.return_value = (json.dumps(expected).encode(), 10)
        self.assertEqual(
            await probe(vm, r"C:\tools\python.exe", ENVIRONMENTS["controls_python"], 1),
            expected,
        )
        self.assertIn("--timeout", vm.guest.call_args.args)

    async def test_blocked_import_has_an_outer_deadline(self):
        async def blocked(*args, **kwargs):
            await asyncio.Event().wait()

        vm = AsyncMock()
        vm.guest.side_effect = blocked
        result = await probe(vm, r"C:\tools\python.exe", [], 0.01)
        self.assertFalse(result["available"])
        self.assertIn("deadline", result["error"])
