import tempfile
import unittest
from pathlib import Path

from common import Failure, read_config


class PolicyTests(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.path = Path(self.scratch.name) / "config.toml"
        self.base = r"""[service]
state_dir = "/synthetic/state"
[vms.windows]
vm = "test-vm"
baseline = "ready"
username = "tester"
password = "synthetic"
python = 'C:\Python\python.exe'
workspace = 'C:\vm-tests'
"""

    def read(self, settings):
        self.path.write_text(self.base + settings)
        return read_config(self.path)

    def test_partial_overrides_and_independent_vm_defaults(self):
        config = self.read(r"""[vms.windows.modify_vm]
cpus = { max = 2 }
memory_mb = { min = 1024 }
graphics_controller = { allowed = ["vboxsvga"] }
accelerate_3d = { allowed = [false] }
vpid = { allowed = [] }
[vms.windows.webcam]
enabled = false
[vms.other]
vm = "other-vm"
baseline = "other-ready"
username = "tester"
password = "synthetic"
python = 'C:\Python\python.exe'
workspace = 'C:\vm-tests'
""")
        windows, other = config["vms"]["windows"], config["vms"]["other"]
        self.assertEqual(
            windows["modify_vm"]["cpus"], {"enabled": True, "min": 1, "max": 2}
        )
        self.assertEqual(
            windows["modify_vm"]["memory_mb"],
            {"enabled": True, "min": 1024, "max": 16384},
        )
        self.assertEqual(windows["modify_vm"]["accelerate_3d"]["allowed"], [False])
        self.assertEqual(windows["modify_vm"]["vpid"]["allowed"], [])
        self.assertFalse(windows["webcam"]["enabled"])
        self.assertEqual(other["modify_vm"]["cpus"]["max"], 4)
        self.assertEqual(other["modify_vm"]["memory_mb"]["min"], 128)
        self.assertEqual(
            other["modify_vm"]["vram_mb"], {"enabled": True, "min": 1, "max": 256}
        )
        self.assertEqual(other["modify_vm"]["accelerate_3d"]["allowed"], [False, True])
        self.assertTrue(other["webcam"]["enabled"])

    def test_config_errors_identify_the_policy_location(self):
        for setting, location in (
            ("cpus = { min = 3, max = 2 }", "cpus.min"),
            ("cpus = { max = 257 }", "cpus.max"),
            ("memory_mb = { max = 16384.5 }", "memory_mb.max"),
            ('accelerate_3d = { enabled = "false" }', "accelerate_3d.enabled"),
            ("nested_paging = { allowed = [0] }", "nested_paging.allowed"),
            (
                'graphics_controller = { allowed = ["unknown"] }',
                "graphics_controller.allowed",
            ),
            ("cpus = { maximum = 2 }", "cpus"),
        ):
            with self.subTest(setting=setting):
                with self.assertRaises(Failure) as failure:
                    self.read("[vms.windows.modify_vm]\n" + setting)
                self.assertIn(
                    "vms.windows.modify_vm." + location, str(failure.exception)
                )


if __name__ == "__main__":
    unittest.main()
