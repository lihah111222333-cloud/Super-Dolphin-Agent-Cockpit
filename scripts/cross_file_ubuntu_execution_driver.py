#!/usr/bin/env python3
"""Ubuntu subprocess driver for the canonical Cross-File A/B runner.

The benchmark runner owns the quality-round state machine.  This module owns
only the execution boundary: it starts Codex in an already provisioned
container, resumes the same Codex session for later turns, runs the official
SWE-bench command under a bounded grader-slot pool, and reads the files that
those processes produced.  No evidence is accepted from a run plan.

Every path, command, image, identity template, and artifact location is
explicit in :class:`UbuntuExecutionDriverConfig`.  Commands are argv arrays,
never shell strings.  Agent subprocesses intentionally have no wall-clock
timeout; a supervisor may stop the process externally without turning a
partial process into a quality result.
"""

from __future__ import annotations

import copy
import dataclasses
import hashlib
import json
import os
import pathlib
import re
import shutil
import subprocess
import tempfile
import threading
import time
from datetime import datetime, timezone
import secrets
from collections.abc import Mapping, Sequence
from typing import Any, Callable, Protocol

try:
    from .cross_file_benchmark_runner import AgentContext, ExecutionDriver, GraderContext
    from .cross_file_artifact_bridge import (
        ArtifactBridgeError,
        ExecutorAttestationError,
        PersistentAttestationReplayStore,
        build_agent_manifest,
        build_executor_final_attestation,
        build_no_lsp_executor_attestation,
        derive_canonical_arm_evidence_from_attestation,
        dynamic_identity,
        parse_codex_jsonl,
        usage_from_events,
    )
    from .cross_file_signed_receipts import Ed25519Signer, Ed25519Verifier
    from .cross_file_five_round_runner import RunnerStep
    from .cross_file_identity_envelope import (
        build_identity_envelope,
        canonical_json,
        parse_identity_envelope,
        sha256_bytes,
        sha256_text,
    )
    from .cross_file_lsp_receipts import validate_canonical_arm_evidence
    from .cross_file_grader_bridge import (
        GraderBridgeError,
        build_signed_feedback,
        build_grader_artifact,
        validate_grader_artifact_hashes,
    )
    from .cross_file_signed_receipts import validate_grader_receipt, verifier_from
    from .cross_file_prompt_contract import (
        PromptContract,
        PromptContractError,
        load_canonical_prompt,
        load_prompt_contract,
    )
    from .cross_file_execution_metrics import (
        ExecutionMetricsError,
        build_execution_metrics,
        build_unavailable_execution_metrics,
    )
except ImportError:  # pragma: no cover - direct script invocation compatibility.
    from cross_file_benchmark_runner import AgentContext, ExecutionDriver, GraderContext  # type: ignore[no-redef]
    from cross_file_artifact_bridge import (  # type: ignore[no-redef]
        ArtifactBridgeError,
        ExecutorAttestationError,
        PersistentAttestationReplayStore,
        build_agent_manifest,
        build_executor_final_attestation,
        build_no_lsp_executor_attestation,
        derive_canonical_arm_evidence_from_attestation,
        dynamic_identity,
        parse_codex_jsonl,
        usage_from_events,
    )
    from cross_file_signed_receipts import Ed25519Signer, Ed25519Verifier  # type: ignore[no-redef]
    from cross_file_five_round_runner import RunnerStep  # type: ignore[no-redef]
    from cross_file_identity_envelope import (  # type: ignore[no-redef]
        build_identity_envelope,
        canonical_json,
        parse_identity_envelope,
        sha256_bytes,
        sha256_text,
    )
    from cross_file_lsp_receipts import validate_canonical_arm_evidence  # type: ignore[no-redef]
    from cross_file_grader_bridge import (  # type: ignore[no-redef]
        GraderBridgeError,
        build_signed_feedback,
        build_grader_artifact,
        validate_grader_artifact_hashes,
    )
    from cross_file_signed_receipts import (  # type: ignore[no-redef]
        validate_grader_receipt,
        verifier_from,
    )
    from cross_file_prompt_contract import (  # type: ignore[no-redef]
        PromptContract,
        PromptContractError,
        load_canonical_prompt,
        load_prompt_contract,
    )
    from cross_file_execution_metrics import (  # type: ignore[no-redef]
        ExecutionMetricsError,
        build_execution_metrics,
        build_unavailable_execution_metrics,
    )


_IMAGE_DIGEST = re.compile(r"^(?:sha256:[0-9a-f]{64}|[A-Za-z0-9][A-Za-z0-9._/:+-]*@sha256:[0-9a-f]{64})$")
_SHA256_DIGEST = re.compile(r"^[0-9a-f]{64}$")
_SAFE_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$")
_LSP_ADDENDUM_MARKER = "## Conditional With-LSP addendum"
_PROMPT_CONTRACT_SCHEMA = "super-dolphin/cross-file-prompt-contract/v1"
_PROMPT_ALLOWED_ARM_DIFFERENCES = ["system.with_lsp_addendum"]
_MAX_AGENT_TRANSPORT_ATTEMPTS = 3
_AGENT_RETRY_BACKOFF_SECONDS = (10.0, 20.0)
_AGENT_RETRY_MARKERS = (
    b"429",
    b"too many requests",
    b"rate limit",
    b"rate_limit",
    b"ratelimit",
    b"stream disconnected",
    b"stream closed",
    b"stream terminated",
    b"stream error",
    b"transport closed",
    b"transport error",
    b"connection reset",
    b"connection aborted",
    b"connection refused",
    b"broken pipe",
    b"unexpected eof",
    b"unexpected end of stream",
    b"eof",
    "断流".encode("utf-8"),
    "连接断开".encode("utf-8"),
    b"error sending request",
    b"timeout waiting for child process",
)
_CONFIG_FIELDS = frozenset(
    {
        "container_command",
        "container_image",
        "codex_exec_command",
        "codex_resume_command",
        "grader_command",
        "workspace",
        "artifact_dir",
        "prompt_template",
        "prompt_contract",
        "canonical_lsp_prompt_path",
        "canonical_lsp_prompt_sha256",
        "identity_bindings",
        "receipt_signing_key",
        "grader_receipt_signing_key",
        "receipt_log_path_template",
        "attestation_path_template",
        "prompt_policy_path_template",
        "executor_attestation_signing_key_path",
        "executor_attestation_key_id",
        "attestation_replay_store_path",
        "grader_receipt_signing_key",
        "agent_result_path_template",
        "agent_stdout_path_template",
        "agent_stderr_path_template",
        "session_state_path_template",
        "arm_evidence_path_template",
        "usage_path_template",
        "metrics_path_template",
        "server_generation_path_template",
        "grader_request_path_template",
        "grader_feedback_path_template",
        "grader_report_path_template",
        "grader_test_output_path_template",
        "grader_stdout_path_template",
        "grader_stderr_path_template",
        "grader_slots",
        "environment",
    }
)


def _is_production_change(path: str) -> bool:
    """Classify source changes without treating tests/docs as production."""

    candidate = pathlib.PurePosixPath(path)
    parts = {part.lower() for part in candidate.parts[:-1]}
    if parts & {"test", "tests", "testing", "doc", "docs", "changelog", "changes"}:
        return False
    name = candidate.name.lower()
    if name in {"changelog", "changes", "history", "news"}:
        return False
    if name.startswith("test_") or name.endswith(("_test.py", "_test.go", ".rst", ".md")):
        return False
    return candidate.suffix.lower() in {
        ".py", ".pyi", ".go", ".js", ".jsx", ".ts", ".tsx", ".java",
        ".rs", ".c", ".cc", ".cpp", ".h", ".hpp", ".cs", ".rb",
        ".php", ".swift", ".kt", ".kts", ".scala", ".vue", ".svelte",
        ".prisma",
    }
_OPTIONAL_ATTESTATION_FIELDS = frozenset(
    {
        "prompt_contract",
        "metrics_path_template",
        "receipt_log_path_template",
        "attestation_path_template",
        "prompt_policy_path_template",
        "executor_attestation_signing_key_path",
        "executor_attestation_key_id",
        "attestation_replay_store_path",
    }
)
_IDENTITY_SURFACES = ("runner_events", "arm_evidence", "grader_feedback", "usage")
_AGENT_MANIFEST_FIELDS = frozenset(
    {"request", "session_id", "thread_id", "code_agent_id", "patch_hash", "tree_sha"}
)
_REQUEST_REQUIRED_FIELDS = frozenset({"changed_files", "trajectory", "patch_hash"})
_PLACEHOLDER_FIELDS = frozenset(
    {
        "question_id",
        "mode",
        "arm_id",
        "round",
        "attempt",
        "run_id",
        "nonce",
        "issued_at",
        "phase",
        "session_id",
        "thread_id",
        "code_agent_id",
        "patch_hash",
        "tree_sha",
        "workspace",
        "artifact_dir",
        "image",
        "result_path",
        "evidence_path",
        "usage_path",
        "server_generation_path",
        "request_path",
        "request_id",
        "feedback_path",
        "report_path",
        "test_output_path",
        "stdout_path",
        "stderr_path",
    }
)


class UbuntuExecutionDriverError(RuntimeError):
    """Base error for a malformed Ubuntu driver configuration or artifact."""


class UbuntuExecutionDriverConfigError(UbuntuExecutionDriverError):
    """The explicit driver configuration is incomplete or unsafe."""


class UbuntuExecutionDriverProtocolError(UbuntuExecutionDriverError):
    """A subprocess did not produce the required canonical artifact."""


@dataclasses.dataclass(frozen=True)
class SubprocessResult:
    """Captured process result returned by the injectable subprocess seam."""

    argv: tuple[str, ...]
    returncode: int
    stdout: bytes
    stderr: bytes


class SubprocessAdapter(Protocol):
    """Minimal command boundary used by production and fake integrations."""

    def run(
        self,
        argv: Sequence[str],
        *,
        cwd: pathlib.Path,
        env: Mapping[str, str],
    ) -> SubprocessResult:
        """Run one argv without a hidden timeout or shell fallback."""
        raise NotImplementedError


class LocalSubprocessAdapter:
    """Use the host subprocess implementation without imposing a timeout."""

    def run(
        self,
        argv: Sequence[str],
        *,
        cwd: pathlib.Path,
        env: Mapping[str, str],
    ) -> SubprocessResult:
        command = tuple(str(item) for item in argv)
        if not command or any(not item for item in command):
            raise UbuntuExecutionDriverProtocolError("subprocess argv must be non-empty")
        completed = subprocess.run(
            list(command),
            cwd=str(cwd),
            env=dict(env),
            capture_output=True,
            check=False,
        )
        return SubprocessResult(
            argv=command,
            returncode=completed.returncode,
            stdout=completed.stdout,
            stderr=completed.stderr,
        )


def _text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise UbuntuExecutionDriverConfigError(f"{label} must be a non-empty string")
    return value.strip()


def _argv(value: Any, label: str) -> tuple[str, ...]:
    if not isinstance(value, Sequence) or isinstance(value, (str, bytes, bytearray)) or not value:
        raise UbuntuExecutionDriverConfigError(f"{label} must be a non-empty argv array")
    result = tuple(_text(item, f"{label}[{index}]") for index, item in enumerate(value))
    if any("\x00" in item for item in result):
        raise UbuntuExecutionDriverConfigError(f"{label} contains a NUL byte")
    return result


