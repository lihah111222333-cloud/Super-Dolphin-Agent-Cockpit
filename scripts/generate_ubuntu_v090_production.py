#!/usr/bin/env python3
"""Generate the v090 Ubuntu driver bundle and live validation-image plan.

The generator consumes episode metadata; it never reads private key material.
Private key arguments are emitted as host-only paths for the provider.
"""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import pathlib
import tempfile
from collections.abc import Mapping, Sequence
from typing import Any

if __package__ in {None, ""}:
    import sys
    sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent.parent))
    from scripts.cross_file_identity_envelope import build_identity_envelope, canonical_json, sha256_text
    from scripts.cross_file_prompt_contract import (
        PromptContractError,
        load_prompt_contract,
        prompt_identity_material,
    )
    from scripts.cross_file_ubuntu_local_provider import (  # type: ignore[no-redef]
        IMAGE_MANIFEST_SCHEMA,
        MIN_VALIDATION_QUESTION_COUNT,
        REQUIRED_GRADER_SLOTS,
        UbuntuLocalProviderError,
    )
else:
    from .cross_file_identity_envelope import build_identity_envelope, canonical_json, sha256_text
    from .cross_file_prompt_contract import (
        PromptContractError,
        load_prompt_contract,
        prompt_identity_material,
    )
    from .cross_file_ubuntu_local_provider import (
        IMAGE_MANIFEST_SCHEMA,
        MIN_VALIDATION_QUESTION_COUNT,
        REQUIRED_GRADER_SLOTS,
        UbuntuLocalProviderError,
    )

SCHEMA = "super-dolphin/cross-file-ubuntu-v090-production/v1"
PLAN_SCHEMA = "super-dolphin/cross-file-benchmark-runner/v1"
_GIT = __import__("re").compile(r"^[0-9a-f]{40}(?:[0-9a-f]{24})?$")
_DIGEST = __import__("re").compile(r"^sha256:[0-9a-f]{64}$")
_IMAGE = __import__("re").compile(r"^(?:sha256:[0-9a-f]{64}|[A-Za-z0-9][A-Za-z0-9._/:+-]*@sha256:[0-9a-f]{64})$")


