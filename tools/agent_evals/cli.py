"""Run isolated Codex routing, behavior, and certification evaluations."""

from __future__ import annotations

import argparse
import json
import os
import sys
from collections.abc import Sequence
from pathlib import Path

from .auth import _auth_init
from .cases import _load_eval_cases
from .common import (
    APPROVAL_POLICIES,
    EVAL_FILES,
    REASONING_EFFORTS,
    SANDBOX_MODES,
    EvalInputError,
    EvalRuntimeError,
    _positive_int,
    _trial_count,
)
from .isolation import _resolve_codex_binary
from .preflight import _perform_preflight
from .runtime import _load_policy
from .suite import _run_suite


def _default_state_dir() -> Path:
    value = os.environ.get("XDG_STATE_HOME")
    if value:
        return Path(value) / "agents-misc" / "agent-evals"
    return Path.home() / ".local" / "state" / "agents-misc" / "agent-evals"


def _add_runtime_arguments(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--root", type=Path, default=Path.cwd(), help="repository root")
    parser.add_argument("--codex-bin", default="codex", help="Codex executable")
    parser.add_argument("--model", required=True, help="model passed to codex exec")
    parser.add_argument(
        "--reasoning-effort",
        choices=REASONING_EFFORTS,
        default="high",
        help="model reasoning effort (default: high)",
    )
    parser.add_argument(
        "--judge-model",
        help="independent behavior judge model (default: subject model)",
    )
    parser.add_argument(
        "--judge-reasoning-effort",
        choices=REASONING_EFFORTS,
        help="judge reasoning effort (default: subject reasoning effort)",
    )
    parser.add_argument(
        "--approval-policy",
        choices=APPROVAL_POLICIES,
        default="inherit",
        help="approval policy; inherit reads only this field from --policy-config",
    )
    parser.add_argument(
        "--sandbox-mode",
        choices=SANDBOX_MODES,
        default="inherit",
        help="sandbox policy; inherit reads only sandbox fields from --policy-config",
    )
    parser.add_argument(
        "--policy-config",
        type=Path,
        default=Path.home() / ".codex" / "config.toml",
        help="source for selectively inherited approval/sandbox policy",
    )
    parser.add_argument(
        "--timeout",
        type=_positive_int,
        default=300,
        help="per-stage timeout in seconds (default: 300)",
    )
    parser.add_argument(
        "--corpus",
        action="append",
        choices=tuple(Path(name).stem for name in EVAL_FILES),
        help="limit to a corpus; repeat to select multiple",
    )
    parser.add_argument("--id", action="append", help="limit to an eval id; repeatable")


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="agent-evals", description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)

    auth = subparsers.add_parser(
        "auth-init", help="seed the independent ChatGPT auth vault"
    )
    auth.add_argument(
        "--source",
        type=Path,
        default=Path.home() / ".codex" / "auth.json",
        help="private ChatGPT auth.json source",
    )
    auth.add_argument("--state-dir", type=Path, default=_default_state_dir())
    auth.add_argument(
        "--replace", action="store_true", help="replace an existing independent vault"
    )

    preflight = subparsers.add_parser(
        "preflight", help="verify prompt sources and the no-execution-tool surface"
    )
    _add_runtime_arguments(preflight)

    run = subparsers.add_parser("run", help="run the isolated Codex eval suite")
    _add_runtime_arguments(run)
    run.add_argument("--state-dir", type=Path, default=_default_state_dir())
    run.add_argument(
        "--artifacts-dir",
        type=Path,
        help="ignored output directory under <root>/tmp/agent (default: tmp/agent/evals)",
    )
    run.add_argument(
        "--repeat",
        type=_positive_int,
        help="trial count (default: 1, or 3 with --certify)",
    )
    run.add_argument(
        "--certify",
        action="store_true",
        help="apply the reviewed per-case thresholds; requires at least 3 trials",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    parser = _build_parser()
    args = parser.parse_args(argv)
    try:
        if args.command == "auth-init":
            result = _auth_init(args.source, args.state_dir, args.replace)
            print(json.dumps(result, ensure_ascii=False, sort_keys=True))
            return 0

        source_root = args.root.resolve()
        codex_bin = _resolve_codex_binary(args.codex_bin)
        policy = _load_policy(
            args.policy_config, args.approval_policy, args.sandbox_mode
        )
        judge_model = args.judge_model or args.model
        judge_reasoning_effort = args.judge_reasoning_effort or args.reasoning_effort
        cases = _load_eval_cases(source_root, args.corpus, args.id)
        if args.command == "preflight":
            result = _perform_preflight(
                source_root=source_root,
                codex_bin=codex_bin,
                model=args.model,
                reasoning_effort=args.reasoning_effort,
                judge_model=judge_model,
                judge_reasoning_effort=judge_reasoning_effort,
                policy=policy,
                timeout=args.timeout,
                cases=cases,
            )
            print(json.dumps(result, ensure_ascii=False, sort_keys=True))
            return 0
        repeat = _trial_count(args.repeat, args.certify)
        summary, status = _run_suite(
            source_root=source_root,
            codex_bin=codex_bin,
            model=args.model,
            reasoning_effort=args.reasoning_effort,
            judge_model=judge_model,
            judge_reasoning_effort=judge_reasoning_effort,
            policy=policy,
            timeout=args.timeout,
            state_dir=args.state_dir,
            artifacts_dir=args.artifacts_dir,
            cases=cases,
            repeat=repeat,
            certify=args.certify,
        )
        print(json.dumps(summary, ensure_ascii=False, sort_keys=True))
        return status
    except EvalInputError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2
    except EvalRuntimeError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    except OSError as exc:
        print(f"error: runtime I/O failure: {exc}", file=sys.stderr)
        return 1