def _mapping(value: Any, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise UbuntuExecutionDriverConfigError(f"{label} must be an object")
    return value


def _path(value: Any, label: str) -> pathlib.Path:
    text = _text(value, label)
    candidate = pathlib.Path(text)
    if not candidate.is_absolute():
        raise UbuntuExecutionDriverConfigError(f"{label} must be an absolute path")
    return candidate


def _strict_mapping(value: Mapping[str, Any], allowed: frozenset[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise UbuntuExecutionDriverConfigError(
            f"{label} has unknown field(s): {', '.join(unknown)}"
        )


def _prompt_text(value: Any, label: str) -> str:
    """Validate prompt text without stripping bytes covered by its digest."""

    if not isinstance(value, str) or not value:
        raise UbuntuExecutionDriverConfigError(f"{label} must be non-empty text")
    return value


def _prompt_digest(value: Any, label: str) -> str:
    """Normalize a prompt SHA-256 with or without the repository prefix."""

    text = _prompt_text(value, label)
    if text.startswith("sha256:"):
        text = text.removeprefix("sha256:")
    if _SHA256_DIGEST.fullmatch(text) is None:
        raise UbuntuExecutionDriverConfigError(
            f"{label} must be a lowercase SHA-256 digest"
        )
    return text


def _prompt_binding_digest(contract: Mapping[str, Any]) -> str:
    """Bind all four prompt surfaces into the identity prompt component."""

    material = {
        "canonical_prompt_sha256": "sha256:" + _prompt_digest(
            contract["canonical_prompt_sha256"], "prompt_contract.canonical_prompt_sha256"
        ),
        "task_prompt_sha256": "sha256:" + _prompt_digest(
            contract["task_prompt_sha256"], "prompt_contract.task_prompt_sha256"
        ),
        "common_developer_instructions_sha256": "sha256:" + _prompt_digest(
            contract["common_developer_instructions_sha256"],
            "prompt_contract.common_developer_instructions_sha256",
        ),
        "with_lsp_addendum_sha256": "sha256:" + _prompt_digest(
            contract["with_lsp_addendum_sha256"],
            "prompt_contract.with_lsp_addendum_sha256",
        ),
    }
    return sha256_text(canonical_json(material))


def _compose_prompt_contract(
    value: Mapping[str, Any],
    *,
    prompt_template: str,
    canonical_path: pathlib.Path,
    canonical_sha256: str,
    explicit: bool,
) -> dict[str, Any]:
    """Validate and canonicalize the fairness-owned prompt contract."""

    try:
        contract = (
            PromptContract.from_dict(_mapping(value, "prompt_contract"))
            if explicit
            else load_prompt_contract(canonical_path, canonical_sha256, prompt_template)
        )
        task_text = contract.task_prompt.decode("utf-8")
        if task_text != prompt_template:
            raise PromptContractError(
                "prompt_contract.task_prompt must exactly equal prompt_template"
            )
        if pathlib.Path(contract.canonical_prompt_path) != canonical_path:
            raise PromptContractError(
                "prompt_contract.canonical_prompt_path must equal canonical_lsp_prompt_path"
            )
        if contract.canonical_prompt_sha256 != _prompt_digest(
            canonical_sha256, "canonical_lsp_prompt_sha256"
        ):
            raise PromptContractError(
                "prompt_contract.canonical_prompt_sha256 must equal canonical_lsp_prompt_sha256"
            )
        canonical_text = load_canonical_prompt(canonical_path, canonical_sha256)
    except PromptContractError as exc:
        raise UbuntuExecutionDriverConfigError(
            f"canonical LSP prompt contract rejected: {exc}"
        ) from exc
    if canonical_text.encode("utf-8") != contract.common_developer_instructions + contract.with_lsp_addendum:
        raise UbuntuExecutionDriverConfigError(
            "prompt_contract common/addendum do not reassemble canonical prompt"
        )
    return contract.to_dict()


def _render(template: str, values: Mapping[str, Any], label: str) -> str:
    class StrictValues(dict[str, Any]):
        def __missing__(self, key: str) -> Any:
            raise UbuntuExecutionDriverConfigError(
                f"{label} references unknown placeholder {{{key}}}"
            )

    try:
        return template.format_map(StrictValues(values))
    except (KeyError, ValueError) as exc:
        raise UbuntuExecutionDriverConfigError(f"invalid {label}: {exc}") from exc


@dataclasses.dataclass(frozen=True)
class UbuntuExecutionDriverConfig:
    """All values needed to bind one Ubuntu container execution."""

    container_command: tuple[str, ...]
    container_image: str
    codex_exec_command: tuple[str, ...]
    codex_resume_command: tuple[str, ...]
    grader_command: tuple[str, ...]
    workspace: pathlib.Path
    artifact_dir: pathlib.Path
    prompt_template: str
    prompt_contract: Mapping[str, Any]
    prompt_contract_explicit: bool
    canonical_lsp_prompt_path: pathlib.Path
    canonical_lsp_prompt_sha256: str
    identity_bindings: Mapping[str, Any]
    receipt_signing_key: Mapping[str, Any]
    grader_receipt_signing_key: Mapping[str, Any] | None
    receipt_log_path_template: str | None
    attestation_path_template: str | None
    prompt_policy_path_template: str | None
    executor_attestation_signing_key_path: pathlib.Path | None
    executor_attestation_key_id: str | None
    attestation_replay_store_path: pathlib.Path | None
    agent_result_path_template: str
    agent_stdout_path_template: str
    agent_stderr_path_template: str
    session_state_path_template: str
    arm_evidence_path_template: str
    usage_path_template: str
    metrics_path_template: str | None
    server_generation_path_template: str
    grader_request_path_template: str
    grader_feedback_path_template: str
    grader_report_path_template: str
    grader_test_output_path_template: str
    grader_stdout_path_template: str
    grader_stderr_path_template: str
    grader_slots: int
    environment: Mapping[str, str]

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "UbuntuExecutionDriverConfig":
        """Parse an explicit JSON-compatible configuration with no defaults."""

        raw = _mapping(value, "Ubuntu execution driver config")
        _strict_mapping(raw, _CONFIG_FIELDS, "Ubuntu execution driver config")
        missing = sorted((_CONFIG_FIELDS - _OPTIONAL_ATTESTATION_FIELDS) - set(raw))
        if missing:
            raise UbuntuExecutionDriverConfigError(
                "Ubuntu execution driver config missing field(s): " + ", ".join(missing)
            )
        image = _text(raw["container_image"], "container_image")
        if _IMAGE_DIGEST.fullmatch(image) is None:
            raise UbuntuExecutionDriverConfigError(
                "container_image must be an immutable digest (image@sha256:<64> or sha256:<64>)"
            )
        slots = raw["grader_slots"]
        if isinstance(slots, bool) or not isinstance(slots, int) or slots < 1:
            raise UbuntuExecutionDriverConfigError("grader_slots must be a positive integer")
        environment = _mapping(raw["environment"], "environment")
        normalized_environment: dict[str, str] = {}
        for key, item in environment.items():
            env_key = _text(key, "environment key")
            normalized_environment[env_key] = _text(item, f"environment[{env_key}]")
        identity = copy.deepcopy(dict(_mapping(raw["identity_bindings"], "identity_bindings")))
        if not identity:
            raise UbuntuExecutionDriverConfigError("identity_bindings must not be empty")
        signing_key = copy.deepcopy(
            dict(_mapping(raw["receipt_signing_key"], "receipt_signing_key"))
        )
        if not signing_key:
            raise UbuntuExecutionDriverConfigError("receipt_signing_key must not be empty")
        grader_signing_key = None
        if "grader_receipt_signing_key" in raw:
            grader_signing_key = copy.deepcopy(
                dict(_mapping(raw["grader_receipt_signing_key"], "grader_receipt_signing_key"))
            )
            if not grader_signing_key:
                raise UbuntuExecutionDriverConfigError(
                    "grader_receipt_signing_key must not be empty"
                )
        attestation_fields = (
            "receipt_log_path_template",
            "attestation_path_template",
            "prompt_policy_path_template",
            "executor_attestation_signing_key_path",
            "executor_attestation_key_id",
            "attestation_replay_store_path",
        )
        configured_attestation = [name for name in attestation_fields if name in raw]
        if configured_attestation and len(configured_attestation) != len(attestation_fields):
            missing_attestation = sorted(set(attestation_fields) - set(configured_attestation))
            raise UbuntuExecutionDriverConfigError(
                "executor attestation config is incomplete; missing: "
                + ", ".join(missing_attestation)
            )
        attestation_enabled = bool(configured_attestation)
        templates = (
            "agent_result_path_template",
            "agent_stdout_path_template",
            "agent_stderr_path_template",
            "session_state_path_template",
            "arm_evidence_path_template",
            "usage_path_template",
            "server_generation_path_template",
            "grader_request_path_template",
            "grader_feedback_path_template",
            "grader_report_path_template",
            "grader_test_output_path_template",
            "grader_stdout_path_template",
            "grader_stderr_path_template",
        )
        prompt_template = _prompt_text(raw["prompt_template"], "prompt_template")
        normalized_templates = {name: _text(raw[name], name) for name in templates}
        receipt_log_template = (
            _text(raw["receipt_log_path_template"], "receipt_log_path_template")
            if attestation_enabled
            else None
        )
        attestation_template = (
            _text(raw["attestation_path_template"], "attestation_path_template")
            if attestation_enabled
            else None
        )
        prompt_policy_template = (
            _text(raw["prompt_policy_path_template"], "prompt_policy_path_template")
            if attestation_enabled
            else None
        )
        executor_key_path = (
            _path(
                raw["executor_attestation_signing_key_path"],
                "executor_attestation_signing_key_path",
            )
            if attestation_enabled
            else None
        )
        executor_key_id = (
            _text(raw["executor_attestation_key_id"], "executor_attestation_key_id")
            if attestation_enabled
            else None
        )
        replay_store_path = (
            _path(raw["attestation_replay_store_path"], "attestation_replay_store_path")
            if attestation_enabled
            else None
        )
        canonical_prompt_path = _path(
            raw["canonical_lsp_prompt_path"], "canonical_lsp_prompt_path"
        )
        canonical_prompt_sha256 = _text(
            raw["canonical_lsp_prompt_sha256"], "canonical_lsp_prompt_sha256"
        )
        # Read and hash the canonical asset during config parsing so a stale or
        # tampered prompt can never reach the first Agent process.
        try:
            load_canonical_prompt(canonical_prompt_path, canonical_prompt_sha256)
        except PromptContractError as exc:
            raise UbuntuExecutionDriverConfigError(
                f"canonical LSP prompt contract rejected: {exc}"
            ) from exc
        explicit_prompt_contract = "prompt_contract" in raw
        if explicit_prompt_contract:
            prompt_contract = _compose_prompt_contract(
                _mapping(raw["prompt_contract"], "prompt_contract"),
                prompt_template=prompt_template,
                canonical_path=canonical_prompt_path,
                canonical_sha256=canonical_prompt_sha256,
                explicit=True,
            )
        else:
            canonical_text = load_canonical_prompt(
                canonical_prompt_path,
                canonical_prompt_sha256,
            )
            marker_at = canonical_text.index(_LSP_ADDENDUM_MARKER)
            common = canonical_text[:marker_at]
            addendum = canonical_text[marker_at:]
            prompt_contract = _compose_prompt_contract(
                {
                    "schema": _PROMPT_CONTRACT_SCHEMA,
                    "task_prompt": prompt_template,
                    "task_prompt_sha256": sha256_text(prompt_template),
                    "common_developer_instructions": common,
                    "common_developer_instructions_sha256": sha256_text(common),
                    "with_lsp_addendum": addendum,
                    "with_lsp_addendum_sha256": sha256_text(addendum),
                    "canonical_prompt_path": str(canonical_prompt_path),
                    "canonical_prompt_sha256": canonical_prompt_sha256,
                    "allowed_arm_differences": list(_PROMPT_ALLOWED_ARM_DIFFERENCES),
                },
                prompt_template=prompt_template,
                canonical_path=canonical_prompt_path,
                canonical_sha256=canonical_prompt_sha256,
                explicit=False,
            )
        return cls(
            container_command=_argv(raw["container_command"], "container_command"),
            container_image=image,
            codex_exec_command=_argv(raw["codex_exec_command"], "codex_exec_command"),
            codex_resume_command=_argv(raw["codex_resume_command"], "codex_resume_command"),
            grader_command=_argv(raw["grader_command"], "grader_command"),
            workspace=_path(raw["workspace"], "workspace"),
            artifact_dir=_path(raw["artifact_dir"], "artifact_dir"),
            prompt_template=prompt_template,
            prompt_contract=prompt_contract,
            prompt_contract_explicit=explicit_prompt_contract,
            canonical_lsp_prompt_path=canonical_prompt_path,
            canonical_lsp_prompt_sha256=canonical_prompt_sha256,
            identity_bindings=identity,
            receipt_signing_key=signing_key,
            grader_receipt_signing_key=grader_signing_key,
            receipt_log_path_template=receipt_log_template,
            attestation_path_template=attestation_template,
            prompt_policy_path_template=prompt_policy_template,
            executor_attestation_signing_key_path=executor_key_path,
            executor_attestation_key_id=executor_key_id,
            attestation_replay_store_path=replay_store_path,
            agent_result_path_template=normalized_templates["agent_result_path_template"],
            agent_stdout_path_template=normalized_templates["agent_stdout_path_template"],
            agent_stderr_path_template=normalized_templates["agent_stderr_path_template"],
            session_state_path_template=normalized_templates["session_state_path_template"],
            arm_evidence_path_template=normalized_templates["arm_evidence_path_template"],
            usage_path_template=normalized_templates["usage_path_template"],
            metrics_path_template=(
                _text(raw["metrics_path_template"], "metrics_path_template")
                if "metrics_path_template" in raw
                else None
            ),
            server_generation_path_template=normalized_templates["server_generation_path_template"],
            grader_request_path_template=normalized_templates["grader_request_path_template"],
            grader_feedback_path_template=normalized_templates["grader_feedback_path_template"],
            grader_report_path_template=normalized_templates["grader_report_path_template"],
            grader_test_output_path_template=normalized_templates["grader_test_output_path_template"],
            grader_stdout_path_template=normalized_templates["grader_stdout_path_template"],
            grader_stderr_path_template=normalized_templates["grader_stderr_path_template"],
            grader_slots=slots,
            environment=normalized_environment,
        )


@dataclasses.dataclass(frozen=True)
class _Session:
    session_id: str
    thread_id: str
    code_agent_id: str


def _json_bytes(value: Any, label: str) -> bytes:
    try:
        return (canonical_json(value) + "\n").encode("utf-8")
    except (TypeError, ValueError) as exc:
        raise UbuntuExecutionDriverProtocolError(f"{label} is not canonical JSON: {exc}") from exc


def _read_bytes(path: pathlib.Path, label: str) -> bytes:
    try:
        data = path.read_bytes()
    except OSError as exc:
        raise UbuntuExecutionDriverProtocolError(f"read {label} {path}: {exc}") from exc
    if not data:
        raise UbuntuExecutionDriverProtocolError(f"{label} {path} is empty")
    return data


def _read_json(path: pathlib.Path, label: str) -> Mapping[str, Any]:
    try:
        raw = json.loads(_read_bytes(path, label).decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise UbuntuExecutionDriverProtocolError(f"{label} {path} is not JSON: {exc}") from exc
    return _mapping(raw, label)


def _read_jsonl_objects(path: pathlib.Path, label: str) -> list[dict[str, Any]]:
    """Read a canonical JSONL stream; an absent or empty stream is explicit empty evidence."""

    if not path.exists():
        return []
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeError) as exc:
        raise UbuntuExecutionDriverProtocolError(f"read {label} {path}: {exc}") from exc
    records: list[dict[str, Any]] = []
    for number, line in enumerate(lines, 1):
        if not line.strip():
            continue
        try:
            raw = json.loads(line)
        except json.JSONDecodeError as exc:
            raise UbuntuExecutionDriverProtocolError(
                f"{label} {path}:{number} is not JSON: {exc}"
            ) from exc
        records.append(dict(_mapping(raw, f"{label} line {number}")))
    return records


def _write_bytes(path: pathlib.Path, value: bytes, label: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary: pathlib.Path | None = None
    try:
        with tempfile.NamedTemporaryFile(
            mode="wb", prefix=f".{path.name}.", suffix=".tmp", dir=path.parent, delete=False
        ) as handle:
            temporary = pathlib.Path(handle.name)
            handle.write(value)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except OSError as exc:
        raise UbuntuExecutionDriverProtocolError(f"write {label} {path}: {exc}") from exc
    finally:
        if temporary is not None:
            try:
                temporary.unlink()
            except FileNotFoundError:
                pass


def _decode_jsonl_ids(value: bytes) -> dict[str, str]:
    candidates: dict[str, set[str]] = {
        "session_id": set(),
        "thread_id": set(),
        "code_agent_id": set(),
    }
    for line in value.decode("utf-8", errors="replace").splitlines():
        try:
            item = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(item, Mapping):
            continue
        for target, names in (
            ("session_id", ("session_id", "sessionId")),
            ("thread_id", ("thread_id", "threadId")),
            ("code_agent_id", ("code_agent_id", "agent_id", "agentId")),
        ):
            for name in names:
                candidate = item.get(name)
                if isinstance(candidate, str) and candidate.strip():
                    candidates[target].add(candidate.strip())
                    break
        nested = item.get("thread")
        if isinstance(nested, Mapping):
            for name in ("id", "thread_id", "threadId"):
                candidate = nested.get(name)
                if isinstance(candidate, str) and candidate.strip():
                    candidates["thread_id"].add(candidate.strip())
                    break
    if not candidates["session_id"] and len(candidates["thread_id"]) == 1:
        candidates["session_id"].update(candidates["thread_id"])
    ambiguous = sorted(
        f"{field}={sorted(values)!r}"
        for field, values in candidates.items()
        if len(values) > 1
    )
    if ambiguous:
        raise UbuntuExecutionDriverProtocolError(
            "Codex partial stdout identity is ambiguous: " + ", ".join(ambiguous)
        )
    return {
        field: next(iter(values))
        for field, values in candidates.items()
        if values
    }


class UbuntuExecutionDriver:
    """Concrete :class:`ExecutionDriver` for an Ubuntu container runtime."""

    def __init__(
        self,
        config: UbuntuExecutionDriverConfig,
        *,
        subprocess_adapter: SubprocessAdapter | None = None,
        grader_semaphore: threading.BoundedSemaphore | None = None,
    ) -> None:
        self.config = config
        self.subprocess = subprocess_adapter or LocalSubprocessAdapter()
        if not config.workspace.is_dir():
            raise UbuntuExecutionDriverConfigError("workspace must be an existing directory")
        config.artifact_dir.mkdir(parents=True, exist_ok=True)
        self._artifact_root = config.artifact_dir.resolve()
        self._workspace = config.workspace.resolve()
        self._sessions: dict[tuple[str, str, str], _Session] = {}
        self._grader_receipt_ledger = self._artifact_root / "grader-receipt-ledger.json"
        self._grader_slots = grader_semaphore or threading.BoundedSemaphore(config.grader_slots)
        self._base_head = self._git_output(("rev-parse", "HEAD"), "base HEAD")
        try:
            shared_prompt = load_canonical_prompt(
                config.canonical_lsp_prompt_path,
                config.canonical_lsp_prompt_sha256,
            )
            if shared_prompt.count(_LSP_ADDENDUM_MARKER) != 1:
                raise PromptContractError(
                    "shared developer instructions require one conditional With-LSP marker"
                )
            marker_at = shared_prompt.index(_LSP_ADDENDUM_MARKER)
            common = shared_prompt[:marker_at]
            addendum = shared_prompt[marker_at:]
            if not common or not addendum:
                raise PromptContractError("shared prompt baseline or With-LSP addendum is empty")
            contract = config.prompt_contract
            if contract["common_developer_instructions"] != common:
                raise PromptContractError(
                    "prompt_contract common developer instructions drifted from canonical prompt"
                )
            if contract["with_lsp_addendum"] != addendum:
                raise PromptContractError(
                    "prompt_contract With-LSP addendum drifted from canonical prompt"
                )
            self._canonical_prompt_text = shared_prompt
        except PromptContractError as exc:
            raise UbuntuExecutionDriverConfigError(
                f"canonical LSP prompt contract rejected: {exc}"
            ) from exc
        self._prompt_contract = copy.deepcopy(dict(config.prompt_contract))
        self._prompt_binding_digest = _prompt_binding_digest(self._prompt_contract)
        self._executor_signer = None
        self._attestation_replay_store = None
        if config.executor_attestation_signing_key_path is not None:
            try:
                from .cross_file_signed_receipts import Ed25519Signer
            except ImportError:  # pragma: no cover - direct script execution fallback.
                from cross_file_signed_receipts import Ed25519Signer  # type: ignore[no-redef]
            try:
                self._executor_signer = Ed25519Signer.from_file(
                    config.executor_attestation_signing_key_path,
                    key_id=config.executor_attestation_key_id or "",
                )
                if config.attestation_replay_store_path is None:
                    raise UbuntuExecutionDriverConfigError(
                        "attestation replay store path is required"
                    )
                self._attestation_replay_store = PersistentAttestationReplayStore(
                    config.attestation_replay_store_path
                )
            except (ExecutorAttestationError, ValueError) as exc:
                raise UbuntuExecutionDriverConfigError(
                    f"executor attestation signing setup rejected: {exc}"
                ) from exc
        self._validate_identity_template()


    def _git_output(self, args: Sequence[str], label: str, *, env: Mapping[str, str] | None = None) -> str:
        command = ("git", "-C", str(self._workspace), *tuple(args))
        merged = dict(os.environ)
        if env:
            merged.update(env)
        try:
            result = subprocess.run(command, capture_output=True, check=False, env=merged)
        except OSError as exc:
            raise UbuntuExecutionDriverProtocolError(f"{label} command failed: {exc}") from exc
        if result.returncode != 0:
            raise UbuntuExecutionDriverProtocolError(
                f"{label} command exited {result.returncode}: {result.stderr.decode('utf-8', errors='replace')}"
            )
        return result.stdout.decode("utf-8", errors="strict").strip()

    def _validate_identity_template(self) -> None:
        parsed = parse_identity_envelope(
            build_identity_envelope(
                self.config.identity_bindings,
                receipt_signing_key=self.config.receipt_signing_key,
            )
        )
        if parsed.bindings.base.commit_sha != self._base_head:
            raise UbuntuExecutionDriverConfigError(
                "identity_bindings.base.commit_sha does not match workspace HEAD"
            )
        if self.config.prompt_contract_explicit:
            if parsed.bindings.prompt.sha256 != self._prompt_binding_digest:
                raise UbuntuExecutionDriverConfigError(
                    "identity_bindings.prompt.sha256 must cover the canonical/task/common/addendum prompt hashes"
                )

    def _values(
        self,
        context: AgentContext | GraderContext,
        *,
        phase: str,
        attempt: int,
        session: _Session | None = None,
        nonce: str | None = None,
        issued_at: str | None = None,
    ) -> dict[str, Any]:
        # The driver supports both ``scripts.<module>`` and direct-script
        # imports.  Those routes can load two nominal AgentContext classes,
        # so isinstance() is not a reliable protocol discriminator here.
        is_agent_context = hasattr(context, "identity") and not hasattr(context, "request")
        if is_agent_context:
            identity = getattr(context, "identity", {})
            arm_id = context.arm.get("arm_id")
        else:
            request = getattr(context, "request", None)
            if request is None:
                raise UbuntuExecutionDriverProtocolError("grader context request is missing")
            identity = {
                "session_id": request.session_id,
                "thread_id": request.thread_id,
                "code_agent_id": request.code_agent_id,
                "patch_hash": request.patch_hash,
            }
            arm_id = request.arm_id
        return {
            "question_id": context.question_id,
            "mode": context.mode,
            "arm_id": arm_id,
            "round": context.round_number,
            "attempt": attempt,
            "run_id": context.run_id or context.runner_snapshot.get("run_id", ""),
            "nonce": nonce or secrets.token_hex(16),
            "issued_at": issued_at or datetime.now(timezone.utc).isoformat(),
            "phase": phase,
            "session_id": (session.session_id if session else identity.get("session_id", "")),
            "thread_id": (session.thread_id if session else identity.get("thread_id", "")),
            "code_agent_id": (session.code_agent_id if session else identity.get("code_agent_id", "")),
            "patch_hash": identity.get("patch_hash", ""),
            "tree_sha": "",
            "workspace": str(self._workspace),
            "artifact_dir": str(self._artifact_root),
            "image": self.config.container_image,
        }

    def _artifact_path(self, template: str, values: Mapping[str, Any], label: str) -> pathlib.Path:
        rendered = _render(template, values, label)
        candidate = pathlib.Path(rendered)
        path = candidate if candidate.is_absolute() else self._artifact_root / candidate
        resolved = path.resolve()
        if resolved != self._artifact_root and self._artifact_root not in resolved.parents:
            raise UbuntuExecutionDriverConfigError(f"{label} escapes artifact_dir")
        return resolved

    def _session_key(self, context: AgentContext) -> tuple[str, str, str]:
        return context.question_id, context.mode, _text(context.arm.get("arm_id"), "arm_id")

    def _session_path(self, context: AgentContext, values: Mapping[str, Any]) -> pathlib.Path:
        return self._artifact_path(self.config.session_state_path_template, values, "session state path")

    def _load_session(self, context: AgentContext, values: Mapping[str, Any]) -> _Session | None:
        key = self._session_key(context)
        current = self._sessions.get(key)
        if current is not None:
            return current
        path = self._session_path(context, values)
        if not path.exists():
            return None
        raw = _read_json(path, "session state")
        _strict_mapping(raw, frozenset({"session_id", "thread_id", "code_agent_id"}), "session state")
        session = _Session(
            session_id=_text(raw.get("session_id"), "session_id"),
            thread_id=_text(raw.get("thread_id"), "thread_id"),
            code_agent_id=_text(raw.get("code_agent_id"), "code_agent_id"),
        )
        self._sessions[key] = session
        return session

    def _session_from_context(self, context: AgentContext) -> _Session | None:
        """Use runner-owned identity when it already proves a real session."""

        fields = tuple(context.identity.get(name, "") for name in (
            "session_id", "thread_id", "code_agent_id"
        ))
        if not all(isinstance(value, str) and value.strip() for value in fields):
            return None
        if any(_SAFE_ID.fullmatch(value.strip()) is None for value in fields):
            raise UbuntuExecutionDriverProtocolError(
                "runner session identity contains unsafe characters"
            )
        return _Session(*(value.strip() for value in fields))

    def _session_from_partial_stdout(
        self,
        context: AgentContext,
        stdout: bytes,
        previous: _Session | None,
    ) -> _Session | None:
        """Recover only a session observed in the real Codex output stream."""

        if not stdout.strip():
            return None
        try:
            ids = _decode_jsonl_ids(stdout)
        except UnicodeDecodeError:
            return None
        if not ids.get("session_id"):
            return None
        expected = dataclasses.asdict(previous) if previous is not None else {}
        for field in ("session_id", "thread_id", "code_agent_id"):
            context_value = context.identity.get(field)
            if context_value:
                expected[field] = context_value
            observed = ids.get(field)
            if observed and expected.get(field) and observed != expected[field]:
                raise UbuntuExecutionDriverProtocolError(
                    f"Codex partial stdout {field} drifted from the real session"
                )
            if not observed and expected.get(field):
                ids[field] = expected[field]
        for field, value in ids.items():
            if value and _SAFE_ID.fullmatch(value) is None:
                raise UbuntuExecutionDriverProtocolError(
                    f"Codex partial stdout {field} has unsafe characters"
                )
        return _Session(
            ids.get("session_id", ""),
            ids.get("thread_id", ""),
            ids.get("code_agent_id", ""),
        )

    def _assert_workspace_clean_for_new_session(self) -> None:
        """Prevent a transport retry from silently starting on a dirty tree."""

        head = self._git_output(("rev-parse", "HEAD"), "workspace HEAD")
        if head != self._base_head:
            raise UbuntuExecutionDriverProtocolError(
                "cannot start a new Agent session after workspace HEAD drift"
            )
        status = self._git_output(("status", "--porcelain"), "workspace status")
        if status:
            raise UbuntuExecutionDriverProtocolError(
                "cannot start a new Agent session on a dirty workspace; "
                "resume the real Codex session or recover the workspace explicitly"
            )

    def _save_session(self, context: AgentContext, values: Mapping[str, Any], session: _Session) -> None:
        path = self._session_path(context, values)
        _write_bytes(path, _json_bytes(dataclasses.asdict(session), "session state"), "session state")
        self._sessions[self._session_key(context)] = session

    def _session_from_manifest(
        self,
        context: AgentContext,
        values: Mapping[str, Any],
        manifest: Mapping[str, Any],
        stdout: bytes,
        previous: _Session | None,
    ) -> _Session:
        del manifest
        try:
            ids = dynamic_identity(parse_codex_jsonl(stdout))
        except (ArtifactBridgeError, UnicodeDecodeError) as exc:
            raise UbuntuExecutionDriverProtocolError(
                f"Codex stdout identity/JSONL rejected: {exc}"
            ) from exc
        if previous is not None:
            expected = dataclasses.asdict(previous)
            for field, expected_value in expected.items():
                if not expected_value:
                    continue
                if ids.get(field, expected_value) != expected_value:
                    raise UbuntuExecutionDriverProtocolError(
                        f"Codex {field} drifted from persisted session"
                    )
                ids[field] = expected_value
        for field in ("session_id", "thread_id", "code_agent_id"):
            expected_value = context.identity.get(field)
            if expected_value and ids.get(field, expected_value) != expected_value:
                raise UbuntuExecutionDriverProtocolError(
                    f"Codex {field} drifted from runner identity"
                )
        for field in ("session_id", "thread_id", "code_agent_id"):
            if not ids.get(field):
                raise UbuntuExecutionDriverProtocolError(
                    f"Codex result is missing dynamic {field}"
                )
            if _SAFE_ID.fullmatch(ids[field]) is None:
                raise UbuntuExecutionDriverProtocolError(f"Codex {field} has unsafe characters")
        session = _Session(ids["session_id"], ids["thread_id"], ids["code_agent_id"])
        self._save_session(context, values, session)
        return session

    def _identity_envelopes(
        self,
        context: AgentContext,
        session: _Session,
        patch_hash: str,
        tree_sha: str,
    ) -> tuple[dict[str, Any], dict[str, dict[str, Any]]]:
        bindings = copy.deepcopy(dict(self.config.identity_bindings))
        for key in ("manifest", "dataset", "task", "source_image", "toolchain", "grader", "runner", "model", "prompt", "pricing", "scoring"):
            if key not in bindings:
                raise UbuntuExecutionDriverConfigError(f"identity_bindings missing {key}")
        bindings["task"] = {"id": context.question_id, "sha256": sha256_text(context.question_id)}
        bindings["session"] = {"id": session.session_id, "sha256": sha256_text(session.session_id)}
        bindings["thread"] = {"id": session.thread_id, "sha256": sha256_text(session.thread_id)}
        bindings["agent"] = {"id": session.code_agent_id, "sha256": sha256_text(session.code_agent_id)}
        bindings["patch"] = {"id": patch_hash, "sha256": sha256_text(patch_hash)}
        bindings["final_tree"] = {"tree_sha": tree_sha, "sha256": sha256_text(tree_sha)}
        prompt_binding = _mapping(bindings.get("prompt"), "identity_bindings.prompt")
        bindings["prompt"] = {
            "id": _text(prompt_binding.get("id"), "identity_bindings.prompt.id"),
            "sha256": self._prompt_binding_digest,
        }
        envelope = build_identity_envelope(
            bindings,
            receipt_signing_key=self.config.receipt_signing_key,
            grader_receipt_signing_key=self.config.grader_receipt_signing_key,
        )
        surfaces = {name: copy.deepcopy(envelope) for name in _IDENTITY_SURFACES}
        return envelope, surfaces

    def _git_patch_and_tree(self) -> tuple[str, str, tuple[str, ...]]:
        head = self._git_output(("rev-parse", "HEAD"), "current HEAD")
        if head != self._base_head:
            raise UbuntuExecutionDriverProtocolError("Agent changed HEAD; benchmark patches must remain uncommitted")
        diff = self._git_output_bytes(("diff", "--binary", "HEAD", "--"), "working-tree patch")
        names = self._git_output(("diff", "--name-only", "HEAD", "--"), "changed file list").splitlines()
        untracked = self._git_output(("ls-files", "--others", "--exclude-standard"), "untracked file list").splitlines()
        all_names = tuple(sorted(set(name.strip() for name in (*names, *untracked) if name.strip())))
        untracked_material = b"".join(
            path.encode("utf-8") + b"\0" + (self._workspace / path).read_bytes()
            for path in untracked
        )
        patch_hash = sha256_bytes(diff + untracked_material).removeprefix("sha256:")
        tree_sha = self._git_tree_sha()
        return patch_hash, tree_sha, all_names

    def _git_output_bytes(self, args: Sequence[str], label: str) -> bytes:
        command = ("git", "-C", str(self._workspace), *tuple(args))
        try:
            result = subprocess.run(command, capture_output=True, check=False)
        except OSError as exc:
            raise UbuntuExecutionDriverProtocolError(f"{label} command failed: {exc}") from exc
        if result.returncode != 0:
            raise UbuntuExecutionDriverProtocolError(
                f"{label} command exited {result.returncode}: {result.stderr.decode('utf-8', errors='replace')}"
            )
        return result.stdout

    def _git_tree_sha(self) -> str:
        index_path = pathlib.Path(tempfile.mkstemp(prefix="cross-file-index-")[1])
        try:
            index_path.unlink(missing_ok=True)
            env = {"GIT_INDEX_FILE": str(index_path)}
            self._git_output(("read-tree", "HEAD"), "temporary index seed", env=env)
            self._git_output(("add", "-A", "--"), "temporary index update", env=env)
            return self._git_output(("write-tree",), "final tree", env=env)
        finally:
            index_path.unlink(missing_ok=True)

    def _agent_paths(self, values: Mapping[str, Any]) -> dict[str, pathlib.Path]:
        usage_path = self._artifact_path(
            self.config.usage_path_template, values, "usage path"
        )
        metrics_path = (
            self._artifact_path(
                self.config.metrics_path_template,
                values,
                "execution metrics path",
            )
            if self.config.metrics_path_template is not None
            else usage_path.with_name(usage_path.stem + ".metrics.json")
        )
        if metrics_path == usage_path:
            raise UbuntuExecutionDriverConfigError(
                "execution metrics path must be distinct from usage path"
            )
        prompt_system_path = self._prompt_system_path(values)
        paths = {
            "result": self._artifact_path(self.config.agent_result_path_template, values, "Agent result path"),
            "stdout": self._artifact_path(self.config.agent_stdout_path_template, values, "Agent stdout path"),
            "stderr": self._artifact_path(self.config.agent_stderr_path_template, values, "Agent stderr path"),
            "evidence": self._artifact_path(self.config.arm_evidence_path_template, values, "arm evidence path"),
            "usage": usage_path,
            "metrics": metrics_path,
            "prompt_system": prompt_system_path,
            "server_generation": self._artifact_path(self.config.server_generation_path_template, values, "server generation path"),
        }
        if self.config.receipt_log_path_template is not None:
            paths.update(
                {
                    "receipt_log": self._artifact_path(
                        self.config.receipt_log_path_template, values, "receipt log path"
                    ),
                    "attestation": self._artifact_path(
                        self.config.attestation_path_template or "", values, "attestation path"
                    ),
                    "prompt_policy": self._artifact_path(
                        self.config.prompt_policy_path_template or "", values, "prompt policy path"
                    ),
                }
            )
        return paths

    def _prompt_system_path(self, values: Mapping[str, Any]) -> pathlib.Path:
        """Return the arm-local system/developer prompt file path."""

        mode = _text(values.get("mode"), "prompt system mode")
        if mode not in {"no_lsp", "with_lsp"}:
            raise UbuntuExecutionDriverProtocolError(f"unsupported arm mode {mode!r}")
        return (self._artifact_root / f"prompt-system-{mode}.md").resolve()

    def _system_prompt(self, mode: str) -> str:
        """Return only the system/developer prompt for one treatment arm."""

        if mode == "no_lsp":
            return self._prompt_contract["common_developer_instructions"]
        if mode == "with_lsp":
            return self._canonical_prompt_text
        raise UbuntuExecutionDriverProtocolError(f"unsupported arm mode {mode!r}")

    def _write_system_prompt(self, mode: str, path: pathlib.Path) -> str:
        prompt = self._system_prompt(mode)
        _write_bytes(path, prompt.encode("utf-8"), "system/developer prompt")
        return prompt

    def _command_environment(self, values: Mapping[str, Any], paths: Mapping[str, pathlib.Path]) -> dict[str, str]:
        environment = dict(os.environ)
        environment.update(self.config.environment)
        environment.update({
            "SUPER_DOLPHIN_CONTAINER_IMAGE": self.config.container_image,
            "SUPER_DOLPHIN_QUESTION_ID": str(values["question_id"]),
            "SUPER_DOLPHIN_ARM_ID": str(values["arm_id"]),
            "SUPER_DOLPHIN_MODE": str(values["mode"]),
            "SUPER_DOLPHIN_ROUND": str(values["round"]),
            "SUPER_DOLPHIN_ATTEMPT": str(values["attempt"]),
            "SUPER_DOLPHIN_RUN_ID": str(values.get("run_id", "")),
            "SUPER_DOLPHIN_NONCE": str(values.get("nonce", "")),
            "SUPER_DOLPHIN_ISSUED_AT": str(values.get("issued_at", "")),
            "SUPER_DOLPHIN_REQUEST_ID": str(values.get("request_id", "")),
            "SUPER_DOLPHIN_PHASE": str(values["phase"]),
            "SUPER_DOLPHIN_WORKSPACE": str(self._workspace),
            "SUPER_DOLPHIN_ARTIFACT_DIR": str(self._artifact_root),
            "SUPER_DOLPHIN_PROMPT_CONTRACT_SCHEMA": str(self._prompt_contract["schema"]),
            "SUPER_DOLPHIN_TASK_PROMPT_SHA256": str(self._prompt_contract["task_prompt_sha256"]),
            "SUPER_DOLPHIN_COMMON_PROMPT_SHA256": str(
                self._prompt_contract["common_developer_instructions_sha256"]
            ),
            "SUPER_DOLPHIN_WITH_LSP_ADDENDUM_SHA256": str(
                self._prompt_contract["with_lsp_addendum_sha256"]
            ),
            "SUPER_DOLPHIN_CANONICAL_PROMPT_SHA256": str(
                self._prompt_contract["canonical_prompt_sha256"]
            ),
        })
        for name, path in paths.items():
            environment[f"SUPER_DOLPHIN_{name.upper()}_PATH"] = str(path)
        if "metrics" in paths:
            environment["SUPER_DOLPHIN_EXECUTION_METRICS_PATH"] = str(paths["metrics"])
        if "receipt_log" in paths:
            environment["MCP_LSP_RECEIPT_LOG_PATH"] = str(paths["receipt_log"])
            environment["MCP_LSP_RECEIPT_INCLUDE_CANONICAL_IO"] = "1"
        return environment

    def _agent_command(self, context: AgentContext, session: _Session | None, prompt: str, values: Mapping[str, Any]) -> tuple[str, ...]:
        command = list(self.config.container_command)
        system_prompt_override = (
            "model_instructions_file="
            + json.dumps(str(self._prompt_system_path(values)), ensure_ascii=False)
        )
        if session is None:
            command.extend(self.config.codex_exec_command)
            command.extend(("--json", "-C", str(self._workspace), "-c", system_prompt_override))
        else:
            command.extend(self.config.codex_resume_command)
            # Codex 0.149 resume accepts global resume options before the
            # session ID and does not expose the top-level -C option.  Docker
            # already pins --workdir to this arm's workspace.
            command.extend(("--json", "-c", system_prompt_override, session.session_id))
        # The prompt was already rendered from prompt_template above and may
        # legitimately contain JSON examples.  Rendering it again would treat
        # JSON braces such as {"action": ...} as command placeholders.
        rendered = tuple(_render(token, values, "Codex command") for token in command)
        return (*rendered, prompt)

    def _build_executor_attestation(
        self,
        context: AgentContext,
        *,
        values: Mapping[str, Any],
        prompt: str,
        command: Sequence[str],
        result: SubprocessResult,
        session: _Session,
        paths: Mapping[str, pathlib.Path],
        patch_hash: str,
        tree_sha: str,
        server_generation: str,
    ) -> Mapping[str, Any]:
        """Build executor-owned evidence from transport output and native log."""

        if self._executor_signer is None or self.config.receipt_log_path_template is None:
            raise UbuntuExecutionDriverProtocolError(
                "executor attestation configuration is not enabled"
            )
        try:
            receipt_verifier = Ed25519Verifier.from_mapping(self.config.receipt_signing_key)
            policy = _read_json(paths["prompt_policy"], "prompt policy")
            policy = copy.deepcopy(dict(policy))
            actual_argv = list(command)
            policy["mode"] = context.mode
            policy["container_argv"] = actual_argv
            policy["container_argv_sha256"] = sha256_bytes(
                _json_bytes(actual_argv, "container argv")
            ).removeprefix("sha256:")
            if context.mode == "no_lsp":
                policy["model_egress"] = {
                    "allowed": True,
                    "proxy": self.config.environment.get("HTTPS_PROXY")
                    or self.config.environment.get("HTTP_PROXY"),
                }
                policy["mcp_sidecar"] = {"disabled": True}
                policy["mcp_ingress"] = {"count": 0}
                policy["stdout_digest"] = sha256_bytes(result.stdout)
                policy["sidecar_digest"] = sha256_bytes(result.stderr)
                policy["ingress_count"] = 0
                _write_bytes(
                    paths["prompt_policy"],
                    _json_bytes(policy, "prompt policy"),
                    "prompt policy",
                )
                receipt_log = b""
                if paths["receipt_log"].exists():
                    receipt_log = paths["receipt_log"].read_bytes()
                    if receipt_log:
                        raise UbuntuExecutionDriverProtocolError(
                            "No-LSP receipt log is non-empty"
                        )
                attestation = build_no_lsp_executor_attestation(
                    stdout=result.stdout,
                    # No-LSP has no sidecar process.  Codex stderr is retained
                    # as transport diagnostics, but it is not a sidecar JSONL
                    # observation and must not be parsed as one.
                    sidecar_observation=b"",
                    task=context.question_id,
                    arm=_text(context.arm.get("arm_id"), "arm_id"),
                    session=session.session_id,
                    thread=session.thread_id,
                    patch_hash=patch_hash,
                    final_tree_hash=tree_sha,
                    server_generation=server_generation,
                    run_id=_text(values.get("run_id"), "run_id"),
                    attempt=values["attempt"],
                    nonce=_text(values.get("nonce"), "nonce"),
                    issued_at=_text(values.get("issued_at"), "issued_at"),
                    prompt=prompt,
                    isolation_policy=policy,
                    signer=self._executor_signer,
                )
            else:
                # With-LSP localization remains Agent-directed.  Zero calls are
                # valid only when no supported production completion scope
                # exists; the closed-loop guard blocks changed production code
                # until patch-bound semantic and diagnostics receipts arrive.
                receipt_log = (
                    _read_bytes(paths["receipt_log"], "native receipt log")
                    if paths["receipt_log"].exists()
                    and paths["receipt_log"].stat().st_size > 0
                    else b""
                )
                attestation = build_executor_final_attestation(
                    receipt_log=receipt_log,
                    task=context.question_id,
                    arm=_text(context.arm.get("arm_id"), "arm_id"),
                    session=session.session_id,
                    thread=session.thread_id,
                    patch_hash=patch_hash,
                    final_tree_hash=tree_sha,
                    server_generation=server_generation,
                    mode=context.mode,
                    run_id=_text(values.get("run_id"), "run_id"),
                    attempt=values["attempt"],
                    nonce=_text(values.get("nonce"), "nonce"),
                    issued_at=_text(values.get("issued_at"), "issued_at"),
                    prompt=prompt,
                    isolation_policy=policy,
                    signer=self._executor_signer,
                    verifier=receipt_verifier,
                )
            _write_bytes(
                paths["attestation"],
                _json_bytes(attestation, "executor final attestation"),
                "executor final attestation",
            )
            # Consume before returning the artifact to the runner.  This is
            # the acceptance fence: a restart or concurrent caller cannot
            # accept the same signed attestation twice.
            self.consume_executor_attestation(attestation)
            return attestation
        except (ExecutorAttestationError, KeyError, OSError, TypeError, ValueError) as exc:
            raise UbuntuExecutionDriverProtocolError(
                f"executor final attestation rejected: {exc}"
            ) from exc

    def consume_executor_attestation(self, attestation: Mapping[str, Any]) -> None:
        """Consume executor evidence once through the durable replay index."""

        if self._attestation_replay_store is None:
            raise UbuntuExecutionDriverProtocolError(
                "executor attestation replay store is not configured"
            )
        try:
            self._attestation_replay_store.consume(attestation)
        except ExecutorAttestationError as exc:
            raise UbuntuExecutionDriverProtocolError(
                f"executor attestation replay rejected: {exc}"
            ) from exc

    def _agent_manifest(self, paths: Mapping[str, pathlib.Path]) -> Mapping[str, Any]:
        manifest = _read_json(paths["result"], "Agent result manifest")
        _strict_mapping(manifest, _AGENT_MANIFEST_FIELDS, "Agent result manifest")
        request = _mapping(manifest.get("request"), "Agent request")
        if not _REQUEST_REQUIRED_FIELDS.issubset(request):
            raise UbuntuExecutionDriverProtocolError(
                "Agent request must include changed_files, trajectory, and patch_hash"
            )
        return manifest

    def _agent_artifacts(self, context: AgentContext, manifest: Mapping[str, Any], session: _Session, paths: Mapping[str, pathlib.Path]) -> dict[str, Any]:
        patch_hash, tree_sha, actual_files = self._git_patch_and_tree()
        request = copy.deepcopy(dict(_mapping(manifest["request"], "Agent request")))
        # These fields cross an authority boundary and must never be accepted
        # from the Agent-authored result manifest.  The executor injects them
        # only after native receipt/attestation verification below.
        for field in (
            "server_receipts",
            "task_id",
            "server_generation",
            "identity_envelope",
            "receipt_public_key",
            "mcp_trajectory",
            "isolation_evidence",
        ):
            request.pop(field, None)
        declared_files = request.get("changed_files")
        if not isinstance(declared_files, Sequence) or isinstance(declared_files, (str, bytes, bytearray)):
            raise UbuntuExecutionDriverProtocolError("Agent request changed_files must be an array")
        declared = tuple(sorted(_text(item, "Agent changed_files item") for item in declared_files))
        if declared != actual_files:
            raise UbuntuExecutionDriverProtocolError(
                f"Agent changed_files do not match Git: declared={declared!r} actual={actual_files!r}"
            )
        supplied_patch = manifest.get("patch_hash", request.get("patch_hash"))
        if supplied_patch != patch_hash or request.get("patch_hash") != patch_hash:
            raise UbuntuExecutionDriverProtocolError("Agent patch_hash does not match the actual Git patch")
        supplied_tree = manifest.get("tree_sha")
        if supplied_tree is not None and supplied_tree != tree_sha:
            raise UbuntuExecutionDriverProtocolError("Agent tree_sha does not match the actual Git tree")
        envelope, surfaces = self._identity_envelopes(context, session, patch_hash, tree_sha)
        server_generation = _text(_read_bytes(paths["server_generation"], "server generation").decode("utf-8"), "server_generation")
        usage = _read_json(paths["usage"], "usage")
        request["patch_hash"] = patch_hash
        request["production_file_changes"] = [
            {"path": path, "production": _is_production_change(path)}
            for path in declared
        ]
        if context.mode == "with_lsp" and "receipt_log" in paths:
            # Canonical authority must be explicit even when the Agent made no
            # MCP call.  Without the empty list RoundInput falls back to the
            # legacy trajectory seam and cannot open patch-bound recovery.
            request["server_receipts"] = _read_jsonl_objects(
                paths["receipt_log"], "canonical MCP receipt log"
            )
            request["task_id"] = context.question_id
            request["server_generation"] = server_generation
            request["identity_envelope"] = copy.deepcopy(dict(envelope))
            request["receipt_public_key"] = copy.deepcopy(
                dict(self.config.receipt_signing_key)
            )
        artifacts: dict[str, Any] = {
            "request": request,
            "identity_envelope": envelope,
            "identity_envelopes": surfaces,
            "server_generation": server_generation,
            "changed_files": list(declared),
            "usage": copy.deepcopy(dict(usage)),
        }
        if self._executor_signer is None:
            evidence = _read_json(paths["evidence"], "canonical arm evidence")
            canonical_evidence = validate_canonical_arm_evidence(
                evidence,
                task=context.question_id,
                arm=context.arm["arm_id"],
                session=session.session_id,
                thread=session.thread_id,
                changed_files=declared,
                server_generation=server_generation,
                patch_hash=patch_hash,
                identity_envelope=envelope,
            )
            artifacts["arm_evidence"] = canonical_evidence.to_dict()
        return artifacts

    @staticmethod
    def _agent_transport_retry_reason(result: SubprocessResult) -> str | None:
        """Return a bounded-retry reason for a rate-limit/stream failure."""

        combined = (result.stderr + b"\n" + result.stdout).lower()
        for marker in _AGENT_RETRY_MARKERS:
            if marker in combined:
                return marker.decode("utf-8")
        return None

    @staticmethod
    def _agent_turn_completed(stdout: bytes) -> bool:
        """Check completion without treating malformed partial JSONL as success."""

        try:
            events = parse_codex_jsonl(stdout)
        except (ArtifactBridgeError, UnicodeDecodeError):
            return False
        return any(event.get("type") == "turn.completed" for event in events)

    def _prompt_metric_sections(self, task_prompt: str, mode: str) -> dict[str, str]:
        """Return the prompt parts whose exact bytes are recorded in metrics."""

        sections = {
            "task": task_prompt,
            "common_developer_instructions": self._prompt_contract[
                "common_developer_instructions"
            ],
            "with_lsp_addendum": self._prompt_contract["with_lsp_addendum"],
        }
        sections["system"] = self._system_prompt(mode)
        return sections

    def _write_unavailable_metrics(
        self,
        paths: Mapping[str, pathlib.Path],
        *,
        mode: str,
        prompt: str,
        reason: str,
        attempts: Sequence[Mapping[str, Any]],
    ) -> None:
        try:
            metrics = build_unavailable_execution_metrics(
                mode=mode,
                prompt=prompt,
                reason=reason,
                prompt_sections=self._prompt_metric_sections(prompt, mode),
                attempts=attempts,
            )
            _write_bytes(
                paths["metrics"],
                _json_bytes(metrics, "unavailable execution metrics"),
                "unavailable execution metrics",
            )
        except ExecutionMetricsError as exc:
            raise UbuntuExecutionDriverProtocolError(
                f"execution metrics rejected: {exc}"
            ) from exc

    def _record_transport_failure(
        self,
        paths: Mapping[str, pathlib.Path],
        *,
        mode: str,
        prompt: str,
        reason: str,
        attempts: Sequence[Mapping[str, Any]],
        stdout: bytes = b"",
        stderr: bytes = b"",
    ) -> None:
        """Persist an unavailable transport record before fail-closed exit."""

        _write_bytes(paths["stdout"], stdout, "Agent stdout")
        _write_bytes(paths["stderr"], stderr, "Agent stderr")
        self._write_unavailable_metrics(
            paths,
            mode=mode,
            prompt=prompt,
            reason=reason,
            attempts=attempts,
        )

    def _run_agent(self, context: AgentContext, *, phase: str, prompt_suffix: str = "") -> Mapping[str, Any]:
        attempt = context.runner_snapshot.get("agent_attempt", 1)
        if isinstance(attempt, bool) or not isinstance(attempt, int) or attempt < 1:
            raise UbuntuExecutionDriverProtocolError("agent_attempt must be a positive integer")
        nonce = secrets.token_hex(16)
        issued_at = datetime.now(timezone.utc).isoformat()
        values = self._values(
            context,
            phase=phase,
            attempt=attempt,
            nonce=nonce,
            issued_at=issued_at,
        )
        previous = self._load_session(context, values)
        if previous is None:
            previous = self._session_from_context(context)
        prompt = (
            self.config.prompt_template
            if self.config.prompt_contract_explicit
            else _render(
                self.config.prompt_template,
                {
                    **values,
                    "session_id": previous.session_id if previous else "",
                    "thread_id": previous.thread_id if previous else "",
                },
                "prompt_template",
            )
        )
        if context.mode not in {"with_lsp", "no_lsp"}:
            raise UbuntuExecutionDriverProtocolError(f"unsupported arm mode {context.mode!r}")
        if prompt_suffix:
            prompt = f"{prompt}\n{prompt_suffix}"
        paths = self._agent_paths(values)
        self._write_system_prompt(context.mode, paths["prompt_system"])
        prompt_sections = self._prompt_metric_sections(prompt, context.mode)
        if self._executor_signer is not None:
            policy = {
                "mode": context.mode,
                "prompt": prompt,
                "prompt_contract": {
                    key: self._prompt_contract[key]
                    for key in (
                        "schema",
                        "task_prompt_sha256",
                        "common_developer_instructions_sha256",
                        "with_lsp_addendum_sha256",
                        "canonical_prompt_sha256",
                        "allowed_arm_differences",
                    )
                },
                "system_prompt_path": str(paths["prompt_system"]),
                "system_prompt_sha256": sha256_bytes(
                    paths["prompt_system"].read_bytes()
                ).removeprefix("sha256:"),
            }
            if context.mode == "no_lsp":
                policy["model_egress"] = {
                    "allowed": True,
                    "proxy": self.config.environment.get("HTTPS_PROXY")
                    or self.config.environment.get("HTTP_PROXY"),
                }
                policy["mcp_sidecar"] = {"disabled": True}
                policy["mcp_ingress"] = {"count": 0}
            _write_bytes(
                paths["prompt_policy"],
                _json_bytes(policy, "prompt policy"),
                "prompt policy",
            )
        retry_stderr: list[bytes] = []
        transport_attempts: list[dict[str, Any]] = []
        result: SubprocessResult | None = None
        retry_session = previous
        command: tuple[str, ...] = ()
        for transport_attempt in range(1, _MAX_AGENT_TRANSPORT_ATTEMPTS + 1):
            current_session = retry_session
            if current_session is None:
                try:
                    self._assert_workspace_clean_for_new_session()
                except UbuntuExecutionDriverProtocolError as exc:
                    self._record_transport_failure(
                        paths,
                        mode=context.mode,
                        prompt=prompt,
                        reason=str(exc),
                        attempts=transport_attempts,
                    )
                    raise
            command = self._agent_command(context, current_session, prompt, values)
            transport_exception: Exception | None = None
            try:
                result = self.subprocess.run(
                    command,
                    cwd=self._workspace,
                    env=self._command_environment(values, paths),
                )
            except (ConnectionError, OSError, TimeoutError) as exc:
                transport_exception = exc
                result = SubprocessResult(
                    argv=command,
                    returncode=1,
                    stdout=b"",
                    stderr=f"{type(exc).__name__}: {exc}".encode("utf-8", errors="replace"),
                )
            assert result is not None
            reason = self._agent_transport_retry_reason(result)
            retryable = transport_exception is not None or (
                reason is not None
                and not (
                    result.returncode == 0 and self._agent_turn_completed(result.stdout)
                )
            )
            transport_attempts.append(
                {
                    "attempt": transport_attempt,
                    "returncode": result.returncode,
                    "retryable": retryable,
                    "reason": reason or (
                        type(transport_exception).__name__
                        if transport_exception is not None
                        else None
                    ),
                    "resumed": current_session is not None,
                    "session_id": current_session.session_id if current_session else None,
                    "stdout_bytes": len(result.stdout),
                    "stderr_bytes": len(result.stderr),
                }
            )
            if not retryable or transport_attempt == _MAX_AGENT_TRANSPORT_ATTEMPTS:
                break
            retry_stderr.append(result.stderr)
            try:
                recovered = self._session_from_partial_stdout(
                    context,
                    result.stdout,
                    current_session,
                )
            except UbuntuExecutionDriverProtocolError as exc:
                self._record_transport_failure(
                    paths,
                    mode=context.mode,
                    prompt=prompt,
                    reason=str(exc),
                    attempts=transport_attempts,
                    stdout=result.stdout,
                    stderr=b"\n".join((*retry_stderr, result.stderr)),
                )
                raise
            if recovered is not None:
                retry_session = recovered
                if recovered.session_id and recovered.thread_id and recovered.code_agent_id:
                    self._save_session(context, values, recovered)
            elif current_session is None:
                # A fresh Agent is permitted only while the candidate tree is
                # still exactly clean.  This check happens before the retry,
                # not after a second Agent has already touched the tree.
                try:
                    self._assert_workspace_clean_for_new_session()
                except UbuntuExecutionDriverProtocolError as exc:
                    self._record_transport_failure(
                        paths,
                        mode=context.mode,
                        prompt=prompt,
                        reason=str(exc),
                        attempts=transport_attempts,
                        stdout=result.stdout,
                        stderr=b"\n".join((*retry_stderr, result.stderr)),
                    )
                    raise
                retry_session = None
            backoff_index = transport_attempt - 1
            if backoff_index < len(_AGENT_RETRY_BACKOFF_SECONDS):
                time.sleep(_AGENT_RETRY_BACKOFF_SECONDS[backoff_index])
        assert result is not None
        if retry_stderr:
            result = dataclasses.replace(
                result,
                stderr=b"\n".join((*retry_stderr, result.stderr)),
            )
        _write_bytes(paths["stdout"], result.stdout, "Agent stdout")
        _write_bytes(paths["stderr"], result.stderr, "Agent stderr")
        try:
            events = parse_codex_jsonl(result.stdout)
            patch_hash, tree_sha, changed_files = self._git_patch_and_tree()
            manifest = build_agent_manifest(
                events=events,
                patch_hash=patch_hash,
                tree_sha=tree_sha,
                changed_files=changed_files,
                mode=context.mode,
            )
            _write_bytes(
                paths["result"],
                _json_bytes(manifest, "executor Agent manifest"),
                "executor Agent manifest",
            )
            usage: dict[str, Any]
            try:
                usage = usage_from_events(events)
            except ArtifactBridgeError as exc:
                if result.returncode == 0 and not any(
                    event.get("type") == "turn.failed" for event in events
                ):
                    self._write_unavailable_metrics(
                        paths,
                        mode=context.mode,
                        prompt=prompt,
                        reason=f"successful Codex stdout has no complete usage: {exc}",
                        attempts=transport_attempts,
                    )
                    raise UbuntuExecutionDriverProtocolError(
                        f"successful Codex stdout has no complete usage: {exc}"
                    ) from exc
                usage = {
                    "status": "unavailable",
                    "reason": "Codex transport failure; usage was not emitted",
                }
            if "reasoning_tokens" in usage:
                usage["reasoning_output_tokens"] = usage.pop("reasoning_tokens")
            if all(
                key in usage
                for key in ("input_tokens", "cached_input_tokens", "output_tokens")
            ):
                try:
                    metrics = build_execution_metrics(
                        mode=context.mode,
                        prompt=prompt,
                        events=events,
                        usage=usage,
                        prompt_sections=prompt_sections,
                        attempts=transport_attempts,
                    )
                except ExecutionMetricsError as exc:
                    raise UbuntuExecutionDriverProtocolError(
                        f"execution metrics rejected: {exc}"
                    ) from exc
                _write_bytes(
                    paths["metrics"],
                    _json_bytes(metrics, "execution metrics"),
                    "execution metrics",
                )
            else:
                self._write_unavailable_metrics(
                    paths,
                    mode=context.mode,
                    prompt=prompt,
                    reason="Codex transport failure; raw token usage was not emitted",
                    attempts=transport_attempts,
                )
            _write_bytes(
                paths["usage"],
                _json_bytes(usage, "executor usage"),
                "executor usage",
            )
            # Server generation is an executor-owned run boundary.  Real
            # Codex/mcp-lsp processes do not write the unit-test sidecar file,
            # so bind this arm attempt to its executor-issued nonce rather
            # than trusting an Agent-provided generation value.
            if not paths["server_generation"].exists():
                _write_bytes(
                    paths["server_generation"],
                    (
                        f"{context.mode}:{_text(values.get('nonce'), 'nonce')}\n"
                    ).encode("utf-8"),
                    "executor server generation",
                )
        except (ArtifactBridgeError, UnicodeDecodeError) as exc:
            self._write_unavailable_metrics(
                paths,
                mode=context.mode,
                prompt=prompt,
                reason=f"Codex JSONL conversion failed: {exc}",
                attempts=transport_attempts,
            )
            raise UbuntuExecutionDriverProtocolError(
                f"executor Codex stdout conversion rejected: {exc}"
            ) from exc
        manifest = self._agent_manifest(paths)
        session = self._session_from_manifest(context, values, manifest, result.stdout, previous)
        artifacts = self._agent_artifacts(context, manifest, session, paths)
        if phase == "evidence_recovery":
            recovery_request = copy.deepcopy(dict(artifacts["request"]))
            recovery_request["evidence_attempt"] = attempt
            artifacts = {**artifacts, "request": recovery_request}
        if self._executor_signer is not None:
            patch_hash, tree_sha, _ = self._git_patch_and_tree()
            server_generation = _text(
                _read_bytes(paths["server_generation"], "server generation").decode("utf-8"),
                "server_generation",
            )
            attestation = self._build_executor_attestation(
                context,
                values=values,
                prompt=prompt,
                command=command,
                result=result,
                session=session,
                paths=paths,
                patch_hash=patch_hash,
                tree_sha=tree_sha,
                server_generation=server_generation,
            )
            artifacts = {
                **artifacts,
                "executor_attestation": copy.deepcopy(dict(attestation)),
                "receipt_log": (
                    paths["receipt_log"].read_text(encoding="utf-8")
                    if context.mode == "with_lsp" and paths["receipt_log"].exists()
                    else ""
                ),
                "executor_attestation_public_key": self._executor_signer.public_key.to_dict(),
            }
            if context.mode == "with_lsp" and artifacts["receipt_log"]:
                # Native mcp-lsp emits its own signed Go v2 hash chain.  It is
                # not the legacy Python MCP receipt schema.  Project only
                # after the executor attestation and native chain have both
                # verified, then feed the guard ordinary semantic events.
                projection = derive_canonical_arm_evidence_from_attestation(
                    attestation,
                    receipt_log=artifacts["receipt_log"],
                    executor_verifier=Ed25519Verifier(self._executor_signer.public_key),
                    receipt_verifier=Ed25519Verifier.from_mapping(
                        self.config.receipt_signing_key
                    ),
                    task=context.question_id,
                    arm=_text(context.arm.get("arm_id"), "arm_id"),
                    session=session.session_id,
                    thread=session.thread_id,
                    patch_hash=patch_hash,
                    final_tree_hash=tree_sha,
                    server_generation=server_generation,
                    mode=context.mode,
                    run_id=_text(values.get("run_id"), "run_id"),
                )
                projected_request = copy.deepcopy(dict(artifacts["request"]))
                projected_request.pop("server_receipts", None)
                projected_request["mcp_trajectory"] = [
                    {
                        "type": "mcp_tool_call",
                        "tool": item.tool,
                        "arguments": copy.deepcopy(dict(item.arguments)),
                        "result": copy.deepcopy(item.result),
                    }
                    for item in projection.receipts
                ]
                artifacts = {**artifacts, "request": projected_request}
        if result.returncode != 0 or any(
            event.get("type") == "turn.failed" for event in events
        ):
            return {**artifacts, "transport_error": f"Codex exited with status {result.returncode}"}
        return artifacts

    def _grader_values(self, context: GraderContext, *, phase: str, attempt: int) -> dict[str, Any]:
        values = self._values(context, phase=phase, attempt=attempt)
        patch_hash, tree_sha, _ = self._git_patch_and_tree()
        if patch_hash != context.request.patch_hash:
            raise UbuntuExecutionDriverProtocolError("grader request patch_hash drifted from actual Git patch")
        values["patch_hash"] = patch_hash
        values["tree_sha"] = tree_sha
        values["request_id"] = (
            f"grader-{context.question_id}-{context.request.arm_id}-"
            f"{context.round_number}-{attempt}"
        )
        values["instance_ids"] = context.question_id
        values["predictions_path"] = ""
        for name in ("dataset_name", "split", "max_workers", "run_id", "timeout", "cache_level", "clean", "namespace"):
            environment_key = f"SUPER_DOLPHIN_GRADER_{name.upper()}"
            if environment_key in self.config.environment:
                values[name] = self.config.environment[environment_key]
        # SWE-bench derives evaluator container names from instance_id and
        # run_id, not model_name.  A shared batch run_id makes A/B arms and
        # retries collide with Docker 409.  Bind every grader attempt to its
        # already-unique request identity.
        values["run_id"] = values["request_id"]
        return values

    def _grader_paths(self, values: Mapping[str, Any]) -> dict[str, pathlib.Path]:
        return {
            "request": self._artifact_path(self.config.grader_request_path_template, values, "grader request path"),
            "feedback": self._artifact_path(self.config.grader_feedback_path_template, values, "grader feedback path"),
            "report": self._artifact_path(self.config.grader_report_path_template, values, "grader report path"),
            "test_output": self._artifact_path(self.config.grader_test_output_path_template, values, "grader test output path"),
            "stdout": self._artifact_path(self.config.grader_stdout_path_template, values, "grader stdout path"),
            "stderr": self._artifact_path(self.config.grader_stderr_path_template, values, "grader stderr path"),
        }

    def _grader_command(self, values: Mapping[str, Any], paths: Mapping[str, pathlib.Path]) -> tuple[str, ...]:
        expanded = {
            **values,
            **{f"{name}_path": str(path) for name, path in paths.items()},
        }
        expanded["predictions_path"] = _text(values.get("predictions_path"), "predictions_path")
        # SWE-bench is a host-side orchestrator: it needs the host Docker
        # daemon, dataset, cache and its complete virtual environment.  The
        # episode container is only the Codex Agent sandbox and may have an
        # older glibc.  Never prefix grader argv with the Agent docker command.
        return tuple(_render(token, expanded, "grader command") for token in self.config.grader_command)

    def _record_grader_receipt(
        self,
        receipt: Mapping[str, Any],
        *,
        request_id: str,
    ) -> str:
        """Persist request-id and receipt digest, rejecting replay or drift."""

        digest = hashlib.sha256(canonical_json(receipt).encode("utf-8")).hexdigest()
        path = self._grader_receipt_ledger
        entries: dict[str, str] = {}
        if path.exists():
            existing = _read_json(path, "grader receipt ledger")
            if existing.get("schema") != "super-dolphin/grader-receipt-ledger/v1":
                raise UbuntuExecutionDriverProtocolError("unsupported grader receipt ledger schema")
            _strict_mapping(
                existing,
                frozenset({"schema", "receipts"}),
                "grader receipt ledger",
            )
            raw_entries = existing.get("receipts")
            if not isinstance(raw_entries, Mapping):
                raise UbuntuExecutionDriverProtocolError("grader receipt ledger receipts must be an object")
            for key, value in raw_entries.items():
                entry_key = _text(key, "grader receipt request_id")
                entry_digest = _text(value, "grader receipt digest")
                if _SHA256_DIGEST.fullmatch(entry_digest) is None:
                    raise UbuntuExecutionDriverProtocolError("grader receipt ledger digest is invalid")
                entries[entry_key] = entry_digest
        previous = entries.get(request_id)
        if previous is not None:
            raise UbuntuExecutionDriverProtocolError(
                f"grader receipt request_id replayed: {request_id}"
            )
        if digest in entries.values():
            raise UbuntuExecutionDriverProtocolError("grader receipt digest replayed")
        entries[request_id] = digest
        _write_bytes(
            path,
            _json_bytes(
                {"schema": "super-dolphin/grader-receipt-ledger/v1", "receipts": entries},
                "grader receipt ledger",
            ),
            "grader receipt ledger",
        )
        return digest

    def _grader_verifier_key(self) -> Mapping[str, Any]:
        """Return the pinned grader-role public key for receipt verification."""

        if self.config.grader_receipt_signing_key is None:
            raise UbuntuExecutionDriverProtocolError(
                "grader_receipt_signing_key is required for live grader execution"
            )
        return self.config.grader_receipt_signing_key

    def _assert_grader_request_fresh(self, request_id: str) -> None:
        """Reject a persisted request replay before consuming a grader slot."""

        path = self._grader_receipt_ledger
        if not path.exists():
            return
        existing = _read_json(path, "grader receipt ledger")
        _strict_mapping(
            existing,
            frozenset({"schema", "receipts"}),
            "grader receipt ledger",
        )
        if existing.get("schema") != "super-dolphin/grader-receipt-ledger/v1":
            raise UbuntuExecutionDriverProtocolError(
                "unsupported grader receipt ledger schema"
            )
        receipts = existing.get("receipts")
        if not isinstance(receipts, Mapping):
            raise UbuntuExecutionDriverProtocolError(
                "grader receipt ledger receipts must be an object"
            )
        if request_id in receipts:
            raise UbuntuExecutionDriverProtocolError(
                f"grader receipt request_id replayed: {request_id}"
            )

    def _run_grader(self, context: GraderContext, *, phase: str, attempt: int) -> Mapping[str, Any]:
        values = self._grader_values(context, phase=phase, attempt=attempt)
        self._assert_grader_request_fresh(values["request_id"])
        paths = self._grader_paths(values)
        request = dataclasses.asdict(context.request)
        _write_bytes(paths["request"], _json_bytes(request, "grader request"), "grader request")
        predictions_path = paths["request"].with_name(
            paths["request"].stem + ".predictions.json"
        )
        model_name = _text(context.request.arm_id, "grader model name")
        try:
            model_patch = self._git_output_bytes(
                ("diff", "--binary", "HEAD"), "grader model patch"
            ).decode("utf-8")
        except UnicodeDecodeError as exc:
            raise UbuntuExecutionDriverProtocolError(
                "grader model patch is not UTF-8"
            ) from exc
        prediction = [{
            "instance_id": context.question_id,
            "model_name_or_path": model_name,
            "model_patch": model_patch,
        }]
        _write_bytes(
            predictions_path,
            _json_bytes(prediction, "SWE-bench predictions"),
            "SWE-bench predictions",
        )
        values["predictions_path"] = str(predictions_path)
        environment = self._command_environment(values, paths)
        with self._grader_slots:
            result = self.subprocess.run(
                self._grader_command(values, paths),
                cwd=self._artifact_root,
                env=environment,
            )
        _write_bytes(paths["stdout"], result.stdout, "grader stdout")
        _write_bytes(paths["stderr"], result.stderr, "grader stderr")
        if result.returncode != 0:
            return {"transport_error": f"SWE-bench exited with status {result.returncode}"}
        if not paths["report"].is_file() or not paths["test_output"].is_file():
            native_root = (
                self._artifact_root
                / "logs"
                / "run_evaluation"
                / _text(values.get("run_id"), "grader run_id")
                / model_name
                / context.question_id
            )
            native_report = native_root / "report.json"
            native_test_output = native_root / "test_output.txt"
            if not native_report.is_file() or not native_test_output.is_file():
                raise UbuntuExecutionDriverProtocolError(
                    f"SWE-bench completed without instance report/test output: {native_root}"
                )
            native_value = _read_json(native_report, "native SWE-bench instance report")
            instance_value = native_value.get(context.question_id)
            if not isinstance(instance_value, Mapping):
                raise UbuntuExecutionDriverProtocolError(
                    "native SWE-bench report does not contain the requested instance"
                )
            resolved = instance_value.get("resolved") is True
            normalized_report = {
                "schema_version": 2,
                "resolved_ids": [context.question_id] if resolved else [],
                "unresolved_ids": [] if resolved else [context.question_id],
                "error_ids": [],
                "completed_ids": [context.question_id],
            }
            native_copy = paths["report"].with_name(paths["report"].stem + ".native.json")
            shutil.copyfile(native_report, native_copy)
            _write_bytes(
                paths["report"],
                _json_bytes(normalized_report, "normalized SWE-bench report"),
                "normalized SWE-bench report",
            )
            shutil.copyfile(native_test_output, paths["test_output"])
        if not paths["feedback"].is_file():
            grader_key_path = self.config.environment.get(
                "SUPER_DOLPHIN_GRADER_RECEIPT_SIGNING_KEY_PATH"
            )
            if not grader_key_path or self.config.grader_receipt_signing_key is None:
                raise UbuntuExecutionDriverProtocolError(
                    "grader private/public signing key configuration is required"
                )
            grader_signer = Ed25519Signer.from_file(
                grader_key_path,
                key_id=_text(
                    self.config.grader_receipt_signing_key.get("key_id"),
                    "grader receipt key_id",
                ),
            )
            signed_feedback = build_signed_feedback(
                task=context.question_id,
                arm=context.request.arm_id,
                session=context.request.session_id,
                thread=context.request.thread_id,
                patch_hash=context.request.patch_hash,
                final_tree_hash=_text(values.get("tree_sha"), "grader tree_sha"),
                request_id=_text(values.get("request_id"), "grader request_id"),
                seq=attempt,
                signer=grader_signer,
                report=paths["report"],
                test_output=paths["test_output"],
                stdout=paths["stdout"],
                stderr=paths["stderr"],
            )
            _write_bytes(
                paths["feedback"],
                _json_bytes(signed_feedback, "signed grader feedback"),
                "signed grader feedback",
            )
        feedback = _read_json(paths["feedback"], "structured grader feedback")
        report = _read_bytes(paths["report"], "official grader report")
        test_output = _read_bytes(paths["test_output"], "official test output")
        current_patch, final_tree, _ = self._git_patch_and_tree()
        if current_patch != context.request.patch_hash:
            raise UbuntuExecutionDriverProtocolError("grader changed the candidate patch")
        if final_tree != values["tree_sha"]:
            raise UbuntuExecutionDriverProtocolError("grader final tree drifted during execution")
        if not isinstance(feedback.get("receipt"), Mapping):
            raise UbuntuExecutionDriverProtocolError("grader feedback must contain one signed receipt")
        receipt = feedback["receipt"]
        request_id = _text(receipt.get("request_id"), "grader receipt request_id")
        if request_id != values["request_id"]:
            raise UbuntuExecutionDriverProtocolError(
                "grader receipt request_id does not match executor request"
            )
        try:
            artifact = build_grader_artifact(
                task=context.question_id,
                request_id=values["request_id"],
                report=report,
                test_output=test_output,
                stdout=result.stdout,
                stderr=result.stderr,
            )
            verifier = verifier_from(public_key=self._grader_verifier_key())
            result_value = validate_grader_receipt(
                receipt,
                verifier=verifier,
                task=context.question_id,
                arm=context.request.arm_id,
                session=context.request.session_id,
                thread=context.request.thread_id,
                patch_hash=context.request.patch_hash,
                grader_artifact=artifact.binding,
                final_tree_hash=final_tree,
            )
            validate_grader_artifact_hashes(result_value, artifact)
        except (GraderBridgeError, ValueError, TypeError) as exc:
            raise UbuntuExecutionDriverProtocolError(
                f"grader bridge receipt rejected: {exc}"
            ) from exc
        receipt_digest = self._record_grader_receipt(receipt, request_id=request_id)
        return {
            "feedback": feedback,
            "official_report": report,
            "official_test_output": test_output,
            "official_stdout": result.stdout,
            "official_stderr": result.stderr,
            "grader_request_id": values["request_id"],
            "grader_receipt_public_key": copy.deepcopy(dict(self._grader_verifier_key())),
            "grader_receipt_digest": receipt_digest,
        }

    def run_agent(self, context: AgentContext) -> Mapping[str, Any]:
        """Start or resume Codex, always returning fresh dynamic artifacts."""

        return self._run_agent(context, phase="agent")

    def run_grader(self, context: GraderContext) -> Mapping[str, Any]:
        """Run SWE-bench and persist its three official output surfaces."""

        return self._run_grader(context, phase="grader", attempt=1)

    def run_evidence_recovery(self, context: AgentContext, pending: RunnerStep) -> Mapping[str, Any]:
        """Resume the same Codex session for completion evidence only."""

        return self._run_agent(
            context,
            phase="evidence_recovery",
            prompt_suffix=(
                "Evidence recovery only: inspect the pending semantic-impact and diagnostics "
                "scope with structure/xref/diagnostics as required, then return the "
                "configured result manifest. Do not start a new quality round or change the patch. "
                f"Pending runner status: {pending.status}."
            ),
        )

    def run_infra_retry(self, context: GraderContext, pending: RunnerStep) -> Mapping[str, Any]:
        """Retry only the official grader in the same quality round."""

        attempt = max(1, context.runner_snapshot.get("infra_attempts", 0) + 1)
        return self._run_grader(context, phase="infra_retry", attempt=attempt)


class UbuntuExecutionDriverFactory:
    """Create one immutable-base Git worktree and driver per benchmark arm."""

    def __init__(
        self,
        config: UbuntuExecutionDriverConfig,
        *,
        grader_semaphore: threading.BoundedSemaphore | None = None,
        subprocess_adapter: SubprocessAdapter | None = None,
        container_command_factory: Callable[
            [str, str, str, pathlib.Path, pathlib.Path, pathlib.Path], Sequence[str]
        ] | None = None,
        arm_provision_factory: Callable[
            [str, str, str, pathlib.Path, pathlib.Path, pathlib.Path], None
        ] | None = None,
    ) -> None:
        self.config = config
        self._source_workspace = config.workspace.resolve()
        self._worktree_root = config.artifact_dir.resolve() / "arm-worktrees"
        self._worktree_root.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()
        self._grader_slots = grader_semaphore or threading.BoundedSemaphore(
            config.grader_slots
        )
        self._subprocess_adapter = subprocess_adapter
        self._container_command_factory = container_command_factory
        self._arm_provision_factory = arm_provision_factory

    def _git(self, *args: str) -> str:
        result = subprocess.run(
            ("git", "-C", str(self._source_workspace), *args),
            capture_output=True,
            check=False,
            text=True,
        )
        if result.returncode != 0:
            raise UbuntuExecutionDriverConfigError(
                f"arm worktree command failed ({' '.join(args)}): {result.stderr.strip()}"
            )
        return result.stdout.strip()

    def for_arm(
        self, question_id: str, mode: str, arm: Mapping[str, Any]
    ) -> UbuntuExecutionDriver:
        arm_id = _text(arm.get("arm_id"), "arm_id")
        token = hashlib.sha256(f"{question_id}\0{mode}\0{arm_id}".encode()).hexdigest()[:24]
        workspace = self._worktree_root / token
        base_commit = _text(
            _mapping(
                self.config.identity_bindings.get("base"), "identity_bindings.base"
            ).get("commit_sha"),
            "identity_bindings.base.commit_sha",
        )
        with self._lock:
            if workspace.exists():
                head = subprocess.run(
                    ("git", "-C", str(workspace), "rev-parse", "HEAD"),
                    capture_output=True,
                    text=True,
                    check=False,
                )
                dirty = subprocess.run(
                    ("git", "-C", str(workspace), "status", "--porcelain"),
                    capture_output=True,
                    text=True,
                    check=False,
                )
                if head.returncode != 0 or head.stdout.strip() != base_commit or dirty.stdout.strip():
                    raise UbuntuExecutionDriverConfigError(
                        f"existing arm workspace is not clean at the configured base: {workspace}"
                    )
            else:
                self._git("worktree", "add", "--detach", str(workspace), base_commit)
        artifact_dir = (self.config.artifact_dir / "arms" / token).resolve()
        workspace = workspace.resolve()
        codex_home = (artifact_dir / "codex-home").resolve()
        codex_home.mkdir(parents=True, exist_ok=True)
        if self._arm_provision_factory is not None:
            self._arm_provision_factory(
                question_id, mode, arm_id, workspace, artifact_dir, codex_home
            )
        arm_config = dataclasses.replace(
            self.config,
            workspace=workspace,
            artifact_dir=artifact_dir,
        )
        if self._container_command_factory is not None:
            command = self._container_command_factory(
                question_id,
                mode,
                arm_id,
                workspace,
                artifact_dir,
                codex_home,
            )
            arm_config = dataclasses.replace(
                arm_config,
                container_command=_argv(command, "arm container_command"),
            )
        return UbuntuExecutionDriver(
            arm_config,
            subprocess_adapter=self._subprocess_adapter,
            grader_semaphore=self._grader_slots,
        )


class QuestionMappedExecutionDriverFactory:
    """Route each question to its own immutable image/workspace configuration."""

    def __init__(
        self,
        configs: Mapping[str, UbuntuExecutionDriverConfig],
        *,
        subprocess_adapter: SubprocessAdapter | None = None,
        container_command_factory: Callable[
            [str, str, str, pathlib.Path, pathlib.Path, pathlib.Path], Sequence[str]
        ] | None = None,
        arm_provision_factory: Callable[
            [str, str, str, pathlib.Path, pathlib.Path, pathlib.Path], None
        ] | None = None,
    ) -> None:
        if not configs:
            raise UbuntuExecutionDriverConfigError(
                "question_configs must contain at least one question"
            )
        slots = {config.grader_slots for config in configs.values()}
        if len(slots) != 1:
            raise UbuntuExecutionDriverConfigError(
                "all question configs must declare the same grader_slots"
            )
        shared = threading.BoundedSemaphore(slots.pop())
        self._factories = {
            question_id: UbuntuExecutionDriverFactory(
                config,
                grader_semaphore=shared,
                subprocess_adapter=subprocess_adapter,
                container_command_factory=container_command_factory,
                arm_provision_factory=arm_provision_factory,
            )
            for question_id, config in configs.items()
        }

    def for_arm(
        self, question_id: str, mode: str, arm: Mapping[str, Any]
    ) -> UbuntuExecutionDriver:
        factory = self._factories.get(question_id)
        if factory is None:
            raise UbuntuExecutionDriverConfigError(
                f"question_configs has no driver for {question_id}"
            )
        return factory.for_arm(question_id, mode, arm)


def load_config(path: str | pathlib.Path) -> UbuntuExecutionDriverConfig:
    """Read one explicit JSON configuration file."""

    candidate = pathlib.Path(path)
    try:
        value = json.loads(candidate.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise UbuntuExecutionDriverConfigError(f"read driver config {candidate}: {exc}") from exc
    return UbuntuExecutionDriverConfig.from_dict(_mapping(value, "driver config"))


def load_driver_factory(
    path: str | pathlib.Path,
) -> UbuntuExecutionDriverFactory | QuestionMappedExecutionDriverFactory:
    """Load either one driver config or a strict per-question config map."""

    candidate = pathlib.Path(path)
    try:
        value = json.loads(candidate.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise UbuntuExecutionDriverConfigError(
            f"read driver factory config {candidate}: {exc}"
        ) from exc
    raw = _mapping(value, "driver factory config")
    if "question_configs" not in raw:
        return UbuntuExecutionDriverFactory(UbuntuExecutionDriverConfig.from_dict(raw))
    _strict_mapping(
        raw,
        frozenset({"schema", "question_configs"}),
        "question-mapped driver factory config",
    )
    if raw.get("schema") != "super-dolphin/cross-file-question-drivers/v1":
        raise UbuntuExecutionDriverConfigError(
            "unsupported question-mapped driver factory schema"
        )
    mapped = _mapping(raw.get("question_configs"), "question_configs")
    configs: dict[str, UbuntuExecutionDriverConfig] = {}
    for question_id, config_value in mapped.items():
        normalized_id = _text(question_id, "question_configs key")
        if _SAFE_ID.fullmatch(normalized_id) is None:
            raise UbuntuExecutionDriverConfigError(
                f"question_configs key has unsafe characters: {normalized_id}"
            )
        configs[normalized_id] = UbuntuExecutionDriverConfig.from_dict(
            _mapping(config_value, f"question_configs[{normalized_id}]")
        )
    return QuestionMappedExecutionDriverFactory(configs)


def build_driver_from_config(path: str | pathlib.Path) -> UbuntuExecutionDriver:
    """Factory suitable for direct callers that already have a config path."""

    return UbuntuExecutionDriver(load_config(path))


def build_driver_from_env() -> UbuntuExecutionDriver:
    """Factory for ``cross_file_benchmark_runner --driver module:build_driver_from_env``."""

    config_path = os.environ.get("SUPER_DOLPHIN_CROSS_FILE_DRIVER_CONFIG")
    if not config_path:
        raise UbuntuExecutionDriverConfigError(
            "SUPER_DOLPHIN_CROSS_FILE_DRIVER_CONFIG must name an explicit config file"
        )
    return build_driver_from_config(config_path)


__all__ = [
    "LocalSubprocessAdapter",
    "QuestionMappedExecutionDriverFactory",
    "SubprocessAdapter",
    "SubprocessResult",
    "UbuntuExecutionDriver",
    "UbuntuExecutionDriverFactory",
    "UbuntuExecutionDriverConfig",
    "UbuntuExecutionDriverConfigError",
    "UbuntuExecutionDriverError",
    "UbuntuExecutionDriverProtocolError",
    "build_driver_from_config",
    "build_driver_from_env",
    "load_config",
    "load_driver_factory",
]
