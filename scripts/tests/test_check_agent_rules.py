from __future__ import annotations

import importlib.util
import json
import shutil
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
SCRIPT = REPO_ROOT / "scripts" / "check-agent-rules.py"
SPEC = importlib.util.spec_from_file_location("check_agent_rules", SCRIPT)
assert SPEC and SPEC.loader
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


class CheckAgentRulesTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        agents = self.root / ".agents"
        for directory in ["rules/core", "skills/demo", "templates", "references"]:
            (agents / directory).mkdir(parents=True, exist_ok=True)
        schemas = self.root / "schemas" / "agent-rules"
        schemas.mkdir(parents=True)
        for schema in (REPO_ROOT / "schemas" / "agent-rules").glob("*.schema.json"):
            shutil.copy2(schema, schemas / schema.name)
        shutil.copyfile(
            REPO_ROOT / ".agents" / "templates" / "shared-rules-lock.schema.json",
            agents / "templates" / "shared-rules-lock.schema.json",
        )
        shutil.copy2(REPO_ROOT / ".agents" / "manifest.json", agents / "manifest.json")
        overlay = self.root / ".project-agent"
        overlay.mkdir()
        shutil.copyfile(
            REPO_ROOT / ".project-agent" / "shared-rules.lock",
            overlay / "shared-rules.lock",
        )
        self._write_rule(
            agents / "rules" / "route-map.md",
            "route-map",
            "index",
            body=".agents/rules/core/example.md\n.agents/skills/demo/SKILL.md\n",
        )
        self._write_rule(agents / "rules" / "core" / "example.md", "core.example", "core")
        (agents / "skills" / "demo" / "SKILL.md").write_text(
            "---\nname: demo\ndescription: Demonstrate a valid skill.\n---\n\n# Demo\n",
            encoding="utf-8",
        )

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _write_rule(
        self,
        path: Path,
        rule_id: str,
        kind: str,
        *,
        companions: str = "{}",
        body: str = "",
    ) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(
            f"---\nid: {rule_id}\nkind: {kind}\ntriggers:\n  - example\nsummary: Example rule.\ncompanions: {companions}\n---\n\n{body}",
            encoding="utf-8",
        )

    def errors(self) -> list[str]:
        return CHECKER.check_repository(self.root)

    def test_valid_tree_passes(self) -> None:
        self.assertEqual([], self.errors())

    def test_companions_array_fails(self) -> None:
        self._write_rule(self.root / ".agents/rules/core/example.md", "core.example", "core", companions="[]")
        self.assertTrue(any("companions" in error and "schema validation" in error for error in self.errors()))

    def test_duplicate_rule_id_fails(self) -> None:
        self._write_rule(self.root / ".agents/rules/core/duplicate.md", "core.example", "core")
        self.assertIn("duplicate rule id: core.example", self.errors())

    def test_dangling_companion_fails(self) -> None:
        self._write_rule(
            self.root / ".agents/rules/core/example.md",
            "core.example",
            "core",
            companions="{required_rules: [core.missing]}",
        )
        self.assertTrue(any("unresolved required_rules companion: core.missing" in error for error in self.errors()))

    def test_missing_route_coverage_fails(self) -> None:
        route = self.root / ".agents/rules/route-map.md"
        self._write_rule(route, "route-map", "index", body=".agents/skills/demo/SKILL.md\n")
        self.assertTrue(any("missing rule route coverage" in error for error in self.errors()))

    def test_skill_name_mismatch_fails(self) -> None:
        skill = self.root / ".agents/skills/demo/SKILL.md"
        skill.write_text("---\nname: renamed\ndescription: Mismatch.\n---\n", encoding="utf-8")
        self.assertTrue(any("must match name" in error for error in self.errors()))

    def test_invalid_json_schema_fails(self) -> None:
        bad = self.root / ".agents/templates/bad.schema.json"
        bad.write_text(json.dumps({"$schema": CHECKER.DIALECT, "type": 7}), encoding="utf-8")
        self.assertTrue(any("invalid Draft 2020-12 schema" in error for error in self.errors()))

    def test_schema_companion_category_fails(self) -> None:
        self._write_rule(
            self.root / ".agents/rules/core/example.md",
            "core.example",
            "core",
            companions="{schemas: [{id: example, when: needed}]}",
        )
        self.assertTrue(any("schema validation" in error and "schemas" in error for error in self.errors()))

    def test_distributed_payload_maintenance_leak_fails(self) -> None:
        leaked = self.root / ".agents/references/leaked.md"
        leaked.write_text("Run scripts/check-agent-rules.py.\n", encoding="utf-8")
        self.assertTrue(any("maintenance-only marker" in error for error in self.errors()))

    def test_invalid_project_lock_is_validated_with_consumer_schema(self) -> None:
        (self.root / ".project-agent" / "shared-rules.lock").write_text("{}\n", encoding="utf-8")
        self.assertTrue(any("shared-rules.lock: schema validation failed" in error for error in self.errors()))

    def test_missing_project_lock_fails(self) -> None:
        (self.root / ".project-agent" / "shared-rules.lock").unlink()
        self.assertTrue(any("required upstream lock is missing" in error for error in self.errors()))

    def test_project_lock_values_must_match_manifest(self) -> None:
        lock_path = self.root / ".project-agent" / "shared-rules.lock"
        baseline = json.loads(lock_path.read_text(encoding="utf-8"))
        mappings = {
            "expected_name": "name",
            "expected_version": "version",
            "expected_manifest_schema_version": "schema_version",
            "expected_rules_schema_version": "rules_schema_version",
            "expected_skills_schema_version": "skills_schema_version",
            "expected_overlay_discovery_version": "overlay_discovery_version",
            "expected_companion_metadata_version": "companion_metadata_version",
        }
        for lock_field, manifest_field in mappings.items():
            with self.subTest(lock_field=lock_field):
                mismatched = dict(baseline)
                mismatched[lock_field] = "mismatch"
                lock_path.write_text(json.dumps(mismatched), encoding="utf-8")
                self.assertTrue(
                    any(
                        f"lock mismatch: {lock_field}='mismatch' does not match manifest.{manifest_field}=" in error
                        for error in self.errors()
                    )
                )

    def test_lock_schema_dimension_drift_fails_without_comparison(self) -> None:
        schema_path = self.root / ".agents" / "templates" / "shared-rules-lock.schema.json"
        schema = json.loads(schema_path.read_text(encoding="utf-8"))
        schema["properties"].pop("expected_name")
        schema["required"].remove("expected_name")
        schema_path.write_text(json.dumps(schema), encoding="utf-8")

        lock_path = self.root / ".project-agent" / "shared-rules.lock"
        lock = json.loads(lock_path.read_text(encoding="utf-8"))
        lock.pop("expected_name")
        lock_path.write_text(json.dumps(lock), encoding="utf-8")

        errors = self.errors()
        self.assertTrue(
            any("lock properties do not match manifest dimensions" in error for error in errors)
        )
        self.assertTrue(
            any("lock required fields do not match manifest dimensions" in error for error in errors)
        )

    def test_missing_central_schema_fails(self) -> None:
        (self.root / "schemas/agent-rules/manifest.schema.json").unlink()
        self.assertTrue(any("manifest.schema.json: invalid JSON" in error for error in self.errors()))


if __name__ == "__main__":
    unittest.main()
