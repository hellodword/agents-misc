from __future__ import annotations

import contextlib
import importlib.util
import io
import tempfile
import unittest
from pathlib import Path


SCRIPT = Path(__file__).resolve().parents[1] / "scripts" / "check-agent-rules.py"
SPEC = importlib.util.spec_from_file_location("check_agent_rules", SCRIPT)
assert SPEC and SPEC.loader
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


class CheckAgentRulesTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name) / "repo"
        self.write(
            "AGENTS.md", "# Instructions\nRead [the index](.agents/rules/index.md).\n"
        )
        self.write(
            ".agents/rules/index.md",
            "# Routes\n[Connections](connections.md)\n[Demo](../skills/demo/SKILL.md)\n",
        )
        self.write(".agents/rules/connections.md", "# Connection defaults\n")
        self.write_skill("demo")

    def write(self, relative: str, text: str) -> Path:
        path = self.root / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def write_skill(self, name: str, body: str = "# Useful guidance\n") -> Path:
        return self.write(
            f".agents/skills/{name}/SKILL.md",
            f"---\nname: {name}\ndescription: Reuse a project practice for the relevant task.\n---\n{body}",
        )

    def errors(self) -> list[str]:
        return CHECKER.check_repository(self.root)

    def assert_fails(self, fragment: str) -> None:
        errors = self.errors()
        self.assertTrue(any(fragment in error for error in errors), errors)

    def test_minimal_payload_needs_no_maintenance_files(self) -> None:
        self.assertEqual([], self.errors())

    def test_rule_and_skill_inventory_follows_files_and_index(self) -> None:
        self.write(".agents/rules/new-rule.md", "# Another rule\n")
        (self.root / ".agents/skills/demo").rename(self.root / ".agents/skills/renamed")
        self.write_skill("renamed")
        self.write(
            ".agents/rules/index.md",
            "[Connections](connections.md)\n[New](new-rule.md)\n[Renamed](../skills/renamed/SKILL.md)\n",
        )
        self.assertEqual([], self.errors())
        self.write_skill("another")
        self.write(".agents/rules/unrouted.md", "# Unrouted\n")
        errors = "\n".join(self.errors())
        self.assertIn("missing routed Markdown link", errors)
        self.assertIn("another/SKILL.md", errors)
        self.assertIn("unrouted.md", errors)

    def test_readme_and_other_repository_content_are_outside_the_check(self) -> None:
        self.write("README.md", "[missing](absent.md)\n")
        self.write(".project-agent/project.md", "[missing](also-absent.md)\n")
        self.assertEqual([], self.errors())

    def test_empty_or_missing_entrypoint_fails(self) -> None:
        path = self.write("AGENTS.md", " \n\t")
        self.assert_fails("entrypoint must be a non-empty")
        path.unlink()
        self.assert_fails("entrypoint must be a non-empty")

    def test_unreadable_markdown_is_reported(self) -> None:
        (self.root / "AGENTS.md").write_bytes(b"\xff")
        self.assert_fails("readable UTF-8")

    def test_missing_index_fails(self) -> None:
        (self.root / ".agents/rules/index.md").unlink()
        self.assert_fails("index must be a non-empty")

    def test_unowned_payload_file_fails(self) -> None:
        self.write(".agents/stray.txt", "orphan")
        self.assert_fails("must belong to rules or skills")

    def test_nested_rule_directory_fails(self) -> None:
        self.write(".agents/rules/nested/rule.md", "# Rule\n")
        self.assert_fails("rules must be flat Markdown files")

    def test_missing_skill_entrypoint_fails(self) -> None:
        (self.root / ".agents/skills/demo/SKILL.md").unlink()
        self.assert_fails("readable SKILL.md is required")

    def test_malformed_skill_metadata_is_reported(self) -> None:
        for metadata in (
            "name: [broken",
            "- name\n- description",
            "name: demo",
            "name: demo\ndescription: null",
            "name: demo\ndescription: ' '",
            "name: demo\ndescription: ok\nextra: true",
            "name: demo\ndescription: ok\n1: unsupported",
        ):
            with self.subTest(metadata=metadata):
                self.write(
                    ".agents/skills/demo/SKILL.md", f"---\n{metadata}\n---\n# Skill\n"
                )
                self.assertTrue(self.errors())

    def test_skill_name_must_be_valid_and_match_the_directory(self) -> None:
        for name, expected in (
            ("Bad_Name", "invalid skill name"),
            ("different", "must match"),
        ):
            with self.subTest(name=name):
                self.write(
                    ".agents/skills/demo/SKILL.md",
                    f"---\nname: {name}\ndescription: Useful guidance.\n---\n# Skill\n",
                )
                self.assert_fails(expected)

    def test_skill_without_frontmatter_fails(self) -> None:
        self.write(".agents/skills/demo/SKILL.md", "# Skill\n")
        self.assert_fails("missing YAML frontmatter")

    def test_resources_need_direct_owner_links(self) -> None:
        self.write(".agents/skills/demo/assets/examples/sample.txt", "sample")
        self.assert_fails("orphan skill resource")
        self.write_skill("demo", "[Sample](assets/examples/sample.txt)\n")
        self.assertEqual([], self.errors())

    def test_unsupported_skill_entry_fails(self) -> None:
        self.write(".agents/skills/demo/extra.txt", "unexpected")
        self.assert_fails("unsupported skill-local entry")

    def test_broken_local_link_fails(self) -> None:
        self.write_skill("demo", "[Missing](references/absent.md)\n")
        self.assert_fails("cannot resolve payload path")

    def test_examples_external_links_and_fragments_do_not_require_local_files(
        self,
    ) -> None:
        self.write_skill(
            "demo",
            "```md\n[Example](absent.md)\n```\n[Docs](https://example.invalid/docs)\n[Section](#heading)\n",
        )
        self.assertEqual([], self.errors())

    def test_image_links_and_encoded_filenames_are_owned_resources(self) -> None:
        self.write(".agents/skills/demo/assets/image.svg", "<svg />")
        self.write(".agents/skills/demo/assets/two words.txt", "example")
        self.write_skill(
            "demo",
            '![Image](assets/image.svg)\n[File](assets/two%20words.txt "Example")\n',
        )
        self.assertEqual([], self.errors())

    def test_links_cannot_escape_the_payload(self) -> None:
        self.write("README.md", "not distributed")
        (self.root.parent / "outside.md").write_text("outside", encoding="utf-8")
        for target in ("README.md", "../outside.md", "..%2Foutside.md", "/README.md"):
            with self.subTest(target=target):
                self.write("AGENTS.md", f"[Outside]({target})\n")
                self.assert_fails("escapes the distributed payload")

    def test_file_uris_are_not_distributable(self) -> None:
        self.write("AGENTS.md", f"[Outside]({(self.root / 'README.md').as_uri()})\n")
        self.assert_fails("file URI is not a distributable link")

    def test_external_file_symlink_is_rejected_before_reading(self) -> None:
        outside = self.root.parent / "outside.md"
        outside.write_bytes(b"\xff")
        (self.root / ".agents/rules/external.md").symlink_to(outside)
        errors = "\n".join(self.errors())
        self.assertIn("escapes the distributed payload", errors)
        self.assertNotIn("UTF-8", errors)

    def test_external_directory_symlink_is_rejected_before_traversing(self) -> None:
        outside = self.root.parent / "external"
        outside.mkdir()
        (outside / "SKILL.md").write_bytes(b"\xff")
        (self.root / ".agents/skills/external").symlink_to(
            outside, target_is_directory=True
        )
        errors = "\n".join(self.errors())
        self.assertIn("escapes the distributed payload", errors)
        self.assertNotIn("UTF-8", errors)

    def test_payload_root_symlink_cannot_escape(self) -> None:
        outside = self.root.parent / "outside"
        (self.root / ".agents").rename(outside)
        (self.root / ".agents").symlink_to(outside, target_is_directory=True)
        self.assert_fails("escapes the distributed payload")

    def test_internal_file_symlink_remains_distributable(self) -> None:
        resource = self.write(".agents/skills/demo/assets/source.txt", "example")
        resource.with_name("alias.txt").symlink_to("source.txt")
        self.write_skill(
            "demo", "[Source](assets/source.txt)\n[Alias](assets/alias.txt)\n"
        )
        self.assertEqual([], self.errors())

    def test_directory_symlink_cycle_is_reported(self) -> None:
        assets = self.root / ".agents/skills/demo/assets"
        assets.mkdir()
        (assets / "loop").symlink_to(".", target_is_directory=True)
        self.assert_fails("directory symlink cycle")

    def test_cli_exit_status_and_diagnostics(self) -> None:
        for text, status, message in (
            ("# Instructions\n", 0, "check passed"),
            ("", 1, "check failed"),
        ):
            with self.subTest(status=status):
                self.write("AGENTS.md", text)
                output = io.StringIO()
                with (
                    contextlib.redirect_stdout(output),
                    contextlib.redirect_stderr(output),
                ):
                    self.assertEqual(status, CHECKER.main(["--root", str(self.root)]))
                self.assertIn(message, output.getvalue())


if __name__ == "__main__":
    unittest.main()