def _obj(value: Any, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise UbuntuLocalProviderError(f"{label} must be an object")
    return value


def _text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise UbuntuLocalProviderError(f"{label} must be non-empty text")
    return value.strip()


def _path(value: Any, label: str) -> str:
    result = _text(value, label)
    if not pathlib.Path(result).is_absolute():
        raise UbuntuLocalProviderError(f"{label} must be an absolute host path")
    return result


def _digest(value: Any, label: str) -> str:
    result = _text(value, label)
    if _DIGEST.fullmatch(result) is None:
        raise UbuntuLocalProviderError(f"{label} must be sha256:<64 lowercase hex>")
    return result


def _binding(identifier: str, value: Any) -> dict[str, str]:
    return {"id": identifier, "sha256": sha256_text(canonical_json(value))}


def _task_prompt(question_id: str, issue: Mapping[str, Any]) -> str:
    """Build one arm-neutral task prompt from the immutable issue title."""

    return f"Fix issue {question_id}: {_text(issue.get('title'), 'issue.title')}"


def _public_key(value: Any, label: str) -> dict[str, str]:
    raw = _obj(value, label)
    if set(raw) != {"algorithm", "key_id", "public_key"} or raw.get("algorithm") != "Ed25519":
        raise UbuntuLocalProviderError(f"{label} must be an Ed25519 public binding")
    return {key: _text(raw[key], f"{label}.{key}") for key in ("algorithm", "key_id", "public_key")}


def _question(raw: Mapping[str, Any], index: int, image: str) -> tuple[str, dict[str, Any], dict[str, Any]]:
    question_id = _text(raw.get("question_id", raw.get("instance_id")), f"questions[{index}].question_id")
    episode = _obj(raw.get("episode"), f"questions[{index}].episode")
    issue = _obj(raw.get("issue"), f"questions[{index}].issue")
    repo = _text(raw.get("repo"), f"questions[{index}].repo")
    commit = _text(episode.get("base_commit"), f"questions[{index}].episode.base_commit")
    tree = _text(episode.get("base_tree"), f"questions[{index}].episode.base_tree")
    if _GIT.fullmatch(commit) is None or _GIT.fullmatch(tree) is None:
        raise UbuntuLocalProviderError(f"questions[{index}] episode base commit/tree must be Git hashes")
    if not issue or not issue.get("title") or not issue.get("body"):
        raise UbuntuLocalProviderError(f"questions[{index}].issue must include title and body")
    episode_image = _text(episode.get("repo_digest", episode.get("image", image)), f"questions[{index}].episode.repo_digest")
    if episode_image != image:
        raise UbuntuLocalProviderError(f"questions[{index}] image digest disagrees with RepoDigest manifest")
    return question_id, {"repo": repo, "issue": copy.deepcopy(dict(issue))}, {"commit_sha": commit, "tree_sha": tree, "image": image}


def generate_bundle(
    representative_manifest: Mapping[str, Any],
    repo_digests: Mapping[str, Any],
    *,
    prompt_sha256: str,
    mcp_public_key: Mapping[str, Any],
    executor_public_key: Mapping[str, Any],
    grader_public_key: Mapping[str, Any],
    mcp_private_key_path: str,
    executor_private_key_path: str,
    grader_private_key_path: str,
    mcp_binary: str,
    codex_binary: str,
    codex_auth_path: str,
    grader_binary: str,
    grader_dataset_path: str,
    workspace: str,
    artifact_dir: str,
    canonical_prompt_path: str,
    dataset_id: str,
    toolchain_id: str,
    model_id: str,
    pricing_id: str,
    scoring_id: str,
    run_id: str = "ubuntu-v090-production",
) -> dict[str, Any]:
    manifest = _obj(representative_manifest, "representative manifest")
    questions = manifest.get("questions", manifest.get("tasks"))
    if not isinstance(questions, Sequence) or isinstance(questions, (str, bytes, bytearray)) or not questions:
        raise UbuntuLocalProviderError("representative manifest must contain at least one question")
    if len(questions) < MIN_VALIDATION_QUESTION_COUNT:
        raise UbuntuLocalProviderError(
            "bundle generation requires exactly 20 questions or a larger "
            "validation image set"
        )
    digest_map = _obj(repo_digests, "RepoDigest map")
    if len(digest_map) != len(questions):
        raise UbuntuLocalProviderError("RepoDigest map must match the question count")
    prompt_sha = _digest("sha256:" + prompt_sha256 if len(prompt_sha256) == 64 else prompt_sha256, "prompt_sha256")
    mcp = _public_key(mcp_public_key, "mcp_public_key")
    executor = _public_key(executor_public_key, "executor_public_key")
    grader = _public_key(grader_public_key, "grader_public_key")
    if len({mcp["public_key"], executor["public_key"], grader["public_key"]}) != 3:
        raise UbuntuLocalProviderError("MCP, executor and grader public keys must be distinct")
    private_paths = {"mcp": _path(mcp_private_key_path, "mcp_private_key_path"), "executor": _path(executor_private_key_path, "executor_private_key_path"), "grader": _path(grader_private_key_path, "grader_private_key_path")}
    binary_paths = {name: _path(value, f"{name}_binary") for name, value in {"mcp": mcp_binary, "codex": codex_binary, "grader": grader_binary}.items()}
    codex_auth_path = _path(codex_auth_path, "codex_auth_path")
    grader_dataset_path = _path(grader_dataset_path, "grader_dataset_path")
    workspace = _path(workspace, "workspace")
    artifact_dir = _path(artifact_dir, "artifact_dir")
    canonical_prompt_path = _path(canonical_prompt_path, "canonical_prompt_path")
    canonical_prompt_sha256 = prompt_sha.removeprefix("sha256:")
    prompt_contracts: dict[str, dict[str, Any]] = {}
    manifest_hash = sha256_text(canonical_json(manifest))
    questions_out = []
    configs = {}
    for index, raw in enumerate(questions):
        item = _obj(raw, f"questions[{index}]")
        qid = _text(item.get("question_id", item.get("instance_id")), f"questions[{index}].question_id")
        image = _text(digest_map.get(qid), f"repo_digests[{qid}]")
        if _IMAGE.fullmatch(image) is None:
            raise UbuntuLocalProviderError(f"repo_digests[{qid}] must be immutable")
        qid, issue_data, episode = _question(item, index, image)
        task_prompt = _task_prompt(qid, issue_data["issue"])
        try:
            prompt_contract = load_prompt_contract(
                canonical_prompt_path,
                canonical_prompt_sha256,
                task_prompt,
            )
        except PromptContractError as exc:
            raise UbuntuLocalProviderError(
                f"prompt contract rejected for {qid}: {exc}"
            ) from exc
        prompt_contract_data = prompt_contract.to_dict()
        prompt_contracts[qid] = prompt_contract_data
        prompt_identity = {
            key: "sha256:" + value
            for key, value in prompt_identity_material(prompt_contract).items()
        }
        static = {
            "manifest": _binding(
                f"question-set-{len(questions)}", manifest
            ), "dataset": _binding(dataset_id, {"id": dataset_id}),
            "task": _binding(qid, issue_data["issue"]),
            "base": {"commit_sha": episode["commit_sha"], "tree_sha": episode["tree_sha"], "sha256": sha256_text(canonical_json(episode))},
            "source_image": _binding(image, {"repo_digest": image}), "toolchain": _binding(toolchain_id, {"mcp": binary_paths["mcp"]}),
            "grader": _binding("grader", {"binary": binary_paths["grader"]}), "runner": _binding("runner-v090", {"run_id": run_id}),
            "model": _binding(model_id, {"id": model_id}),
            "prompt": _binding("prompt", prompt_identity),
            "pricing": _binding(pricing_id, {"id": pricing_id}), "scoring": _binding(scoring_id, {"id": scoring_id}),
            "session": _binding(f"{qid}-session-v090", {"question": qid}), "thread": _binding(f"{qid}-thread-v090", {"question": qid}),
            "agent": _binding(f"{qid}-agent-v090", {"question": qid}), "patch": _binding(sha256_text(canonical_json(issue_data["issue"])), issue_data["issue"]),
            "final_tree": {"tree_sha": episode["tree_sha"], "sha256": sha256_text(episode["tree_sha"])},
        }
        envelope = build_identity_envelope(static, receipt_signing_key=mcp, grader_receipt_signing_key=grader)
        config = {
            "container_command": ["docker", "run", "--rm", "--pull=never", "{image}"], "container_image": image,
            "codex_exec_command": [binary_paths["codex"], "exec"], "codex_resume_command": [binary_paths["codex"], "exec", "resume"],
            "grader_command": [binary_paths["grader"], "-m", "swebench.harness.run_evaluation", "--dataset_name", "{dataset_name}", "--split", "{split}", "--predictions_path", "{predictions_path}", "--max_workers", "{max_workers}", "--run_id", "{run_id}", "--timeout", "{timeout}", "--cache_level", "{cache_level}", "--clean", "{clean}", "--namespace", "{namespace}", "--instance_ids", "{instance_ids}"], "workspace": workspace, "artifact_dir": f"{artifact_dir}/questions/{qid}",
            # ``prompt_template`` remains the task-only input for existing
            # drivers.  The explicit contract below lets a newer driver keep
            # task bytes separate from system instructions and select the
            # With-LSP addendum only at the system layer.
            "prompt_template": task_prompt,
            "prompt_contract": prompt_contract_data,
            "canonical_lsp_prompt_path": canonical_prompt_path,
            "canonical_lsp_prompt_sha256": canonical_prompt_sha256,
            "identity_bindings": static, "receipt_signing_key": mcp, "grader_receipt_signing_key": grader,
            "receipt_log_path_template": "receipt-{question_id}-{mode}-{round}.jsonl", "attestation_path_template": "attestation-{question_id}-{mode}-{round}.json", "prompt_policy_path_template": "policy-{question_id}-{mode}-{round}.json",
            "executor_attestation_signing_key_path": private_paths["executor"], "executor_attestation_key_id": executor["key_id"], "attestation_replay_store_path": f"{artifact_dir}/replay",
            "agent_result_path_template": "agent-{question_id}-{mode}-{round}-{phase}.json", "agent_stdout_path_template": "agent-{question_id}-{mode}-{round}-{phase}.stdout", "agent_stderr_path_template": "agent-{question_id}-{mode}-{round}-{phase}.stderr", "session_state_path_template": "session-{question_id}-{mode}.json", "arm_evidence_path_template": "evidence-{question_id}-{mode}-{round}-{phase}.json", "usage_path_template": "usage-{question_id}-{mode}-{round}-{phase}.json", "server_generation_path_template": "generation-{question_id}-{mode}-{round}-{phase}.txt", "grader_request_path_template": "grader-request-{question_id}-{mode}-{round}-{attempt}.json", "grader_feedback_path_template": "grader-feedback-{question_id}-{mode}-{round}-{attempt}.json", "grader_report_path_template": "grader-report-{question_id}-{mode}-{round}-{attempt}.json", "grader_test_output_path_template": "grader-test-{question_id}-{mode}-{round}-{attempt}.txt", "grader_stdout_path_template": "grader-{question_id}-{mode}-{round}-{attempt}.stdout", "grader_stderr_path_template": "grader-{question_id}-{mode}-{round}-{attempt}.stderr", "grader_slots": REQUIRED_GRADER_SLOTS,
            "environment": {"SUPER_DOLPHIN_CODEX_AUTH_PATH": codex_auth_path, "MCP_LSP_RECEIPT_SIGNING_KEY_PATH": private_paths["mcp"], "MCP_LSP_BINARY": binary_paths["mcp"], "SUPER_DOLPHIN_GRADER_RECEIPT_SIGNING_KEY_PATH": private_paths["grader"], "SUPER_DOLPHIN_GRADER_DATASET_NAME": grader_dataset_path, "SUPER_DOLPHIN_GRADER_SPLIT": "test", "SUPER_DOLPHIN_GRADER_MAX_WORKERS": "1", "SUPER_DOLPHIN_GRADER_TIMEOUT": "7200", "SUPER_DOLPHIN_GRADER_CACHE_LEVEL": "env", "SUPER_DOLPHIN_GRADER_CLEAN": "false", "SUPER_DOLPHIN_GRADER_NAMESPACE": "swebench"},
        }
        configs[qid] = config
        questions_out.append({"question_id": qid, "discovery_shape": "issue-driven", "arms": {"no_lsp": {"mode": "no_lsp", "arm_id": f"{qid}-no-lsp"}, "with_lsp": {"mode": "with_lsp", "arm_id": f"{qid}-with-lsp"}}})
    first_contract = prompt_contracts[next(iter(prompt_contracts))]
    prompt_metadata = {
        "schema": first_contract["schema"],
        "canonical_prompt_path": first_contract["canonical_prompt_path"],
        "canonical_prompt_sha256": first_contract["canonical_prompt_sha256"],
        "common_developer_instructions": first_contract["common_developer_instructions"],
        "common_developer_instructions_sha256": first_contract["common_developer_instructions_sha256"],
        "with_lsp_addendum": first_contract["with_lsp_addendum"],
        "with_lsp_addendum_sha256": first_contract["with_lsp_addendum_sha256"],
        "allowed_arm_differences": first_contract["allowed_arm_differences"],
        "tasks": {
            question_id: {
                "task_prompt": contract["task_prompt"],
                "task_prompt_sha256": contract["task_prompt_sha256"],
                "allowed_arm_differences": contract["allowed_arm_differences"],
            }
            for question_id, contract in prompt_contracts.items()
        },
    }
    plan = {"schema": PLAN_SCHEMA, "run_id": run_id, "max_concurrency": 40, "pricing_manifest": {"id": pricing_id, "sha256": sha256_text(pricing_id)}, "questions": questions_out}
    return {"schema": SCHEMA, "prompt_contract": prompt_metadata, "driver_template": configs[next(iter(configs))], "question_configs": configs, "plan": plan, "provider": {"mcp_private_key_path": private_paths["mcp"], "executor_private_key_path": private_paths["executor"], "grader_private_key_path": private_paths["grader"]}}


def write_json(path: str | pathlib.Path, value: Mapping[str, Any]) -> None:
    candidate = pathlib.Path(path)
    candidate.parent.mkdir(parents=True, exist_ok=True)
    payload = (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()
    temporary: pathlib.Path | None = None
    try:
        with tempfile.NamedTemporaryFile(mode="wb", dir=candidate.parent, delete=False) as handle:
            temporary = pathlib.Path(handle.name); handle.write(payload); handle.flush()
        temporary.replace(candidate)
    finally:
        if temporary is not None: temporary.unlink(missing_ok=True)


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--repo-digests", required=True)
    parser.add_argument("--output", required=True)
    for name in ("mcp-public-key", "executor-public-key", "grader-public-key"):
        parser.add_argument(f"--{name}", required=True)
    for name in ("mcp-private-key-path", "executor-private-key-path", "grader-private-key-path", "mcp-binary", "codex-binary", "codex-auth-path", "grader-binary", "grader-dataset-path", "workspace", "artifact-dir", "canonical-prompt-path", "dataset-id", "toolchain-id", "model-id", "pricing-id", "scoring-id", "prompt-sha256"):
        parser.add_argument(f"--{name}", required=True)
    args = parser.parse_args(argv)
    def read(path: str) -> Mapping[str, Any]:
        try:
            value = json.loads(pathlib.Path(path).read_text(encoding="utf-8"))
        except (OSError, UnicodeError, json.JSONDecodeError) as exc:
            raise UbuntuLocalProviderError(f"read JSON {path}: {exc}") from exc
        return _obj(value, path)
    try:
        bundle = generate_bundle(
            read(args.manifest), read(args.repo_digests), prompt_sha256=args.prompt_sha256,
            mcp_public_key=read(args.mcp_public_key), executor_public_key=read(args.executor_public_key), grader_public_key=read(args.grader_public_key),
            mcp_private_key_path=args.mcp_private_key_path, executor_private_key_path=args.executor_private_key_path, grader_private_key_path=args.grader_private_key_path,
            mcp_binary=args.mcp_binary, codex_binary=args.codex_binary, codex_auth_path=args.codex_auth_path, grader_binary=args.grader_binary, grader_dataset_path=args.grader_dataset_path, workspace=args.workspace, artifact_dir=args.artifact_dir, canonical_prompt_path=args.canonical_prompt_path,
            dataset_id=args.dataset_id, toolchain_id=args.toolchain_id, model_id=args.model_id, pricing_id=args.pricing_id, scoring_id=args.scoring_id,
        )
        write_json(args.output, bundle)
        return 0
    except UbuntuLocalProviderError as exc:
        parser.error(str(exc))
    return 2


__all__ = ["SCHEMA", "generate_bundle", "main", "write_json"]


if __name__ == "__main__":
    raise SystemExit(main())
