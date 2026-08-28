#!/usr/bin/env python3
"""Run a Cross-File A/B plan through the production Ubuntu execution path.

This is the deployable entry point for remote benchmark jobs.  It deliberately
owns no quality state, guard calls, or grader parsing: the benchmark runner
drives :class:`FiveRoundRunner`, while the Ubuntu local-image provider performs
explicit container/Codex/SWE-bench execution.  Evidence replay is not an
accepted mode for this launcher.
"""

from __future__ import annotations

import argparse
import json
import pathlib
import sys
from collections.abc import Sequence

try:
    from . import cross_file_benchmark_runner as benchmark_runner
    from .cross_file_benchmark_runner import execute_plan
    from .cross_file_ubuntu_local_provider import (
        MIN_VALIDATION_QUESTION_COUNT,
        load_local_image_provider,
        load_question_image_manifest,
    )
except ImportError:  # pragma: no cover - direct ``python scripts/...`` use.
    import cross_file_benchmark_runner as benchmark_runner  # type: ignore[no-redef]
    from cross_file_benchmark_runner import execute_plan  # type: ignore[no-redef]
    from cross_file_ubuntu_local_provider import (  # type: ignore[no-redef]
        MIN_VALIDATION_QUESTION_COUNT,
        load_local_image_provider,
        load_question_image_manifest,
    )


# The original representative run had 20 questions.  Validation image sets
# may now contain more questions (for example the 100-question multilingual
# representative set), while retaining the original minimum size for a
# production run.
MIN_PRODUCTION_QUESTION_COUNT = MIN_VALIDATION_QUESTION_COUNT
# Backward-compatible name for integrations that imported the old constant.
PRODUCTION_QUESTION_COUNT = MIN_PRODUCTION_QUESTION_COUNT
PRODUCTION_ARM_COUNT = 40
PRODUCTION_GRADER_SLOTS = 8


def _read_plan(path: str | pathlib.Path) -> object:
    candidate = pathlib.Path(path)
    try:
        return json.loads(candidate.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise RuntimeError(f"read benchmark plan {candidate}: {exc}") from exc


def run_remote_plan(
    plan_path: str | pathlib.Path,
    *,
    state_dir: str | pathlib.Path,
    driver_config_path: str | pathlib.Path,
    image_manifest_path: str | pathlib.Path,
) -> benchmark_runner.BenchmarkExecution:
    """Execute a production validation-image plan through local Ubuntu Docker."""

    plan = _read_plan(plan_path)
    if not isinstance(plan, dict):
        raise RuntimeError("production benchmark plan must be an object")
    questions = plan.get("questions")
    if (
        not isinstance(questions, list)
        or len(questions) < MIN_PRODUCTION_QUESTION_COUNT
    ):
        raise RuntimeError(
            "production benchmark requires exactly 20 questions or a larger "
            "validation image set"
        )
    if plan.get("max_concurrency") != PRODUCTION_ARM_COUNT:
        raise RuntimeError("production benchmark requires max_concurrency=40")
    images = load_question_image_manifest(image_manifest_path)
    question_ids: list[str] = []
    for index, question in enumerate(questions):
        if not isinstance(question, dict) or not isinstance(question.get("question_id"), str):
            raise RuntimeError(f"question plan question[{index}] must contain a question_id")
        question_ids.append(question["question_id"])
    if len(set(question_ids)) != len(question_ids):
        raise RuntimeError("question plan must not contain duplicate question IDs")
    if set(question_ids) != set(images):
        raise RuntimeError(
            "question plan and validation image manifest must have the same question IDs"
        )
    driver = load_local_image_provider(driver_config_path, image_manifest_path)
    if driver.config.grader_slots != PRODUCTION_GRADER_SLOTS:
        raise RuntimeError("production benchmark requires exactly 8 grader slots")
    return execute_plan(plan, state_dir=state_dir, driver=driver)


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", required=True, help="benchmark runner plan JSON")
    parser.add_argument("--state-dir", required=True, help="durable per-arm state directory")
    parser.add_argument(
        "--driver-config",
        required=True,
        help="explicit Ubuntu execution-driver JSON configuration",
    )
    parser.add_argument(
        "--image-manifest",
        required=True,
        help="strict immutable validation image manifest (20-question baseline or larger)",
    )
    parser.add_argument("--output", help="result JSON path")
    args = parser.parse_args(argv)
    try:
        execution = run_remote_plan(
            args.plan,
            state_dir=args.state_dir,
            driver_config_path=args.driver_config,
            image_manifest_path=args.image_manifest,
        )
        rendered = (
            execution.audit_result.json()
            if execution.scoreable and execution.audit_result is not None
            else json.dumps(execution.to_dict(), ensure_ascii=False, sort_keys=True, indent=2) + "\n"
        )
        if args.output:
            pathlib.Path(args.output).write_text(rendered, encoding="utf-8")
        else:
            sys.stdout.write(rendered)
        return 0 if execution.scoreable else 3
    except Exception as exc:  # noqa: BLE001 - CLI must fail closed.
        print(f"cross-file remote launcher failed: {type(exc).__name__}: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())


__all__ = ["main", "run_remote_plan"]
