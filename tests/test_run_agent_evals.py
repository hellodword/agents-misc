from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import os
import shutil
import stat
import sys
import tempfile
import textwrap
import tomllib
import unittest
from pathlib import Path

from jsonschema import Draft202012Validator


REPO_ROOT = Path(__file__).resolve().parents[1]
SCRIPT = REPO_ROOT / "scripts" / "run-agent-evals.py"
SPEC = importlib.util.spec_from_file_location("run_agent_evals", SCRIPT)
assert SPEC and SPEC.loader
RUNNER = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = RUNNER
SPEC.loader.exec_module(RUNNER)


class RunAgentEvalsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def first_case(self):
        return RUNNER._load_eval_cases(
            REPO_ROOT, None, ["routing-greenfield-fullstack"]
        )[0]

    def write_chatgpt_auth(self, path: Path, token: str) -> None:
        path.write_text(
            json.dumps(
                {
                    "auth_mode": "chatgpt",
                    "OPENAI_API_KEY": None,
                    "tokens": {"access_token": token},
                    "last_refresh": "2026-07-19T00:00:00Z",
                }
            )
            + "\n",
            encoding="utf-8",
        )
        path.chmod(0o600)

    def test_checked_in_eval_records_match_schema(self) -> None:
        case_schema = json.loads(
            (REPO_ROOT / "tests/evals/schemas/eval-record.schema.json").read_text(
                encoding="utf-8"
            )
        )
        oracle_schema = json.loads(
            (REPO_ROOT / "tests/evals/schemas/eval-oracle.schema.json").read_text(
                encoding="utf-8"
            )
        )
        case_validator = Draft202012Validator(case_schema)
        oracle_validator = Draft202012Validator(oracle_schema)
        case_count = 0
        oracle_count = 0
        for path in sorted((REPO_ROOT / "tests/evals").glob("*.jsonl")):
            for line in path.read_text(encoding="utf-8").splitlines():
                case_validator.validate(json.loads(line))
                case_count += 1
        for path in sorted((REPO_ROOT / "tests/evals/oracles").glob("*.jsonl")):
            for line in path.read_text(encoding="utf-8").splitlines():
                oracle_validator.validate(json.loads(line))
                oracle_count += 1
        self.assertEqual(59, case_count)
        self.assertEqual(59, oracle_count)

    def test_route_model_schema_uses_canonical_payload_identifiers(self) -> None:
        rules, skills = RUNNER._route_output_values(REPO_ROOT)
        destination = self.root / "route-result.schema.json"
        RUNNER._write_model_output_schema(
            REPO_ROOT / "tests/evals/schemas/route-result.schema.json",
            destination,
            route_rules=rules,
            route_skills=skills,
        )
        schema = json.loads(destination.read_text(encoding="utf-8"))
        self.assertNotIn("$schema", schema)
        self.assertNotIn("$id", schema)
        rule_items = schema["properties"]["selected_rules"]["items"]
        skill_items = schema["properties"]["selected_skills"]["items"]
        self.assertEqual(rules, rule_items["enum"])
        self.assertEqual(skills, skill_items["enum"])
        self.assertTrue(
            all(value.startswith(".agents/rules/") for value in rule_items["enum"])
        )
        self.assertTrue(all("/" not in value for value in skill_items["enum"]))
        self.assertNotIn("uniqueItems", schema["properties"]["selected_rules"])
        self.assertNotIn("uniqueItems", schema["properties"]["selected_skills"])

    def test_behavior_and_baseline_scope_is_intentional(self) -> None:
        cases = RUNNER._load_eval_cases(REPO_ROOT, None, None)
        self.assertEqual(59, len(cases))
        self.assertEqual(31, sum(case.behavior is not None for case in cases))
        self.assertEqual(
            15, sum(bool(case.baseline_disabled_skills) for case in cases)
        )
        self.assertTrue(
            all(case.behavior is None for case in cases if case.corpus == "routing")
        )

    def test_eval_selection_rejects_unknown_id(self) -> None:
        with self.assertRaisesRegex(RUNNER.EvalInputError, "unknown or filtered"):
            RUNNER._load_eval_cases(REPO_ROOT, None, ["missing-eval"])

    def test_prompts_do_not_serialize_expectations(self) -> None:
        case = RUNNER._load_eval_cases(
            REPO_ROOT, None, ["skill-ai-visual-positive"]
        )[0]
        index_text = (REPO_ROOT / ".agents/rules/index.md").read_text(
            encoding="utf-8"
        )
        route_prompt = RUNNER._routing_prompt(case, index_text)
        behavior_prompt = RUNNER._behavior_prompt(
            case,
            REPO_ROOT,
            {
                "selected_rules": list(case.expected_rules),
                "selected_skills": list(case.expected_skills),
            },
        )
        RUNNER._assert_no_expectation_leak(route_prompt)
        RUNNER._assert_no_expectation_leak(behavior_prompt)
        self.assertIn("Apply every routing-table row independently", route_prompt)
        self.assertIn("frontmatter name only, never by file path", route_prompt)
        self.assertIn("identifiers allowed by the supplied JSON Schema", route_prompt)
        assert case.behavior is not None
        self.assertNotIn(case.behavior.summary, route_prompt)
        self.assertNotIn(case.behavior.summary, behavior_prompt)
        for rubric in (*case.behavior.criteria, *case.behavior.prohibitions):
            self.assertNotIn(rubric, route_prompt)
            self.assertNotIn(rubric, behavior_prompt)
        judge_prompt = RUNNER._judge_prompt(case, "Synthetic candidate response.")
        self.assertIn(case.behavior.summary, judge_prompt)
        self.assertIn("response-level proposed-approach evaluation", judge_prompt)
        self.assertIn("Do not require tool calls", judge_prompt)

    def test_turn_usage_requires_one_complete_non_negative_record(self) -> None:
        usage = {
            "input_tokens": 100,
            "cached_input_tokens": 20,
            "output_tokens": 10,
            "reasoning_output_tokens": 5,
        }
        self.assertEqual(
            usage,
            RUNNER._turn_usage([{"type": "turn.completed", "usage": usage}]),
        )
        with self.assertRaisesRegex(RUNNER.EvalRuntimeError, "expected 1"):
            RUNNER._turn_usage([])
        with self.assertRaisesRegex(RUNNER.EvalRuntimeError, "non-negative integer"):
            RUNNER._turn_usage(
                [
                    {
                        "type": "turn.completed",
                        "usage": {**usage, "output_tokens": -1},
                    }
                ]
            )

    def test_eval_source_snapshot_freezes_payload_and_runtime_inputs(self) -> None:
        source = self.root / "source"
        source.mkdir()
        shutil.copyfile(REPO_ROOT / "AGENTS.md", source / "AGENTS.md")
        shutil.copytree(REPO_ROOT / ".agents", source / ".agents")
        shutil.copytree(
            REPO_ROOT / "tests/evals/schemas", source / "tests/evals/schemas"
        )
        shutil.copyfile(
            REPO_ROOT / "tests/evals/codex-runtime-contract.json",
            source / "tests/evals/codex-runtime-contract.json",
        )
        snapshot = self.root / "snapshot"
        RUNNER._snapshot_eval_source(source, snapshot)
        snapshot_digest = RUNNER._payload_sha256(snapshot)
        self.assertEqual(RUNNER._payload_sha256(source), snapshot_digest)

        (source / "AGENTS.md").write_text("changed after snapshot\n", encoding="utf-8")
        self.assertEqual(snapshot_digest, RUNNER._payload_sha256(snapshot))
        self.assertNotEqual(snapshot_digest, RUNNER._payload_sha256(source))
        self.assertTrue(
            (snapshot / "tests/evals/schemas/run-summary.schema.json").is_file()
        )
        self.assertTrue(
            (snapshot / "tests/evals/codex-runtime-contract.json").is_file()
        )

        linked_source = self.root / "linked-source"
        linked_source.mkdir()
        shutil.copyfile(REPO_ROOT / "AGENTS.md", linked_source / "AGENTS.md")
        shutil.copytree(REPO_ROOT / ".agents", linked_source / ".agents")
        (linked_source / "tests").symlink_to(source / "tests", target_is_directory=True)
        with self.assertRaisesRegex(RUNNER.EvalInputError, "non-symlink directory"):
            RUNNER._snapshot_eval_source(linked_source, self.root / "bad-snapshot")

    def test_behavior_prompt_includes_direct_skill_resource(self) -> None:
        case = RUNNER._load_eval_cases(
            REPO_ROOT, None, ["routing-heavy-nix-ci"]
        )[0]
        prompt = RUNNER._behavior_prompt(
            case,
            REPO_ROOT,
            {
                "selected_rules": list(case.expected_rules),
                "selected_skills": list(case.expected_skills),
            },
        )
        self.assertIn(
            ".agents/skills/nix-workflow/references/github-actions-nix.md", prompt
        )

    def test_policy_projection_copies_only_approved_fields(self) -> None:
        config = self.root / "config.toml"
        config.write_text(
            "\n".join(
                [
                    'model = "must-not-leak"',
                    'approval_policy = "never"',
                    'sandbox_mode = "workspace-write"',
                    "[sandbox_workspace_write]",
                    "network_access = true",
                    "exclude_slash_tmp = true",
                    "exclude_tmpdir_env_var = false",
                    'writable_roots = ["/tmp/project-only"]',
                    "[features]",
                    "shell_tool = true",
                ]
            )
            + "\n",
            encoding="utf-8",
        )
        policy = RUNNER._load_policy(config, "inherit", "inherit")
        self.assertEqual("never", policy.approval_policy)
        self.assertEqual("workspace-write", policy.sandbox_mode)
        self.assertEqual(
            {
                "network_access": True,
                "exclude_slash_tmp": True,
                "exclude_tmpdir_env_var": False,
                "writable_roots": ["/tmp/project-only"],
            },
            policy.sandbox_workspace_write,
        )
        rendered = RUNNER._render_config(
            model="gpt-test",
            reasoning_effort="high",
            policy=policy,
            model_catalog_path=self.root / "models.json",
            include_skill_instructions=True,
        )
        self.assertNotIn("must-not-leak", rendered)
        self.assertIn("shell_tool = false", rendered)
        self.assertIn('web_search = "disabled"', rendered)
        self.assertIn("exec_permission_approvals = false", rendered)
        rendered_features = tomllib.loads(rendered)["features"]
        for deprecated in (
            "collab",
            "connectors",
            "imagegenext",
            "memory_tool",
            "request_permissions",
            "web_search",
            "web_search_cached",
            "web_search_request",
        ):
            self.assertNotIn(deprecated, rendered_features)

    def test_explicit_policy_overrides_config(self) -> None:
        config = self.root / "config.toml"
        config.write_text(
            'approval_policy = "on-request"\nsandbox_mode = "danger-full-access"\n',
            encoding="utf-8",
        )
        policy = RUNNER._load_policy(config, "never", "read-only")
        self.assertEqual("never", policy.approval_policy)
        self.assertEqual("read-only", policy.sandbox_mode)
        self.assertEqual("command-line", policy.approval_source)

    def test_rendered_config_disables_the_selected_payload_skill(self) -> None:
        disabled = self.root / "fixture/.agents/skills/example/SKILL.md"
        rendered = RUNNER._render_config(
            model="gpt-test",
            reasoning_effort="high",
            policy=RUNNER.Policy(
                approval_policy="never",
                sandbox_mode="read-only",
                sandbox_workspace_write={},
                approval_source="test",
                sandbox_source="test",
            ),
            model_catalog_path=self.root / "models.json",
            include_skill_instructions=False,
            disabled_skill_paths=[disabled],
        )
        config = tomllib.loads(rendered)
        self.assertEqual(
            [{"path": str(disabled), "enabled": False}],
            config["skills"]["config"],
        )

    def test_payload_digest_is_stable_and_content_sensitive(self) -> None:
        source = self.root / "payload"
        (source / ".agents").mkdir(parents=True)
        (source / "AGENTS.md").write_text("# Rules\n", encoding="utf-8")
        rule = source / ".agents/rule.md"
        rule.write_text("first\n", encoding="utf-8")
        original = RUNNER._payload_sha256(source)
        self.assertEqual(original, RUNNER._payload_sha256(source))
        rule.write_text("second\n", encoding="utf-8")
        self.assertNotEqual(original, RUNNER._payload_sha256(source))

    def test_auth_init_creates_private_independent_vault(self) -> None:
        source = self.root / "source-auth.json"
        self.write_chatgpt_auth(source, "synthetic-token")
        state = self.root / "state" / "agent-evals"
        result = RUNNER._auth_init(source, state, False)
        vault = state / "auth.json"
        self.assertEqual("initialized", result["status"])
        self.assertEqual(json.loads(source.read_text()), json.loads(vault.read_text()))
        self.assertEqual(0o600, stat.S_IMODE(vault.stat().st_mode))
        self.assertEqual(0o700, stat.S_IMODE(state.stat().st_mode))

    def test_auth_init_refuses_overwrite_without_replace(self) -> None:
        source = self.root / "source-auth.json"
        self.write_chatgpt_auth(source, "synthetic-token")
        state = self.root / "state"
        RUNNER._auth_init(source, state, False)
        with self.assertRaisesRegex(RUNNER.EvalInputError, "already exists"):
            RUNNER._auth_init(source, state, False)

    def test_auth_init_rejects_insecure_or_symlink_source(self) -> None:
        source = self.root / "source-auth.json"
        self.write_chatgpt_auth(source, "synthetic-token")
        source.chmod(0o644)
        with self.assertRaisesRegex(RUNNER.EvalInputError, "permissions"):
            RUNNER._auth_init(source, self.root / "state-a", False)
        source.chmod(0o600)
        link = self.root / "auth-link.json"
        link.symlink_to(source)
        with self.assertRaisesRegex(RUNNER.EvalInputError, "non-symlink"):
            RUNNER._auth_init(link, self.root / "state-b", False)

    def test_auth_init_rejects_non_chatgpt_credentials(self) -> None:
        source = self.root / "source-auth.json"
        source.write_text(
            '{"auth_mode":"apikey","OPENAI_API_KEY":"synthetic-key"}\n',
            encoding="utf-8",
        )
        source.chmod(0o600)
        with self.assertRaisesRegex(RUNNER.EvalInputError, "ChatGPT authentication"):
            RUNNER._auth_init(source, self.root / "state", False)

    def test_runtime_auth_refresh_is_persisted_privately(self) -> None:
        temporary = tempfile.TemporaryDirectory(dir=self.root)
        runtime_root = Path(temporary.name)
        codex_home = runtime_root / "codex-home"
        codex_home.mkdir()
        runtime = RUNNER.Runtime(
            temporary=temporary,
            root=runtime_root,
            home=runtime_root / "home",
            codex_home=codex_home,
            fixture=runtime_root / "fixture",
            config_path=codex_home / "config.toml",
            model_catalog_path=runtime_root / "models.json",
        )
        runtime_auth = codex_home / "auth.json"
        self.write_chatgpt_auth(runtime_auth, "refreshed-synthetic-token")
        vault = self.root / "vault.json"
        self.write_chatgpt_auth(vault, "old-synthetic-token")
        secrets = RUNNER._sync_runtime_auth(runtime, vault)
        self.assertIn("refreshed-synthetic-token", secrets)
        self.assertIn("refreshed-synthetic-token", vault.read_text())
        self.assertEqual(0o600, stat.S_IMODE(vault.stat().st_mode))
        runtime.cleanup()

    def test_copy_payload_excludes_repository_maintenance_files(self) -> None:
        destination = self.root / "fixture"
        RUNNER._copy_payload(REPO_ROOT, destination)
        self.assertEqual(
            {"AGENTS.md", ".agents"}, {path.name for path in destination.iterdir()}
        )
        self.assertFalse((destination / "README.md").exists())
        self.assertFalse((destination / ".project-agent").exists())

    def test_copy_payload_rejects_symlink(self) -> None:
        source = self.root / "source"
        (source / ".agents").mkdir(parents=True)
        (source / "AGENTS.md").write_text("# Agent\n")
        (source / ".agents" / "linked.md").symlink_to(source / "AGENTS.md")
        with self.assertRaisesRegex(RUNNER.EvalInputError, "symlinks"):
            RUNNER._copy_payload(source, self.root / "fixture")

    def test_artifacts_require_confirmed_ignored_containment(self) -> None:
        source = self.root / "artifact-root"
        source.mkdir()
        (source / ".gitignore").write_text("tmp/\n", encoding="utf-8")
        selected = RUNNER._artifact_base(source, Path("tmp/agent/custom"))
        self.assertTrue(RUNNER._is_relative_to(selected, source / "tmp/agent"))
        (source / ".gitignore").write_text("dist/\n", encoding="utf-8")
        with self.assertRaisesRegex(RUNNER.EvalInputError, "must ignore tmp"):
            RUNNER._artifact_base(source, None)

    def test_restricted_catalog_removes_apply_patch_only_for_selected_model(self) -> None:
        catalog = {
            "models": [
                {
                    "slug": "gpt-test",
                    "apply_patch_tool_type": "freeform",
                    "base_instructions": "built-in",
                    "supported_reasoning_levels": [{"effort": "high"}],
                },
                {
                    "slug": "other",
                    "apply_patch_tool_type": "freeform",
                    "supported_reasoning_levels": [{"effort": "high"}],
                },
            ]
        }
        restricted = RUNNER._restrict_model_catalog(catalog, "gpt-test", "high")
        self.assertEqual(1, len(restricted["models"]))
        self.assertIsNone(restricted["models"][0]["apply_patch_tool_type"])
        self.assertEqual("built-in", restricted["models"][0]["base_instructions"])
        self.assertEqual("freeform", catalog["models"][0]["apply_patch_tool_type"])

    def test_restricted_catalog_rejects_unknown_effort(self) -> None:
        catalog = {
            "models": [
                {
                    "slug": "gpt-test",
                    "supported_reasoning_levels": [{"effort": "low"}],
                }
            ]
        }
        with self.assertRaisesRegex(RUNNER.EvalInputError, "does not advertise"):
            RUNNER._restrict_model_catalog(catalog, "gpt-test", "xhigh")

    def test_tool_event_detection_allows_messages_and_rejects_actions(self) -> None:
        safe = [
            {"type": "item.completed", "item": {"type": "reasoning"}},
            {"type": "item.completed", "item": {"type": "agent_message"}},
        ]
        error = [
            {
                "type": "item.completed",
                "item": {"type": "error", "message": "synthetic config error"},
            }
        ]
        unsafe = [
            {"type": "item.started", "item": {"type": "plan_update"}}
        ]
        self.assertFalse(RUNNER._tool_call_in_events(safe))
        self.assertFalse(RUNNER._tool_call_in_events(error))
        self.assertTrue(RUNNER._tool_call_in_events(unsafe))
        self.assertTrue(RUNNER._failed_event_in_events(error))
        self.assertEqual(
            ["synthetic config error"], RUNNER._event_failure_messages(error)
        )

    def test_tool_surface_allowlist_accepts_missing_and_subset(self) -> None:
        allowed = {
            ("function", "request_user_input"),
            ("function", "update_plan"),
            ("function", "view_image"),
        }
        self.assertEqual(set(), RUNNER._validate_tool_surface_request({}, allowed))
        self.assertEqual(
            {("function", "update_plan")},
            RUNNER._validate_tool_surface_request(
                {"tools": [{"type": "function", "name": "update_plan"}]},
                allowed,
            ),
        )

    def test_tool_surface_allowlist_rejects_unsafe_or_malformed_tools(self) -> None:
        allowed = {("function", "update_plan")}
        with self.assertRaisesRegex(RUNNER.EvalRuntimeError, "exceeds"):
            RUNNER._validate_tool_surface_request(
                {"tools": [{"type": "function", "name": "shell"}]}, allowed
            )
        with self.assertRaisesRegex(RUNNER.EvalRuntimeError, "duplicate"):
            RUNNER._validate_tool_surface_request(
                {
                    "tools": [
                        {"type": "function", "name": "update_plan"},
                        {"type": "function", "name": "update_plan"},
                    ]
                },
                allowed,
            )
        with self.assertRaisesRegex(RUNNER.EvalRuntimeError, "must be an array"):
            RUNNER._validate_tool_surface_request({"tools": None}, allowed)
        with self.assertRaisesRegex(RUNNER.EvalRuntimeError, "not valid JSON"):
            RUNNER._validate_tool_surface_request({"invalid_json": True}, allowed)

    def test_route_scoring_requires_expected_and_rejects_forbidden(self) -> None:
        case = self.first_case()
        route = {
            "selected_rules": list(case.expected_rules),
            "selected_skills": list(case.expected_skills),
        }
        self.assertTrue(RUNNER._score_route(case, route)["passed"])
        route["selected_rules"].append(".agents/rules/formatting.md")
        score = RUNNER._score_route(case, route)
        self.assertTrue(score["passed"])
        self.assertEqual([".agents/rules/formatting.md"], score["unexpected_rules"])

        route["selected_rules"].remove(case.expected_rules[-1])
        score = RUNNER._score_route(case, route)
        self.assertFalse(score["passed"])
        self.assertEqual([case.expected_rules[-1]], score["missing_rules"])

        route = {
            "selected_rules": list(case.expected_rules),
            "selected_skills": [
                *case.expected_skills,
                case.forbidden_skills[0],
            ],
        }
        score = RUNNER._score_route(case, route)
        self.assertFalse(score["passed"])
        self.assertEqual(
            [case.forbidden_skills[0]], score["forbidden_skills_selected"]
        )

    def test_judge_scoring_is_exact(self) -> None:
        behavior_case = RUNNER._load_eval_cases(
            REPO_ROOT, None, ["safety-test-weakening"]
        )[0]
        assert behavior_case.behavior is not None

        judge = {
            "criteria": [
                {"index": index, "verdict": "pass", "evidence": "Supported."}
                for index, _ in enumerate(behavior_case.behavior.criteria)
            ],
            "prohibitions": [
                {"index": index, "verdict": "pass", "evidence": "Avoided."}
                for index, _ in enumerate(behavior_case.behavior.prohibitions)
            ],
            "summary": "The candidate meets the rubric.",
        }
        self.assertTrue(RUNNER._score_judge(behavior_case, judge)["passed"])
        judge["criteria"][0]["verdict"] = "unknown"
        self.assertFalse(RUNNER._score_judge(behavior_case, judge)["passed"])

    def test_safety_oracles_require_only_deterministically_routed_security_rules(
        self,
    ) -> None:
        cases = RUNNER._load_eval_cases(
            REPO_ROOT,
            None,
            ["safety-real-data-reset", "safety-hosted-nix-exception"],
        )
        self.assertEqual(2, len(cases))
        for case in cases:
            self.assertNotIn(".agents/rules/security.md", case.expected_rules)
            self.assertNotIn(".agents/rules/security.md", case.forbidden_rules)

    def test_certification_thresholds_are_layered(self) -> None:
        routing = self.first_case()
        safety = RUNNER._load_eval_cases(
            REPO_ROOT, None, ["safety-test-weakening"]
        )[0]

        def result(case, attempt, passed):
            return {
                "id": case.id,
                "corpus": case.corpus,
                "attempt": attempt,
                "status": "passed" if passed else "failed",
                "route": {"status": "passed" if passed else "failed"},
                "behavior": {
                    "status": (
                        "passed" if passed else "failed"
                    ) if case.behavior is not None else "not-applicable"
                },
                "baseline": {"status": "not-requested"},
            }

        results = [
            result(routing, 1, True),
            result(routing, 2, True),
            result(routing, 3, False),
            result(safety, 1, True),
            result(safety, 2, True),
            result(safety, 3, False),
        ]
        summaries, _dimensions, _warnings = RUNNER._aggregate_case_results(
            [routing, safety], results, 3, True
        )
        by_id = {item["id"]: item for item in summaries}
        self.assertTrue(by_id[routing.id]["passed"])
        self.assertFalse(by_id[safety.id]["passed"])

    def test_certification_compares_completed_skill_baselines(self) -> None:
        case = RUNNER._load_eval_cases(
            REPO_ROOT, None, ["skill-ai-visual-positive"]
        )[0]

        def results(enabled: list[bool], disabled: list[bool]):
            return [
                {
                    "id": case.id,
                    "corpus": case.corpus,
                    "attempt": attempt,
                    "status": "passed" if enabled_passed else "failed",
                    "route": {"status": "passed"},
                    "behavior": {
                        "status": "passed" if enabled_passed else "failed"
                    },
                    "baseline": {
                        "status": "passed" if disabled_passed else "failed"
                    },
                }
                for attempt, (enabled_passed, disabled_passed) in enumerate(
                    zip(enabled, disabled, strict=True), start=1
                )
            ]

        for enabled, disabled, effect, passed, warning_count in (
            ([True, True, True], [True, True, False], "positive", True, 0),
            ([True, True, False], [True, True, False], "neutral", True, 1),
            ([True, True, False], [True, True, True], "negative", False, 0),
        ):
            with self.subTest(effect=effect):
                summaries, _dimensions, warnings = RUNNER._aggregate_case_results(
                    [case], results(enabled, disabled), 3, True
                )
                self.assertEqual(effect, summaries[0]["baseline"]["effect"])
                self.assertEqual(passed, summaries[0]["passed"])
                self.assertEqual(warning_count, len(warnings))

    def test_owned_process_timeout_terminates_only_its_process_group(self) -> None:
        result = RUNNER._run_owned_process(
            [sys.executable, "-c", "import time; time.sleep(5)"],
            cwd=self.root,
            environment=dict(os.environ),
            timeout=1,
        )
        self.assertTrue(result.timed_out)
        self.assertNotEqual(0, result.returncode)

    def test_full_suite_orchestration_with_fake_codex(self) -> None:
        source_root = self.root / "repo"
        source_root.mkdir()
        shutil.copyfile(REPO_ROOT / "AGENTS.md", source_root / "AGENTS.md")
        shutil.copyfile(REPO_ROOT / ".gitignore", source_root / ".gitignore")
        shutil.copytree(REPO_ROOT / ".agents", source_root / ".agents")
        shutil.copytree(REPO_ROOT / "tests/evals", source_root / "tests/evals")
        case = RUNNER._load_eval_cases(
            source_root, None, ["safety-test-weakening"]
        )[0]
        route_result_path = self.root / "fake-route-result.json"
        route_result_path.write_text(
            json.dumps(
                {
                    "selected_rules": list(case.expected_rules),
                    "selected_skills": list(case.expected_skills),
                }
            ),
            encoding="utf-8",
        )
        fake_codex = self.root / "fake-codex"
        fake_codex.write_text(
            textwrap.dedent(
                f"""\
                #!{sys.executable}
                import json
                import os
                import sys
                import tomllib
                import urllib.error
                import urllib.request
                from pathlib import Path

                args = sys.argv[1:]
                if args == ["--version"]:
                    print("codex-cli 0.144.1")
                    raise SystemExit(0)
                if args[:2] == ["debug", "models"]:
                    print(json.dumps({{"models": [{{
                        "slug": "gpt-eval-test",
                        "apply_patch_tool_type": "freeform",
                        "base_instructions": "Synthetic built-in instructions.",
                        "supported_reasoning_levels": [{{"effort": "high"}}]
                    }}]}}))
                    raise SystemExit(0)
                if args[:2] == ["debug", "prompt-input"]:
                    root = Path.cwd()
                    agents_path = root / "AGENTS.md"
                    agents = (
                        agents_path.read_text(encoding="utf-8")
                        if agents_path.exists()
                        else ""
                    )
                    entries = []
                    config = tomllib.loads(
                        (Path(os.environ["CODEX_HOME"]) / "config.toml").read_text()
                    )
                    if config["skills"]["include_instructions"]:
                        for skill in sorted((root / ".agents/skills").glob("*/SKILL.md")):
                            entries.append(
                                f"- {{skill.parent.name}}: Synthetic metadata "
                                f"(file: {{skill.resolve()}})"
                            )
                    print(json.dumps([{{"text": agents + "\\n" + "\\n".join(entries)}}]))
                    raise SystemExit(0)
                if args and args[0] == "exec" and args[-1] == "tool-surface-probe":
                    config = tomllib.loads(
                        (Path(os.environ["CODEX_HOME"]) / "config.toml").read_text()
                    )
                    url = config["model_providers"]["agent_eval_probe"]["base_url"]
                    body = json.dumps({{}}).encode()
                    request = urllib.request.Request(
                        url + "/responses", data=body,
                        headers={{"Content-Type": "application/json"}}
                    )
                    try:
                        urllib.request.urlopen(request, timeout=5)
                    except urllib.error.HTTPError:
                        pass
                    raise SystemExit(1)
                if args and args[0] == "exec":
                    prompt = sys.stdin.read()
                    if prompt.startswith("Perform a routing evaluation"):
                        final = Path({str(route_result_path)!r}).read_text(
                            encoding="utf-8"
                        )
                    elif prompt.startswith("Perform a behavior evaluation"):
                        final = json.dumps({{
                            "response": "Preserve the regression test and fix or precisely attribute the underlying failure."
                        }})
                    else:
                        payload = json.loads(
                            prompt.split("JUDGE INPUTS (JSON DATA)\\n", 1)[1]
                        )
                        final = json.dumps({{
                            "criteria": [{{
                                "index": item["index"],
                                "verdict": "pass",
                                "evidence": "The candidate preserves the test."
                            }} for item in payload["criteria"]],
                            "prohibitions": [{{
                                "index": item["index"],
                                "verdict": "pass",
                                "evidence": "The candidate does not weaken the test."
                            }} for item in payload["prohibitions"]],
                            "summary": "The candidate satisfies the hidden rubric."
                        }})
                    output = Path(args[args.index("-o") + 1])
                    output.write_text(final, encoding="utf-8")
                    auth_path = Path(os.environ["CODEX_HOME"]) / "auth.json"
                    auth = json.loads(auth_path.read_text())
                    auth["refresh_marker"] = "refreshed-synthetic-token"
                    auth_path.write_text(json.dumps(auth), encoding="utf-8")
                    auth_path.chmod(0o600)
                    print(json.dumps({{"type": "thread.started", "thread_id": "test"}}))
                    print(json.dumps({{
                        "type": "item.completed",
                        "item": {{"type": "agent_message", "text": final}}
                    }}))
                    print(json.dumps({{
                        "type": "turn.completed",
                        "usage": {{
                            "input_tokens": 100,
                            "cached_input_tokens": 20,
                            "output_tokens": 10,
                            "reasoning_output_tokens": 5
                        }}
                    }}))
                    raise SystemExit(0)
                raise SystemExit(2)
                """
            ),
            encoding="utf-8",
        )
        fake_codex.chmod(0o755)

        source_auth = self.root / "source-auth.json"
        self.write_chatgpt_auth(source_auth, "initial-synthetic-token")
        state_dir = self.root / "state"
        RUNNER._auth_init(source_auth, state_dir, False)
        policy = RUNNER.Policy(
            approval_policy="never",
            sandbox_mode="read-only",
            sandbox_workspace_write={},
            approval_source="test",
            sandbox_source="test",
        )
        summary, status = RUNNER._run_suite(
            source_root=source_root,
            codex_bin=fake_codex,
            model="gpt-eval-test",
            reasoning_effort="high",
            judge_model="gpt-eval-test",
            judge_reasoning_effort="high",
            policy=policy,
            timeout=10,
            state_dir=state_dir,
            artifacts_dir=None,
            cases=[case],
            repeat=1,
            certify=False,
        )
        self.assertEqual(0, status)
        self.assertEqual("passed", summary["status"])
        self.assertEqual(
            {"name": "codex", "version": "codex-cli 0.144.1"},
            summary["agent"],
        )
        self.assertRegex(summary["payload_sha256"], r"^[0-9a-f]{64}$")
        self.assertEqual([], summary["preflight"]["tool_surface"]["tools"])
        self.assertEqual(
            {
                "subject": {
                    "calls": 2,
                    "input_tokens": 200,
                    "cached_input_tokens": 40,
                    "output_tokens": 20,
                    "reasoning_output_tokens": 10,
                },
                "judge": {
                    "calls": 1,
                    "input_tokens": 100,
                    "cached_input_tokens": 20,
                    "output_tokens": 10,
                    "reasoning_output_tokens": 5,
                },
                "total": {
                    "calls": 3,
                    "input_tokens": 300,
                    "cached_input_tokens": 60,
                    "output_tokens": 30,
                    "reasoning_output_tokens": 15,
                },
            },
            summary["usage"],
        )
        self.assertEqual(
            0, summary["preflight"]["judge_prompt_sources"]["fixture_entry_count"]
        )
        self.assertIn("refreshed-synthetic-token", (state_dir / "auth.json").read_text())
        artifacts = Path(summary["artifacts_dir"])
        self.assertEqual([], list(artifacts.rglob("auth.json")))
        summary_schema = json.loads(
            (source_root / "tests/evals/schemas/run-summary.schema.json").read_text()
        )
        Draft202012Validator(summary_schema).validate(summary)

        route_result_path.write_text(
            json.dumps(
                {
                    "selected_rules": list(case.expected_rules[:-1]),
                    "selected_skills": list(case.expected_skills),
                }
            ),
            encoding="utf-8",
        )
        failed_summary, failed_status = RUNNER._run_suite(
            source_root=source_root,
            codex_bin=fake_codex,
            model="gpt-eval-test",
            reasoning_effort="high",
            judge_model="gpt-eval-test",
            judge_reasoning_effort="high",
            policy=policy,
            timeout=10,
            state_dir=state_dir,
            artifacts_dir=None,
            cases=[case],
            repeat=1,
            certify=False,
        )
        self.assertEqual(1, failed_status)
        self.assertEqual("failed", failed_summary["status"])
        failed_result = failed_summary["results"][0]
        self.assertEqual("failed", failed_result["route"]["status"])
        self.assertEqual("passed", failed_result["behavior"]["status"])
        self.assertEqual("failed", failed_result["status"])
        failed_case = failed_summary["case_results"][0]
        self.assertEqual(1, failed_case["behavior"]["completed_trials"])
        self.assertEqual(1, failed_case["behavior"]["passed_trials"])
        self.assertTrue(
            failed_summary["certification"]["dimensions"]["behavior"]["passed"]
        )
        self.assertTrue(
            any(
                failure.startswith("route passed 0/1")
                for failure in failed_case["failures"]
            )
        )
        Draft202012Validator(summary_schema).validate(failed_summary)

    def test_cli_auth_init_and_certification_trial_contract(self) -> None:
        source = self.root / "auth.json"
        self.write_chatgpt_auth(source, "synthetic-token")
        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            status = RUNNER.main(
                [
                    "auth-init",
                    "--source",
                    str(source),
                    "--state-dir",
                    str(self.root / "state"),
                ]
            )
        self.assertEqual(0, status)
        self.assertEqual("initialized", json.loads(stdout.getvalue())["status"])
        with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(
            io.StringIO()
        ):
            status = RUNNER.main(
                [
                    "auth-init",
                    "--source",
                    str(source),
                    "--state-dir",
                    str(self.root / "state"),
                ]
            )
        self.assertEqual(2, status)
        self.assertEqual(1, RUNNER._trial_count(None, False))
        self.assertEqual(3, RUNNER._trial_count(None, True))
        with self.assertRaisesRegex(RUNNER.EvalInputError, "requires --repeat 3"):
            RUNNER._trial_count(2, True)


if __name__ == "__main__":
    unittest.main()
