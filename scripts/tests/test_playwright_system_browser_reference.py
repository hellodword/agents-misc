from __future__ import annotations

import re
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
REFERENCE = REPO_ROOT / ".agents" / "references" / "playwright-system-browser.ts"


class PlaywrightSystemBrowserReferenceTests(unittest.TestCase):
    def test_container_detection_contract(self) -> None:
        source = REFERENCE.read_text(encoding="utf-8")
        match = re.search(r"const CONTAINER_MARKERS = \[(.*?)\] as const;", source, re.DOTALL)
        self.assertIsNotNone(match)
        assert match is not None
        self.assertEqual(
            ["/.dockerenv", "/run/.containerenv", "/var/run/.containerenv"],
            re.findall(r'"([^"]+)"', match.group(1)),
        )
        self.assertIn("CONTAINER_MARKERS.some((path) => existsSync(path))", source)
        self.assertIn('"/proc/1/cgroup"', source)
        self.assertIn('"/proc/self/cgroup"', source)


if __name__ == "__main__":
    unittest.main()
