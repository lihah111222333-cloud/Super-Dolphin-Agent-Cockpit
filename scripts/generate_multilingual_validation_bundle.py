#!/usr/bin/env python3
"""Build a runnable bundle for the stratified SWE-bench Multilingual sample.

The representative-selection file is intentionally small and contains only
question metadata plus a Docker tag.  This adapter joins it with the official
Multilingual JSONL, resolves every tag to a local immutable RepoDigest, and
derives the exact base tree from that image before emitting the existing
Cross-File runner inputs.  Missing records, tag drift, mutable images, or base
identity drift fail closed.
"""

from __future__ import annotations

import argparse
import copy
import json
import pathlib
import re
import subprocess
import tempfile
from collections.abc import Mapping, Sequence
from typing import Any, Protocol

try:
    from .cross_file_benchmark_runner import RUN_PLAN_SCHEMA
    from .cross_file_identity_envelope import canonical_json, sha256_text
    from .cross_file_prompt_contract import load_prompt_contract, prompt_identity_material
    from .cross_file_ubuntu_execution_driver import UbuntuExecutionDriverConfig
    from .cross_file_ubuntu_local_provider import (
        IMAGE_MANIFEST_SCHEMA,
        REQUIRED_GRADER_SLOTS,
    )
except ImportError:  # pragma: no cover - direct script invocation compatibility.
    from cross_file_benchmark_runner import RUN_PLAN_SCHEMA  # type: ignore[no-redef]
    from cross_file_identity_envelope import canonical_json, sha256_text  # type: ignore[no-redef]
    from cross_file_prompt_contract import load_prompt_contract, prompt_identity_material  # type: ignore[no-redef]
    from cross_file_ubuntu_execution_driver import UbuntuExecutionDriverConfig  # type: ignore[no-redef]
    from cross_file_ubuntu_local_provider import (  # type: ignore[no-redef]
        IMAGE_MANIFEST_SCHEMA,
        REQUIRED_GRADER_SLOTS,
    )


_SAFE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:-]*$")
_GIT = re.compile(r"^[0-9a-f]{40}(?:[0-9a-f]{24})?$")
_IMAGE = re.compile(
    r"^(?:sha256:[0-9a-f]{64}|[A-Za-z0-9][A-Za-z0-9._/:+-]*@sha256:[0-9a-f]{64})$"
)


class MultilingualBundleError(RuntimeError):
    """The selection, official dataset, image, or driver template is invalid."""


class ImageResolver(Protocol):
    """Resolve one selected Docker tag to digest and repository tree."""

    def resolve(self, image: str, base_commit: str) -> tuple[str, str]:
        raise NotImplementedError


class LocalDockerImageResolver:
    """Resolve images without pulling and verify the commit embedded in them."""

    def __init__(self, docker: str | pathlib.Path = "docker") -> None:
        self._docker = str(docker)
        if pathlib.Path(self._docker).name != "docker":
            raise MultilingualBundleError("docker executable must invoke docker")

    def _run(self, args: Sequence[str], label: str) -> subprocess.CompletedProcess[str]:
        try:
            result = subprocess.run(
                (self._docker, *args),
                capture_output=True,
                text=True,
                check=False,
            )
        except OSError as exc:
            raise MultilingualBundleError(f"{label} failed: {exc}") from exc
        if result.returncode != 0:
            raise MultilingualBundleError(
                f"{label} exited {result.returncode}: {result.stderr.strip()}"
            )
        return result

    def resolve(self, image: str, base_commit: str) -> tuple[str, str]:
        inspected = self._run(
            ("image", "inspect", "--format", "{{json .RepoDigests}}", image),
            f"inspect validation image {image}",
        )
        try:
            repo_digests = json.loads(inspected.stdout)
        except json.JSONDecodeError as exc:
            raise MultilingualBundleError(
                f"validation image {image} returned invalid RepoDigests"
            ) from exc
        if not isinstance(repo_digests, list) or not all(
            isinstance(item, str) for item in repo_digests
        ):
            raise MultilingualBundleError(
                f"validation image {image} returned invalid RepoDigests"
            )
        immutable = next((item for item in repo_digests if _IMAGE.fullmatch(item)), None)
        if immutable is None:
            raise MultilingualBundleError(
                f"validation image {image} has no immutable RepoDigest"
            )
        tree_probe = self._run(
            (
                "run",
                "--rm",
                "--pull=never",
                "--entrypoint",
                "git",
                immutable,
                "-C",
                "/testbed",
                "rev-parse",
                "HEAD",
                "HEAD^{tree}",
            ),
            f"probe validation image {immutable}",
        )
        lines = [line.strip() for line in tree_probe.stdout.splitlines() if line.strip()]
        if len(lines) != 2 or not all(_GIT.fullmatch(line) for line in lines):
            raise MultilingualBundleError(
                f"validation image {image} returned invalid HEAD/tree identity"
            )
        if lines[0] != base_commit:
            raise MultilingualBundleError(
                f"validation image {image} HEAD does not match selected base_commit"
            )
        return immutable, lines[1]


