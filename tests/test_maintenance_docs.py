from __future__ import annotations

import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
SCRIPT = REPO_ROOT / "scripts" / "check-maintenance-docs.py"
SPEC = importlib.util.spec_from_file_location("check_maintenance_docs", SCRIPT)
assert SPEC and SPEC.loader
CHECKER = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = CHECKER
SPEC.loader.exec_module(CHECKER)


class MaintenanceDocsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        (self.root / "justfile").write_text(
            (REPO_ROOT / "justfile").read_text(encoding="utf-8"), encoding="utf-8"
        )
        for relative in CHECKER.MAINTENANCE_DOCS:
            path = self.root / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(
                "# Guide\n\n```sh\njust check-docs\n```\n", encoding="utf-8"
            )

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def errors(self) -> list[str]:
        errors, _smokes = CHECKER.check_repository(self.root)
        return errors

    def test_valid_documents_extract_reviewed_smokes(self) -> None:
        errors, smokes = CHECKER.check_repository(self.root)
        self.assertEqual([], errors)
        self.assertEqual(len(CHECKER.MAINTENANCE_DOCS), len(smokes))
        self.assertTrue(
            all(smoke.argv == ("just", "--show", "check-docs") for smoke in smokes)
        )

    def test_missing_local_link_and_heading_fail(self) -> None:
        root_readme = self.root / "README.md"
        root_readme.write_text(
            "# Guide\n\n[missing](missing.md)\n[heading](codex/README.md#absent)\n",
            encoding="utf-8",
        )
        errors = self.errors()
        self.assertTrue(any("does not exist" in error for error in errors))
        self.assertTrue(any("heading does not exist" in error for error in errors))

    def test_example_links_inside_fences_are_not_repository_links(self) -> None:
        (self.root / "README.md").write_text(
            "# Guide\n\n```md\n[consumer](../outside.md)\n```\n", encoding="utf-8"
        )
        self.assertEqual([], self.errors())

    def test_insecure_external_link_fails(self) -> None:
        (self.root / "README.md").write_text(
            "# Guide\n\n[insecure](http://example.com/path)\n", encoding="utf-8"
        )
        self.assertTrue(any("absolute HTTPS" in error for error in self.errors()))

    def test_unreviewed_shell_command_fails(self) -> None:
        (self.root / "README.md").write_text(
            "# Guide\n\n```sh\nrm -rf build\n```\n", encoding="utf-8"
        )
        self.assertTrue(
            any("no reviewed help/smoke" in error for error in self.errors())
        )

    def test_nix_and_server_commands_are_mapped_without_side_effects(self) -> None:
        build = CHECKER.DocumentedCommand(
            Path("README.md"),
            1,
            "nix build --no-link .#agents-viewer",
        )
        server = CHECKER.DocumentedCommand(
            Path("README.md"),
            2,
            "nix develop .#codex --command python3 "
            "tools/codex-hooks/codex_hook_notify_server.py --host 127.0.0.1",
        )
        self.assertEqual(
            (
                "nix",
                "build",
                "--dry-run",
                "--no-link",
                "--accept-flake-config",
                ".#agents-viewer",
            ),
            CHECKER.smoke_for(build).argv,
        )
        self.assertEqual(
            (
                "nix",
                "develop",
                ".#codex",
                "--command",
                "python3",
                "tools/codex-hooks/codex_hook_notify_server.py",
                "--help",
            ),
            CHECKER.smoke_for(server).argv,
        )

        app = CHECKER.DocumentedCommand(
            Path("README.md"),
            3,
            "nix run --accept-flake-config .#agents-viewer -- --help",
        )
        self.assertEqual(
            (
                "nix",
                "eval",
                "--raw",
                "--accept-flake-config",
                ".#apps.__NIX_SYSTEM__.agents-viewer.program",
            ),
            CHECKER.smoke_for(app).argv,
        )

    def test_live_eval_recipe_is_only_inspected(self) -> None:
        command = CHECKER.DocumentedCommand(
            Path("tests/evals/README.md"),
            10,
            "just -- agent-evals --model gpt-test --certify",
        )
        self.assertEqual(
            ("just", "--show", "agent-evals"), CHECKER.smoke_for(command).argv
        )

    def test_private_or_nested_just_recipes_fail(self) -> None:
        justfile = self.root / "justfile"
        text = justfile.read_text(encoding="utf-8")
        text = text.replace(
            "  nix develop .#dev --command python3 scripts/check-maintenance-docs.py --root . --execute-commands",
            "  nix develop .#dev --command just check-docs",
            1,
        )
        justfile.write_text(f"[private]\n{text}", encoding="utf-8")
        errors = self.errors()
        self.assertTrue(
            any("private recipes are forbidden" in error for error in errors)
        )
        self.assertTrue(any("nested Just invocation" in error for error in errors))

    def test_missing_or_extra_public_recipe_fails(self) -> None:
        justfile = self.root / "justfile"
        justfile.write_text(
            justfile.read_text(encoding="utf-8")
            + "\nextra-recipe:\n"
            + "  nix develop .#dev --command python3 --version\n",
            encoding="utf-8",
        )
        errors = self.errors()
        self.assertTrue(any("unexpected public recipe" in error for error in errors))


if __name__ == "__main__":
    unittest.main()
