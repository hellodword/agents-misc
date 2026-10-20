import asyncio
import base64
import contextlib
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path

from common import Failure, atomic_json, read_config
from nix_worker import Worker, WorkerClient
from tasks import Controller
from test_server import RecordingVM


class WorkerTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name)
        fake = self.root / "nix"
        fake.write_text(f"""#!{sys.executable}
import json, pathlib, sys, time
pathlib.Path({str(self.root / "calls")!r}).open('a').write(json.dumps(sys.argv[1:]) + '\\n')
if sys.argv[1] == 'build':
    print('test-node: startup', file=sys.stderr, flush=True)
    if '-slow.drv' in sys.argv[-1]: time.sleep(30)
    if '-failed.drv' in sys.argv[-1]: sys.exit(100)
    print(json.dumps([{{'outputs': {{'out': '/nix/store/' + '0'*32 + '-output'}}}}]))
""")
        fake.chmod(0o700)
        self.config_path = self.root / "config.toml"
        settings = {
            "service": {"state_dir": str(self.root / "controller")},
            "nix": {
                "state_dir": str(self.root / "worker"),
                "socket": str(self.root / "worker.sock"),
                "nix": str(fake),
                "cleanup_timeout": 0.1,
            },
        }
        self.config_path.write_text(
            "\n".join(
                f"[{section}]\n"
                + "\n".join(
                    f"{key} = {json.dumps(value)}" for key, value in values.items()
                )
                for section, values in settings.items()
            )
        )
        self.shared_config = read_config(self.config_path)
        self.config = self.shared_config["nix"]
        self.worker = Worker(self.config)
        self.server = asyncio.create_task(self.worker.serve())
        async with asyncio.timeout(3):
            while not Path(self.config["socket"]).exists():
                await asyncio.sleep(0.01)
        self.client = WorkerClient(self.config["socket"])

    async def asyncTearDown(self):
        self.server.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await self.server
        self.worker.lock_handle.close()

    async def wait_job(self, key):
        async with asyncio.timeout(5):
            while True:
                job = await self.client.call("status", id=key)
                if job["state"] != "running":
                    return job
                await asyncio.sleep(0.03)

    async def test_unix_submission_logs_exit_retry_and_scoped_artifacts(self):
        data = {
            "id": "build",
            "drv": "/nix/store/" + "0" * 32 + "-test.drv",
            "cache_id": "a" * 43,
            "timeout": 10,
        }
        await self.client.call("submit", **data)
        await self.client.call("submit", **data)
        final = await self.wait_job("build")
        self.assertEqual(final["state"], "succeeded")
        self.assertTrue(final["cleanup_confirmed"])
        log = await self.client.call("log", id="build", offset=0, limit=65536)
        self.assertIn("test-node: startup", log["text"])
        calls = [
            json.loads(line) for line in (self.root / "calls").read_text().splitlines()
        ]
        self.assertEqual([call[0] for call in calls], ["copy", "build"])
        self.assertIn("--keep-failed", calls[1])
        self.assertEqual(calls[1][calls[1].index("--builders") + 1], "")
        output = self.root / "output"
        output.mkdir()
        (output / "report.txt").write_text("test report")
        (self.root / "private.txt").write_text("private host fixture")
        (output / "escape").symlink_to(self.root / "private.txt")
        (output / "linked-directory").symlink_to(self.root, target_is_directory=True)
        self.worker.jobs["build"]["outputs"] = {"out": str(output)}
        listed = await self.client.call("list", id="build", root="out", path="")
        self.assertEqual(listed["entries"], [{"name": "report.txt", "kind": "file"}])
        for path in (
            "escape",
            "linked-directory/private.txt",
            "../private.txt",
            str(self.root / "private.txt"),
        ):
            with self.assertRaises(Failure):
                await self.client.call(
                    "read", id="build", root="out", path=path, offset=0, limit=100
                )
        downloaded = await self.client.call(
            "read", id="build", root="out", path="report.txt", offset=0, limit=100
        )
        self.assertEqual(base64.b64decode(downloaded["data"]), b"test report")
        with self.assertRaises(Failure):
            await self.client.call(
                "list", id="build", root="out", path="linked-directory"
            )
        self.worker.jobs["build"]["outputs"]["linked-root"] = str(
            output / "linked-directory"
        )
        with self.assertRaises(Failure):
            await self.client.call("list", id="build", root="linked-root", path="")
        many = output / "many"
        many.mkdir()
        for index in range(2001):
            (many / str(index)).symlink_to(self.root / "private.txt")
        with self.assertRaisesRegex(Failure, "2000 entries"):
            await self.client.call("list", id="build", root="out", path="many")
        self.assertEqual(
            (self.root / "private.txt").read_text(), "private host fixture"
        )
        await self.client.call(
            "submit",
            **(
                data
                | {"id": "failure", "drv": "/nix/store/" + "0" * 32 + "-failed.drv"}
            ),
        )
        failed = await self.wait_job("failure")
        self.assertEqual(failed["state"], "failed")
        self.assertEqual(failed["failure_kind"], "build")
        self.assertTrue(failed["cleanup_confirmed"])
        self.assertIn(
            "test-node",
            (await self.client.call("log", id="failure", offset=0, limit=1000))["text"],
        )

    async def test_cancel_and_worker_recovery_require_cleanup_reconciliation(self):
        data = {
            "id": "slow",
            "drv": "/nix/store/" + "0" * 32 + "-slow.drv",
            "cache_id": "b" * 43,
            "timeout": 20,
        }
        await self.client.call("cancel", id="before-submit")
        delayed = await self.client.call("submit", **(data | {"id": "before-submit"}))
        self.assertEqual(delayed["state"], "cancelled")
        self.assertFalse((self.root / "calls").exists())
        await self.client.call("retire", id="before-submit")
        self.assertEqual(
            await self.client.call("retire", id="before-submit"),
            {"retired": "before-submit"},
        )
        await self.client.call("submit", **data)
        async with asyncio.timeout(5):
            while (
                "test-node"
                not in (await self.client.call("log", id="slow", offset=0, limit=1000))[
                    "text"
                ]
            ):
                await asyncio.sleep(0.02)
        cancelled = await self.client.call("cancel", id="slow")
        self.assertEqual(cancelled["state"], "cleanup_unknown")
        self.assertFalse(cancelled["cleanup_confirmed"])
        self.assertEqual(cancelled["failure_kind"], "cancelled")
        with self.assertRaises(Failure):
            await self.client.call("retire", id="slow")
        with self.assertRaises(Failure):
            await self.client.call("submit", **(data | {"id": "next"}))
        with self.assertRaises(Failure):
            await self.worker.dispatch(
                {"operation": "reconcile", "id": "slow"}, os.getuid() + 1
            )
        reconciliation = await asyncio.create_subprocess_exec(
            sys.executable,
            "-B",
            str(Path(__file__).with_name("nix_worker.py")),
            "--config",
            str(self.config_path),
            "--reconcile",
            "slow",
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        out, err = await asyncio.wait_for(reconciliation.communicate(), 5)
        self.assertEqual(reconciliation.returncode, 0, err.decode())
        clear = json.loads(out)
        self.assertTrue(clear["cleanup_confirmed"])
        self.server.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await self.server
        self.worker.lock_handle.close()
        directory = Path(self.config["state_dir"]) / "interrupted"
        directory.mkdir()
        atomic_json(
            directory / "state.json",
            {"id": "interrupted", "state": "running", "cleanup_confirmed": False},
        )
        self.worker = Worker(self.config)
        self.server = asyncio.create_task(self.worker.serve())
        self.assertEqual(self.worker.get("interrupted")["state"], "cleanup_unknown")

    async def test_virtualization_switch_waits_for_cleanup(self):
        vm = RecordingVM(self.root)
        config = self.shared_config | {
            "service": self.shared_config["service"] | {"shutdown_timeout": 0.2},
            "vms": {"windows": vm.profile},
        }
        ctl = Controller(config, {"windows": vm})
        await ctl.start()
        try:
            cache = ctl.transfers.create("cache")
            await ctl.begin(
                "nixos",
                "linux",
                cache_id=cache["id"],
                drv="/nix/store/" + "0" * 32 + "-slow.drv",
                timeout=20,
            )
            await ctl.begin("windows", "desktop", target="windows")
            async with asyncio.timeout(5):
                while (
                    "test-node"
                    not in (
                        await self.client.call("log", id="linux", offset=0, limit=1000)
                    )["text"]
                    if "linux" in self.worker.jobs
                    else True
                ):
                    await asyncio.sleep(0.03)
            self.assertEqual(ctl.task("desktop")["state"], "queued")
            await ctl.finish("linux", cancel=True)
            async with asyncio.timeout(5):
                while ctl.task("linux")["state"] != "cleanup_unknown":
                    await asyncio.sleep(0.03)
            self.assertEqual(ctl.task("desktop")["state"], "queued")
            await self.client.call("reconcile", id="linux")
            async with asyncio.timeout(5):
                while ctl.task("desktop")["state"] != "ready":
                    await asyncio.sleep(0.03)
            self.assertTrue(ctl.task("linux")["cleanup_confirmed"])
        finally:
            await ctl.close()


if __name__ == "__main__":
    unittest.main()
