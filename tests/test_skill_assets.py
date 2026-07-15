from __future__ import annotations

import json
import re
import unittest
from copy import deepcopy
from pathlib import Path

from jsonschema import Draft202012Validator


REPO_ROOT = Path(__file__).resolve().parents[1]
BROWSER_HELPER = (
    REPO_ROOT
    / ".agents"
    / "skills"
    / "browser-e2e"
    / "assets"
    / "playwright-system-browser.ts"
)
VISUAL_ASSETS = REPO_ROOT / ".agents" / "skills" / "ai-visual-review" / "assets"
NIX_GITHUB_ACTIONS_REFERENCE = (
    REPO_ROOT
    / ".agents"
    / "skills"
    / "nix-workflow"
    / "references"
    / "github-actions-nix.md"
)


class BrowserAssetTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.source = BROWSER_HELPER.read_text(encoding="utf-8")

    def test_browser_order_is_exact(self) -> None:
        match = re.search(r"const BROWSER_NAMES = \[(.*?)\] as const;", self.source, re.DOTALL)
        self.assertIsNotNone(match)
        assert match is not None
        self.assertEqual(
            ["google-chrome", "chromium", "microsoft-edge"],
            re.findall(r'"([^"]+)"', match.group(1)),
        )

    def test_headless_is_fixed_false(self) -> None:
        self.assertIn("headless: false;", self.source)
        self.assertIn("headless: false,", self.source)
        self.assertNotIn("headless: true", self.source)

    def test_linux_without_display_fails_clearly(self) -> None:
        self.assertIn('process.platform !== "linux"', self.source)
        self.assertIn("!environment.DISPLAY && !environment.WAYLAND_DISPLAY", self.source)
        self.assertIn("Headful Playwright requires DISPLAY or WAYLAND_DISPLAY", self.source)

    def test_container_detection_contract(self) -> None:
        match = re.search(r"const CONTAINER_MARKERS = \[(.*?)\] as const;", self.source, re.DOTALL)
        self.assertIsNotNone(match)
        assert match is not None
        self.assertEqual(
            ["/.dockerenv", "/run/.containerenv", "/var/run/.containerenv"],
            re.findall(r'"([^"]+)"', match.group(1)),
        )
        self.assertIn("CONTAINER_MARKERS.some((path) => existsSync(path))", self.source)
        self.assertIn('"/proc/1/cgroup"', self.source)
        self.assertIn('"/proc/self/cgroup"', self.source)

    def test_sandbox_and_shared_memory_flags_are_container_only(self) -> None:
        match = re.search(r"if \(isContainer\(\)\) \{(.*?)\n  \}", self.source, re.DOTALL)
        self.assertIsNotNone(match)
        assert match is not None
        body = match.group(1)
        self.assertIn('args.push("--no-sandbox")', body)
        self.assertIn("bytes !== undefined && bytes < ONE_GIB", body)
        self.assertIn('args.push("--disable-dev-shm-usage")', body)
        outside = self.source[: match.start()] + self.source[match.end() :]
        self.assertNotIn('args.push("--no-sandbox")', outside)
        self.assertNotIn('args.push("--disable-dev-shm-usage")', outside)


class NixGitHubActionsReferenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.source = NIX_GITHUB_ACTIONS_REFERENCE.read_text(encoding="utf-8")

    def section(self, number: int) -> str:
        match = re.search(
            rf"^## {number}\. .*?(?=^## \d+\. |\Z)",
            self.source,
            re.MULTILINE | re.DOTALL,
        )
        self.assertIsNotNone(match)
        assert match is not None
        return match.group(0)

    def test_exactly_four_conditional_recipes(self) -> None:
        self.assertEqual(
            [
                "1. Heavy Nix disk preparation",
                "2. `dockerTools.pullImage` container-store workaround",
                "3. Install and configure Nix",
                "4. Inherit reviewed input-flake caches",
            ],
            re.findall(r"^## (\d+\. .+)$", self.source, re.MULTILINE),
        )

    def test_heavy_disk_constants_are_exact(self) -> None:
        expected = {
            "ROOT_SAFE_HAVEN_MB": "40000",
            "ROOT_FALLBACK_SAFE_HAVEN_MB": "12288",
            "ROOT_MIN_NIX_VOLUME_MB": "20480",
            "MNT_SAFE_HAVEN_MB": "1024",
        }
        found = dict(
            re.findall(
                r'^\s+(ROOT_SAFE_HAVEN_MB|ROOT_FALLBACK_SAFE_HAVEN_MB|ROOT_MIN_NIX_VOLUME_MB|MNT_SAFE_HAVEN_MB): "(\d+)"$',
                self.section(1),
                re.MULTILINE,
            )
        )
        self.assertEqual(expected, found)

    def test_sdk_removal_list_is_exact_and_has_no_wildcards(self) -> None:
        match = re.search(
            r"sudo rm -rf \\\n(?P<body>.*?)\n\s+\|\| true",
            self.section(1),
            re.DOTALL,
        )
        self.assertIsNotNone(match)
        assert match is not None
        self.assertNotRegex(match.group("body"), r"[*?\[]")
        paths = re.findall(r"^\s+(/[^\s\\]+)\s+\\$", match.group("body"), re.MULTILINE)
        self.assertEqual(
            [
                "/usr/share/dotnet",
                "/usr/local/lib/android",
                "/opt/ghc",
                "/opt/hostedtoolcache",
                "/opt/az",
                "/opt/microsoft",
                "/opt/google",
                "/usr/local/.ghcup",
                "/usr/share/swift",
            ],
            paths,
        )

    def test_hosted_runner_guards_and_nix_precondition_are_present(self) -> None:
        heavy = self.section(1)
        install = self.section(3)
        for section in (heavy, install):
            self.assertIn("runner.environment == 'github-hosted'", section)
            self.assertIn('${GITHUB_ACTIONS:-}', section)
            self.assertIn('lsb_release -is', section)
            self.assertIn('"Ubuntu"', section)
        self.assertIn('[[ ! -e /nix ]]', heavy)

    def test_container_store_paths_are_exact(self) -> None:
        workaround = self.section(2)
        self.assertIn("sudo chmod 755 /run/containers", workaround)
        self.assertIn('sudo mkdir -p "/run/containers/$(id -u runner)"', workaround)
        self.assertIn('sudo chown runner: "/run/containers/$(id -u runner)"', workaround)
        self.assertEqual(
            [
                "/run/containers",
                "/run/containers/$(id -u runner)",
                "/run/containers/$(id -u runner)",
            ],
            re.findall(r"/run/containers(?:/\$\(id -u runner\))?", workaround),
        )

    def test_official_installer_command_and_parameterized_caches(self) -> None:
        install = self.section(3)
        self.assertIn(
            "curl -fsSL https://nixos.org/nix/install | sh -s -- --daemon",
            install,
        )
        self.assertNotIn("hellodword", self.source.lower())

    def test_input_cache_collector_contract_is_present(self) -> None:
        collector = self.section(4)
        for token in (
            "inputNames",
            "lib.hasInfix substring value",
            '"substituters"',
            '"extra-substituters"',
            '"trusted-substituters"',
            '"extra-trusted-substituters"',
            '"trusted-public-keys"',
            '"extra-trusted-public-keys"',
            "lib.flatten",
            "lib.unique",
            "inherit substituters trustedPublicKeys",
            "settings =",
            "lib.<system>.inheritedNixConfig",
            "lib.mkAfter",
            "--no-write-lock-file",
        ):
            with self.subTest(token=token):
                self.assertIn(token, collector)


class VisualSchemaTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.finding_schema = json.loads(
            (VISUAL_ASSETS / "finding.schema.json").read_text(encoding="utf-8")
        )
        cls.synthesis_schema = json.loads(
            (VISUAL_ASSETS / "synthesis.schema.json").read_text(encoding="utf-8")
        )
        cls.finding_validator = Draft202012Validator(cls.finding_schema)
        cls.synthesis_validator = Draft202012Validator(cls.synthesis_schema)
        cls.finding_instance = {
            "schema": "visual-review-finding/v1",
            "batch_id": "batch-1",
            "findings": [
                {
                    "finding_id": "finding-1",
                    "screenshot_id": "desktop-home",
                    "page_or_component": "Home page header",
                    "severity": "P2",
                    "category": "alignment",
                    "evidence": "The title and primary action do not share a baseline.",
                    "recommendation": "Align both elements to the same grid row.",
                    "affected_context": "Desktop viewport at 1440px",
                    "duplicate_candidate_id": None,
                    "confidence": "high",
                    "needs_human_decision": False,
                }
            ],
        }
        cls.synthesis_instance = {
            "schema": "visual-review-synthesis/v1",
            "run_id": "run-1",
            "proposed_findings": [
                {
                    "finding_id": "finding-1",
                    "merged_from": ["batch-1/finding-1"],
                    "severity": "P2",
                    "category": "alignment",
                    "summary": "Header elements use inconsistent baselines.",
                    "implementation_guidance": "Use the shared header grid alignment.",
                    "affected_screenshot_ids": ["desktop-home"],
                    "confidence": "high",
                }
            ],
            "rejected_or_duplicate_findings": [],
            "conflicts_needing_user_decision": [],
            "proposed_implementation_plan": ["Align the header grid."],
        }

    def test_full_instances_are_valid(self) -> None:
        self.finding_validator.validate(self.finding_instance)
        self.synthesis_validator.validate(self.synthesis_instance)

    def test_schema_constants_cannot_be_interchanged(self) -> None:
        finding = deepcopy(self.finding_instance)
        finding["schema"] = "visual-review-synthesis/v1"
        synthesis = deepcopy(self.synthesis_instance)
        synthesis["schema"] = "visual-review-finding/v1"
        finding_errors = list(self.finding_validator.iter_errors(finding))
        synthesis_errors = list(self.synthesis_validator.iter_errors(synthesis))
        self.assertTrue(finding_errors)
        self.assertTrue(synthesis_errors)

    def test_top_level_unknown_fields_are_rejected(self) -> None:
        for validator, instance in (
            (self.finding_validator, self.finding_instance),
            (self.synthesis_validator, self.synthesis_instance),
        ):
            candidate = deepcopy(instance)
            candidate["unknown"] = True
            with self.subTest(schema=instance["schema"]):
                self.assertTrue(list(validator.iter_errors(candidate)))

    def test_nested_unknown_fields_are_rejected(self) -> None:
        finding = deepcopy(self.finding_instance)
        finding["findings"][0]["unknown"] = True
        synthesis = deepcopy(self.synthesis_instance)
        synthesis["proposed_findings"][0]["unknown"] = True
        self.assertTrue(list(self.finding_validator.iter_errors(finding)))
        self.assertTrue(list(self.synthesis_validator.iter_errors(synthesis)))


if __name__ == "__main__":
    unittest.main()
