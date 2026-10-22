import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from native_paths import library_directory, sdk_directory


class NativePathsTests(unittest.TestCase):
    def test_installation_symlink_follows_package_upgrade(self):
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            command = root / "bin" / "VBoxManage"
            command.parent.mkdir()
            for version in ("first", "second"):
                library = root / version / "libexec" / "virtualbox"
                sdk = library / "sdk"
                glue = sdk / "bindings" / "c" / "glue" / "VBoxCAPIGlue.c"
                glue.parent.mkdir(parents=True)
                glue.touch()
                (library / "VBoxXPCOMC.so").touch()
                binary = library / "VBoxManage"
                binary.touch(mode=0o700)
                command.unlink(missing_ok=True)
                command.symlink_to(binary)
                with patch.dict("os.environ", {"PATH": str(command.parent)}):
                    self.assertEqual(library_directory({}), library)
                    self.assertEqual(sdk_directory({}), sdk)

    def test_prefix_layout_and_configured_library(self):
        with tempfile.TemporaryDirectory() as scratch:
            prefix = Path(scratch)
            binary = prefix / "bin" / "VBoxManage"
            binary.parent.mkdir()
            binary.touch(mode=0o700)
            library = prefix / "lib" / "virtualbox"
            library.mkdir(parents=True)
            (library / "VBoxXPCOMC.so").touch()
            sdk = prefix / "share" / "virtualbox" / "sdk"
            glue = sdk / "bindings" / "c" / "glue" / "VBoxCAPIGlue.c"
            glue.parent.mkdir(parents=True)
            glue.touch()
            settings = {"vboxmanage": str(binary)}
            self.assertEqual(library_directory(settings), library)
            self.assertEqual(sdk_directory(settings), sdk)
            settings = {"vbox_library_dir": str(library)}
            self.assertEqual(library_directory(settings), library)
            self.assertEqual(sdk_directory(settings), sdk)


if __name__ == "__main__":
    unittest.main()
