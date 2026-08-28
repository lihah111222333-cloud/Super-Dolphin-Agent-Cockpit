#!/usr/bin/env python3
"""Bind runner evidence to the canonical Cross-File A/B audit input.

This module is an adapter, not a second evidence validator.  The strict
schema owners are imported directly:

* ``cross_file_identity_envelope`` owns the 17-component identity binding;
* ``cross_file_lsp_receipts`` owns MCP receipt and No-LSP isolation evidence;
* ``grader_feedback`` owns structured official grader feedback; and
* ``cross_file_ab_audit`` owns the final trajectory, pricing, and integrity
  audit.

The adapter only loads those surfaces, checks the runner event lifecycle well
enough to assemble quality-round summaries, and converts validated typed
objects into the audit module's canonical four evidence surfaces.
"""

from __future__ import annotations

import copy
import dataclasses
import hashlib
import json
import pathlib
from collections.abc import Mapping, Sequence
from typing import Any

try:
    from . import cross_file_ab_audit as audit_module
    from .cross_file_five_round_runner import (
        EVENT_SCHEMA_VERSION as RUNNER_EVENT_SCHEMA_VERSION,
        RunnerEvent,
    )
    from .cross_file_identity_envelope import (
        parse_identity_envelope,
        verify_identity_envelope,
    )
    from .cross_file_artifact_bridge import (
        derive_canonical_arm_evidence_from_attestation,
    )
    from .cross_file_signed_receipts import Ed25519Verifier
    from .cross_file_lsp_receipts import validate_canonical_arm_evidence
    from .cross_file_signed_receipts import validate_grader_receipt, verifier_from
    from .cross_file_grader_bridge import (
        build_grader_artifact,
        validate_grader_artifact_hashes,
        validate_official_grader_sources,
    )
    from .grader_feedback import canonical_source_hash, validate_grader_feedback
except ImportError:
    import cross_file_ab_audit as audit_module  # type: ignore[no-redef]
    from cross_file_five_round_runner import (  # type: ignore[no-redef]
        EVENT_SCHEMA_VERSION as RUNNER_EVENT_SCHEMA_VERSION,
        RunnerEvent,
    )
    from cross_file_identity_envelope import (  # type: ignore[no-redef]
        parse_identity_envelope,
        verify_identity_envelope,
    )
    from cross_file_artifact_bridge import (  # type: ignore[no-redef]
        derive_canonical_arm_evidence_from_attestation,
    )
    from cross_file_signed_receipts import Ed25519Verifier  # type: ignore[no-redef]
    from cross_file_lsp_receipts import validate_canonical_arm_evidence  # type: ignore[no-redef]
    from cross_file_signed_receipts import (  # type: ignore[no-redef]
        validate_grader_receipt,
        verifier_from,
    )
    from cross_file_grader_bridge import (  # type: ignore[no-redef]
        build_grader_artifact,
        validate_grader_artifact_hashes,
        validate_official_grader_sources,
    )
    from grader_feedback import (  # type: ignore[no-redef]
        canonical_source_hash,
        validate_grader_feedback,
    )


ADAPTER_SCHEMA = "super-dolphin/cross-file-evidence-adapter/v2"
_MODES = ("no_lsp", "with_lsp")
_ARM_FIELDS = {
    "mode",
    "arm_id",
    "code_agent_id",
    "server_generation",
    "identity_envelope",
    "identity_envelopes",
    "runner_events",
    "arm_evidence",
    "grader_feedback",
    "official_report",
    "official_test_output",
    "official_stdout",
    "official_stderr",
    "grader_request_id",
    "grader_receipt_digest",
    "grader_receipt_public_key",
    "usage",
    "pricing",
    "changed_files",
    "executor_attestation",
    "receipt_log",
    "executor_attestation_public_key",
}
_TOP_FIELDS = {
    "schema",
    "run_id",
    "question_id",
    "discovery_shape",
    "pricing_manifest",
    "arms",
    "questions",
}
_SURFACE_NAMES = frozenset({"runner_events", "arm_evidence", "grader_feedback", "usage"})
_UNSCOREABLE_RUNNER_STATUSES = frozenset(
    {
        "active",
        "evidence_recovery_required",
        "infrastructure_failure",
        "unverified",
        "contract_failure",
        "relocalize_required",
    }
)
_SCOREABLE_RUNNER_STATUSES = frozenset({"succeeded", "max_rounds_reached"})
_LEGAL_RUNNER_STATUSES = _UNSCOREABLE_RUNNER_STATUSES | _SCOREABLE_RUNNER_STATUSES
_TERMINAL_RUNNER_STATUSES = frozenset(
    {"succeeded", "max_rounds_reached", "unverified", "contract_failure", "relocalize_required"}
)


