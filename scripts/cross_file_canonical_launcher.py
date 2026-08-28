#!/usr/bin/env python3
"""Execute the canonical multi-question Cross-File evidence audit.

This is intentionally a thin production entry point.  It accepts only the
evidence-adapter envelope and delegates all validation and scoring to
``cross_file_evidence_adapter.audit_evidence_set``.  It never imports or
invokes the lower-level audit module directly, so a successful stdout payload
is always the adapter-bound canonical audit JSON.
"""

from __future__ import annotations

import argparse
import json
import pathlib
import sys
from collections.abc import Sequence

if __package__ in {None, ""}:
    # Direct ``python scripts/...`` execution does not put the repository
    # root on sys.path; import the adapter as the package so its strict
    # sibling bindings resolve identically to library callers.
    sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))
    from scripts import cross_file_evidence_adapter as evidence_adapter
    from scripts import cross_file_benchmark_runner as benchmark_runner
    from scripts.cross_file_ubuntu_local_provider import (
        MIN_VALIDATION_QUESTION_COUNT,
        load_local_image_provider,
        load_question_image_manifest,
    )
else:
    from . import cross_file_evidence_adapter as evidence_adapter
    from . import cross_file_benchmark_runner as benchmark_runner
    from .cross_file_ubuntu_local_provider import (
        MIN_VALIDATION_QUESTION_COUNT,
        load_local_image_provider,
        load_question_image_manifest,
    )


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, help="multi-question evidence-adapter JSON path")
    parser.add_argument("--output", help="optional path for the single canonical audit JSON")
    parser.add_argument(
        "--mode",
        choices=("audit", "execute"),
        default="audit",
        help="audit an evidence envelope, or execute a live runner plan",
    )
    parser.add_argument("--state-dir", help="durable state directory required for execute")
    parser.add_argument("--driver-config", help="explicit Ubuntu driver config required for execute")
    parser.add_argument(
        "--image-manifest",
        help="strict immutable validation image manifest required for execute",
    )
    args = parser.parse_args(argv)
    try:
        if args.mode == "audit":
            if args.state_dir or args.driver_config or args.image_manifest:
                raise ValueError(
                    "--state-dir/--driver-config/--image-manifest are only valid with --mode execute"
                )
            result = evidence_adapter.audit_evidence_set(args.input)
            rendered = result.json()
            successful = True
        else:
            if not args.state_dir or not args.driver_config:
                raise ValueError("--mode execute requires --state-dir and --driver-config")
            if not args.image_manifest:
                raise ValueError("--mode execute requires --image-manifest")
            plan = benchmark_runner._read_json(args.input, "benchmark run plan")
            if not isinstance(plan, dict):
                raise ValueError("production benchmark plan must be an object")
            questions = plan.get("questions")
            if (
                not isinstance(questions, list)
                or len(questions) < MIN_VALIDATION_QUESTION_COUNT
            ):
                raise ValueError(
                    "production benchmark requires exactly 20 questions or a larger "
                    "validation image set"
                )
            if plan.get("max_concurrency") != 40:
                raise ValueError("production benchmark requires max_concurrency=40")
            images = load_question_image_manifest(args.image_manifest)
            ids: list[str] = []
            for index, question in enumerate(questions):
                if not isinstance(question, dict) or not isinstance(question.get("question_id"), str):
                    raise ValueError(f"question plan question[{index}] must contain a question_id")
                ids.append(question["question_id"])
            if len(set(ids)) != len(ids):
                raise ValueError("question plan must not contain duplicate question IDs")
            if set(ids) != set(images):
                raise ValueError(
                    "question plan and validation image manifest must have the same question IDs"
                )
            driver = load_local_image_provider(args.driver_config, args.image_manifest)
            if driver.config.grader_slots != 8:
                raise ValueError("production benchmark requires exactly 8 grader slots")
            execution = benchmark_runner.execute_plan(
                plan,
                state_dir=args.state_dir,
                driver=driver,
            )
            rendered = (
                execution.audit_result.json()
                if execution.scoreable and execution.audit_result is not None
                else json.dumps(execution.to_dict(), ensure_ascii=False, sort_keys=True, indent=2) + "\n"
            )
            successful = execution.scoreable
        if args.output:
            pathlib.Path(args.output).write_text(rendered, encoding="utf-8")
        else:
            sys.stdout.write(rendered)
        return 0 if successful else 3
    except Exception as exc:  # noqa: BLE001 - CLI must convert every audit failure to non-zero.
        print(f"canonical evidence audit failed: {type(exc).__name__}: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