def _object(value: Any, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise MultilingualBundleError(f"{label} must be an object")
    return value


def _text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise MultilingualBundleError(f"{label} must be non-empty text")
    return value.strip()


def _read_json(path: str | pathlib.Path, label: str) -> Any:
    candidate = pathlib.Path(path)
    try:
        return json.loads(candidate.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise MultilingualBundleError(f"read {label} {candidate}: {exc}") from exc


def _read_jsonl(path: str | pathlib.Path) -> dict[str, Mapping[str, Any]]:
    candidate = pathlib.Path(path)
    result: dict[str, Mapping[str, Any]] = {}
    try:
        lines = candidate.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeError) as exc:
        raise MultilingualBundleError(f"read official Multilingual dataset {candidate}: {exc}") from exc
    for line_number, line in enumerate(lines, 1):
        if not line.strip():
            continue
        try:
            item = _object(json.loads(line), f"official dataset line {line_number}")
        except json.JSONDecodeError as exc:
            raise MultilingualBundleError(
                f"official dataset line {line_number} is invalid JSON"
            ) from exc
        question_id = _text(item.get("instance_id"), f"official dataset line {line_number}.instance_id")
        if question_id in result:
            raise MultilingualBundleError(f"official dataset contains duplicate {question_id}")
        result[question_id] = item
    if not result:
        raise MultilingualBundleError("official Multilingual dataset must not be empty")
    return result


def _selection(value: Any) -> list[Mapping[str, Any]]:
    raw = value
    if isinstance(raw, Mapping):
        raw = raw.get("questions", raw.get("tasks"))
    if not isinstance(raw, list) or not raw:
        raise MultilingualBundleError("Multilingual selection must be a non-empty array")
    result: list[Mapping[str, Any]] = []
    seen: set[str] = set()
    for index, item in enumerate(raw):
        entry = _object(item, f"selection[{index}]")
        question_id = _text(entry.get("instance_id", entry.get("question_id")), f"selection[{index}].instance_id")
        if _SAFE_ID.fullmatch(question_id) is None or question_id in seen:
            raise MultilingualBundleError(f"selection has unsafe or duplicate instance_id: {question_id}")
        _text(entry.get("repo"), f"selection[{index}].repo")
        base_commit = _text(entry.get("base_commit"), f"selection[{index}].base_commit")
        if _GIT.fullmatch(base_commit) is None:
            raise MultilingualBundleError(f"selection[{index}].base_commit is not a Git hash")
        _text(entry.get("image"), f"selection[{index}].image")
        seen.add(question_id)
        result.append(entry)
    return result


def _binding(identifier: str, value: Any) -> dict[str, str]:
    return {"id": identifier, "sha256": sha256_text(canonical_json(value))}


def _question_data(
    selected: Mapping[str, Any], official: Mapping[str, Any], image: str, tree: str
) -> tuple[dict[str, Any], dict[str, Any]]:
    question_id = _text(selected.get("instance_id"), "selection.instance_id")
    if _text(official.get("instance_id"), f"official[{question_id}].instance_id") != question_id:
        raise MultilingualBundleError(f"official dataset identity mismatch for {question_id}")
    if _text(official.get("repo"), f"official[{question_id}].repo") != _text(selected.get("repo"), "selection.repo"):
        raise MultilingualBundleError(f"official dataset repo mismatch for {question_id}")
    if _text(official.get("base_commit"), f"official[{question_id}].base_commit") != _text(selected.get("base_commit"), "selection.base_commit"):
        raise MultilingualBundleError(f"official dataset base_commit mismatch for {question_id}")
    problem = _text(official.get("problem_statement"), f"official[{question_id}].problem_statement")
    if _GIT.fullmatch(tree) is None:
        raise MultilingualBundleError(f"resolved tree for {question_id} is invalid")
    issue = {"title": f"Fix {question_id}", "body": problem}
    episode = {
        "base_commit": _text(selected.get("base_commit"), "selection.base_commit"),
        "base_tree": tree,
        "repo_digest": image,
    }
    return {"repo": _text(selected.get("repo"), "selection.repo"), "issue": issue}, episode


def _prompt_contract(config: dict[str, Any], task_prompt: str) -> dict[str, Any]:
    contract = load_prompt_contract(
        config["canonical_lsp_prompt_path"],
        config["canonical_lsp_prompt_sha256"],
        task_prompt,
    )
    return contract.to_dict()


def build_bundle(
    selection: Any,
    official_dataset: Mapping[str, Mapping[str, Any]],
    template: Mapping[str, Any],
    *,
    resolver: ImageResolver,
    artifact_root: str | pathlib.Path,
    run_id: str = "multilingual-representative100-v112",
    pricing_manifest_hash: str | None = None,
) -> dict[str, Any]:
    selected_items = _selection(selection)
    raw_template = _object(template.get("driver_template", template), "driver template")
    parsed_template = UbuntuExecutionDriverConfig.from_dict(raw_template)
    if parsed_template.grader_slots != REQUIRED_GRADER_SLOTS:
        raise MultilingualBundleError(
            f"driver template must declare exactly {REQUIRED_GRADER_SLOTS} grader slots"
        )
    root = pathlib.Path(artifact_root)
    if not root.is_absolute():
        raise MultilingualBundleError("artifact_root must be an absolute path")
    set_id = f"multilingual-representative{len(selected_items)}-v112"
    manifest_value = {
        "selection_sha256": sha256_text(
            canonical_json({"items": selected_items})
        ),
        "dataset_size": len(official_dataset),
        "set_id": set_id,
    }
    manifest_binding = _binding(set_id, manifest_value)
    pricing_hash = pricing_manifest_hash or sha256_text(
        canonical_json({"run_id": run_id, "set_id": set_id})
    )
    if not isinstance(pricing_hash, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", pricing_hash):
        raise MultilingualBundleError("pricing_manifest_hash must be sha256:<64 lowercase hex>")
    images: dict[str, str] = {}
    configs: dict[str, dict[str, Any]] = {}
    questions: list[dict[str, Any]] = []
    for selected in selected_items:
        question_id = _text(selected.get("instance_id"), "selection.instance_id")
        official = official_dataset.get(question_id)
        if official is None:
            raise MultilingualBundleError(f"official Multilingual dataset has no {question_id}")
        selected_image = _text(selected.get("image"), f"selection[{question_id}].image")
        official_image = _text(official.get("image"), f"official[{question_id}].image")
        if official_image != selected_image:
            raise MultilingualBundleError(
                f"official dataset image mismatch for {question_id}"
            )
        image, tree = resolver.resolve(selected_image, _text(selected.get("base_commit"), f"selection[{question_id}].base_commit"))
        if _IMAGE.fullmatch(image) is None:
            raise MultilingualBundleError(f"resolved image for {question_id} is not immutable")
        issue_data, episode = _question_data(selected, official, image, tree)
        task_prompt = f"Fix issue {question_id}: {issue_data['issue']['title']}\n\n{issue_data['issue']['body']}"
        config = copy.deepcopy(dict(raw_template))
        config["container_image"] = image
        config["artifact_dir"] = str(root / "questions" / question_id)
        config["prompt_template"] = task_prompt
        config["prompt_contract"] = _prompt_contract(config, task_prompt)
        bindings = copy.deepcopy(dict(_object(config.get("identity_bindings"), f"identity_bindings[{question_id}]")))
        bindings["manifest"] = manifest_binding
        bindings["task"] = _binding(question_id, issue_data["issue"])
        bindings["base"] = {"commit_sha": episode["base_commit"], "tree_sha": episode["base_tree"], "sha256": sha256_text(canonical_json(episode))}
        bindings["source_image"] = _binding(image, {"repo_digest": image})
        prompt_material = {key: "sha256:" + value for key, value in prompt_identity_material(config["prompt_contract"]).items()}
        bindings["prompt"] = _binding("prompt", prompt_material)
        config["identity_bindings"] = bindings
        UbuntuExecutionDriverConfig.from_dict(config)
        images[question_id] = image
        configs[question_id] = config
        questions.append({"question_id": question_id, "discovery_shape": "issue-driven", "arms": {mode: {"mode": mode, "arm_id": f"{question_id}-{mode.replace('_', '-') }"} for mode in ("no_lsp", "with_lsp")}})
    return {
        "plan": {"schema": RUN_PLAN_SCHEMA, "run_id": run_id, "pricing_manifest": {"manifest_hash": pricing_hash}, "max_concurrency": 40, "questions": questions},
        "images": {"schema": IMAGE_MANIFEST_SCHEMA, "questions": images},
        "drivers": {"schema": "super-dolphin/cross-file-question-drivers/v1", "question_configs": configs},
        "metadata": {"schema": "super-dolphin/multilingual-validation-bundle/v1", "set_id": set_id, "question_count": len(questions), "arm_count": len(questions) * 2, "source_dataset_count": len(official_dataset), "pricing_manifest_hash": pricing_hash},
    }


def write_json(path: str | pathlib.Path, value: Mapping[str, Any]) -> None:
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
        raise MultilingualBundleError(f"write {candidate}: {exc}") from exc
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--selection", required=True, help="100-question representative selection JSON")
    parser.add_argument("--dataset", required=True, help="official SWE-bench Multilingual JSONL")
    parser.add_argument("--template", required=True, help="explicit Ubuntu driver template JSON")
    parser.add_argument("--artifact-root", required=True, help="absolute artifact root")
    parser.add_argument("--output-dir", required=True, help="bundle output directory")
    parser.add_argument("--docker", default="docker", help="Docker executable used only for local inspect/run probes")
    parser.add_argument("--run-id", default="multilingual-representative100-v112")
    parser.add_argument("--pricing-manifest-hash")
    args = parser.parse_args(argv)
    try:
        bundle = build_bundle(
            _read_json(args.selection, "selection"),
            _read_jsonl(args.dataset),
            _object(_read_json(args.template, "driver template"), "driver template"),
            resolver=LocalDockerImageResolver(args.docker),
            artifact_root=args.artifact_root,
            run_id=args.run_id,
            pricing_manifest_hash=args.pricing_manifest_hash,
        )
        output = pathlib.Path(args.output_dir)
        write_json(output / "plan.json", bundle["plan"])
        write_json(output / "images.json", bundle["images"])
        write_json(output / "drivers.json", bundle["drivers"])
        write_json(output / "metadata.json", bundle["metadata"])
        return 0
    except MultilingualBundleError as exc:
        parser.error(str(exc))
    return 2


__all__ = [
    "ImageResolver",
    "LocalDockerImageResolver",
    "MultilingualBundleError",
    "build_bundle",
    "main",
    "write_json",
]


if __name__ == "__main__":
    raise SystemExit(main())
