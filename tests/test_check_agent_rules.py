from __future__ import annotations

import importlib.util
import json
import shutil
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
SCRIPT = REPO_ROOT / "scripts" / "check-agent-rules.py"
SPEC = importlib.util.spec_from_file_location("check_agent_rules", SCRIPT)
assert SPEC and SPEC.loader
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


class CheckAgentRulesTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        shutil.copyfile(REPO_ROOT / "AGENTS.md", self.root / "AGENTS.md")
        shutil.copyfile(REPO_ROOT / "README.md", self.root / "README.md")
        shutil.copytree(
            REPO_ROOT / ".agents",
            self.root / ".agents",
            copy_function=shutil.copyfile,
        )
        shutil.copytree(
            REPO_ROOT / "tests" / "evals",
            self.root / "tests" / "evals",
            copy_function=shutil.copyfile,
        )
        overlay = self.root / ".project-agent"
        overlay.mkdir()
        shutil.copyfile(REPO_ROOT / ".project-agent" / "project.md", overlay / "project.md")
        for path in self.root.rglob("*"):
            path.chmod(path.stat().st_mode | 0o200)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def errors(self) -> list[str]:
        return CHECKER.check_repository(self.root)

    def skill(self, name: str = "ai-visual-review") -> Path:
        return self.root / ".agents" / "skills" / name / "SKILL.md"

    def replace_skill_frontmatter(self, name: str, frontmatter: str) -> None:
        path = self.skill(name)
        text = path.read_text(encoding="utf-8")
        end = text.index("\n---\n", 4)
        path.write_text(f"---\n{frontmatter}\n---\n{text[end + 5:]}", encoding="utf-8")

    def write_skill_with_line_count(self, name: str, line_count: int) -> None:
        lines = [
            "---",
            f"name: {name}",
            "description: Exercise the line limit; use only in this test and not for production.",
            "---",
            "# Test Skill",
        ]
        lines.extend("Do the test." for _ in range(line_count - len(lines)))
        self.skill(name).write_text("\n".join(lines) + "\n", encoding="utf-8")

    def test_valid_target_tree_passes(self) -> None:
        self.assertEqual([], self.errors())

    def test_agents_over_16_kib_fails(self) -> None:
        (self.root / "AGENTS.md").write_text("x" * (16 * 1024 + 1), encoding="utf-8")
        self.assertTrue(any("exceeds 16384" in error for error in self.errors()))

    def test_rule_yaml_frontmatter_fails(self) -> None:
        path = self.root / ".agents" / "rules" / "defaults.md"
        path.write_text("---\nname: old\n---\n" + path.read_text(encoding="utf-8"), encoding="utf-8")
        self.assertTrue(any("rule YAML frontmatter" in error for error in self.errors()))

    def test_h1_inside_fenced_example_is_ignored(self) -> None:
        path = self.root / ".agents" / "rules" / "defaults.md"
        path.write_text(
            path.read_text(encoding="utf-8") + "\n```md\n# Example only\n```\n",
            encoding="utf-8",
        )
        self.assertEqual([], self.errors())

    def test_rule_subdirectory_fails(self) -> None:
        nested = self.root / ".agents" / "rules" / "old"
        nested.mkdir()
        (nested / "rule.md").write_text("# Old\n", encoding="utf-8")
        self.assertTrue(any("rule directories are not allowed" in error for error in self.errors()))

    def test_index_missing_rule_fails(self) -> None:
        path = self.root / ".agents" / "rules" / "index.md"
        text = path.read_text(encoding="utf-8").replace("[defaults](defaults.md)", "defaults")
        path.write_text(text, encoding="utf-8")
        self.assertTrue(any("missing routed Markdown link" in error and "defaults.md" in error for error in self.errors()))

    def test_index_missing_skill_fails(self) -> None:
        path = self.root / ".agents" / "rules" / "index.md"
        text = path.read_text(encoding="utf-8").replace(
            "[atomic-commit](../skills/atomic-commit/SKILL.md)", "atomic commit"
        )
        path.write_text(text, encoding="utf-8")
        self.assertTrue(any("missing routed Markdown link" in error and "atomic-commit" in error for error in self.errors()))

    def test_broken_local_markdown_link_fails(self) -> None:
        path = self.root / "README.md"
        path.write_text(path.read_text(encoding="utf-8") + "\n[broken](missing.md)\n", encoding="utf-8")
        self.assertTrue(any("broken local Markdown link" in error for error in self.errors()))

    def test_skill_extra_metadata_fails(self) -> None:
        self.replace_skill_frontmatter(
            "atomic-commit",
            "name: atomic-commit\ndescription: Commit an authorized change; do not auto-trigger.\nmetadata: {}",
        )
        self.assertTrue(any("extra=['metadata']" in error for error in self.errors()))

    def test_skill_invalid_name_fails(self) -> None:
        self.replace_skill_frontmatter(
            "atomic-commit",
            "name: Atomic_Commit\ndescription: Commit an authorized change; do not auto-trigger.",
        )
        self.assertTrue(any("invalid Agent Skills name" in error for error in self.errors()))

    def test_skill_directory_name_mismatch_fails(self) -> None:
        self.replace_skill_frontmatter(
            "atomic-commit",
            "name: browser-e2e\ndescription: Commit an authorized change; do not auto-trigger.",
        )
        self.assertTrue(any("must match name" in error for error in self.errors()))

    def test_skill_empty_description_fails(self) -> None:
        self.replace_skill_frontmatter("atomic-commit", 'name: atomic-commit\ndescription: ""')
        self.assertTrue(any("description must be a non-empty string" in error for error in self.errors()))

    def test_skill_description_too_long_fails(self) -> None:
        self.replace_skill_frontmatter(
            "atomic-commit",
            f'name: atomic-commit\ndescription: "{"x" * 1025}"',
        )
        self.assertTrue(any("description exceeds 1024" in error for error in self.errors()))

    def test_skill_with_499_lines_passes(self) -> None:
        self.write_skill_with_line_count("atomic-commit", 499)
        self.assertEqual([], self.errors())

    def test_skill_with_500_lines_fails(self) -> None:
        self.write_skill_with_line_count("atomic-commit", 500)
        self.assertTrue(any("SKILL.md must stay below 500 lines" in error for error in self.errors()))

    def test_orphan_skill_asset_fails(self) -> None:
        path = self.root / ".agents" / "skills" / "atomic-commit" / "assets" / "orphan.md"
        path.parent.mkdir()
        path.write_text("# Orphan\n", encoding="utf-8")
        self.assertTrue(any("orphan skill-local file" in error for error in self.errors()))

    def test_invalid_json_schema_fails(self) -> None:
        path = self.root / ".agents" / "skills" / "ai-visual-review" / "assets" / "finding.schema.json"
        value = json.loads(path.read_text(encoding="utf-8"))
        value["type"] = "not-a-json-type"
        path.write_text(json.dumps(value), encoding="utf-8")
        self.assertTrue(any("invalid Draft 2020-12 schema" in error for error in self.errors()))

    def test_payload_maintenance_leak_fails(self) -> None:
        path = self.root / ".agents" / "rules" / "defaults.md"
        path.write_text(path.read_text(encoding="utf-8") + "\nRun scripts/check-agent-rules.py.\n", encoding="utf-8")
        self.assertTrue(any("maintenance-only marker" in error for error in self.errors()))

    def test_duplicate_eval_id_fails(self) -> None:
        source = self.root / "tests" / "evals" / "routing.jsonl"
        duplicate = source.read_text(encoding="utf-8").splitlines()[0]
        target = self.root / "tests" / "evals" / "safety.jsonl"
        target.write_text(target.read_text(encoding="utf-8") + duplicate + "\n", encoding="utf-8")
        self.assertTrue(any("duplicate eval id" in error for error in self.errors()))

    def test_invalid_eval_rule_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "routing.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["expected_rules"][0] = ".agents/rules/missing.md"
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(any("unknown rule path" in error for error in self.errors()))

    def test_invalid_forbidden_eval_rule_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "routing.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["forbidden_rules"] = [".agents/rules/missing.md"]
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(any("unknown rule path" in error for error in self.errors()))

    def test_duplicate_forbidden_eval_rule_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "routing.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["forbidden_rules"] = [
            ".agents/rules/formatting.md",
            ".agents/rules/formatting.md",
        ]
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(
            any("forbidden_rules contains duplicates" in error for error in self.errors())
        )

    def test_expected_and_forbidden_eval_rule_overlap_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "routing.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["forbidden_rules"] = [record["expected_rules"][0]]
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(
            any("rules cannot be expected and forbidden" in error for error in self.errors())
        )

    def test_missing_forbidden_rules_field_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "routing.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        del record["forbidden_rules"]
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(any("eval oracle fields must contain" in error for error in self.errors()))

    def test_non_list_forbidden_rules_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "routing.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["forbidden_rules"] = ".agents/rules/formatting.md"
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(
            any("forbidden_rules must be an array of strings" in error for error in self.errors())
        )

    def test_empty_eval_task_fails(self) -> None:
        path = self.root / "tests" / "evals" / "routing.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["task"] = " "
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(any("task must be a non-empty string" in error for error in self.errors()))

    def test_case_and_oracle_ids_must_match(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "routing.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["id"] = "different-id"
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(any("case/oracle IDs must match" in error for error in self.errors()))

    def test_safety_oracle_requires_behavior(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "safety.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        del record["behavior"]
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(any("safety eval requires behavior" in error for error in self.errors()))

    def test_behavior_criteria_must_not_be_empty(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "safety.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["behavior"]["criteria"] = []
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(any("criteria must contain" in error for error in self.errors()))

    def test_positive_skill_oracle_requires_baseline(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "skills.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        del record["baseline_disabled_skills"]
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(
            any("positive skill eval requires behavior and baseline" in error for error in self.errors())
        )

    def test_safety_oracle_rejects_skill_baseline(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "safety.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["baseline_disabled_skills"] = record["expected_skills"]
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(
            any("safety eval must not define a baseline" in error for error in self.errors())
        )

    def test_runtime_contract_tool_shape_is_schema_validated(self) -> None:
        path = self.root / "tests" / "evals" / "codex-runtime-contract.json"
        value = json.loads(path.read_text(encoding="utf-8"))
        value["codex_versions"]["codex-cli 0.144.1"]["allowed_tools"][0][
            "unexpected"
        ] = True
        path.write_text(json.dumps(value), encoding="utf-8")
        self.assertTrue(
            any("runtime contract schema failure" in error for error in self.errors())
        )

    def test_missing_rule_coverage_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "routing.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        for index, line in enumerate(lines):
            record = json.loads(line)
            record["expected_rules"] = [
                rule
                for rule in record["expected_rules"]
                if rule != ".agents/rules/flutter-rust.md"
            ]
            lines[index] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(
            any(
                "eval corpus missing rule coverage" in error
                and "flutter-rust.md" in error
                for error in self.errors()
            )
        )

    def test_invalid_eval_skill_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "skills.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["expected_skills"] = ["missing-skill"]
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(any("unknown skill name" in error for error in self.errors()))

    def test_expected_and_forbidden_eval_skill_overlap_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "skills.jsonl"
        lines = path.read_text(encoding="utf-8").splitlines()
        record = json.loads(lines[0])
        record["forbidden_skills"] = [record["expected_skills"][0]]
        lines[0] = json.dumps(record)
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        self.assertTrue(
            any("skills cannot be expected and forbidden" in error for error in self.errors())
        )

    def test_missing_skill_positive_coverage_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "skills.jsonl"
        records = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()]
        for record in records:
            record["expected_skills"] = [
                name for name in record["expected_skills"] if name != "ai-visual-review"
            ]
        path.write_text("\n".join(json.dumps(record) for record in records) + "\n", encoding="utf-8")
        self.assertTrue(any("missing positive coverage: ai-visual-review" in error for error in self.errors()))

    def test_missing_skill_negative_coverage_fails(self) -> None:
        path = self.root / "tests" / "evals" / "oracles" / "skills.jsonl"
        records = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()]
        for record in records:
            record["forbidden_skills"] = [
                name for name in record["forbidden_skills"] if name != "ai-visual-review"
            ]
        path.write_text("\n".join(json.dumps(record) for record in records) + "\n", encoding="utf-8")
        self.assertTrue(any("missing negative coverage: ai-visual-review" in error for error in self.errors()))

    def test_recreated_manifest_fails(self) -> None:
        (self.root / ".agents" / "manifest.json").write_text("{}\n", encoding="utf-8")
        self.assertTrue(any("deleted metadata or global catalog path" in error for error in self.errors()))

    def test_recreated_global_catalog_fails(self) -> None:
        (self.root / ".agents" / "templates").mkdir()
        self.assertTrue(any("deleted metadata or global catalog path" in error for error in self.errors()))


if __name__ == "__main__":
    unittest.main()
