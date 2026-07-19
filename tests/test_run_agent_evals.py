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
        schema = json.loads(
            (REPO_ROOT / "tests/evals/schemas/eval-record.schema.json").read_text(
                encoding="utf-8"
            )
        )
        validator = Draft202012Validator(schema)
        count = 0
        for path in sorted((REPO_ROOT / "tests/evals").glob("*.jsonl")):
            for line in path.read_text(encoding="utf-8").splitlines():
                validator.validate(json.loads(line))
                count += 1
        self.assertEqual(59, count)

    def test_all_cases_have_balanced_behavior_checks(self) -> None:
        cases = RUNNER._load_eval_cases(REPO_ROOT, None, None)
        self.assertEqual(59, len(cases))
        for case in cases:
            self.assertEqual(
                {False, True}, {check["expected"] for check in case.behavior_checks}
            )

    def test_eval_selection_rejects_unknown_id(self) -> None:
        with self.assertRaisesRegex(RUNNER.EvalInputError, "unknown or filtered"):
            RUNNER._load_eval_cases(REPO_ROOT, None, ["missing-eval"])

    def test_prompts_do_not_serialize_expectations(self) -> None:
        case = self.first_case()
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
        self.assertNotIn(case.expected_behavior, route_prompt)
        self.assertNotIn('"expected":', behavior_prompt)
        self.assertTrue(
            all(
                case.expected_behavior not in check["question"]
                for check in case.behavior_checks
            )
        )

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

    def test_route_and_behavior_scoring_are_exact(self) -> None:
        case = self.first_case()
        route = {
            "selected_rules": list(case.expected_rules),
            "selected_skills": list(case.expected_skills),
            "rationale": "The index routes match.",
        }
        self.assertTrue(RUNNER._score_route(case, route)["passed"])
        route["selected_rules"].append(".agents/rules/formatting.md")
        self.assertFalse(RUNNER._score_route(case, route)["passed"])

        behavior = {
            "decisions": [
                {
                    "id": check["id"],
                    "answer": check["expected"],
                    "evidence": "A supplied rule supports this decision.",
                }
                for check in case.behavior_checks
            ],
            "summary": "All decisions are grounded.",
        }
        self.assertTrue(RUNNER._score_behavior(case, behavior)["passed"])
        behavior["decisions"][0]["answer"] = not behavior["decisions"][0]["answer"]
        self.assertFalse(RUNNER._score_behavior(case, behavior)["passed"])

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
            source_root, None, ["routing-greenfield-fullstack"]
        )[0]
        route_result = json.dumps(
            {
                "selected_rules": list(case.expected_rules),
                "selected_skills": list(case.expected_skills),
                "rationale": "Synthetic route evidence.",
            }
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
                    agents = (root / "AGENTS.md").read_text(encoding="utf-8")
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
                        final = {route_result!r}
                    else:
                        payload = json.loads(
                            prompt.split("BEHAVIOR INPUTS (JSON DATA)\\n", 1)[1]
                        )
                        decisions = []
                        for question in payload["questions"]:
                            decisions.append({{
                                "id": question["id"],
                                "answer": question["id"] == "required-behavior",
                                "evidence": "Synthetic supplied-source evidence."
                            }})
                        final = json.dumps({{
                            "decisions": decisions,
                            "summary": "Synthetic behavior evidence."
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
                    print(json.dumps({{"type": "turn.completed", "usage": {{}}}}))
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
            policy=policy,
            timeout=10,
            state_dir=state_dir,
            artifacts_dir=None,
            cases=[case],
            repeat=1,
        )
        self.assertEqual(0, status)
        self.assertEqual("passed", summary["status"])
        self.assertEqual([], summary["preflight"]["tool_surface"]["tools"])
        self.assertIn("refreshed-synthetic-token", (state_dir / "auth.json").read_text())
        artifacts = Path(summary["artifacts_dir"])
        self.assertEqual([], list(artifacts.rglob("auth.json")))
        summary_schema = json.loads(
            (source_root / "tests/evals/schemas/run-summary.schema.json").read_text()
        )
        Draft202012Validator(summary_schema).validate(summary)

    def test_cli_auth_init_has_json_stdout_and_usage_exit_for_repeat(self) -> None:
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


if __name__ == "__main__":
    unittest.main()
