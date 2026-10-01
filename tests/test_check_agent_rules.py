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
        self.write("README.md", "[missing](absent.md)\n")
        self.write(".project-agent/project.md", "[missing](also-absent.md)\n")
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

    def test_required_documents_must_be_present_and_readable(self) -> None:
        for relative in (
            "AGENTS.md",
            ".agents/rules/index.md",
            ".agents/skills/demo/SKILL.md",
        ):
            path = self.root / relative
            original = path.read_bytes()
            for content in (None, b" \n\t", b"\xff"):
                with self.subTest(path=relative, content=content):
                    if content is None:
                        path.unlink()
                    else:
                        path.write_bytes(content)
                    self.assert_fails(str(path))
            path.write_bytes(original)

    def test_unowned_payload_file_fails(self) -> None:
        self.write(".agents/stray.txt", "orphan")
        self.assert_fails("must belong to rules or skills")

    def test_nested_rule_directory_fails(self) -> None:
        self.write(".agents/rules/nested/rule.md", "# Rule\n")
        self.assert_fails("rules must be flat Markdown files")

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

    def test_links_in_rules_and_skill_references_report_source_lines(self) -> None:
        self.write_skill("demo", "[Guide](references/guide.md)\n")
        self.write(".agents/skills/demo/references/guide.md", "# Guide\n")
        for relative in (
            ".agents/rules/connections.md",
            ".agents/skills/demo/references/guide.md",
        ):
            self.write(relative, "# Guide\n\n[Missing](absent.md)\n")
            self.assert_fails(f"{self.root / relative}:3:")
            self.write(relative, "# Guide\n")
        self.assertEqual([], self.errors())

    def test_skill_frontmatter_does_not_hide_links_in_other_documents(self) -> None:
        self.write(
            ".agents/skills/demo/SKILL.md",
            "---\nname: demo\ndescription: '[Example](absent.md)'\n---\n# Skill\n",
        )
        self.assertEqual([], self.errors())
        self.write(".agents/rules/connections.md", "---\n[Missing](absent.md)\n---\n")
        self.assert_fails("connections.md:2:")

    def test_code_examples_are_skipped_without_hiding_real_links(self) -> None:
        for example in (
            "`[Example](absent.md)`",
            "``[Example](absent.md) with a ` inside``",
            "`multiline\n[Example](absent.md)`",
            "```md\n[Example](absent.md)\n[Reference][id]\n```",
            "~~~~md\n~~~\n[Example](absent.md)\n[id]: absent.md\n~~~~~",
            "> ```md\n> [Example](absent.md)\n> ```",
            "````md\n```\n[Example](absent.md)\n````",
            "```[Example](absent.md)```",
            r"\[Example](absent.md)",
        ):
            with self.subTest(example=example):
                self.write("AGENTS.md", example + "\n[Rules](.agents/rules/index.md)\n")
                self.assertEqual([], self.errors())
                self.write("AGENTS.md", example + "\n[Missing](absent.md)\n")
                line_number = example.count("\n") + 2
                self.assert_fails(f"AGENTS.md:{line_number}:")

    def test_external_links_fragments_and_html_links_are_not_resolved(self) -> None:
        self.write_skill(
            "demo",
            "[Docs](https://example.invalid/docs)\n[CDN](//example.invalid/a.png)\n"
            '[Section](#heading%20name)\n<a href="absent.md">HTML</a>\n',
        )
        self.assertEqual([], self.errors())

    def test_images_and_plain_paths_with_fragments_are_owned_resources(self) -> None:
        self.write(".agents/skills/demo/assets/image.svg", "<svg />")
        self.write(".agents/skills/demo/references/指南.md", "# Guide")
        self.write_skill(
            "demo",
            "![Image](assets/image.svg)\n[File](references/指南.md#section)\n"
            "[Rules](/.agents/rules/index.md)\n",
        )
        self.assertEqual([], self.errors())

    def test_unsupported_local_link_syntax_is_reported(self) -> None:
        for target in (
            ".agents/rules/two words.md",
            ".agents/rules/index.md(extra)",
            "<.agents/rules/index.md>",
            '.agents/rules/index.md "Title"',
            ".agents/rules/two%20words.md",
            "..%2Foutside.md",
            ".agents/rules/index.md?view=1",
            r".agents\rules\index.md",
            "",
        ):
            with self.subTest(target=target):
                self.write("AGENTS.md", f"[Example]({target})\n")
                self.assert_fails("AGENTS.md:1: unsupported local link")
        self.write("AGENTS.md", "[Example](.agents/rules/index.md\n")
        self.assert_fails("AGENTS.md:1: unsupported local link")

    def test_reference_links_and_definitions_require_inline_links(self) -> None:
        for text in (
            "[Example][guide]",
            "[Example][]",
            "[Example]\n[guide]",
            "![Image][guide]",
            "[guide]: .agents/rules/index.md",
            "> [guide]: .agents/rules/index.md",
        ):
            with self.subTest(text=text):
                self.write("AGENTS.md", text + "\n")
                self.assert_fails("AGENTS.md:1: reference-style")

    def test_links_cannot_escape_the_payload(self) -> None:
        self.write("README.md", "not distributed")
        (self.root.parent / "outside.md").write_text("outside", encoding="utf-8")
        for target in ("README.md", "../outside.md", "../absent.md", "/README.md"):
            with self.subTest(target=target):
                self.write("AGENTS.md", f"[Outside]({target})\n")
                self.assert_fails("escapes the distributed payload")

    def test_file_uris_are_not_distributable(self) -> None:
        self.write("AGENTS.md", f"[Outside]({(self.root / 'README.md').as_uri()})\n")
        self.assert_fails("file URI is not a distributable link")

    def test_file_symlinks_are_rejected_without_reading_the_target(self) -> None:
        outside = self.root.parent / "outside.md"
        outside.write_bytes(b"\xff")
        alias = self.root / ".agents/rules/alias.md"
        for target in ("connections.md", outside, "missing.md", "alias.md"):
            with self.subTest(target=target):
                alias.symlink_to(target)
                errors = "\n".join(self.errors())
                self.assertIn(f"{alias}: symlinks", errors)
                self.assertNotIn("UTF-8", errors)
                alias.unlink()

    def test_directory_symlinks_are_rejected_without_traversal(self) -> None:
        outside = self.root.parent / "external"
        outside.mkdir()
        (outside / "SKILL.md").write_bytes(b"\xff")
        alias = self.root / ".agents/skills/alias"
        for target in ("demo", outside, "."):
            with self.subTest(target=target):
                alias.symlink_to(target, target_is_directory=True)
                errors = "\n".join(self.errors())
                self.assertIn(f"{alias}: symlinks", errors)
                self.assertNotIn("UTF-8", errors)
                alias.unlink()

    def test_distribution_entrypoints_cannot_be_symlinks(self) -> None:
        for name in ("AGENTS.md", ".agents"):
            with self.subTest(name=name):
                path = self.root / name
                outside = self.root.parent / name
                path.rename(outside)
                path.symlink_to(outside, target_is_directory=outside.is_dir())
                self.assert_fails(f"{path}: symlinks")
                path.unlink()
                outside.rename(path)

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
