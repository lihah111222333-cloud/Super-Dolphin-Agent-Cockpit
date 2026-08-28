#!/usr/bin/env python3
"""Deployable Cross-File A/B runner with one canonical evidence path.

The input is a run plan containing one or more questions and exactly two arms
per question.  Each arm provides its full identity envelope, runner quality
rounds, canonical MCP/No-LSP evidence, usage/pricing, and official grader
sources.  This module executes the round plan through :class:`FiveRoundRunner`
and emits the final result only through ``cross_file_evidence_adapter``.

The runner deliberately does not import the low-level guard or parse free-form
grader output.  Identity/receipt/isolation validation belongs to their schema
owners, while official report/test-output binding is always performed through
``validate_grader_feedback(..., strict_provenance=True)`` before the round is
submitted to ``FiveRoundRunner``.
"""

from __future__ import annotations

import argparse
import base64
import copy
import concurrent.futures
import dataclasses
import hashlib
import importlib
import json
import os
import pathlib
import re
import sys
import tempfile
import threading
import time
from collections import deque
from collections.abc import Mapping, Sequence
from concurrent.futures import FIRST_COMPLETED, wait
from typing import Any, Protocol, cast

try:
    from . import cross_file_evidence_adapter as evidence_adapter
    from .cross_file_artifact_bridge import (
        ExecutorAttestationError,
        validate_executor_final_attestation,
    )
    from .cross_file_five_round_runner import (
        EVENT_SCHEMA_VERSION,
        FiveRoundRunner,
        RunnerProtocolError,
        RunnerStep,
    )
    from .cross_file_identity_envelope import parse_identity_envelope, verify_identity_envelope
    from .cross_file_five_round_runner import RoundInput
    from .cross_file_signed_receipts import validate_grader_receipt, verifier_from
    from .cross_file_signed_receipts import Ed25519Verifier
    from .cross_file_grader_bridge import (
        GraderBridgeError,
        build_grader_artifact,
        validate_grader_artifact_hashes,
        validate_official_grader_sources,
    )
    from .grader_feedback import (
        FailureKind as FeedbackFailureKind,
        GraderFeedback,
        validate_grader_feedback,
    )
except ImportError:
    import cross_file_evidence_adapter as evidence_adapter  # type: ignore[no-redef]
    from cross_file_artifact_bridge import (  # type: ignore[no-redef]
        ExecutorAttestationError,
        validate_executor_final_attestation,
    )
    from cross_file_five_round_runner import (  # type: ignore[no-redef]
        EVENT_SCHEMA_VERSION,
        FiveRoundRunner,
        RoundInput,
        RunnerProtocolError,
        RunnerStep,
    )
    from cross_file_identity_envelope import (  # type: ignore[no-redef]
        parse_identity_envelope,
        verify_identity_envelope,
    )
    from grader_feedback import (  # type: ignore[no-redef]
        FailureKind as FeedbackFailureKind,
        GraderFeedback,
        validate_grader_feedback,
    )
    from cross_file_signed_receipts import (  # type: ignore[no-redef]
        Ed25519Verifier,
        validate_grader_receipt,
        verifier_from,
    )
    from cross_file_grader_bridge import (  # type: ignore[no-redef]
        GraderBridgeError,
        build_grader_artifact,
        validate_grader_artifact_hashes,
        validate_official_grader_sources,
    )


RUN_PLAN_SCHEMA = "super-dolphin/cross-file-benchmark-runner/v1"
MAX_QUALITY_ROUNDS = 3
QUALITY_POINTS = (10, 7, 4)
_MODES = ("no_lsp", "with_lsp")
_TOP_FIELDS = {
    "schema", "run_id", "pricing_manifest", "questions", "max_infra_retries",
    "max_concurrency",
}
_QUESTION_FIELDS = {"question_id", "discovery_shape", "arms"}
_STATIC_ARM_FIELDS = {
    "mode",
    "arm_id",
    "code_agent_id",
    "identity_envelope",
    "identity_envelopes",
    "server_generation",
    "arm_evidence",
    "changed_files",
    "usage",
    "pricing",
}
_REPLAY_ARM_FIELDS = _STATIC_ARM_FIELDS | {"rounds"}
_EXECUTE_ARM_FIELDS = {"mode", "arm_id"}
_DRIVER_ARTIFACT_FIELDS = {
    "identity_envelope",
    "identity_envelopes",
    "server_generation",
    "arm_evidence",
    "changed_files",
    "usage",
    "executor_attestation",
    "receipt_log",
    "executor_attestation_public_key",
}
_DRIVER_AGENT_FIELDS = _DRIVER_ARTIFACT_FIELDS | {"request", "transport_error"}
_ROUND_FIELDS = {"round", "request", "grader", "evidence_recovery", "infra_retries"}
_GRADER_FIELDS = {
    "feedback",
    "official_report",
    "official_test_output",
    "official_stdout",
    "official_stderr",
    "grader_request_id",
    "grader_receipt_public_key",
    "grader_receipt_digest",
    "transport_error",
}
_REQUEST_FIELDS = {
    field.name
    for field in dataclasses.fields(RoundInput)
    if field.name
    not in {"round_number", "arm_id", "session_id", "thread_id", "code_agent_id", "patch_hash", "action", "resume"}
}
_REQUEST_IDENTITY_FIELDS = {"round_number", "arm_id", "session_id", "thread_id", "code_agent_id", "patch_hash"}


class BenchmarkRunnerError(RuntimeError):
    """Base class for malformed plans or incomplete executions."""


class PlanError(BenchmarkRunnerError):
    """A run plan violates the runner-owned schema."""


class TransportFailure(RuntimeError):
    """A grader transport failed and may be retried in the same quality round."""


@dataclasses.dataclass(frozen=True)
class AgentContext:
    """Immutable input supplied to an execution driver's Agent phase."""

    question_id: str
    mode: str
    round_number: int
    identity: Mapping[str, str]
    arm: Mapping[str, Any]
    runner_snapshot: Mapping[str, Any]
    run_id: str = ""


@dataclasses.dataclass(frozen=True)
class GraderContext:
    """Immutable input supplied to an execution driver's grader phase."""

    question_id: str
    mode: str
    round_number: int
    request: RoundInput
    arm: Mapping[str, Any]
    runner_snapshot: Mapping[str, Any]
    run_id: str = ""


class ExecutionDriver(Protocol):
    """Explicit production boundary for Agent and official-grader execution."""

    def run_agent(self, context: AgentContext) -> Mapping[str, Any]:
        """Run the Agent and return a request object, never grader evidence."""
        raise NotImplementedError

    def run_grader(self, context: GraderContext) -> Mapping[str, Any]:
        """Run official grading and return structured feedback/source evidence."""
        raise NotImplementedError

    def run_evidence_recovery(self, context: AgentContext, pending: RunnerStep) -> Mapping[str, Any]:
        """Run an evidence-only continuation for the same quality round."""
        raise NotImplementedError

    def run_infra_retry(self, context: GraderContext, pending: RunnerStep) -> Mapping[str, Any]:
        """Retry official grading without starting another quality round."""
        raise NotImplementedError


class ExecutionDriverFactory(Protocol):
    """Produces one isolated execution driver for a single A/B arm."""

    def for_arm(
        self, question_id: str, mode: str, arm: Mapping[str, Any]
    ) -> ExecutionDriver:
        """Return a driver whose workspace and artifacts are arm-local."""
        raise NotImplementedError


@dataclasses.dataclass(frozen=True)
class ArmExecution:
    question_id: str
    mode: str
    runner: FiveRoundRunner
    final_feedback: GraderFeedback | None = None
    final_grader_receipt: Mapping[str, Any] | None = None
    official_report: str | bytes | None = None
    official_test_output: str | bytes | None = None
    official_stdout: str | bytes | None = None
    official_stderr: str | bytes | None = None
    grader_request_id: str | None = None
    grader_receipt_digest: str | None = None
    grader_receipt_public_key: Mapping[str, Any] | None = None
    status: str = ""
    error: str | None = None
    artifacts: Mapping[str, Any] | None = None


@dataclasses.dataclass(frozen=True)
class BenchmarkExecution:
    audit_result: Any | None
    arms: tuple[ArmExecution, ...]
    execution_mode: str = "execute"
    scoreable: bool = False
    error: str | None = None

    def to_dict(self) -> dict[str, Any]:
        """Expose raw, authoritative, and final views for every arm."""

        raw = _raw_execution_view(self.arms)
        authoritative = _authoritative_execution_view(self.arms, raw["raw_ledger_hash"])
        arm_summaries = [
            {
                "question_id": arm.question_id,
                "mode": arm.mode,
                "status": arm.status,
                "error": arm.error,
                "runner": arm.runner.summary(),
                "artifacts": (
                    copy.deepcopy(dict(arm.artifacts))
                    if arm.artifacts is not None
                    else None
                ),
                "official_report_present": arm.official_report is not None,
                "official_test_output_present": arm.official_test_output is not None,
                "final_feedback": (
                    arm.final_feedback.to_dict()
                    if arm.final_feedback is not None
                    else None
                ),
            }
            for arm in self.arms
        ]
        final = {
            "schema": "super-dolphin/cross-file-benchmark-final/v1",
            "execution_mode": self.execution_mode,
            "status": "succeeded" if self.scoreable else "unverified",
            "scoreable": self.scoreable,
            "error": self.error,
            "arms": copy.deepcopy(raw["arms"]),
            "records": copy.deepcopy(raw["records"]),
            "raw_ledger_hash": raw["raw_ledger_hash"],
            "authoritative": copy.deepcopy(authoritative),
            "audit": (
                self.audit_result.to_dict() if self.audit_result is not None else None
            ),
        }
        return {
            "schema": "super-dolphin/cross-file-benchmark-runner/v1",
            "execution_mode": self.execution_mode,
            "scoreable": self.scoreable,
            "status": "succeeded" if self.scoreable else "unverified",
            "error": self.error,
            "arms": arm_summaries,
            "raw_records": copy.deepcopy(raw["records"]),
            # ``raw`` is the byte/source-preserving benchmark record; it is
            # never replaced by the authoritative interpretation below.
            "raw": raw,
            "raw_ledger": copy.deepcopy(raw),
            "authoritative": authoritative,
            "authoritative_ledger": copy.deepcopy(authoritative),
            "final_3_round_scores": copy.deepcopy(
                authoritative["final_3_round_scores"]
            ),
            "final": final,
            "audit": self.audit_result.to_dict() if self.audit_result is not None else None,
        }


