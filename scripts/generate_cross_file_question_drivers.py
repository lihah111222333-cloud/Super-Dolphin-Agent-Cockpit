#!/usr/bin/env python3
"""Generate strict per-question driver configurations for local Ubuntu Docker."""

from __future__ import annotations

import argparse
import copy
import json
import pathlib
import tempfile
from collections.abc import Mapping, Sequence
from typing import Any

try:
    from .cross_file_ubuntu_execution_driver import UbuntuExecutionDriverConfig
    from .cross_file_ubuntu_local_provider import (
        MIN_VALIDATION_QUESTION_COUNT,
        REQUIRED_GRADER_SLOTS,
        UbuntuLocalProviderError,
        parse_question_image_manifest,
    )
except ImportError:  # pragma: no cover - direct script invocation compatibility.
    from cross_file_ubuntu_execution_driver import UbuntuExecutionDriverConfig  # type: ignore[no-redef]
    from cross_file_ubuntu_local_provider import (  # type: ignore[no-redef]
        MIN_VALIDATION_QUESTION_COUNT,
        REQUIRED_GRADER_SLOTS,
        UbuntuLocalProviderError,
        parse_question_image_manifest,
    )


QUESTION_DRIVER_SCHEMA = "super-dolphin/cross-file-question-drivers/v1"
# Preserve the historical export while making the new minimum terminology
# explicit for callers building larger validation sets.
EXPECTED_QUESTION_COUNT = MIN_VALIDATION_QUESTION_COUNT


def _object(value: Any, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise UbuntuLocalProviderError(f"{label} must be an object")
    return value


def _text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise UbuntuLocalProviderError(f"{label} must be a non-empty string")
    return value.strip()


def _strict(value: Mapping[str, Any], allowed: set[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise UbuntuLocalProviderError(f"{label} has unknown field(s): {', '.join(unknown)}")


def _docker_run_command(command: Sequence[str]) -> list[str]:
    """Validate a provider command and reject every pull-capable form."""

    rendered = [_text(item, f"container_command[{index}]") for index, item in enumerate(command)]
    if not rendered:
        raise UbuntuLocalProviderError("container_command must not be empty")
    if pathlib.Path(rendered[0]).name != "docker":
        raise UbuntuLocalProviderError("container_command must invoke docker")
    if "run" not in rendered[1:]:
        raise UbuntuLocalProviderError("container_command must invoke docker run")
    if "--pull=never" not in rendered:
        raise UbuntuLocalProviderError("container_command must include --pull=never")
    if any(token == "pull" or token.startswith("pull=") or token.startswith("--pull=") and token != "--pull=never" for token in rendered):
        raise UbuntuLocalProviderError("container_command contains a Docker pull operation")
    if "{image}" not in rendered:
        raise UbuntuLocalProviderError("container_command must contain the {image} placeholder")
    return rendered


def generate_question_driver_configs(
    template: Mapping[str, Any],
    image_manifest: Mapping[str, Any],
    *,
    artifact_root: str | pathlib.Path | None = None,
    container_command: Sequence[str] = ("docker", "run", "--rm", "--pull=never", "{image}"),
) -> dict[str, Any]:
    """Generate a loadable map for the complete validation image set."""

    raw_template = copy.deepcopy(dict(_object(template, "driver template")))
    images = parse_question_image_manifest(image_manifest)
    if len(images) < MIN_VALIDATION_QUESTION_COUNT:
        raise UbuntuLocalProviderError(
            "driver generation requires exactly 20 question images or a larger "
            "validation image set"
        )
    # Parse before replacing the command so all other fields retain the driver's
    # fail-fast schema and canonical prompt checks.
    parsed_template = UbuntuExecutionDriverConfig.from_dict(raw_template)
    if parsed_template.grader_slots != REQUIRED_GRADER_SLOTS:
        raise UbuntuLocalProviderError(
            f"driver generation requires exactly {REQUIRED_GRADER_SLOTS} grader slots"
        )
    command = _docker_run_command(container_command)
    configured_root = pathlib.Path(
        artifact_root if artifact_root is not None else _text(raw_template["artifact_dir"], "artifact_dir")
    )
    if not configured_root.is_absolute():
        raise UbuntuLocalProviderError("artifact_root must be an absolute path")
    question_configs: dict[str, dict[str, Any]] = {}
    for question_id, image in images.items():
        config = copy.deepcopy(raw_template)
        config["container_command"] = list(command)
        config["container_image"] = image
        config["artifact_dir"] = str(configured_root / "questions" / question_id)
        # Re-parse each generated object: output is never allowed to contain a
        # template that only works before question substitution.
        UbuntuExecutionDriverConfig.from_dict(config)
        question_configs[question_id] = config
    return {"schema": QUESTION_DRIVER_SCHEMA, "question_configs": question_configs}


def load_json(path: str | pathlib.Path, label: str) -> Mapping[str, Any]:
    """Read one JSON object for the CLI without accepting implicit defaults."""

    candidate = pathlib.Path(path)
    try:
        value = json.loads(candidate.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise UbuntuLocalProviderError(f"read {label} {candidate}: {exc}") from exc
    return _object(value, label)


def write_json(path: str | pathlib.Path, value: Mapping[str, Any]) -> None:
    """Atomically write generated configuration JSON."""

    candidate = pathlib.Path(path)
    candidate.parent.mkdir(parents=True, exist_ok=True)
    payload = (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode("utf-8")
    temporary: pathlib.Path | None = None
    try:
        with tempfile.NamedTemporaryFile(mode="wb", dir=candidate.parent, prefix=f".{candidate.name}.", delete=False) as handle:
            temporary = pathlib.Path(handle.name)
            handle.write(payload)
            handle.flush()
        temporary.replace(candidate)
    except OSError as exc:
        raise UbuntuLocalProviderError(f"write generated driver config {candidate}: {exc}") from exc
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--template", required=True, help="fully explicit single-driver config")
    parser.add_argument(
        "--images",
        required=True,
        help="strict validation image manifest (20-question baseline or larger)",
    )
    parser.add_argument("--output", required=True, help="generated question driver map")
    parser.add_argument("--artifact-root", help="absolute artifact root for question outputs")
    args = parser.parse_args(argv)
    try:
        generated = generate_question_driver_configs(
            load_json(args.template, "driver template"),
            load_json(args.images, "question image manifest"),
            artifact_root=args.artifact_root,
        )
        write_json(args.output, generated)
        return 0
    except UbuntuLocalProviderError as exc:
        parser.error(str(exc))
    return 2


if __name__ == "__main__":
    raise SystemExit(main())


__all__ = [
    "EXPECTED_QUESTION_COUNT",
    "MIN_VALIDATION_QUESTION_COUNT",
    "QUESTION_DRIVER_SCHEMA",
    "generate_question_driver_configs",
    "load_json",
    "main",
    "write_json",
]
