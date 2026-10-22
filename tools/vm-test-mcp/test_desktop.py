import asyncio
import json
import struct
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch

from common import DEFAULTS, Failure
from desktop import Backend, scancodes
from server import Tools
from tasks import Controller
from test_server import PNG, RecordingVM


class DesktopVM(RecordingVM):
    def __init__(self, directory):
        super().__init__(directory)
        self.config.update(
            monitorcount="1",
            nic1="nat",
            cableconnected1="on",
            audio_in="off",
            audio_out="on",
        )
        self.width, self.height = 1920, 1080
        self.trace_active, self.trace_path = False, "none"
        self.key_error, self.stop_error = False, False
        self.sample_number = 0

    async def command(self, args, **kwargs):
        if args == ["showvminfo", self.target]:
            data = f"NIC 1: MAC: 0000, Trace: {'on' if self.trace_active else 'off'} (file: {self.trace_path}), Type: virtio\n".encode()
        elif args[:3] == ["guestcontrol", self.target, "list"]:
            data = (
                "Session #0 ID=1 User=test Status=[Started] Name=private\n"
                "  Process #0 PID=99 Status=[Started] Command=secret-password\n"
                "  File #0 ID=8 Status=[Open] Name=private\n"
            ).encode()
        elif args[0] == "metrics":
            if args[1] == "query":
                self.sample_number += 1
                data = f"Object Metric Values\ntest VM CPU/Load/User {self.sample_number}.25%\ntest VM RAM/Usage/Used 1234 kB\n".encode()
            else:
                data = b"configured"
        elif args[:2] == ["controlvm", self.target]:
            action = args[2]
            if action == "nictracefile1":
                self.trace_path = args[3]
            elif action == "nictrace1":
                if args[3] == "off" and self.stop_error:
                    raise Failure("NIC trace stop transport failed")
                self.trace_active = args[3] == "on"
                if self.trace_active:
                    Path(self.trace_path).write_bytes(
                        struct.pack("<IHHIIII", 0xA1B2C3D4, 2, 4, 0, 0, 65535, 1)
                    )
            elif action == "setvideomodehint":
                self.width, self.height = int(args[3]), int(args[4])
            elif action == "setlinkstate1":
                self.config["cableconnected1"] = args[3]
            elif action in {"audioin", "audioout"}:
                self.config["audio_in" if action == "audioin" else "audio_out"] = args[
                    3
                ]
            elif action == "keyboardputscancode":
                if self.key_error:
                    self.calls.append(args)
                    raise Failure("Input delivery uncertain")
            else:
                result = await super().command(args, **kwargs)
                if action == "poweroff":
                    self.trace_active = False
                return result
            data = b"accepted"
        else:
            return await super().command(args, **kwargs)
        self.calls.append(args)
        return data, len(data)

    async def screenshot(self, destination, screen=0):
        data = bytearray(PNG)
        data[16:24] = struct.pack(">II", self.width, self.height)
        Path(destination).write_bytes(data)


class DesktopTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name)
        self.vm = DesktopVM(self.root)
        self.config = {
            "service": {
                **DEFAULTS,
                "state_dir": str(self.root / "state"),
                "task_timeout": 120,
            },
            "vms": {"windows": self.vm.profile},
            "nix": {},
        }
        self.ctl = Controller(self.config, {"windows": self.vm})
        self.tools = Tools(self.ctl)

        self.task = await self.ctl.begin("windows", "run", "windows")
        self.task.update(state="preparing", deadline=time.time() + 120)
        await self.ctl.prepare(self.task)
        self.assertEqual(self.task["state"], "ready")

    async def asyncTearDown(self):
        await self.ctl.close()

    async def test_keys_in_preparation_are_released_and_not_replayed(self):
        self.task["state"] = "preparing"
        result = await self.tools.vm_keyboard(
            "run", "keys", [["CTRL", "ALT", "DELETE"]]
        )
        self.assertEqual(result["state"], "delivered")
        codes, releases = scancodes([["CTRL", "ALT", "DELETE"]])
        self.assertEqual(codes, ["1d", "38", "e0", "53", "e0", "d3", "b8", "9d"])
        self.assertEqual(self.vm.calls[-1][3:], releases)
        count = len(self.vm.calls)
        self.assertEqual(
            await self.tools.vm_keyboard("run", "keys", [["CTRL", "ALT", "DELETE"]]),
            result,
        )
        self.assertEqual(len(self.vm.calls), count)
        self.vm.key_error = True
        failed = await self.tools.vm_keyboard("run", "uncertain", [["ENTER"]])
        self.assertEqual(failed["state"], "unconfirmed")
        count = len(self.vm.calls)
        await self.tools.vm_keyboard("run", "uncertain", [["ENTER"]])
        self.assertEqual(len(self.vm.calls), count)
        self.assertEqual(self.task["held_keys"], ["9c"])

    async def test_capability_and_task_ownership_precede_mutation(self):
        self.vm.profile["network_link"]["enabled"] = False
        capabilities = await self.ctl.desktop.capabilities(self.vm)
        self.assertFalse(capabilities["network_link"]["available"])
        self.assertTrue(capabilities["packet_capture"]["available"])
        with self.assertRaises(Failure):
            await self.tools.vm_network_link("run", connected=False, request_id="link")
        self.assertEqual(self.vm.config["cableconnected1"], "on")
        await self.ctl.begin("windows", "other", "windows")
        with self.assertRaises(Failure):
            await self.tools.vm_audio("other", input=True, request_id="audio")
        self.assertEqual(self.vm.config["audio_in"], "off")
        self.vm.profile["network_link"]["enabled"] = True
        result = await self.tools.vm_network_link(
            "run", connected=False, request_id="link"
        )
        self.assertFalse(result["result"]["connected"])
        result = await self.tools.vm_audio(
            "run", input=True, output=False, request_id="audio"
        )
        self.assertEqual(result["result"], {"input": True, "output": False})
        sessions = await self.tools.guest_sessions("run", limit=1)
        self.assertEqual(
            (
                sessions["session_count"],
                sessions["process_count"],
                sessions["next_offset"],
            ),
            (1, 1, 1),
        )
        self.assertEqual(
            sessions["items"], [{"kind": "session", "id": 1, "status": "Started"}]
        )

    async def test_display_change_reports_observed_size_and_preserves_receipt(self):
        self.assertEqual(
            await self.tools.vm_display("run"),
            {"screen": 0, "width": 1920, "height": 1080},
        )
        result = await self.tools.vm_display(
            "run", width=1280, height=720, request_id="resize"
        )
        self.assertTrue(result["result"]["applied"], result)
        self.assertEqual(
            result["result"]["observed"], {"screen": 0, "width": 1280, "height": 720}
        )
        count = len(self.vm.calls)
        self.assertEqual(
            await self.tools.vm_display(
                "run", width=1280, height=720, request_id="resize"
            ),
            result,
        )
        self.assertEqual(len(self.vm.calls), count)

    async def test_aborted_vm_exports_captures_and_releases_input_ownership(self):
        trace = await self.tools.vm_packet_capture("run", "start", request_id="trace")
        metrics = await self.tools.vm_metrics("run", "start", request_id="metrics")
        await self.ctl.captures.sample(
            self.task, self.task["captures"][metrics["result"]["id"]]
        )
        self.task.update(held_keys=["9d"], mouse_used=True)
        self.vm.config["VMState"] = "aborted"

        with (
            patch.object(
                self.vm, "power", side_effect=Failure("VM is not running")
            ) as power,
            patch.object(
                Backend, "release", side_effect=Failure("No desktop")
            ) as release,
        ):
            await self.ctl.finish("run")
            await self.ctl.runners["run"]
        power.assert_not_awaited()
        release.assert_not_awaited()
        self.assertEqual(self.task["state"], "failed")
        self.assertTrue(self.task["cleanup_confirmed"])
        self.assertEqual(self.task["failure"]["phase"], "vm_runtime")
        self.assertEqual(self.task["held_keys"], [])
        self.assertFalse(self.task["mouse_used"])
        for receipt in (metrics, trace):
            capture = self.task["captures"][receipt["result"]["id"]]
            self.assertEqual(capture["state"], "stopped")
            self.assertIn("artifact", capture["segments"][0]["files"][0])

    async def test_capture_stop_failure_does_not_prevent_poweroff(self):
        await self.tools.vm_packet_capture("run", "start", request_id="trace")
        self.vm.stop_error = True
        self.vm.key_error = True
        await self.tools.vm_keyboard("run", "uncertain", [["CTRL", "ENTER"]])
        await self.ctl.finish("run")
        await self.ctl.runners["run"]
        self.assertEqual(self.vm.config["VMState"], "poweroff")
        self.assertTrue(self.task["cleanup_confirmed"])
        self.assertEqual(self.task["held_keys"], [])
        self.assertTrue(
            all(c["state"] == "stopped" for c in self.task["captures"].values())
        )
        self.assertTrue(any(c["errors"] for c in self.task["captures"].values()))

    async def test_capture_stop_timeout_retains_uncertainty_and_allows_cleanup(self):
        result = await self.tools.vm_packet_capture("run", "start", request_id="trace")
        capture = self.task["captures"][result["result"]["id"]]
        self.ctl.settings["command_timeout"] = 0.02

        async def blocked_stop(task, capture):
            await asyncio.Event().wait()

        with patch.object(self.ctl.captures, "stop_backend", side_effect=blocked_stop):
            result = await self.tools.vm_packet_capture(
                "run", "stop", request_id="stop", capture_id=capture["id"]
            )
        self.assertEqual(result["state"], "unconfirmed")
        self.assertIn("deadline", result["error"])
        self.assertEqual(capture["state"], "unconfirmed")
        self.assertTrue(capture["errors"])
        self.assertTrue(self.vm.trace_active)
        self.assertEqual(
            await self.tools.vm_packet_capture(
                "run", "stop", request_id="stop", capture_id=capture["id"]
            ),
            result,
        )
        await self.ctl.finish("run")
        await self.ctl.runners["run"]
        self.assertTrue(self.task["cleanup_confirmed"])
        self.assertEqual(self.task["state"], "failed")
        self.assertEqual(capture["state"], "stopped")
        self.assertEqual(self.vm.config["VMState"], "poweroff")

    async def test_capture_file_identity_and_symlink_are_checked(self):
        secret = self.root / "private"
        secret.write_bytes(b"private-host-content")
        result = await self.tools.vm_packet_capture("run", "start", request_id="trace")
        capture = self.task["captures"][result["result"]["id"]]
        path = Path(self.vm.trace_path)
        path.unlink()
        path.symlink_to(secret)
        result = await self.tools.vm_packet_capture(
            "run", "stop", request_id="stop", capture_id=capture["id"]
        )
        self.assertTrue(result["result"]["errors"])
        self.assertEqual(self.task["artifacts"], [])
        self.assertEqual(secret.read_bytes(), b"private-host-content")

    async def test_restart_reconciles_captures_without_repeating_input(self):
        result = await self.tools.vm_packet_capture("run", "start", request_id="trace")
        capture_id = result["result"]["id"]
        await self.tools.vm_keyboard("run", "keys", [["ENTER"]])
        await self.ctl.close()
        self.ctl = Controller(self.config, {"windows": self.vm})
        self.tools = Tools(self.ctl)
        await self.ctl.start()
        recovered = self.ctl.task("run")["captures"][capture_id]
        self.assertEqual(recovered["state"], "stopped")
        self.assertEqual(recovered["stop_reason"], "controller_restart")
        self.assertFalse(self.vm.trace_active)
        self.assertEqual(len(self.ctl.task("run")["artifacts"]), 1)
        count = len(self.vm.calls)
        await self.tools.vm_keyboard("run", "keys", [["ENTER"]])
        self.assertEqual(len(self.vm.calls), count)

    async def test_metrics_are_task_scoped_paged_and_exported(self):
        result = await self.tools.vm_metrics("run", "start", request_id="metrics")
        capture = self.task["captures"][result["result"]["id"]]
        for _ in range(3):
            await self.ctl.captures.sample(self.task, capture)
        result = await self.tools.vm_metrics(
            "run", "read", capture_id=capture["id"], offset=1, limit=1
        )
        self.assertEqual((result["total"], result["next_offset"]), (3, 2))
        self.assertEqual(result["items"][0]["metrics"][0]["value"], 2.25)
        stopped = await self.tools.vm_metrics(
            "run", "stop", request_id="stop", capture_id=capture["id"]
        )
        self.assertFalse(stopped["result"]["errors"])
        artifact = self.task["artifacts"][0]
        records = [
            json.loads(line)
            for line in self.ctl.transfers.file(artifact["id"]).read_text().splitlines()
        ]
        self.assertEqual(len(records), 3)
        self.assertTrue(
            all(
                self.vm.target in call for call in self.vm.calls if call[0] == "metrics"
            )
        )

    async def test_mouse_helper_failure_is_independent_and_receipted(self):
        result = await self.tools.vm_mouse("run", "mouse", "click", x=10, y=10)
        self.assertEqual(result["state"], "unconfirmed")
        keys = await self.tools.vm_keyboard("run", "key", [["ENTER"]])
        self.assertEqual(keys["state"], "delivered")
        with patch.object(Backend, "helper", return_value={"released": True}):
            await self.ctl.desktop.cleanup(self.task)
        self.assertFalse(self.task["mouse_used"])

    async def test_cancel_interrupts_a_blocked_metrics_observer(self):
        entered = asyncio.Event()

        async def blocked_sample(task, capture):
            entered.set()
            await asyncio.Event().wait()

        with patch.object(self.ctl.captures, "sample", side_effect=blocked_sample):
            result = await self.tools.vm_metrics("run", "start", request_id="metrics")
            self.task["captures"][result["result"]["id"]]["next_sample"] = 0
            await asyncio.wait_for(entered.wait(), 2)
            await self.ctl.finish("run", cancel=True)
            await asyncio.wait_for(self.ctl.runners["run"], 2)
        self.assertTrue(self.task["cleanup_confirmed"])
        self.assertEqual(self.vm.config["VMState"], "poweroff")

    async def test_replaced_capture_is_retained_without_export(self):
        result = await self.tools.vm_packet_capture("run", "start", request_id="trace")
        capture = self.task["captures"][result["result"]["id"]]
        segment = capture["segments"][0]
        path = self.ctl.captures.directory(self.task, capture, segment) / "traffic.pcap"
        replacement = self.root / "replacement"
        replacement.write_bytes(path.read_bytes())
        replacement.replace(path)
        stopped = await self.tools.vm_packet_capture(
            "run", "stop", request_id="stop", capture_id=capture["id"]
        )
        self.assertTrue(stopped["result"]["errors"])
        self.assertEqual(self.task["artifacts"], [])
        self.assertTrue(path.exists())


if __name__ == "__main__":
    unittest.main()