def _source_view(value: Any) -> Any:
    """Keep official source bytes JSON-safe without silently decoding them."""

    if not isinstance(value, bytes):
        return copy.deepcopy(value)
    try:
        return value.decode("utf-8")
    except UnicodeDecodeError:
        return {
            "encoding": "base64",
            "data": base64.b64encode(value).decode("ascii"),
            "sha256": hashlib.sha256(value).hexdigest(),
        }


def _raw_execution_view(arms: Sequence[ArmExecution]) -> dict[str, Any]:
    """Build an immutable-style raw snapshot before authoritative scoring."""

    rows: list[dict[str, Any]] = []
    for arm in arms:
        rows.append(
            {
                "question_id": arm.question_id,
                "mode": arm.mode,
                "status": arm.status,
                "error": arm.error,
                "runner": arm.runner.summary(),
                "artifacts": (
                    copy.deepcopy(dict(arm.artifacts))
                    if arm.artifacts is not None
                    else None
                ),
                "final_feedback": (
                    arm.final_feedback.to_dict()
                    if arm.final_feedback is not None
                    else None
                ),
                "final_grader_receipt": (
                    copy.deepcopy(dict(arm.final_grader_receipt))
                    if arm.final_grader_receipt is not None
                    else None
                ),
                "official_report": _source_view(arm.official_report),
                "official_test_output": _source_view(arm.official_test_output),
                "official_stdout": _source_view(arm.official_stdout),
                "official_stderr": _source_view(arm.official_stderr),
                "grader_request_id": arm.grader_request_id,
                "grader_receipt_digest": arm.grader_receipt_digest,
                "grader_receipt_public_key": (
                    copy.deepcopy(dict(arm.grader_receipt_public_key))
                    if arm.grader_receipt_public_key is not None
                    else None
                ),
            }
        )
    material = json.dumps(
        {"schema": "super-dolphin/cross-file-benchmark-raw/v1", "arms": rows},
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")
    return {
        "schema": "super-dolphin/cross-file-benchmark-raw/v1",
        "raw_ledger_hash": hashlib.sha256(material).hexdigest(),
        "arms": rows,
        "records": copy.deepcopy(rows),
        "arm_count": len(rows),
    }


def _arm_quality_score(arm: ArmExecution) -> tuple[int | None, int]:
    """Derive the first passing quality round from persisted runner events."""

    passing_rounds = [
        event.round_number
        for event in arm.runner.events
        if event.kind == "grader_result" and event.grader_passed is True
    ]
    first_round = min(passing_rounds) if passing_rounds else None
    if first_round not in (1, 2, 3):
        return None, 0
    return first_round, QUALITY_POINTS[first_round - 1]


def _authoritative_execution_view(
    arms: Sequence[ArmExecution],
    raw_ledger_hash: str,
) -> dict[str, Any]:
    """Publish official-grader/runner outcomes without rewriting raw input."""

    records: list[dict[str, Any]] = []
    final_scores: list[dict[str, Any]] = []
    grouped: dict[str, dict[str, dict[str, Any]]] = {}
    for arm in arms:
        feedback = arm.final_feedback
        quality_round, quality_score = _arm_quality_score(arm)
        if feedback is None:
            classification = "unverified"
            passed = False
            failure_kind = None
            resolved = None
            completed = None
            source_hash = None
        else:
            passed = bool(feedback.passed)
            failure_kind = feedback.failure_kind or None
            resolved = feedback.official_resolved
            completed = feedback.official_completed
            source_hash = feedback.source_hash or None
            if passed:
                classification = "pass"
            elif failure_kind == FeedbackFailureKind.MODEL_TEST_FAILURE.value:
                classification = "model"
            elif failure_kind:
                classification = "infra"
            else:
                classification = "unverified"
        record = {
            "attempt_id": arm.grader_request_id or f"{arm.question_id}:{arm.mode}",
            "question_id": arm.question_id,
            "mode": arm.mode,
            "status": arm.status,
            "passed": passed,
            "classification": classification,
            "failure_kind": failure_kind,
            "official_resolved": resolved,
            "official_completed": completed,
            "quality_round": quality_round,
            "quality_score": quality_score,
            "source_hash": source_hash,
            "error": arm.error,
        }
        round_scores = [
            quality_score if quality_round is not None and number >= quality_round else 0
            for number in (1, 2, 3)
        ]
        record["final_3_round_scores"] = round_scores
        final_scores.append(
            {
                "question_id": arm.question_id,
                "mode": arm.mode,
                "first_passing_round": quality_round,
                "rounds": round_scores,
                "final_score": quality_score,
            }
        )
        records.append(record)
        grouped.setdefault(arm.question_id, {})[arm.mode] = record

    pairs: list[dict[str, Any]] = []
    for question_id in sorted(grouped):
        pair = grouped[question_id]
        no_lsp = pair.get("no_lsp")
        with_lsp = pair.get("with_lsp")
        complete = no_lsp is not None and with_lsp is not None
        pair_rows = {
            "question_id": question_id,
            "complete": complete,
            "scoreable": bool(
                complete
                and no_lsp["status"] in {"succeeded", "max_rounds_reached"}
                and with_lsp["status"] in {"succeeded", "max_rounds_reached"}
            ) if complete else False,
            "no_lsp": copy.deepcopy(no_lsp),
            "with_lsp": copy.deepcopy(with_lsp),
            "quality_scores": {
                "no_lsp": no_lsp["quality_score"] if no_lsp is not None else 0,
                "with_lsp": with_lsp["quality_score"] if with_lsp is not None else 0,
            },
            "final_3_round_scores": {
                "no_lsp": (
                    no_lsp["final_3_round_scores"] if no_lsp is not None else [0, 0, 0]
                ),
                "with_lsp": (
                    with_lsp["final_3_round_scores"] if with_lsp is not None else [0, 0, 0]
                ),
            },
        }
        pair_rows["total_quality_score"] = sum(pair_rows["quality_scores"].values())
        pairs.append(pair_rows)
    return {
        "schema": "super-dolphin/cross-file-benchmark-authoritative/v1",
        "authority": "OFFICIAL_GRADER_AND_RUNNER",
        "raw_ledger_hash": raw_ledger_hash,
        "records": records,
        "final_3_round_scores": final_scores,
        "pairs": pairs,
        "paired_scores": copy.deepcopy(pairs),
        "quality_points_by_first_passing_round": list(QUALITY_POINTS),
        "arm_count": len(records),
        "pair_count": len(pairs),
    }


def _required(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise PlanError(f"{label} must be a non-empty string")
    return value.strip()


def _reject_unknown(value: Mapping[str, Any], allowed: set[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise PlanError(f"{label} has unknown field(s): {', '.join(unknown)}")


def _read_json(value: Any, label: str) -> Any:
    if isinstance(value, (Mapping, list, tuple)):
        return value
    if not isinstance(value, (str, pathlib.Path)):
        raise PlanError(f"{label} must be JSON or a JSON path")
    source = str(value)
    candidate = pathlib.Path(source)
    try:
        try:
            is_file = "\n" not in source and candidate.is_file()
        except OSError:
            is_file = False
        text = candidate.read_text(encoding="utf-8") if is_file else source
        return json.loads(text)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise PlanError(f"read {label}: {exc}") from exc


def _object(value: Any, label: str) -> Mapping[str, Any]:
    parsed = _read_json(value, label)
    if not isinstance(parsed, Mapping):
        raise PlanError(f"{label} must be an object")
    return parsed


def _source_text(value: Any, label: str) -> str | bytes | None:
    if isinstance(value, bytes):
        return value
    if value is None:
        return None
    if not isinstance(value, str):
        raise PlanError(f"{label} must be text or a file path")
    candidate = pathlib.Path(value)
    try:
        is_file = "\n" not in value and candidate.is_file()
    except OSError:
        is_file = False
    if is_file:
        try:
            return candidate.read_bytes()
        except OSError as exc:
            raise PlanError(f"read {label}: {exc}") from exc
    return value


def _safe_state_name(question_id: str, mode: str) -> str:
    value = f"{question_id}.{mode}"
    if re.fullmatch(r"[A-Za-z0-9_.-]+", value) is None:
        raise PlanError("question_id contains unsafe state-path characters")
    return value


def _feedback_payload(value: Any) -> Any:
    """Project runner metadata out of the nested feedback identity only."""

    parsed = _read_json(value, "structured grader feedback")
    if isinstance(parsed, Mapping) and isinstance(parsed.get("identity"), Mapping):
        parsed = dict(parsed)
        parsed["identity"] = {
            key: parsed["identity"][key]
            for key in ("question_id", "arm_id", "session_id", "thread_id", "patch_hash")
            if key in parsed["identity"]
        }
        for key in ("phase", "collected_count", "executed_count"):
            if parsed.get(key) in (None, ""):
                parsed.pop(key, None)
        if parsed.get("missing_symbols") == []:
            parsed.pop("missing_symbols", None)
    return parsed


def _identity_context(
    arm: Mapping[str, Any],
    *,
    question_id: str,
    mode: str,
) -> tuple[Any, dict[str, str]]:
    envelope = parse_identity_envelope(
        _read_json(arm.get("identity_envelope"), "identity envelope")
    )
    bindings = envelope.bindings
    task = _required(bindings.task.id, "identity.bindings.task.id")
    if task != question_id:
        raise PlanError("identity task does not match question_id")
    declared_mode = _required(arm.get("mode"), "arm.mode").lower().replace("-", "_")
    if declared_mode != mode:
        raise PlanError("arm mode does not match question mode")
    arm_id = _required(arm.get("arm_id"), "arm_id")
    code_agent = _required(bindings.agent.id, "identity.bindings.agent.id")
    declared_agent = arm.get("code_agent_id")
    if declared_agent is not None and _required(declared_agent, "code_agent_id") != code_agent:
        raise PlanError("code_agent_id does not match identity envelope")
    return envelope, {
        "question_id": task,
        "arm_id": arm_id,
        "mode": mode,
        "session_id": _required(bindings.session.id, "identity.bindings.session.id"),
        "thread_id": _required(bindings.thread.id, "identity.bindings.thread.id"),
        "patch_hash": _required(bindings.patch.id, "identity.bindings.patch.id"),
        "identity_hash": envelope.binding_sha256,
    }


def _driver_artifact(
    value: Any,
    *,
    question_id: str,
    mode: str,
    arm_id: str,
    expected_identity: Mapping[str, str] | None = None,
    run_id: str | None = None,
) -> tuple[Mapping[str, Any], Any, dict[str, str]]:
    """Read dynamic Agent artifacts without deriving evidence in the runner."""

    raw = _object(value, "execution driver Agent artifacts")
    _reject_unknown(raw, _DRIVER_ARTIFACT_FIELDS | {"request", "transport_error"}, "execution driver Agent result")
    artifact = {key: raw[key] for key in _DRIVER_ARTIFACT_FIELDS if key in raw}
    required_base = _DRIVER_ARTIFACT_FIELDS - {
        "arm_evidence",
        "executor_attestation",
        "receipt_log",
        "executor_attestation_public_key",
    }
    missing = sorted(required_base - set(artifact))
    has_attestation = "executor_attestation" in artifact
    if not has_attestation and "arm_evidence" not in artifact:
        missing.append("arm_evidence or executor_attestation")
    if missing:
        raise PlanError(
            "execution driver Agent result missing dynamic artifact(s): "
            + ", ".join(missing)
        )
    envelope, identity = _identity_context(
        {
            "mode": mode,
            "arm_id": arm_id,
            "code_agent_id": artifact.get("code_agent_id"),
            "identity_envelope": artifact["identity_envelope"],
            "identity_envelopes": artifact["identity_envelopes"],
        },
        question_id=question_id,
        mode=mode,
    )
    if expected_identity is not None:
        for field in ("question_id", "arm_id", "mode", "session_id", "thread_id"):
            if identity[field] != expected_identity[field]:
                raise PlanError(f"driver artifact {field} drifted during one arm")
        expected_agent = expected_identity.get("code_agent_id")
        if expected_agent is not None and envelope.bindings.agent.id != expected_agent:
            raise PlanError("driver artifact code_agent_id drifted during one arm")
    changed_files = artifact["changed_files"]
    if not isinstance(changed_files, Sequence) or isinstance(changed_files, (str, bytes, bytearray)) or not changed_files:
        raise PlanError("driver Agent changed_files must be a non-empty array")
    has_executor_attestation = "executor_attestation" in artifact
    if has_executor_attestation:
        _object(artifact["executor_attestation"], "driver executor attestation")
        executor_key = artifact.get("executor_attestation_public_key")
        if not isinstance(executor_key, Mapping):
            raise PlanError("driver executor attestation public key is required")
        if not isinstance(artifact.get("receipt_log"), str):
            raise PlanError("driver executor receipt_log must be text")
        try:
            executor_verifier = Ed25519Verifier.from_mapping(executor_key)
            receipt_verifier = verifier_from(identity_envelope=envelope)
            attestation = artifact["executor_attestation"]
            validate_executor_final_attestation(
                attestation,
                receipt_log=artifact["receipt_log"],
                verifier=executor_verifier,
                receipt_verifier=receipt_verifier,
                task=question_id,
                arm=arm_id,
                session=identity["session_id"],
                thread=identity["thread_id"],
                patch_hash=identity["patch_hash"],
                final_tree_hash=envelope.bindings.final_tree.tree_sha,
                server_generation=artifact["server_generation"],
                mode=mode,
                run_id=run_id if run_id is not None else attestation.get("run_id"),
                attempt=attestation.get("attempt"),
                nonce=attestation.get("nonce"),
                issued_at=attestation.get("issued_at"),
            )
        except (ExecutorAttestationError, KeyError, TypeError, ValueError) as exc:
            raise PlanError(f"driver executor attestation rejected: {exc}") from exc
    else:
        _object(artifact["arm_evidence"], "driver canonical arm evidence")
    _object(artifact["usage"], "driver usage")
    _required(artifact["server_generation"], "driver server_generation")
    return artifact, envelope, identity


def _artifact_state_path(state_path: pathlib.Path) -> pathlib.Path:
    return state_path.with_name(state_path.stem + ".artifacts.json")


def _persist_artifact(
    path: pathlib.Path,
    artifact: Mapping[str, Any],
    request: RoundInput,
) -> None:
    """Persist the latest driver artifact so a pending child can restart safely."""

    path.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "schema": "super-dolphin/cross-file-driver-artifact/v1",
        "artifact": copy.deepcopy(dict(artifact)),
        "request": dataclasses.asdict(request),
    }
    temporary: pathlib.Path | None = None
    try:
        fd, name = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=path.parent)
        temporary = pathlib.Path(name)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(payload, handle, ensure_ascii=False, sort_keys=True, indent=2)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    except OSError as exc:
        raise RunnerProtocolError(f"persist driver artifact: {exc}") from exc
    finally:
        if temporary is not None:
            try:
                temporary.unlink()
            except FileNotFoundError:
                pass


def _load_artifact_state(path: pathlib.Path) -> tuple[Mapping[str, Any], Mapping[str, Any]]:
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise RunnerProtocolError(f"read persisted driver artifact: {exc}") from exc
    if not isinstance(raw, Mapping) or raw.get("schema") != "super-dolphin/cross-file-driver-artifact/v1":
        raise RunnerProtocolError("unsupported persisted driver artifact schema")
    artifact = _object(raw.get("artifact"), "persisted driver artifact")
    request = _object(raw.get("request"), "persisted driver request")
    return artifact, request


def _request(
    value: Any,
    *,
    round_number: int,
    action: str,
    identity: Mapping[str, str],
    code_agent_id: str,
    mode: str,
    expected_patch_hash: str | None = None,
) -> RoundInput:
    raw = _object(value, f"round {round_number} request")
    _reject_unknown(raw, _REQUEST_FIELDS | _REQUEST_IDENTITY_FIELDS | {"evidence_attempt"}, f"round {round_number} request")
    for field, expected in {
        "round_number": round_number,
        "arm_id": identity["arm_id"],
        "session_id": identity["session_id"],
        "thread_id": identity["thread_id"],
        "code_agent_id": code_agent_id,
    }.items():
        if field in raw and raw[field] != expected:
            raise PlanError(f"round {round_number} request {field} drifted from identity")
    patch_hash = raw.get("patch_hash")
    if not isinstance(patch_hash, str) or not patch_hash.strip():
        raise PlanError(f"round {round_number} request requires patch_hash")
    if expected_patch_hash is not None and patch_hash != expected_patch_hash:
        raise PlanError(f"round {round_number} child patch_hash drifted from its quality patch")
    if "changed_files" not in raw or "trajectory" not in raw:
        raise PlanError(f"round {round_number} request requires changed_files and trajectory")
    if "mode" in raw and raw["mode"] != mode:
        raise PlanError(f"round {round_number} request mode drifted from arm mode")
    kwargs = {field: raw[field] for field in _REQUEST_FIELDS if field in raw}
    kwargs.update(
        {
            "round_number": round_number,
            "arm_id": identity["arm_id"],
            "session_id": identity["session_id"],
            "thread_id": identity["thread_id"],
            "code_agent_id": code_agent_id,
            "patch_hash": patch_hash.strip(),
            "mode": mode,
            "action": action,
            "resume": action != "start",
        }
    )
    if action == "evidence_recovery":
        attempt = raw.get("evidence_attempt")
        if isinstance(attempt, bool) or not isinstance(attempt, int) or attempt < 1:
            raise PlanError("evidence recovery requires a positive evidence_attempt")
        kwargs["evidence_attempt"] = attempt
    return RoundInput(**kwargs)


def _strict_feedback(
    value: Any,
    *,
    identity: Mapping[str, str],
    patch_hash: str,
    identity_envelope: Any,
    require_executor_artifacts: bool = False,
) -> tuple[GraderFeedback, str | bytes | None, str | bytes | None, Mapping[str, Any]]:
    raw = _object(value, "grader evidence")
    _reject_unknown(raw, _GRADER_FIELDS, "grader evidence")
    if "transport_error" in raw:
        raise PlanError("transport grader evidence cannot be used as structured feedback")
    signed_value = _object(raw.get("feedback"), "signed grader feedback")
    if set(signed_value) != {"receipt"}:
        raise PlanError("grader feedback must be a signed receipt envelope")
    signed_receipt = _object(signed_value.get("receipt"), "signed grader receipt")
    if require_executor_artifacts:
        required = {
            "official_stdout", "official_stderr", "grader_request_id",
            "grader_receipt_digest", "grader_receipt_public_key",
        }
        missing = sorted(field for field in required if field not in raw)
        if missing:
            raise PlanError(
                "live grader evidence missing executor artifact field(s): "
                + ", ".join(missing)
            )
    report = _source_text(raw.get("official_report"), "official_report")
    test_output = _source_text(raw.get("official_test_output"), "official_test_output")
    try:
        parsed_identity = parse_identity_envelope(identity_envelope)
        pinned_role_key = parsed_identity.grader_receipt_signing_key
        role_key = raw.get("grader_receipt_public_key")
        if pinned_role_key is not None:
            if role_key is not None and role_key != pinned_role_key.to_dict():
                raise PlanError("grader receipt public key drifted from identity role binding")
            role_key = pinned_role_key.to_dict()
        verifier = (
            verifier_from(public_key=_object(role_key, "grader receipt public key"))
            if role_key is not None
            else verifier_from(identity_envelope=identity_envelope)
        )
        receipt_artifact = _object(
            signed_receipt.get("grader_artifact"),
            "signed grader receipt grader_artifact",
        )
        if "official_stdout" in raw or "official_stderr" in raw:
            if "official_stdout" not in raw or "official_stderr" not in raw:
                raise PlanError("grader evidence must include both official stdout and stderr")
            stdout = _source_text(raw.get("official_stdout"), "official_stdout")
            stderr = _source_text(raw.get("official_stderr"), "official_stderr")
            request_id = _required(raw.get("grader_request_id"), "grader_request_id")
            if signed_receipt.get("request_id") != request_id:
                raise PlanError("grader receipt request_id drifted from executor request")
            receipt_digest = _required(
                raw.get("grader_receipt_digest"), "grader_receipt_digest"
            )
            expected_receipt_digest = hashlib.sha256(
                json.dumps(
                    signed_receipt,
                    ensure_ascii=False,
                    sort_keys=True,
                    separators=(",", ":"),
                    allow_nan=False,
                ).encode("utf-8")
            ).hexdigest()
            if receipt_digest != expected_receipt_digest:
                raise PlanError("grader receipt digest drifted from executor receipt")
            expected_artifact = build_grader_artifact(
                task=identity["question_id"],
                request_id=request_id,
                report=report,
                test_output=test_output,
                stdout=stdout,
                stderr=stderr,
            )
            if receipt_artifact != expected_artifact.binding:
                raise PlanError("grader receipt artifact does not match executor output")
        else:
            # Replay fixtures from before the bridge carried the two process
            # streams remain readable, but live Ubuntu driver output always
            # includes both and takes the strict executor-bound path above.
            expected_artifact = None
        final_tree = parsed_identity.bindings.final_tree.tree_sha
        feedback_value = validate_grader_receipt(
            signed_receipt,
            verifier=verifier,
            task=identity["question_id"],
            arm=identity["arm_id"],
            session=identity["session_id"],
            thread=identity["thread_id"],
            patch_hash=patch_hash,
            grader_artifact=expected_artifact.binding if expected_artifact is not None else receipt_artifact,
            final_tree_hash=(final_tree if "final_tree_hash" in signed_receipt else None),
        )
        if expected_artifact is not None:
            validate_grader_artifact_hashes(feedback_value, expected_artifact)
    except Exception as exc:  # noqa: BLE001 - convert key/receipt errors at plan boundary.
        raise PlanError(f"signed grader receipt rejected: {exc}") from exc
    # The official SWE-bench aggregate commonly has only ``instance_id``;
    # session/thread/patch are executor facts and therefore are not expected
    # to be present in that source.  Parse the source independently, then
    # bind the signed result to the runner identity in the normal validator.
    try:
        source_feedback = validate_official_grader_sources(
            report,
            test_output,
            stdout=(
                _source_text(raw.get("official_stdout"), "official_stdout")
                if "official_stdout" in raw
                else None
            ),
            stderr=(
                _source_text(raw.get("official_stderr"), "official_stderr")
                if "official_stderr" in raw
                else None
            ),
        )
        for field, expected in {
            "question_id": identity["question_id"],
            "arm_id": identity["arm_id"],
            "session_id": identity["session_id"],
            "thread_id": identity["thread_id"],
            "patch_hash": patch_hash,
        }.items():
            actual = getattr(source_feedback, field)
            if actual and actual != expected:
                raise RunnerProtocolError(f"official grader source {field} drifted")
        if (
            source_feedback.official_instance_id
            and source_feedback.official_instance_id != identity["question_id"]
        ):
            raise RunnerProtocolError("official grader source instance_id drifted")
    except Exception as exc:  # noqa: BLE001 - normalize source/identity failures.
        raise RunnerProtocolError(f"official grader feedback rejected: {exc}") from exc
    validation_warning: str | None = None
    try:
        feedback = validate_grader_feedback(
            _feedback_payload(feedback_value),
            question_id=identity["question_id"],
            arm_id=identity["arm_id"],
            session_id=identity["session_id"],
            thread_id=identity["thread_id"],
            patch_hash=patch_hash,
        )
    except Exception as exc:  # DTO/parser drift must not discard official truth.
        validation_warning = f"signed feedback normalization pending: {type(exc).__name__}: {exc}"
        feedback = dataclasses.replace(
            source_feedback,
            question_id=identity["question_id"],
            arm_id=identity["arm_id"],
            session_id=identity["session_id"],
            thread_id=identity["thread_id"],
            patch_hash=patch_hash,
            evidence=tuple(source_feedback.evidence) + (validation_warning,),
        )
    source_fields = (
        "status",
        "failure_kind",
        "phase",
        "collected_count",
        "executed_count",
        "missing_symbols",
        "official_resolved",
        "official_completed",
        "passed",
    )
    if any(getattr(feedback, field) != getattr(source_feedback, field) for field in source_fields):
        validation_warning = "signed grader result disagrees with official source"
    if expected_artifact is not None and feedback.source_hash != source_feedback.source_hash:
        validation_warning = "signed grader source hash disagrees with official source"
    if validation_warning is not None:
        feedback = dataclasses.replace(
            source_feedback,
            question_id=identity["question_id"],
            arm_id=identity["arm_id"],
            session_id=identity["session_id"],
            thread_id=identity["thread_id"],
            patch_hash=patch_hash,
            evidence=tuple(source_feedback.evidence) + (
                "validation_pending:" + validation_warning,
            ),
        )
    return feedback, report, test_output, signed_receipt


def _grader_callback(
    value: Any,
    *,
    identity: Mapping[str, str],
    patch_hash: str,
    identity_envelope: Any,
    require_executor_artifacts: bool = False,
) -> tuple[Any, GraderFeedback | None, str | bytes | None, str | bytes | None, Mapping[str, Any] | None]:
    raw = _object(value, "grader evidence")
    _reject_unknown(raw, _GRADER_FIELDS, "grader evidence")
    if "transport_error" in raw:
        message = _required(raw["transport_error"], "transport_error")

        def fail(_: Any) -> Any:
            raise TransportFailure(message)

        return fail, None, None, None, None
    feedback, report, test_output, signed_receipt = _strict_feedback(
        raw,
        identity=identity,
        patch_hash=patch_hash,
        identity_envelope=identity_envelope,
        require_executor_artifacts=require_executor_artifacts,
    )

    def complete(_: Any) -> GraderFeedback:
        return feedback

    return complete, feedback, report, test_output, signed_receipt


def _driver_method(driver: ExecutionDriver, name: str) -> Any:
    method = getattr(driver, name, None)
    if not callable(method):
        raise PlanError(f"execution driver must implement {name}()")
    return method


def _driver_grader_callback(
    driver: ExecutionDriver,
    *,
    context: GraderContext,
    identity: Mapping[str, str],
) -> tuple[Any, dict[str, Any]]:
    """Lazily invoke the official grader only after the round gate allows it."""

    holder: dict[str, Any] = {}

    def callback(outcome: Any) -> Any:
        try:
            result = _driver_method(driver, "run_grader")(
                dataclasses.replace(context, runner_snapshot=dict(context.runner_snapshot))
            )
        except Exception as exc:  # noqa: BLE001 - driver transport is a retryable infra channel.
            result = {"transport_error": f"run_grader: {exc}"}
        try:
            grader_callback, feedback, report, test_output, signed_receipt = _grader_callback(
                result,
                identity=identity,
                patch_hash=context.request.patch_hash,
                identity_envelope=context.arm.get("identity_envelope"),
                require_executor_artifacts=True,
            )
        except PlanError as exc:
            raise RunnerProtocolError(str(exc)) from exc
        holder.update({
            "feedback": feedback,
            "official_report": report,
            "official_test_output": test_output,
            "grader_receipt": signed_receipt,
            "official_stdout": result.get("official_stdout"),
            "official_stderr": result.get("official_stderr"),
            "grader_request_id": result.get("grader_request_id"),
            "grader_receipt_digest": result.get("grader_receipt_digest"),
            "grader_receipt_public_key": result.get("grader_receipt_public_key"),
        })
        return grader_callback(outcome)

    return callback, holder


def _replay_arm(
    *,
    question_id: str,
    mode: str,
    arm: Mapping[str, Any],
    state_dir: pathlib.Path,
    max_infra_retries: int,
) -> ArmExecution:
    envelope, identity = _identity_context(arm, question_id=question_id, mode=mode)
    code_agent_id = envelope.bindings.agent.id
    rounds = arm.get("rounds")
    if not isinstance(rounds, Sequence) or isinstance(rounds, (str, bytes, bytearray)) or not rounds:
        raise PlanError(f"{question_id}:{mode} rounds must be a non-empty array")
    state_path = state_dir / f"{_safe_state_name(question_id, mode)}.state.json"
    runner = FiveRoundRunner(
        state_path,
        max_infra_retries=max_infra_retries,
        question_id=question_id,
    )
    final_feedback: GraderFeedback | None = None
    final_grader_receipt: Mapping[str, Any] | None = None
    final_report: str | bytes | None = None
    final_test_output: str | bytes | None = None
    final_stdout: str | bytes | None = None
    final_stderr: str | bytes | None = None
    final_request_id: str | None = None
    final_receipt_digest: str | None = None
    final_receipt_public_key: Mapping[str, Any] | None = None
    expected_round = 1
    for raw_round in rounds:
        round_value = _object(raw_round, f"{question_id}:{mode} round")
        _reject_unknown(round_value, _ROUND_FIELDS, f"{question_id}:{mode} round")
        number = round_value.get("round")
        if isinstance(number, bool) or not isinstance(number, int) or number != expected_round:
            raise PlanError(
                f"{question_id}:{mode} expected quality round {expected_round}, got {number!r}"
            )
        request = _request(
            round_value.get("request"),
            round_number=number,
            action="start" if number == 1 else "resume",
            identity=identity,
            code_agent_id=code_agent_id,
            mode=mode,
        )
        callback, feedback, report, test_output, signed_receipt = _grader_callback(
            round_value.get("grader"),
            identity=identity,
            patch_hash=request.patch_hash,
            identity_envelope=envelope,
        )
        step = runner.submit_quality_round(request, callback)
        if step.needs_evidence_recovery:
            recovery = round_value.get("evidence_recovery")
            if recovery is None:
                raise PlanError(f"{question_id}:{mode} round {number} needs evidence_recovery")
            recovery_obj = _object(recovery, f"{question_id}:{mode} evidence recovery")
            _reject_unknown(recovery_obj, {"request", "grader"}, "evidence recovery")
            recovery_request = _request(
                recovery_obj.get("request"),
                round_number=number,
                action="evidence_recovery",
                identity=identity,
                code_agent_id=code_agent_id,
                mode=mode,
                expected_patch_hash=runner.state.pending_evidence_patch_hash,
            )
            recovery_callback, feedback, report, test_output, signed_receipt = _grader_callback(
                recovery_obj.get("grader"),
                identity=identity,
                patch_hash=recovery_request.patch_hash,
                identity_envelope=envelope,
            )
            step = runner.submit_evidence_recovery(recovery_request, recovery_callback)
        retries = round_value.get("infra_retries", [])
        if retries is None:
            retries = []
        if isinstance(retries, Mapping):
            retries = [retries]
        if not isinstance(retries, Sequence) or isinstance(retries, (str, bytes, bytearray)):
            raise PlanError("infra_retries must be an array")
        retry_index = 0
        while step.needs_infra_retry:
            if retry_index >= len(retries):
                error = (
                    f"{question_id}:{mode} round {number} has unresolved transport "
                    "without a replay retry"
                )
                break
            retry_obj = _object(retries[retry_index], "infrastructure retry")
            _reject_unknown(retry_obj, {"request", "grader"}, "infrastructure retry")
            retry_request = _request(
                retry_obj.get("request"),
                round_number=number,
                action="infra_recovery",
                identity=identity,
                code_agent_id=code_agent_id,
                mode=mode,
                expected_patch_hash=(runner.state.rounds[-1].patch_hash if runner.state.rounds else None),
            )
            retry_callback, feedback, report, test_output, signed_receipt = _grader_callback(
                retry_obj.get("grader"),
                identity=identity,
                patch_hash=retry_request.patch_hash,
                identity_envelope=envelope,
            )
            step = runner.retry_infrastructure(retry_request, retry_callback)
            retry_index += 1
        if step.feedback is not None:
            final_feedback = step.feedback
            final_grader_receipt = signed_receipt
            final_report = report
            final_test_output = test_output
        if step.terminal:
            break
        expected_round += 1
    snapshot = runner.snapshot()
    return ArmExecution(
        question_id=question_id,
        mode=mode,
        runner=runner,
        final_feedback=final_feedback,
        final_grader_receipt=final_grader_receipt,
        official_report=final_report,
        official_test_output=final_test_output,
        status=snapshot.status,
        error=(
            None
            if snapshot.terminal and snapshot.scoreable and final_feedback is not None
            else f"terminal={snapshot.terminal} scoreable={snapshot.scoreable} "
            f"feedback={'present' if final_feedback is not None else 'missing'}"
        ),
    )




def _execute_arm_with_driver(
    *,
    question_id: str,
    mode: str,
    arm: Mapping[str, Any],
    state_dir: pathlib.Path,
    max_infra_retries: int,
    driver: ExecutionDriver,
    run_id: str = "",
) -> ArmExecution:
    """Run Agent/grader calls and use driver artifacts as final evidence."""

    arm_id = _required(arm.get("arm_id"), "arm_id")
    # No dynamic identity/evidence may come from the run plan in live mode.
    if set(arm) - _EXECUTE_ARM_FIELDS:
        raise PlanError(
            f"{question_id}:{mode} live arm accepts only mode and arm_id; "
            "dynamic evidence must come from the execution driver"
        )
    state_path = state_dir / f"{_safe_state_name(question_id, mode)}.state.json"
    runner = FiveRoundRunner(
        state_path,
        max_infra_retries=max_infra_retries,
        question_id=question_id,
    )
    initial_snapshot = runner.snapshot()
    if initial_snapshot.terminal:
        raise RunnerProtocolError(
            f"runner state is already terminal for {question_id}:{mode}; start a new lineage"
        )
    pending_action = initial_snapshot.pending_action
    if runner.events:
        tail = runner.events[-1]
        identity_hint = {
            "question_id": question_id,
            "arm_id": arm_id,
            "mode": mode,
            "session_id": tail.session_id,
            "thread_id": tail.thread_id,
            "code_agent_id": tail.code_agent_id,
            "patch_hash": tail.patch_hash,
        }
        first_round = (
            tail.round_number
            if pending_action == "infra_retry"
            else initial_snapshot.pending_round or initial_snapshot.last_round + 1
        )
    else:
        identity_hint = {"question_id": question_id, "arm_id": arm_id, "mode": mode}
        first_round = 1
    first_context = AgentContext(
        question_id=question_id,
        mode=mode,
        round_number=first_round,
        identity=identity_hint,
        arm=dict(arm),
        runner_snapshot=initial_snapshot.to_dict(),
        run_id=run_id,
    )
    try:
        if pending_action == "infra_retry":
            artifact_path = _artifact_state_path(state_path)
            saved_artifact, saved_request = _load_artifact_state(artifact_path)
            first_result = dict(saved_artifact)
            first_result["request"] = {
                key: item
                for key, item in saved_request.items()
                if key not in {"action", "resume"}
            }
        elif pending_action == "evidence_recovery":
            pending_step = RunnerStep(
                outcome=None,
                grader=None,
                event_sequences=(),
                needs_evidence_recovery=True,
                needs_infra_retry=False,
                terminal=False,
                scoreable=False,
            )
            first_result = _object(
                _driver_method(driver, "run_evidence_recovery")(first_context, pending_step),
                "execution driver evidence recovery result",
            )
        else:
            first_result = _object(
                _driver_method(driver, "run_agent")(first_context),
                "execution driver Agent result",
            )
    except Exception as exc:  # noqa: BLE001 - no identity means no trustworthy state owner.
        raise RunnerProtocolError(
            "execution driver must return a dynamic identity envelope even on Agent transport "
            f"failure; cause={type(exc).__name__}: {exc}"
        ) from exc
    first_artifact, first_envelope, identity = _driver_artifact(
        first_result,
        question_id=question_id,
        mode=mode,
        arm_id=arm_id,
        expected_identity=(
            {**identity_hint, "code_agent_id": identity_hint["code_agent_id"]}
            if pending_action in {"evidence_recovery", "infra_retry"}
            else None
        ),
        run_id=run_id,
    )
    code_agent_id = first_envelope.bindings.agent.id
    if "transport_error" in first_result:
        if pending_action is not None:
            raise RunnerProtocolError(
                f"pending {pending_action} child returned transport_error; state remains recoverable"
            )
        request = RoundInput(
            round_number=first_round,
            arm_id=identity["arm_id"],
            session_id=identity["session_id"],
            thread_id=identity["thread_id"],
            code_agent_id=code_agent_id,
            patch_hash=identity["patch_hash"],
            changed_files=(),
            trajectory=(),
            mode=mode,
            action="start",
        )
        runner.record_non_quality_failure(
            request,
            failure_kind=FeedbackFailureKind.AGENT_TRANSPORT_FAILURE.value,
            message=_required(first_result["transport_error"], "transport_error"),
        )
        return ArmExecution(
            question_id=question_id,
            mode=mode,
            runner=runner,
            status=runner.snapshot().status,
            error="AGENT_TRANSPORT_FAILURE: " + str(first_result["transport_error"]),
            artifacts=first_artifact,
        )
    first_request = _request(
        first_result.get("request"),
        round_number=first_round,
        action=(
            "evidence_recovery"
            if pending_action == "evidence_recovery"
            else "infra_recovery"
            if pending_action == "infra_retry"
            else "start"
        ),
        identity=identity,
        code_agent_id=first_envelope.bindings.agent.id,
        mode=mode,
        expected_patch_hash=(
            runner.state.pending_evidence_patch_hash
            if pending_action == "evidence_recovery"
            else None
        ),
    )
    if first_request.patch_hash != identity["patch_hash"]:
        raise PlanError("first Agent request patch_hash must match dynamic identity envelope")
    _persist_artifact(_artifact_state_path(state_path), first_artifact, first_request)
    latest_artifact: Mapping[str, Any] = first_artifact
    final_feedback: GraderFeedback | None = None
    final_grader_receipt: Mapping[str, Any] | None = None
    final_report: str | bytes | None = None
    final_test_output: str | bytes | None = None
    final_stdout: str | bytes | None = None
    final_stderr: str | bytes | None = None
    final_request_id: str | None = None
    final_receipt_digest: str | None = None
    final_receipt_public_key: Mapping[str, Any] | None = None
    error: str | None = None

    def run_one_grade(request: RoundInput, *, context_arm: Mapping[str, Any], driver_context: GraderContext):
        return _driver_grader_callback(
            driver,
            context=driver_context,
            identity=identity,
        )

    for number in range(first_round, MAX_QUALITY_ROUNDS + 1):
        if number == first_round:
            agent_result = first_result
            artifact = first_artifact
            artifact_envelope = first_envelope
            request = first_request
        else:
            agent_context = AgentContext(
                question_id=question_id,
                mode=mode,
                round_number=number,
                identity=dict(identity),
                arm=dict(arm),
                runner_snapshot=runner.snapshot().to_dict(),
                run_id=run_id,
            )
            try:
                agent_result = _object(
                    _driver_method(driver, "run_agent")(agent_context),
                    "execution driver Agent result",
                )
                artifact, artifact_envelope, candidate_identity = _driver_artifact(
                    agent_result,
                    question_id=question_id,
                    mode=mode,
                    arm_id=arm_id,
                    expected_identity={**identity, "code_agent_id": code_agent_id},
                    run_id=run_id,
                )
                request = _request(
                    agent_result.get("request"),
                    round_number=number,
                    action="resume",
                    identity=identity,
                    code_agent_id=artifact_envelope.bindings.agent.id,
                    mode=mode,
                )
                _persist_artifact(_artifact_state_path(state_path), artifact, request)
            except Exception as exc:
                # Preserve the already-owned session and classify agent-side
                # transport as non-quality infra without fabricating a patch.
                request = RoundInput(
                    round_number=number,
                    arm_id=identity["arm_id"],
                    session_id=identity["session_id"],
                    thread_id=identity["thread_id"],
                    code_agent_id=code_agent_id,
                    patch_hash=identity["patch_hash"],
                    changed_files=(),
                    trajectory=(),
                    mode=mode,
                    action="resume",
                )
                runner.record_non_quality_failure(
                    request,
                    failure_kind=FeedbackFailureKind.AGENT_TRANSPORT_FAILURE.value,
                    message=f"run_agent: {exc}",
                )
                error = f"AGENT_TRANSPORT_FAILURE: {exc}"
                break
        if request.patch_hash != artifact_envelope.bindings.patch.id:
            raise PlanError("Agent request patch_hash does not match its dynamic identity envelope")
        latest_artifact = artifact
        grader_context = GraderContext(
            question_id=question_id,
            mode=mode,
            round_number=number,
            request=request,
            arm=dict(artifact),
            runner_snapshot=runner.snapshot().to_dict(),
            run_id=run_id,
        )
        if pending_action == "infra_retry" and number == first_round:
            pending_step = RunnerStep(
                outcome=None,
                grader=None,
                event_sequences=(),
                needs_evidence_recovery=False,
                needs_infra_retry=True,
                terminal=False,
                scoreable=False,
            )
            retry_result = _driver_method(driver, "run_infra_retry")(
                grader_context,
                pending_step,
            )
            try:
                grader_callback, retry_feedback, retry_report, retry_output, retry_receipt = _grader_callback(
                    retry_result,
                    identity=identity,
                    patch_hash=request.patch_hash,
                    identity_envelope=artifact_envelope,
                )
            except PlanError as exc:
                raise RunnerProtocolError(str(exc)) from exc
            holder = {
                "feedback": retry_feedback,
                "official_report": retry_report,
                "official_test_output": retry_output,
                "grader_receipt": retry_receipt,
                "official_stdout": retry_result.get("official_stdout"),
                "official_stderr": retry_result.get("official_stderr"),
                "grader_request_id": retry_result.get("grader_request_id"),
                "grader_receipt_digest": retry_result.get("grader_receipt_digest"),
                "grader_receipt_public_key": retry_result.get("grader_receipt_public_key"),
            }
        else:
            grader_callback, holder = run_one_grade(
                request,
                context_arm=arm,
                driver_context=grader_context,
            )
        if pending_action == "evidence_recovery" and number == first_round:
            step = runner.submit_evidence_recovery(request, grader_callback)
        elif pending_action == "infra_retry" and number == first_round:
            step = runner.retry_infrastructure(request, grader_callback)
        else:
            step = runner.submit_quality_round(request, grader_callback)
        if step.needs_evidence_recovery:
            recovery_context = AgentContext(
                question_id=question_id,
                mode=mode,
                round_number=number,
                identity=dict(identity),
                arm=dict(arm),
                runner_snapshot=runner.snapshot().to_dict(),
                run_id=run_id,
            )
            recovery_result = _object(
                _driver_method(driver, "run_evidence_recovery")(recovery_context, step),
                "execution driver evidence recovery result",
            )
            recovery_artifact, recovery_envelope, _ = _driver_artifact(
                recovery_result,
                question_id=question_id,
                mode=mode,
                arm_id=arm_id,
                expected_identity={**identity, "code_agent_id": code_agent_id},
                run_id=run_id,
            )
            recovery_request = _request(
                recovery_result.get("request"),
                round_number=number,
                action="evidence_recovery",
                identity=identity,
                code_agent_id=recovery_envelope.bindings.agent.id,
                mode=mode,
                expected_patch_hash=runner.state.pending_evidence_patch_hash,
            )
            latest_artifact = recovery_artifact
            _persist_artifact(_artifact_state_path(state_path), recovery_artifact, recovery_request)
            recovery_grader_context = GraderContext(
                question_id=question_id,
                mode=mode,
                round_number=number,
                request=recovery_request,
                arm=dict(recovery_artifact),
                runner_snapshot=runner.snapshot().to_dict(),
                run_id=run_id,
            )
            recovery_callback, holder = run_one_grade(
                recovery_request,
                context_arm=arm,
                driver_context=recovery_grader_context,
            )
            step = runner.submit_evidence_recovery(recovery_request, recovery_callback)
        while step.needs_infra_retry:
            retry_context = GraderContext(
                question_id=question_id,
                mode=mode,
                round_number=number,
                request=request,
                arm=dict(latest_artifact),
                runner_snapshot=runner.snapshot().to_dict(),
                run_id=run_id,
            )
            try:
                retry_result = _driver_method(driver, "run_infra_retry")(retry_context, step)
            except Exception as exc:  # noqa: BLE001 - same-round transport retry.
                retry_result = {"transport_error": f"run_infra_retry: {exc}"}
            retry_callback, retry_feedback, retry_report, retry_output, retry_receipt = _grader_callback(
                retry_result,
                identity=identity,
                patch_hash=request.patch_hash,
            identity_envelope=latest_artifact.get("identity_envelope", first_envelope),
            require_executor_artifacts=True,
            )
            step = runner.retry_infrastructure(request, retry_callback)
            if retry_feedback is not None:
                holder = {
                    "feedback": retry_feedback,
                    "official_report": retry_report,
                    "official_test_output": retry_output,
                    "grader_receipt": retry_receipt,
                    "official_stdout": retry_result.get("official_stdout"),
                    "official_stderr": retry_result.get("official_stderr"),
                    "grader_request_id": retry_result.get("grader_request_id"),
                    "grader_receipt_digest": retry_result.get("grader_receipt_digest"),
                    "grader_receipt_public_key": retry_result.get("grader_receipt_public_key"),
                }
        if step.feedback is not None:
            final_feedback = step.feedback
            final_grader_receipt = holder.get("grader_receipt")
            final_report = holder.get("official_report")
            final_test_output = holder.get("official_test_output")
            final_stdout = holder.get("official_stdout")
            final_stderr = holder.get("official_stderr")
            final_request_id = holder.get("grader_request_id")
            final_receipt_digest = holder.get("grader_receipt_digest")
            final_receipt_public_key = holder.get("grader_receipt_public_key")
        if step.terminal:
            break
    snapshot = runner.snapshot()
    if not snapshot.terminal:
        error = error or f"execution ended non-terminal at quality round {snapshot.last_round}"
    elif not snapshot.scoreable:
        error = error or f"execution ended unscoreable with status {snapshot.status}"
    elif final_feedback is None or final_report is None or final_test_output is None:
        error = error or "final strict grader evidence is missing"
    return ArmExecution(
        question_id=question_id,
        mode=mode,
        runner=runner,
        final_feedback=final_feedback,
        final_grader_receipt=final_grader_receipt,
        official_report=final_report,
        official_test_output=final_test_output,
        official_stdout=final_stdout,
        official_stderr=final_stderr,
        grader_request_id=final_request_id,
        grader_receipt_digest=final_receipt_digest,
        grader_receipt_public_key=final_receipt_public_key,
        status=snapshot.status,
        error=error,
        artifacts=latest_artifact,
    )


def _adapter_arm(
    arm: Mapping[str, Any],
    execution: ArmExecution,
    pricing_manifest: Mapping[str, Any],
) -> dict[str, Any]:
    result = copy.deepcopy(dict(execution.artifacts or arm))
    result["mode"] = execution.mode
    result["arm_id"] = result.get("arm_id", arm.get("arm_id"))
    result["pricing"] = {"manifest_hash": pricing_manifest.get("manifest_hash")}
    result["runner_events"] = {
        "schema_version": EVENT_SCHEMA_VERSION,
        "events": execution.runner.summary()["events"],
    }
    if execution.final_grader_receipt is not None:
        result["grader_feedback"] = {"receipt": copy.deepcopy(dict(execution.final_grader_receipt))}
    if execution.official_report is not None:
        result["official_report"] = execution.official_report
    if execution.official_test_output is not None:
        result["official_test_output"] = execution.official_test_output
    if execution.official_stdout is not None:
        result["official_stdout"] = execution.official_stdout
    if execution.official_stderr is not None:
        result["official_stderr"] = execution.official_stderr
    if execution.grader_request_id is not None:
        result["grader_request_id"] = execution.grader_request_id
    if execution.grader_receipt_digest is not None:
        result["grader_receipt_digest"] = execution.grader_receipt_digest
    if execution.grader_receipt_public_key is not None:
        result["grader_receipt_public_key"] = copy.deepcopy(
            dict(execution.grader_receipt_public_key)
        )
    result.pop("rounds", None)
    return result


def _validate_driver(driver: ExecutionDriver | ExecutionDriverFactory) -> None:
    factory = getattr(driver, "for_arm", None)
    if callable(factory):
        return
    for name in ("run_agent", "run_grader", "run_evidence_recovery", "run_infra_retry"):
        _driver_method(cast(ExecutionDriver, driver), name)


def _plan_header(value: Any) -> tuple[Mapping[str, Any], str, Mapping[str, Any], Sequence[Any], int, int]:
    raw = _object(value, "benchmark run plan")
    _reject_unknown(raw, _TOP_FIELDS, "benchmark run plan")
    if raw.get("schema") != RUN_PLAN_SCHEMA:
        raise PlanError("unsupported benchmark run plan schema")
    run_id = _required(raw.get("run_id"), "run_id")
    pricing_manifest = _object(raw.get("pricing_manifest"), "pricing_manifest")
    questions = raw.get("questions")
    if not isinstance(questions, Sequence) or isinstance(questions, (str, bytes, bytearray)) or not questions:
        raise PlanError("benchmark run plan questions must be a non-empty array")
    max_infra_retries = raw.get("max_infra_retries", 1)
    if isinstance(max_infra_retries, bool) or not isinstance(max_infra_retries, int) or max_infra_retries < 0:
        raise PlanError("max_infra_retries must be a non-negative integer")
    max_concurrency = raw.get("max_concurrency")
    if (
        isinstance(max_concurrency, bool)
        or not isinstance(max_concurrency, int)
        or not 1 <= max_concurrency <= 40
    ):
        raise PlanError("max_concurrency must be an integer between 1 and 40")
    return raw, run_id, pricing_manifest, questions, max_infra_retries, max_concurrency


def _adapter_payload(
    *,
    run_id: str,
    pricing_manifest: Mapping[str, Any],
    questions: Sequence[Mapping[str, Any]],
) -> dict[str, Any]:
    return {
        "schema": evidence_adapter.ADAPTER_SCHEMA,
        "run_id": run_id,
        "pricing_manifest": copy.deepcopy(dict(pricing_manifest)),
        "questions": [copy.deepcopy(dict(question)) for question in questions],
    }


def execute_plan(
    value: Any,
    *,
    state_dir: str | pathlib.Path,
    driver: ExecutionDriver | ExecutionDriverFactory | None = None,
) -> BenchmarkExecution:
    """Execute a run plan through an explicit Agent/grader driver.

    Prefilled quality rounds are intentionally rejected here.  They belong to
    :func:`replay_evidence_plan`, whose result is explicitly unscoreable.
    """

    if driver is None:
        raise PlanError(
            "an explicit execution driver is required; use replay_evidence_plan "
            "for prefilled evidence replay"
        )
    _validate_driver(driver)
    raw, run_id, pricing_manifest, questions, max_infra_retries, max_concurrency = _plan_header(value)
    del raw
    if max_concurrency > 1 and not callable(getattr(driver, "for_arm", None)):
        raise PlanError(
            "max_concurrency greater than 1 requires a per-arm execution-driver factory"
        )
    state_root = pathlib.Path(state_dir)
    state_root.mkdir(parents=True, exist_ok=True)
    prepared: list[tuple[int, str, str, Mapping[str, Any], str]] = []
    for raw_question in questions:
        question = _object(raw_question, "benchmark question")
        _reject_unknown(question, _QUESTION_FIELDS, "benchmark question")
        question_id = _required(question.get("question_id"), "question.question_id")
        discovery_shape = _required(question.get("discovery_shape"), "question.discovery_shape")
        arms = question.get("arms")
        if not isinstance(arms, Mapping) or set(arms) != set(_MODES):
            raise PlanError(f"question {question_id} requires exactly no_lsp and with_lsp arms")
        for mode in _MODES:
            arm = _object(arms[mode], f"{question_id}:{mode} arm")
            if set(arm) - _EXECUTE_ARM_FIELDS:
                raise PlanError(
                    f"{question_id}:{mode} live arm accepts only mode and arm_id; "
                    "dynamic evidence must come from the execution driver"
                )
            prepared.append((len(prepared), question_id, mode, arm, discovery_shape))

    execution_slots = threading.BoundedSemaphore(max_concurrency)

    def failed_execution(
        item: tuple[int, str, str, Mapping[str, Any], str], exc: BaseException
    ) -> tuple[int, ArmExecution]:
        index, question_id, mode, _, _ = item
        runner = FiveRoundRunner(
            state_root / f"{_safe_state_name(question_id, mode)}.state.json",
            max_infra_retries=max_infra_retries,
            question_id=question_id,
        )
        return index, ArmExecution(
            question_id=question_id,
            mode=mode,
            runner=runner,
            status="contract_failure",
            error=f"{type(exc).__name__}: {exc}",
        )

    def execute_one(item: tuple[int, str, str, Mapping[str, Any], str]) -> tuple[int, ArmExecution]:
        index, question_id, mode, arm, _ = item
        with execution_slots:
            factory = getattr(driver, "for_arm", None)
            arm_driver = (
                cast(ExecutionDriverFactory, driver).for_arm(question_id, mode, arm)
                if callable(factory)
                else cast(ExecutionDriver, driver)
            )
            for name in ("run_agent", "run_grader", "run_evidence_recovery", "run_infra_retry"):
                _driver_method(arm_driver, name)
            return index, _execute_arm_with_driver(
                question_id=question_id,
                mode=mode,
                arm=arm,
                state_dir=state_root,
                max_infra_retries=max_infra_retries,
                driver=arm_driver,
                run_id=run_id,
            )

    ordered: list[ArmExecution | None] = [None] * len(prepared)
    queues = {
        mode: deque(item for item in prepared if item[2] == mode)
        for mode in _MODES
    }
    running_counts = {mode: 0 for mode in _MODES}
    generic_interval_text = os.environ.get("SUPER_DOLPHIN_ARM_LAUNCH_INTERVAL_SECONDS", "0")
    try:
        generic_launch_interval = float(generic_interval_text)
    except ValueError as exc:
        raise PlanError("SUPER_DOLPHIN_ARM_LAUNCH_INTERVAL_SECONDS must be numeric") from exc
    if generic_launch_interval < 0:
        raise PlanError("SUPER_DOLPHIN_ARM_LAUNCH_INTERVAL_SECONDS must be non-negative")
    last_generic_submit = [0.0]

    # A 40-arm wave is a scheduling optimization for the historical
    # representative run.  Larger validation image sets continue through the
    # normal lane queues and are replenished as arms finish; no question-count
    # assumption belongs in the runner.
    if max_concurrency == 40 and len(prepared) == 40:
        errors: list[BaseException] = []
        error_lock = threading.Lock()
        interval_text = os.environ.get("SUPER_DOLPHIN_ARM_LAUNCH_INTERVAL_SECONDS", "0")
        try:
            launch_interval = float(interval_text)
        except ValueError as exc:
            raise PlanError("SUPER_DOLPHIN_ARM_LAUNCH_INTERVAL_SECONDS must be numeric") from exc
        if launch_interval < 0:
            raise PlanError("SUPER_DOLPHIN_ARM_LAUNCH_INTERVAL_SECONDS must be non-negative")

        def run_full_wave(item: tuple[int, str, str, Mapping[str, Any], str]) -> None:
            try:
                index, execution = execute_one(item)
                ordered[index] = execution
            except BaseException as exc:  # noqa: BLE001 - rethrow on coordinator thread.
                index, execution = failed_execution(item, exc)
                ordered[index] = execution
                with error_lock:
                    errors.append(exc)
                print(
                    json.dumps(
                        {
                            "event": "cross_file_arm_start_failed",
                            "index": item[0],
                            "question_id": item[1],
                            "mode": item[2],
                            "error_type": type(exc).__name__,
                            "error": str(exc),
                        },
                        sort_keys=True,
                    ),
                    file=sys.stderr,
                    flush=True,
                )

        wave_threads = [
            threading.Thread(
                target=run_full_wave,
                args=(item,),
                name=f"cross-file-{item[2]}-{item[0]}",
            )
            for item in prepared
        ]
        for position, thread in enumerate(wave_threads):
            thread.start()
            if launch_interval and position + 1 < len(wave_threads):
                time.sleep(launch_interval)
        print(
            json.dumps(
                {
                    "event": "cross_file_scheduler_full_wave",
                    "threads": len(wave_threads),
                    "no_lsp": sum(item[2] == "no_lsp" for item in prepared),
                    "with_lsp": sum(item[2] == "with_lsp" for item in prepared),
                    "launch_interval_seconds": launch_interval,
                },
                sort_keys=True,
            ),
            file=sys.stderr,
            flush=True,
        )
        for thread in wave_threads:
            thread.join()
        for queue in queues.values():
            queue.clear()

    def submit_one(mode: str, pool: concurrent.futures.ThreadPoolExecutor, running: dict[Any, str]) -> None:
        if not queues[mode]:
            return
        if generic_launch_interval and last_generic_submit[0]:
            remaining = generic_launch_interval - (time.monotonic() - last_generic_submit[0])
            if remaining > 0:
                time.sleep(remaining)
        item = queues[mode].popleft()
        future = pool.submit(execute_one, item)
        last_generic_submit[0] = time.monotonic()
        setattr(future, "_cross_file_item", item)
        running[future] = mode
        running_counts[mode] += 1

    lane_workers = {
        "no_lsp": max(1, (max_concurrency + 1) // 2),
        "with_lsp": max(1, max_concurrency // 2),
    }
    with (
        concurrent.futures.ThreadPoolExecutor(
            max_workers=lane_workers["no_lsp"],
            thread_name_prefix="cross-file-no-lsp",
        ) as no_lsp_pool,
        concurrent.futures.ThreadPoolExecutor(
            max_workers=lane_workers["with_lsp"],
            thread_name_prefix="cross-file-with-lsp",
        ) as with_lsp_pool,
    ):
        pools = {"no_lsp": no_lsp_pool, "with_lsp": with_lsp_pool}
        running: dict[Any, str] = {}
        # Fill the first wave symmetrically so a full 40-arm run starts 20
        # no_lsp and 20 with_lsp workers before dynamic replenishment begins.
        initial_capacity = min(max_concurrency, len(prepared))
        initial_each = initial_capacity // 2
        initial_remainder = initial_capacity % 2
        initial_targets = {
            mode: min(
                initial_each + (1 if mode == "no_lsp" and initial_remainder else 0),
                len(queues[mode]),
            )
            for mode in _MODES
        }
        # Submit modes round-robin.  A provider may serialize arm workspace
        # materialization behind one lock; submitting one whole class first
        # would otherwise make all no_lsp arms acquire that lock before any
        # with_lsp arm despite a numerically balanced future set.
        while any(initial_targets.values()):
            for mode in _MODES:
                if initial_targets[mode] > 0:
                    submit_one(mode, pools[mode], running)
                    initial_targets[mode] -= 1
        while len(running) < initial_capacity:
            available = [mode for mode in _MODES if queues[mode]]
            if not available:
                break
            mode = min(available, key=lambda candidate: running_counts[candidate])
            submit_one(mode, pools[mode], running)

        print(
            json.dumps(
                {
                    "event": "cross_file_scheduler_initial",
                    "initial_capacity": initial_capacity,
                    "lane_workers": lane_workers,
                    "running_counts": running_counts,
                    "running_futures": len(running),
                    "remaining_queues": {
                        mode: len(queues[mode]) for mode in _MODES
                    },
                },
                sort_keys=True,
            ),
            file=sys.stderr,
            flush=True,
        )

        while running:
            completed, _ = wait(tuple(running), return_when=FIRST_COMPLETED)
            for future in completed:
                mode = running.pop(future)
                running_counts[mode] -= 1
                try:
                    index, execution = future.result()
                except BaseException as exc:  # noqa: BLE001 - isolate one arm and refill.
                    item = getattr(future, "_cross_file_item", None)
                    if item is None:
                        raise
                    index, execution = failed_execution(item, exc)
                    print(
                        json.dumps(
                            {
                                "event": "cross_file_arm_start_failed",
                                "index": item[0],
                                "question_id": item[1],
                                "mode": item[2],
                                "error_type": type(exc).__name__,
                                "error": str(exc),
                            },
                            sort_keys=True,
                        ),
                        file=sys.stderr,
                        flush=True,
                    )
                ordered[index] = execution
            for _ in completed:
                available = [mode for mode in _MODES if queues[mode]]
                if not available:
                    break
                # Refill the least represented class first.  If its queue is
                # exhausted, the remaining class naturally fills the slot.
                mode = min(available, key=lambda candidate: running_counts[candidate])
                submit_one(mode, pools[mode], running)
    executions = [item for item in ordered if item is not None]
    if len(executions) != len(prepared) or any(execution.error for execution in executions):
        return BenchmarkExecution(
            audit_result=None,
            arms=tuple(executions),
            execution_mode="execute",
            scoreable=False,
            error="one or more arms ended with isolated execution failure",
        )
    adapter_questions: list[dict[str, Any]] = []
    by_key = {(execution.question_id, execution.mode): execution for execution in executions}
    for raw_question in questions:
        question = _object(raw_question, "benchmark question")
        question_id = _required(question.get("question_id"), "question.question_id")
        discovery_shape = _required(question.get("discovery_shape"), "question.discovery_shape")
        arms = _object(question.get("arms"), f"{question_id} arms")
        adapter_arms = {
            mode: _adapter_arm(
                _object(arms[mode], f"{question_id}:{mode} arm"),
                by_key[(question_id, mode)],
                pricing_manifest,
            )
            for mode in _MODES
        }
        adapter_questions.append({
            "question_id": question_id,
            "discovery_shape": discovery_shape,
            "arms": adapter_arms,
        })
    if any(
        execution.status not in evidence_adapter._SCOREABLE_RUNNER_STATUSES
        or execution.final_feedback is None
        or execution.official_report is None
        or execution.official_test_output is None
        for execution in executions
    ):
        return BenchmarkExecution(
            audit_result=None,
            arms=tuple(executions),
            execution_mode="execute",
            scoreable=False,
            error="one or more arms ended without scoreable terminal evidence",
        )
    try:
        audit_result = evidence_adapter.audit_evidence_set(
            _adapter_payload(
                run_id=run_id,
                pricing_manifest=pricing_manifest,
                questions=adapter_questions,
            )
        )
    except Exception as exc:  # noqa: BLE001 - preserve arm terminal evidence in the API result.
        return BenchmarkExecution(
            audit_result=None,
            arms=tuple(executions),
            execution_mode="execute",
            scoreable=False,
            error=f"canonical audit rejected execution evidence: {type(exc).__name__}: {exc}",
        )
    return BenchmarkExecution(
        audit_result=audit_result,
        arms=tuple(executions),
        execution_mode="execute",
        scoreable=True,
    )


def replay_evidence_plan(value: Any, *, state_dir: str | pathlib.Path) -> BenchmarkExecution:
    """Replay prefilled evidence explicitly; never present it as live execution."""

    _, run_id, pricing_manifest, questions, max_infra_retries, _ = _plan_header(value)
    state_root = pathlib.Path(state_dir)
    state_root.mkdir(parents=True, exist_ok=True)
    adapter_questions: list[dict[str, Any]] = []
    executions: list[ArmExecution] = []
    for raw_question in questions:
        question = _object(raw_question, "benchmark question")
        _reject_unknown(question, _QUESTION_FIELDS, "benchmark question")
        question_id = _required(question.get("question_id"), "question.question_id")
        discovery_shape = _required(question.get("discovery_shape"), "question.discovery_shape")
        arms = question.get("arms")
        if not isinstance(arms, Mapping) or set(arms) != set(_MODES):
            raise PlanError(f"question {question_id} requires exactly no_lsp and with_lsp arms")
        adapter_arms: dict[str, Any] = {}
        for mode in _MODES:
            arm = _object(arms[mode], f"{question_id}:{mode} arm")
            _reject_unknown(arm, _REPLAY_ARM_FIELDS, f"{question_id}:{mode} arm")
            execution = _replay_arm(
                question_id=question_id,
                mode=mode,
                arm=arm,
                state_dir=state_root,
                max_infra_retries=max_infra_retries,
            )
            executions.append(execution)
            adapter_arms[mode] = _adapter_arm(arm, execution, pricing_manifest)
        adapter_questions.append({
            "question_id": question_id,
            "discovery_shape": discovery_shape,
            "arms": adapter_arms,
        })
    audit_result = None
    error = None
    if all(
        execution.final_feedback is not None
        and execution.official_report is not None
        and execution.official_test_output is not None
        for execution in executions
    ):
        try:
            audit_result = evidence_adapter.audit_evidence_set(
                _adapter_payload(
                    run_id=run_id,
                    pricing_manifest=pricing_manifest,
                    questions=adapter_questions,
                )
            )
        except Exception as exc:  # noqa: BLE001 - replay status remains explicit and unscoreable.
            error = f"replay evidence validation failed: {type(exc).__name__}: {exc}"
    else:
        error = "replay evidence is incomplete"
    return BenchmarkExecution(
        audit_result=audit_result,
        arms=tuple(executions),
        execution_mode="evidence_replay",
        scoreable=False,
        error=error,
    )


def _load_driver(specification: str) -> ExecutionDriver:
    value = _required(specification, "driver specification")
    module_name, separator, factory_name = value.partition(":")
    if not separator or not module_name or not factory_name:
        raise PlanError("driver must use module:factory notation")
    try:
        module = importlib.import_module(module_name)
        factory = getattr(module, factory_name)
    except (ImportError, AttributeError) as exc:
        raise PlanError(f"load execution driver {value}: {exc}") from exc
    if not callable(factory):
        raise PlanError(f"execution driver factory is not callable: {value}")
    driver = cast(ExecutionDriver, factory())
    _validate_driver(driver)
    return driver


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, help="benchmark run plan JSON path")
    parser.add_argument("--state-dir", required=True, help="durable per-arm state directory")
    parser.add_argument(
        "--mode",
        choices=("execute", "evidence-replay"),
        default="execute",
        help="execute with an explicit driver, or validate prefilled evidence as unscoreable replay",
    )
    parser.add_argument(
        "--driver",
        help="execution driver factory in module:callable form (required for execute)",
    )
    parser.add_argument("--output", help="canonical audit or explicit replay report path")
    args = parser.parse_args(argv)
    try:
        plan = _read_json(args.input, "benchmark run plan")
        if args.mode == "evidence-replay":
            if args.driver:
                raise PlanError("evidence-replay cannot accept an execution driver")
            execution = replay_evidence_plan(plan, state_dir=args.state_dir)
            rendered = json.dumps(execution.to_dict(), ensure_ascii=False, sort_keys=True, indent=2) + "\n"
        else:
            driver = _load_driver(args.driver or "")
            execution = execute_plan(plan, state_dir=args.state_dir, driver=driver)
            # Keep the formal raw and authoritative ledgers alongside the
            # canonical audit.  Returning only ``audit_result.json()`` loses
            # arm-local receipts and makes a successful run unreproducible.
            rendered = json.dumps(
                execution.to_dict(), ensure_ascii=False, sort_keys=True, indent=2
            ) + "\n"
        if args.output:
            pathlib.Path(args.output).write_text(rendered, encoding="utf-8")
        else:
            sys.stdout.write(rendered)
        return 0 if execution.scoreable else 3
    except Exception as exc:  # noqa: BLE001 - CLI boundary must be non-zero on any evidence failure.
        print(
            f"cross-file benchmark runner failed: {type(exc).__name__}: {exc}",
            file=sys.stderr,
        )
        return 2


if __name__ == "__main__":
    raise SystemExit(main())


__all__ = [
    "AgentContext",
    "BenchmarkExecution",
    "BenchmarkRunnerError",
    "ExecutionDriver",
    "GraderContext",
    "MAX_QUALITY_ROUNDS",
    "QUALITY_POINTS",
    "PlanError",
    "RUN_PLAN_SCHEMA",
    "TransportFailure",
    "execute_plan",
    "replay_evidence_plan",
    "main",
]