def _required(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise audit_module.MissingEvidenceError(f"{label} must be a non-empty string")
    return value.strip()


def _reject_unknown(value: Mapping[str, Any], allowed: set[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise audit_module.UnknownFieldError(
            f"{label} has unknown field(s): {', '.join(unknown)}"
        )


def _read_json(value: Any, label: str) -> Any:
    if isinstance(value, (Mapping, list, tuple)):
        return value
    if not isinstance(value, (str, pathlib.Path)):
        raise audit_module.MissingEvidenceError(f"{label} must be JSON or a JSON path")
    source = str(value)
    candidate = pathlib.Path(source)
    try:
        try:
            is_file = "\n" not in source and candidate.is_file()
        except OSError:
            is_file = False
        text = (
            candidate.read_text(encoding="utf-8")
            if is_file
            else source
        )
        return json.loads(text)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise audit_module.AuditError(f"read {label}: {exc}") from exc


def _object(value: Any, label: str) -> Mapping[str, Any]:
    parsed = _read_json(value, label)
    if not isinstance(parsed, Mapping):
        raise audit_module.ContractFailure(f"{label} must be an object")
    return parsed


def _mode(value: Any, label: str) -> str:
    text = _required(value, label).lower().replace("-", "_")
    aliases = {"lsp": "with_lsp", "withlsp": "with_lsp", "nolsp": "no_lsp"}
    text = aliases.get(text, text)
    if text not in _MODES:
        raise audit_module.ContractFailure(f"unsupported {label}: {value!r}")
    return text


def _identity_context(
    arm: Mapping[str, Any],
    *,
    question_id: str,
    mode: str,
) -> tuple[Any, dict[str, str]]:
    """Parse all bindings and derive the audit identity from them."""

    envelope = parse_identity_envelope(
        _read_json(arm.get("identity_envelope"), "identity envelope")
    )
    surfaces = _object(arm.get("identity_envelopes"), "identity surface envelopes")
    if set(surfaces) != set(_SURFACE_NAMES):
        raise audit_module.MissingEvidenceError(
            "identity surface envelopes require exactly: "
            + ", ".join(sorted(_SURFACE_NAMES))
        )
    for name in sorted(_SURFACE_NAMES):
        verify_identity_envelope(
            _read_json(surfaces[name], f"identity envelope for {name}"),
            envelope,
        )

    bindings = envelope.bindings
    task = _required(bindings.task.id, "identity.bindings.task.id")
    session = _required(bindings.session.id, "identity.bindings.session.id")
    thread = _required(bindings.thread.id, "identity.bindings.thread.id")
    patch = _required(bindings.patch.id, "identity.bindings.patch.id")
    arm_id = _required(arm.get("arm_id"), "arm_id")
    code_agent = _required(bindings.agent.id, "identity.bindings.agent.id")
    if task != _required(question_id, "question_id"):
        raise audit_module.IdentityError("identity task drifted from question_id")
    if _mode(arm.get("mode"), "arm.mode") != mode:
        raise audit_module.IdentityError("arm mode drifted")
    declared_agent = arm.get("code_agent_id")
    if declared_agent is not None and _required(declared_agent, "code_agent_id") != code_agent:
        raise audit_module.IdentityError("code agent drifted from identity envelope")
    return envelope, {
        "question_id": task,
        "arm_id": arm_id,
        "mode": mode,
        "session_id": session,
        "thread_id": thread,
        "patch_hash": patch,
        "identity_hash": envelope.binding_sha256,
    }


def _runner_events(
    value: Any,
    identity: Mapping[str, str],
    code_agent_id: str,
) -> list[RunnerEvent]:
    raw = _read_json(value, "runner events")
    if isinstance(raw, Mapping):
        if raw.get("schema_version") != RUNNER_EVENT_SCHEMA_VERSION:
            raise audit_module.ContractFailure("runner events have unsupported schema")
        raw = raw.get("events")
    if not isinstance(raw, Sequence) or isinstance(raw, (str, bytes, bytearray)) or not raw:
        raise audit_module.MissingEvidenceError("runner events must be a non-empty array")
    try:
        events = [RunnerEvent.from_dict(item) for item in raw]
    except (KeyError, TypeError, ValueError, RuntimeError) as exc:
        raise audit_module.ContractFailure(f"invalid runner events: {exc}") from exc
    sequences = [event.sequence for event in events]
    if sequences != list(range(1, len(events) + 1)):
        raise audit_module.ContractFailure("runner event sequence must be contiguous 1..N")
    by_sequence = {event.sequence: event for event in events}
    for event in events:
        if event.status not in _LEGAL_RUNNER_STATUSES:
            raise audit_module.ContractFailure(
                f"runner event {event.sequence} has illegal status {event.status!r}"
            )
        if event.parent_sequence is not None:
            if event.parent_sequence not in by_sequence:
                raise audit_module.ContractFailure(
                    f"runner event {event.sequence} parent is missing"
                )
            if event.parent_sequence >= event.sequence:
                raise audit_module.ContractFailure(
                    f"runner event {event.sequence} parent must precede child"
                )
        if event.kind == "terminal" and event.sequence != len(events):
            raise audit_module.ContractFailure("terminal runner event must be last")
        if event.status in _TERMINAL_RUNNER_STATUSES and event.sequence != len(events):
            raise audit_module.ContractFailure(
                f"terminal runner status {event.status!r} must be final"
            )
    expected = {
        "question_id": identity["question_id"],
        "arm_id": identity["arm_id"],
        "session_id": identity["session_id"],
        "thread_id": identity["thread_id"],
        "code_agent_id": code_agent_id,
    }
    for event in events:
        for field, expected_value in expected.items():
            if getattr(event, field) != expected_value:
                raise audit_module.IdentityError(
                    f"runner event {field} drifted from identity envelope"
                )
    quality = [event for event in events if event.kind == "quality_round"]
    numbers = [event.round_number for event in quality]
    max_rounds = audit_module.MAX_QUALITY_ROUNDS
    if not quality or numbers != list(range(1, len(numbers) + 1)) or len(quality) > max_rounds:
        raise audit_module.ContractFailure(
            f"runner quality rounds are not sequential 1..{max_rounds}"
        )
    final_status = events[-1].status
    if final_status in _UNSCOREABLE_RUNNER_STATUSES:
        raise audit_module.ContractFailure(f"runner status {final_status!r} is not scoreable")
    if final_status not in _SCOREABLE_RUNNER_STATUSES:
        raise audit_module.ContractFailure(f"runner has illegal terminal status: {final_status!r}")
    if final_status == "max_rounds_reached" and len(quality) != max_rounds:
        raise audit_module.ContractFailure(
            f"max_rounds_reached requires {max_rounds} quality rounds"
        )

    quality_by_round = {event.round_number: event for event in quality}
    for event in events:
        if event.kind not in {"evidence_recovery", "infra_retry"}:
            continue
        quality_parent = quality_by_round.get(event.round_number)
        if quality_parent is None or event.patch_hash != quality_parent.patch_hash:
            raise audit_module.IdentityError(
                f"{event.kind} round {event.round_number} patch drifted from quality round"
            )
        children = [
            child
            for child in events
            if child.kind == "grader_result" and child.parent_sequence == event.sequence
        ]
        if not children:
            raise audit_module.MissingEvidenceError(
                f"{event.kind} round {event.round_number} is missing grader child evidence"
            )
        if event.kind == "evidence_recovery" and not event.evidence_recovery_succeeded:
            raise audit_module.ContractFailure(
                f"evidence recovery round {event.round_number} was not successful"
            )
        if any(child.patch_hash != event.patch_hash for child in children):
            raise audit_module.IdentityError(
                f"{event.kind} grader child patch drifted from parent"
            )
    for quality_event in quality:
        results = [
            result
            for result in events
            if result.kind == "grader_result"
            and result.round_number == quality_event.round_number
        ]
        if not results or results[-1].grader_passed is None:
            raise audit_module.MissingEvidenceError(
                f"quality round {quality_event.round_number} is missing final grader evidence"
            )
        if results[-1].patch_hash != quality_event.patch_hash:
            raise audit_module.IdentityError(
                f"quality round {quality_event.round_number} grader patch drifted"
            )
        if results[-1].failure_kind == "infrastructure":
            raise audit_module.ContractFailure(
                f"quality round {quality_event.round_number} has unresolved infrastructure"
            )
    if events[-1].patch_hash != identity["patch_hash"]:
        raise audit_module.IdentityError("final runner patch drifted from identity envelope")
    return events


def _round_summary(
    events: Sequence[RunnerEvent],
    identity: Mapping[str, str],
    changed_files: Sequence[str],
    passed: bool,
) -> dict[str, Any]:
    rounds: list[dict[str, Any]] = []
    for quality in (event for event in events if event.kind == "quality_round"):
        results = [
            result
            for result in events
            if result.kind == "grader_result"
            and result.round_number == quality.round_number
        ]
        quality_passed = bool(results[-1].grader_passed)
        rounds.append({
            "round": quality.round_number,
            "kind": "quality",
            "status": "passed" if quality_passed else "failed",
            "passed": quality_passed,
        })
    summary = {
        **dict(identity),
        "schema": "super-dolphin/cross-file-ab-summary/v1",
        "guard_terminal": True,
        "guard_status": events[-1].status,
        "rounds": rounds,
        "changed_files": list(changed_files),
        "passed": passed,
        "passed_to_grading": passed,
        "verified": True,
        "unverified": False,
    }
    if identity["mode"] == "no_lsp":
        summary["mcp_events"] = 0
    return summary


def _source_text(value: Any, label: str) -> str | bytes | None:
    """Load an official source as content; do not let a path become report text."""

    if value is None:
        return None
    if isinstance(value, bytes):
        return value
    if not isinstance(value, str):
        raise audit_module.MissingEvidenceError(f"{label} must be text or a path")
    candidate = pathlib.Path(value)
    try:
        is_file = "\n" not in value and candidate.is_file()
    except OSError:
        is_file = False
    if is_file:
        try:
            return candidate.read_bytes()
        except OSError as exc:
            raise audit_module.AuditError(f"read {label}: {exc}") from exc
    return value


def _feedback(
    value: Any,
    *,
    identity: Mapping[str, str],
    identity_envelope: Any,
    official_report: Any,
    official_test_output: Any,
    official_stdout: Any = None,
    official_stderr: Any = None,
    grader_request_id: Any = None,
    grader_receipt_digest: Any = None,
    grader_receipt_public_key: Any = None,
) -> tuple[Any, dict[str, Any], dict[str, Any]]:
    """Verify a signed grader receipt, then select audit-owned feedback fields."""

    official_report = _source_text(official_report, "official_report")
    official_test_output = _source_text(official_test_output, "official_test_output")
    receipt_envelope = _object(value, "signed grader receipt envelope")
    if set(receipt_envelope) != {"receipt"}:
        raise audit_module.MissingEvidenceError(
            "grader_feedback must contain exactly one signed receipt"
        )
    receipt = _object(receipt_envelope.get("receipt"), "signed grader receipt")
    parsed_identity_envelope = parse_identity_envelope(identity_envelope)
    try:
        pinned_role_key = parsed_identity_envelope.grader_receipt_signing_key
        if pinned_role_key is not None and grader_receipt_public_key is not None:
            if grader_receipt_public_key != pinned_role_key.to_dict():
                raise audit_module.ContractFailure(
                    "grader receipt public key drifted from identity role binding"
                )
        verifier = (
            verifier_from(public_key=pinned_role_key.to_dict())
            if pinned_role_key is not None
            else (
            verifier_from(public_key=_object(grader_receipt_public_key, "grader receipt public key"))
            if grader_receipt_public_key is not None
            else verifier_from(identity_envelope=identity_envelope)
            )
        )
        expected_artifact = None
        if any(value is not None for value in (official_stdout, official_stderr, grader_request_id)):
            if official_stdout is None or official_stderr is None or grader_request_id is None:
                raise audit_module.ContractFailure(
                    "grader executor source streams and request id are required together"
                )
            expected_artifact = build_grader_artifact(
                task=identity["question_id"],
                request_id=_required(grader_request_id, "grader_request_id"),
                report=official_report,
                test_output=official_test_output,
                stdout=official_stdout,
                stderr=official_stderr,
            )
            if receipt.get("grader_artifact") != expected_artifact.binding:
                raise audit_module.ContractFailure(
                    "grader receipt artifact does not match executor outputs"
                )
            if grader_receipt_digest is not None:
                expected_receipt_digest = hashlib.sha256(
                    json.dumps(
                        receipt,
                        ensure_ascii=False,
                        sort_keys=True,
                        separators=(",", ":"),
                        allow_nan=False,
                    ).encode("utf-8")
                ).hexdigest()
                if grader_receipt_digest != expected_receipt_digest:
                    raise audit_module.ContractFailure(
                        "grader receipt digest drifted from receipt"
                    )
        feedback_value = validate_grader_receipt(
            receipt,
            verifier=verifier,
            task=identity["question_id"],
            arm=identity["arm_id"],
            session=identity["session_id"],
            thread=identity["thread_id"],
            patch_hash=identity["patch_hash"],
            grader_artifact=(
                expected_artifact.binding
                if expected_artifact is not None
                else receipt.get("grader_artifact")
            ),
            final_tree_hash=(
                parsed_identity_envelope.bindings.final_tree.tree_sha
                if "final_tree_hash" in receipt
                else None
            ),
        )
        if expected_artifact is not None:
            validate_grader_artifact_hashes(feedback_value, expected_artifact)
    except Exception as exc:  # noqa: BLE001 - adapter boundary maps signer errors.
        raise audit_module.ContractFailure(f"signed grader receipt rejected: {exc}") from exc
    if isinstance(feedback_value, Mapping) and isinstance(feedback_value.get("identity"), Mapping):
        # GraderFeedback.to_dict may carry runner-only execution metadata in
        # its nested identity.  Keep only the five fields owned by the
        # feedback schema before handing the mapping to its validator.
        feedback_value = dict(feedback_value)
        feedback_value["identity"] = {
            key: feedback_value["identity"][key]
            for key in ("question_id", "arm_id", "session_id", "thread_id", "patch_hash")
            if key in feedback_value["identity"]
        }
        for key in ("phase", "collected_count", "executed_count"):
            if feedback_value.get(key) in (None, ""):
                feedback_value.pop(key, None)
        if feedback_value.get("missing_symbols") == []:
            feedback_value.pop("missing_symbols", None)
    source_feedback = validate_official_grader_sources(
        official_report,
        official_test_output,
        stdout=official_stdout,
        stderr=official_stderr,
    )
    result = validate_grader_feedback(
        feedback_value,
        question_id=identity["question_id"],
        arm_id=identity["arm_id"],
        session_id=identity["session_id"],
        thread_id=identity["thread_id"],
        patch_hash=identity["patch_hash"],
    )
    if not result.source_hash:
        result = dataclasses.replace(
            result,
            source_artifact=source_feedback.source_artifact,
            source_hash=source_feedback.source_hash,
        )
    for field in (
        "status", "failure_kind", "phase", "collected_count",
        "executed_count", "missing_symbols", "passed",
    ):
        if getattr(result, field) != getattr(source_feedback, field):
            raise audit_module.ContractFailure(
                "signed grader result disagrees with official source"
            )
    runner_result = result.as_runner_result()
    canonical: dict[str, Any] = {
        "schema": result.schema,
        "status": result.status.lower(),
        "passed": result.passed,
        **dict(identity),
        "source_artifact": result.source_artifact,
        "source_hash": result.source_hash,
    }
    if runner_result.get("failure_kind"):
        canonical["failure_kind"] = runner_result["failure_kind"]
    if result.failures:
        canonical["failures"] = [item.to_dict() for item in result.failures]
    return result, canonical, {
        "source_artifact": result.source_artifact,
        "source_hash": result.source_hash,
        "official_report_hash": canonical_source_hash(official_report, None),
        "official_test_output_hash": canonical_source_hash(None, official_test_output),
    }
    if expected_artifact is not None:
        feedback_source["grader_artifact"] = expected_artifact.to_dict()
        feedback_source["grader_request_id"] = _required(
            grader_request_id, "grader_request_id"
        )
        feedback_source["grader_receipt_digest"] = hashlib.sha256(
            json.dumps(receipt, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        ).hexdigest()


def _receipt_events(canonical_evidence: Any, identity: Mapping[str, str]) -> list[dict[str, Any]]:
    if identity["mode"] == "no_lsp":
        return [{
            "type": "run_started",
            **dict(identity),
            "source": "no_lsp_isolation_attestation",
            "status": "verified",
            "mcp": False,
        }]
    events: list[dict[str, Any]] = []
    for receipt in canonical_evidence.receipts:
        raw = receipt.to_dict()
        events.append({
            "type": "mcp_tool_call",
            "tool": raw["tool"],
            "arguments": copy.deepcopy(raw["arguments"]),
            "result": copy.deepcopy(raw["result"]),
            "source": "mcp",
            **dict(identity),
        })
    return events


def _evidence_metadata(
    envelope: Any,
    canonical_evidence: Any,
    events: Sequence[RunnerEvent],
    server_generation: str,
    feedback_source: Mapping[str, Any],
) -> dict[str, Any]:
    """Preserve every validated evidence surface under the integrity hash."""

    arm_evidence = canonical_evidence.to_dict()
    receipt_metadata = []
    for receipt in canonical_evidence.receipts:
        raw = receipt.to_dict()
        receipt_metadata.append({
            key: copy.deepcopy(raw[key])
            for key in (
                "schema",
                "source",
                "task",
                "arm",
                "session",
                "thread",
                "seq",
                "request_id",
                "args_hash",
                "result_hash",
                "status",
                "server_generation",
                "tool",
            )
        })
    bindings = envelope.bindings
    common_identity = {
        "manifest": bindings.manifest.id,
        "dataset": bindings.dataset.id,
        "task": bindings.task.id,
        "base": {
            "commit_sha": bindings.base.commit_sha,
            "tree_sha": bindings.base.tree_sha,
        },
        "source_image": bindings.source_image.id,
        "toolchain": bindings.toolchain.id,
        "grader": bindings.grader.id,
        "runner": bindings.runner.id,
        "model": bindings.model.id,
        "prompt": bindings.prompt.id,
        "pricing": bindings.pricing.id,
        "scoring": bindings.scoring.id,
    }
    return {
        "identity_envelope": envelope.to_dict(),
        "common_identity": common_identity,
        "canonical_arm_evidence": arm_evidence,
        "receipt_metadata": receipt_metadata,
        "isolation": copy.deepcopy(arm_evidence.get("isolation")),
        "runner_events": [event.to_dict() for event in events],
        "server_generation": server_generation,
        "grader_source": copy.deepcopy(dict(feedback_source)),
    }


def _arm(
    value: Any,
    *,
    question_id: str,
    mode: str,
    pricing_manifest: Mapping[str, Any],
    run_id: str | None = None,
) -> tuple[dict[str, Any], dict[str, Any]]:
    raw = _object(value, f"{mode} arm evidence")
    _reject_unknown(raw, _ARM_FIELDS, f"{mode} arm evidence")
    if _mode(raw.get("mode"), f"{mode} arm.mode") != mode:
        raise audit_module.IdentityError(f"{mode} arm mode drifted")
    server_generation = _required(raw.get("server_generation"), f"{mode} server_generation")
    envelope, identity = _identity_context(raw, question_id=question_id, mode=mode)
    changed_files = raw.get("changed_files")
    if not isinstance(changed_files, Sequence) or isinstance(changed_files, (str, bytes, bytearray)) or not changed_files:
        raise audit_module.MissingEvidenceError(f"{mode} changed_files is required")
    changed = tuple(_required(item, f"{mode} changed_files item") for item in changed_files)
    if "executor_attestation" in raw:
        if "arm_evidence" in raw:
            raise audit_module.ContractFailure(
                f"{mode} cannot mix executor attestation with Agent arm_evidence"
            )
        if not isinstance(run_id, str) or not run_id.strip():
            raise audit_module.ContractFailure("executor attestation requires run_id")
        executor_key = _object(
            raw.get("executor_attestation_public_key"),
            f"{mode} executor attestation public key",
        )
        receipt_log = raw.get("receipt_log")
        if not isinstance(receipt_log, str):
            raise audit_module.MissingEvidenceError(f"{mode} receipt_log must be text")
        try:
            canonical_arm_evidence = derive_canonical_arm_evidence_from_attestation(
                _object(raw.get("executor_attestation"), f"{mode} executor attestation"),
                receipt_log=receipt_log,
                executor_verifier=Ed25519Verifier.from_mapping(executor_key),
                receipt_verifier=verifier_from(identity_envelope=envelope),
                task=identity["question_id"],
                arm=identity["arm_id"],
                session=identity["session_id"],
                thread=identity["thread_id"],
                patch_hash=identity["patch_hash"],
                final_tree_hash=envelope.bindings.final_tree.tree_sha,
                server_generation=server_generation,
                mode=mode,
                run_id=run_id,
            )
        except Exception as exc:
            raise audit_module.ContractFailure(
                f"{mode} executor attestation rejected: {exc}"
            ) from exc
    else:
        canonical_arm_evidence = validate_canonical_arm_evidence(
            _object(raw.get("arm_evidence"), f"{mode} canonical arm evidence"),
            task=identity["question_id"],
            arm=identity["arm_id"],
            session=identity["session_id"],
            thread=identity["thread_id"],
            changed_files=changed,
            server_generation=server_generation,
            patch_hash=identity["patch_hash"],
            identity_envelope=envelope,
        )
    events = _runner_events(
        raw.get("runner_events"),
        identity,
        envelope.bindings.agent.id,
    )
    feedback, canonical_feedback, feedback_source = _feedback(
        raw.get("grader_feedback"),
        identity=identity,
        identity_envelope=envelope,
        official_report=raw.get("official_report"),
        official_test_output=raw.get("official_test_output"),
        official_stdout=raw.get("official_stdout"),
        official_stderr=raw.get("official_stderr"),
        grader_request_id=raw.get("grader_request_id"),
        grader_receipt_digest=raw.get("grader_receipt_digest"),
        grader_receipt_public_key=raw.get("grader_receipt_public_key"),
    )
    if bool(feedback.passed) != bool(events[-1].status == "succeeded"):
        raise audit_module.ContractFailure(
            f"{mode} grader feedback contradicts runner terminal status"
        )
    usage = _object(raw.get("usage"), f"{mode} usage")
    pricing = _object(raw.get("pricing"), f"{mode} pricing reference")
    _reject_unknown(pricing, {"manifest_hash"}, f"{mode} pricing reference")
    manifest_hash = _required(pricing.get("manifest_hash"), f"{mode} pricing.manifest_hash")
    if manifest_hash != pricing_manifest.get("manifest_hash"):
        raise audit_module.CostError(f"{mode} pricing manifest drifted")
    agent_events = _receipt_events(canonical_arm_evidence, identity)
    canonical_arm = {
        "mode": mode,
        "summary": _round_summary(events, identity, changed, feedback.passed),
        "agent_jsonl": "\n".join(json.dumps(event, sort_keys=True) for event in agent_events),
        "grader": canonical_feedback,
        "cost": {
            "identity": dict(identity),
            "usage": copy.deepcopy(dict(usage)),
            "pricing_manifest": copy.deepcopy(dict(pricing_manifest)),
            "pricing_manifest_hash": manifest_hash,
        },
    }
    return canonical_arm, _evidence_metadata(
        envelope,
        canonical_arm_evidence,
        events,
        server_generation,
        feedback_source,
    )


def _question(
    value: Any,
    *,
    pricing_manifest: Mapping[str, Any],
    run_id: str | None = None,
) -> tuple[dict[str, Any], dict[str, Any]]:
    question = _object(value, "evidence adapter question")
    _reject_unknown(question, {"question_id", "discovery_shape", "arms"}, "question")
    question_id = _required(question.get("question_id"), "question.question_id")
    discovery_shape = _required(question.get("discovery_shape"), "question.discovery_shape")
    arms = question.get("arms")
    if not isinstance(arms, Mapping) or set(arms) != set(_MODES):
        raise audit_module.MissingEvidenceError(
            f"question {question_id} requires exactly no_lsp and with_lsp arms"
        )
    canonical_arms: dict[str, Any] = {}
    metadata_arms: dict[str, Any] = {}
    for mode in _MODES:
        canonical_arm, metadata = _arm(
            arms[mode],
            question_id=question_id,
            mode=mode,
            pricing_manifest=pricing_manifest,
            run_id=run_id,
        )
        canonical_arms[mode] = canonical_arm
        metadata_arms[mode] = metadata
    return (
        {
            "question_id": question_id,
            "discovery_shape": discovery_shape,
            "arms": canonical_arms,
        },
        {
            "question_id": question_id,
            "discovery_shape": discovery_shape,
            "arms": metadata_arms,
        },
    )


def build_canonical_input(value: Any) -> dict[str, Any]:
    """Build a canonical audit input for one or many questions."""

    raw = _object(value, "evidence adapter input")
    _reject_unknown(raw, _TOP_FIELDS, "evidence adapter input")
    if raw.get("schema") != ADAPTER_SCHEMA:
        raise audit_module.ContractFailure("unsupported evidence adapter schema")
    pricing_manifest = _object(raw.get("pricing_manifest"), "pricing_manifest")
    if "questions" in raw:
        if any(key in raw for key in ("question_id", "discovery_shape", "arms")):
            raise audit_module.ContractFailure(
                "multi-question input cannot mix single-question fields"
            )
        raw_questions = raw["questions"]
        if not isinstance(raw_questions, Sequence) or isinstance(raw_questions, (str, bytes, bytearray)) or not raw_questions:
            raise audit_module.MissingEvidenceError("evidence adapter questions must be a non-empty array")
    else:
        raw_questions = [{
            "question_id": raw.get("question_id"),
            "discovery_shape": raw.get("discovery_shape"),
            "arms": raw.get("arms"),
        }]
    questions: list[dict[str, Any]] = []
    metadata_questions: list[dict[str, Any]] = []
    owners: dict[str, dict[str, str]] = {}
    for raw_question in raw_questions:
        question, metadata = _question(
            raw_question, pricing_manifest=pricing_manifest, run_id=raw.get("run_id")
        )
        questions.append(question)
        metadata_questions.append(metadata)
        for mode in _MODES:
            summary = question["arms"][mode]["summary"]
            arm_metadata = metadata["arms"][mode]
            code_agent = arm_metadata["identity_envelope"]["bindings"]["agent"]["id"]
            owner = f"{question['question_id']}:{mode}"
            for field, value_key in (
                ("session_id", "session_id"),
                ("thread_id", "thread_id"),
            ):
                value = summary[value_key]
                previous = owners.get(f"{field}:{value}")
                if previous is not None:
                    raise audit_module.ContractFailure(
                        f"duplicate {field} {value!r}: {previous['owner']} and {owner}"
                    )
                owners[f"{field}:{value}"] = {"owner": owner}
            previous = owners.get(f"code_agent_id:{code_agent}")
            if previous is not None:
                raise audit_module.ContractFailure(
                    f"duplicate code_agent_id {code_agent!r}: {previous['owner']} and {owner}"
                )
            owners[f"code_agent_id:{code_agent}"] = {"owner": owner}
    canonical: dict[str, Any] = {
        "schema": audit_module.SCHEMA,
        "policy": copy.deepcopy(audit_module.APPROVED_POLICY),
        "policy_hash": audit_module.POLICY_HASH,
        "pricing_manifest": copy.deepcopy(dict(pricing_manifest)),
        "pricing_manifest_hash": _required(
            pricing_manifest.get("manifest_hash"), "pricing_manifest.manifest_hash"
        ),
        "questions": questions,
        "metadata": {
            "evidence_adapter": {
                "schema": ADAPTER_SCHEMA,
                "questions": metadata_questions,
            }
        },
    }
    if "run_id" in raw:
        canonical["run_id"] = _required(raw["run_id"], "run_id")
    canonical["evidence_hash"] = audit_module.canonical_evidence_hash(canonical)
    return canonical


def build_canonical_inputs(value: Any) -> dict[str, Any]:
    """Plural entry point kept explicit for multi-question callers."""

    return build_canonical_input(value)


def audit_evidence(value: Any) -> Any:
    """Build canonical input and invoke the existing authoritative audit."""

    return audit_module.audit(build_canonical_input(value))


def audit_evidence_many(value: Any) -> Any:
    """Audit a multi-question adapter envelope through the same authority."""

    return audit_module.audit(build_canonical_inputs(value))


def audit_evidence_set(value: Any) -> Any:
    """Canonical multi-question entry point used by the production launcher."""

    return audit_evidence_many(value)


adapt = build_canonical_input
build_input = build_canonical_input
audit = audit_evidence


__all__ = [
    "ADAPTER_SCHEMA",
    "audit",
    "audit_evidence",
    "audit_evidence_many",
    "audit_evidence_set",
    "adapt",
    "build_canonical_input",
    "build_canonical_inputs",
    "build_input",
]
