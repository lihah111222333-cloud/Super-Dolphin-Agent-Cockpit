#!/usr/bin/env python3
"""Fail-closed audit and scoring for a representative cross-file A/B run.

The runner which produced an experiment owns prompts, model calls, LSP
servers, and the official grader.  This module is intentionally independent
of all of those components: it only consumes the four immutable evidence
surfaces for every arm (summary, agent JSONL, official grader evidence, and
cost).  It never treats a runner's ``passed_to_grading`` hint as a pass.

The public entry point is :func:`audit`.  A JSON document has this shape::

    {
      "schema": "super-dolphin/cross-file-ab-audit/v1",
      "policy": {"quality_points_by_first_passing_round": [10, 7, 4], ...},
      "policy_hash": "<pre-run policy sha256>",
      "evidence_hash": "<canonical input sha256>",
      "pricing_manifest": {"input_usd_per_token": "...", ...,
                           "manifest_hash": "<locked sha256>"},
      "questions": [{
        "question_id": "q-01",
        "discovery_shape": "cross_file",
        "arms": {
          "no_lsp": {
            "summary": {...},
            "agent_jsonl": "{...}\\n{...}",
            "grader": {...},
            "cost": {"input_tokens": 1000, "cached_input_tokens": 0,
                     "output_tokens": 0}
          },
          "with_lsp": {...}
        }
      }]
    }

The parser is strict on every audit-owned envelope.  In particular, an
unknown field, missing arm/evidence, identity drift, duplicate session, or
contradictory official report raises :class:`ContractFailure`; callers cannot
silently obtain a partial leaderboard.
"""

from __future__ import annotations

import argparse
import dataclasses
import hashlib
import json
import math
import pathlib
import re
import sys
from collections.abc import Iterable, Mapping, Sequence
from decimal import Decimal, InvalidOperation
from typing import Any


SCHEMA = "super-dolphin/cross-file-ab-audit/v1"
MAX_QUALITY_ROUNDS = 3
QUALITY_POINTS = (10, 7, 4)
ARM_MODES = ("no_lsp", "with_lsp")
ROUND_KINDS = ("quality", "evidence_recovery", "infra_retry")
CACHE_INPUT_DISCOUNT = "0.9"
PRICING_MANIFEST_SCHEMA = "super-dolphin/cross-file-pricing/v1"

# This is the business scoring policy approved for the representative
# experiment.  It is deliberately separate from the statistical layer: the
# points are descriptive product/business points and must never be presented
# as a significance test or a confidence interval.
APPROVED_POLICY = {
    "quality_points_by_first_passing_round": list(QUALITY_POINTS),
    "cost_points_enabled": False,
    "token_usage_scored": False,
    "evidence_recovery_consumes_quality_round": False,
    "infra_retry_consumes_quality_round": False,
    "cached_input_discount": 0.9,
}


def _canonical_json_bytes(value: Any) -> bytes:
    """Encode JSON evidence deterministically for integrity hashes."""

    try:
        rendered = json.dumps(
            value,
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
            allow_nan=False,
        )
    except (TypeError, ValueError) as exc:
        raise ContractFailure(f"canonical JSON encoding failed: {exc}") from exc
    return rendered.encode("utf-8")


def _canonical_hash(value: Any) -> str:
    return hashlib.sha256(_canonical_json_bytes(value)).hexdigest()


def canonical_policy_hash(policy: Mapping[str, Any] | None = None) -> str:
    """Return the SHA-256 hash of the canonical scoring policy."""

    return _canonical_hash(APPROVED_POLICY if policy is None else policy)


POLICY_HASH = canonical_policy_hash()


def canonical_evidence_hash(payload: Mapping[str, Any]) -> str:
    """Hash the submitted evidence envelope without its self-referential hash.

    ``evidence_hash`` and ``input_evidence_hash`` are excluded only at the
    envelope root.  All question/arm evidence, policy declarations, and
    pricing-manifest declarations remain covered by the digest.
    """

    if not isinstance(payload, Mapping):
        raise ContractFailure("audit input must be an object for evidence hashing")
    material = dict(payload)
    material.pop("evidence_hash", None)
    material.pop("input_evidence_hash", None)
    metadata = material.get("metadata")
    if isinstance(metadata, Mapping):
        metadata = dict(metadata)
        metadata.pop("evidence_hash", None)
        metadata.pop("input_evidence_hash", None)
        material["metadata"] = metadata
    return _canonical_hash(material)


def _declared_integrity_value(payload: Mapping[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in payload:
            return payload[key]
    metadata = payload.get("metadata")
    if isinstance(metadata, Mapping):
        for key in keys:
            if key in metadata:
                return metadata[key]
    return None


class AuditError(RuntimeError):
    """Base class for malformed or incomplete A/B audit evidence."""


class ContractFailure(AuditError):
    """Evidence violates a fail-closed audit contract."""


class UnknownFieldError(ContractFailure):
    """An audit-owned object contains a field outside its schema."""


class MissingEvidenceError(ContractFailure):
    """A required evidence surface or field is absent."""


class IdentityError(ContractFailure):
    """Evidence belongs to a different question, arm, session, or patch."""


class OfficialReportError(ContractFailure):
    """The official grader document is malformed or contradictory."""


class CostError(ContractFailure):
    """The cost document is malformed or missing its charged amount."""


# Compatibility aliases make the failure category easy to discover for
# callers which use the wording from the audit contract.
ABAuditError = AuditError
AuditContractError = ContractFailure


def _required_text(value: Any, label: str) -> str:
    if not isinstance(value, str):
        raise MissingEvidenceError(f"{label} must be a non-empty string")
    text = value.strip()
    if not text:
        raise MissingEvidenceError(f"{label} is required")
    return text


def _normalise_mode(value: Any, label: str = "mode") -> str:
    text = _required_text(value, label).lower().replace("-", "_")
    aliases = {"nolsp": "no_lsp", "lsp": "with_lsp", "withlsp": "with_lsp"}
    text = aliases.get(text, text)
    if text not in ARM_MODES:
        raise ContractFailure(f"unsupported {label}: {value!r}")
    return text


def _normalise_path(value: Any, label: str = "path") -> str:
    text = _required_text(value, label).replace("\\", "/")
    if text.startswith("file://"):
        text = text[7:]
    while text.startswith("./"):
        text = text[2:]
    if not text:
        raise MissingEvidenceError(f"{label} is required")
    return pathlib.PurePosixPath(text).as_posix()


def _normalise_hash(value: Any, label: str) -> str:
    return _required_text(value, label)


def _reject_unknown(value: Mapping[str, Any], allowed: set[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise UnknownFieldError(f"{label} has unknown field(s): {', '.join(unknown)}")


def _first(value: Mapping[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in value:
            return value[key]
    return None


def _as_bool(value: Any, label: str) -> bool:
    if isinstance(value, bool):
        return value
    raise ContractFailure(f"{label} must be boolean")


_UNSCOREABLE_STATUS_MARKERS = {
    "active",
    "running",
    "pending",
    "infra",
    "infra_failure",
    "infrastructure",
    "infrastructure_failure",
    "timeout",
    "censored",
    "unverified",
    "unknown",
    "outcome_unknown",
}


def _normalise_status(value: Any) -> str:
    return str(value).strip().lower().replace("-", "_").replace(" ", "_")


def _reject_unscoreable_status(value: Any, label: str) -> None:
    """Reject evidence which is not a verified terminal observation."""

    if value is None:
        return
    if isinstance(value, bool):
        return
    status = _normalise_status(value)
    if status in _UNSCOREABLE_STATUS_MARKERS:
        raise ContractFailure(f"{label} is not scoreable: {status}")


def _as_int(value: Any, label: str) -> int:
    if isinstance(value, bool):
        raise ContractFailure(f"{label} must be an integer")
    try:
        result = int(value)
    except (TypeError, ValueError) as exc:
        raise ContractFailure(f"{label} must be an integer") from exc
    if isinstance(value, float) and value != result:
        raise ContractFailure(f"{label} must be an integer")
    return result


def _read_json_value(value: Any, label: str) -> Any:
    """Read an inline JSON value or a JSON file without accepting YAML."""

    if isinstance(value, (Mapping, list, tuple)):
        return value
    if not isinstance(value, str):
        raise MissingEvidenceError(f"{label} must be an object, array, JSON, or path")
    candidate = pathlib.Path(value)
    try:
        try:
            is_file = "\n" not in value and candidate.is_file()
        except OSError:
            is_file = False
        if is_file:
            text = candidate.read_text(encoding="utf-8")
        else:
            text = value
        return json.loads(text)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise AuditError(f"read {label}: {exc}") from exc


def _read_agent_events(value: Any) -> tuple[Mapping[str, Any], ...]:
    """Read a JSONL string/path, an event list, or an ``{"events": ...}`` object."""

    if isinstance(value, Mapping):
        _reject_unknown(value, {"events", "lines", "identity", "hash", "trajectory_hash"}, "agent JSONL envelope")
        value = value.get("events", value.get("lines"))
        if value is None:
            raise MissingEvidenceError("agent JSONL envelope needs events")
    elif isinstance(value, str):
        source = value
        candidate = pathlib.Path(source)
        try:
            try:
                is_file = "\n" not in source and candidate.is_file()
            except OSError:
                is_file = False
            if is_file:
                source = candidate.read_text(encoding="utf-8")
        except OSError as exc:
            raise AuditError(f"read agent JSONL: {exc}") from exc
        events: list[Mapping[str, Any]] = []
        for line_number, line in enumerate(source.splitlines(), 1):
            if not line.strip():
                continue
            try:
                event = json.loads(line)
            except json.JSONDecodeError as exc:
                raise AuditError(f"agent JSONL line {line_number} is not JSON: {exc}") from exc
            if not isinstance(event, Mapping):
                raise ContractFailure(f"agent JSONL line {line_number} must be an object")
            events.append(event)
        value = events
    if not isinstance(value, Sequence) or isinstance(value, (str, bytes, bytearray)):
        raise MissingEvidenceError("agent JSONL must contain an event sequence")
    if not value:
        raise MissingEvidenceError("agent JSONL must not be empty")
    result: list[Mapping[str, Any]] = []
    for index, event in enumerate(value, 1):
        if not isinstance(event, Mapping):
            raise ContractFailure(f"agent event {index} must be an object")
        result.append(event)
    return tuple(result)


def adapt_codex_jsonl(value: Any) -> tuple[Mapping[str, Any], ...]:
    """Adapt direct or wrapped Codex JSONL into canonical event objects.

    Codex runners may persist an event as ``{"payload": {...}}`` while the
    benchmark event stream stores the payload directly.  The adapter unwraps
    only that transport envelope and never synthesizes arm identity fields;
    identity validation remains the responsibility of the canonical scorer.
    """

    if isinstance(value, Mapping) and "payload" in value and "events" not in value and "lines" not in value:
        value = [value]
    events = _read_agent_events(value)
    adapted: list[Mapping[str, Any]] = []
    for index, event in enumerate(events, 1):
        payload = event.get("payload")
        if payload is None:
            adapted.append(event)
            continue
        if not isinstance(payload, Mapping):
            raise ContractFailure(f"Codex JSONL event {index}.payload must be an object")
        canonical = dict(payload)
        for aliases in _IDENTITY_FIELD_ALIASES.values():
            for key in aliases:
                if key in event and key not in canonical:
                    canonical[key] = event[key]
        adapted.append(canonical)
    return tuple(adapted)


@dataclasses.dataclass(frozen=True)
class Identity:
    question_id: str
    arm_id: str
    mode: str
    session_id: str
    thread_id: str
    patch_hash: str
    identity_hash: str = ""

    def key(self) -> tuple[str, str, str, str, str, str]:
        return (
            self.question_id,
            self.arm_id,
            self.mode,
            self.session_id,
            self.thread_id,
            self.patch_hash,
        )

    def canonical_hash(self) -> str:
        payload = json.dumps(self.key(), ensure_ascii=False, separators=(",", ":"))
        return hashlib.sha256(payload.encode("utf-8")).hexdigest()


_IDENTITY_FIELD_ALIASES = {
    "question_id": ("question_id", "question", "task_id"),
    "arm_id": ("arm_id", "arm", "variant_id"),
    "mode": ("mode", "run_mode"),
    "session_id": ("session_id", "session"),
    "thread_id": ("thread_id", "thread"),
    "patch_hash": ("patch_hash", "candidate_hash", "artifact_hash", "hash"),
    "identity_hash": ("identity_hash", "run_hash", "identity_digest"),
}
_REQUIRED_IDENTITY_FIELDS = frozenset({
    "question_id", "arm_id", "mode", "session_id", "thread_id", "patch_hash",
})
_COMMON_IDENTITY_COMPONENTS = (
    "manifest", "dataset", "task", "base", "source_image", "toolchain",
    "grader", "runner", "model", "prompt", "pricing", "scoring", "policy",
)
_REQUIRED_COMMON_IDENTITY_COMPONENTS = frozenset({
    "task", "base", "source_image", "model", "prompt", "policy", "grader",
})
_COMMON_IDENTITY_ALIASES = {
    "manifest": ("manifest_hash", "manifest_id", "manifest"),
    "dataset": ("dataset_hash", "dataset_id", "dataset"),
    "task": ("task_hash", "task_id", "task", "question_id"),
    "base": ("base_hash", "base_id", "base", "base_commit", "base_commit_sha", "base_tree", "base_tree_sha"),
    "source_image": ("source_image_hash", "source_image_id", "source_image", "image_hash", "image_id", "image"),
    "toolchain": ("toolchain_hash", "toolchain_id", "toolchain"),
    "grader": ("grader_hash", "grader_id", "grader"),
    "runner": ("runner_hash", "runner_id", "runner"),
    "model": ("model_hash", "model_id", "model"),
    "prompt": ("prompt_hash", "prompt_id", "prompt"),
    "pricing": ("pricing_hash", "pricing_id", "pricing"),
    "scoring": ("scoring_hash", "scoring_id", "scoring"),
    "policy": ("policy_hash", "policy_id", "policy"),
}


@dataclasses.dataclass(frozen=True)
class CommonIdentity:
    """The A/B-shared identity components; patch identity is intentionally absent."""

    components: Mapping[str, str]


def _common_identity_from(value: Mapping[str, Any] | None, *, label: str) -> CommonIdentity | None:
    if value is None or not isinstance(value, Mapping):
        return None
    source: Mapping[str, Any] = value
    envelope = value.get("identity_envelope")
    if isinstance(envelope, Mapping):
        bindings = envelope.get("bindings")
        if isinstance(bindings, Mapping):
            source = bindings
    explicit = value.get("common_identity")
    if isinstance(explicit, Mapping):
        source = explicit
    components: dict[str, str] = {}
    for component in _COMMON_IDENTITY_COMPONENTS:
        aliases = _COMMON_IDENTITY_ALIASES[component]
        candidate = next((source[key] for key in aliases if key in source), None)
        if candidate is None:
            continue
        if isinstance(candidate, Mapping):
            components[component] = _canonical_hash(candidate)
        else:
            components[component] = _required_text(candidate, f"{label}.{component}")
    if not components:
        return None
    return CommonIdentity(components)


def _merge_common_identity(
    primary: CommonIdentity | None,
    secondary: CommonIdentity | None,
    *,
    label: str,
) -> CommonIdentity | None:
    if primary is None:
        return secondary
    if secondary is None:
        return primary
    merged = dict(primary.components)
    for component, value in secondary.components.items():
        if component in merged and merged[component] != value:
            raise IdentityError(f"{label} common identity drifted at {component}")
        merged[component] = value
    return CommonIdentity(merged)


def _bind_policy_identity(common: CommonIdentity | None, policy_hash: str) -> CommonIdentity | None:
    if common is None:
        return None
    components = dict(common.components)
    declared = components.get("policy")
    if declared is not None and declared != policy_hash:
        raise IdentityError("common policy identity drifted from policy_hash")
    components["policy"] = policy_hash
    return CommonIdentity(components)


@dataclasses.dataclass(frozen=True)
class PartialIdentity:
    """A transport event's declared subset of the arm identity envelope."""

    fields: Mapping[str, str]


def _identity_declarations(value: Mapping[str, Any], *, label: str) -> dict[str, Any]:
    nested = value.get("identity")
    if nested is not None and not isinstance(nested, Mapping):
        raise IdentityError(f"{label}.identity must be an object")
    sources: list[Mapping[str, Any]] = []
    if isinstance(nested, Mapping):
        sources.append(nested)
    sources.append(value)
    declarations: dict[str, Any] = {}
    for canonical, aliases in _IDENTITY_FIELD_ALIASES.items():
        seen: list[tuple[str, Any]] = []
        for source in sources:
            for alias in aliases:
                if alias in source:
                    seen.append((alias, source[alias]))
        if not seen:
            continue
        first = seen[0][1]
        for alias, candidate in seen[1:]:
            if canonical == "mode":
                equivalent = _normalise_mode(candidate, f"{label}.{canonical}") == _normalise_mode(first, f"{label}.{canonical}")
            elif isinstance(candidate, str) and isinstance(first, str):
                equivalent = candidate.strip() == first.strip()
            else:
                equivalent = candidate == first
            if not equivalent:
                raise IdentityError(f"{label}.{canonical} has conflicting aliases ({alias})")
        declarations[canonical] = first
    return declarations


def _normalise_identity_declarations(declarations: Mapping[str, Any], *, label: str) -> dict[str, str]:
    result: dict[str, str] = {}
    for field, value in declarations.items():
        if field == "mode":
            result[field] = _normalise_mode(value, f"{label}.{field}")
        else:
            result[field] = _required_text(value, f"{label}.{field}")
    return result


def _is_transport_identity_event(event: Mapping[str, Any]) -> bool:
    """Return whether a Codex lifecycle/transport event may carry partial IDs."""

    event_type = _event_type(event)
    return event_type.startswith((
        "thread_", "turn_", "item_", "response_", "session_", "task_",
    )) or event_type in {"token_count", "run_started", "run_completed"}


def _identity_from(value: Mapping[str, Any], *, label: str, question_id: str | None = None,
                   arm_id: str | None = None, mode: str | None = None,
                   require_all: bool = True) -> Identity | None:
    nested = value.get("identity")
    if nested is not None:
        if not isinstance(nested, Mapping):
            raise IdentityError(f"{label}.identity must be an object")
        # Flat fields are intentionally allowed to override only when equal;
        # duplicate declarations with drift are caught below.
        merged = dict(nested)
        merged.update({key: value[key] for key in (
            "question_id", "arm_id", "mode", "session_id", "thread_id",
            "patch_hash", "identity_hash"
        ) if key in value})
    else:
        merged = value

    qid = _first(merged, "question_id", "question", "task_id")
    aid = _first(merged, "arm_id", "arm", "variant_id")
    mde = _first(merged, "mode", "run_mode")
    sid = _first(merged, "session_id", "session")
    tid = _first(merged, "thread_id", "thread")
    phash = _first(merged, "patch_hash", "candidate_hash", "artifact_hash", "hash")
    ihash = _first(merged, "identity_hash", "run_hash", "identity_digest")

    if all(item is None for item in (qid, aid, mde, sid, tid, phash, ihash)) and not require_all:
        return None
    if not require_all and all(item is None for item in (qid, aid, mde, sid, tid, phash)):
        return None

    qid = _required_text(qid if qid is not None else question_id, f"{label}.question_id")
    aid = _required_text(aid if aid is not None else arm_id, f"{label}.arm_id")
    mde = _normalise_mode(mde if mde is not None else mode, f"{label}.mode")
    sid = _required_text(sid, f"{label}.session_id")
    tid = _required_text(tid, f"{label}.thread_id")
    phash = _normalise_hash(phash, f"{label}.patch_hash")
    ihash = "" if ihash is None else _required_text(ihash, f"{label}.identity_hash")
    identity = Identity(qid, aid, mde, sid, tid, phash, ihash)
    if question_id is not None and identity.question_id != question_id:
        raise IdentityError(f"{label}.question_id drifted from question {question_id}")
    if arm_id is not None and identity.arm_id != arm_id:
        raise IdentityError(f"{label}.arm_id drifted from arm {arm_id}")
    if mode is not None and identity.mode != mode:
        raise IdentityError(f"{label}.mode drifted from arm mode {mode}")
    return identity


def _check_identity(
    expected: Identity,
    actual: Identity | PartialIdentity | None,
    label: str,
    *,
    required: bool = False,
) -> None:
    if actual is None:
        if required:
            raise IdentityError(f"{label} identity is required")
        return
    if isinstance(actual, PartialIdentity):
        expected_fields = {
            "question_id": expected.question_id,
            "arm_id": expected.arm_id,
            "mode": expected.mode,
            "session_id": expected.session_id,
            "thread_id": expected.thread_id,
            "patch_hash": expected.patch_hash,
            "identity_hash": expected.identity_hash,
        }
        for field, value in actual.fields.items():
            expected_value = expected_fields[field]
            if field == "identity_hash" and not expected_value:
                raise IdentityError(f"{label} identity_hash cannot be verified")
            if value != expected_value:
                raise IdentityError(f"{label} identity/hash drifted")
        return
    if actual.key() != expected.key():
        raise IdentityError(f"{label} identity/hash drifted")
    if actual.identity_hash:
        if not expected.identity_hash or actual.identity_hash != expected.identity_hash:
            raise IdentityError(f"{label} identity_hash drifted")


_SUMMARY_FIELDS = {
    "schema", "question_id", "question", "task_id", "arm_id", "arm", "mode", "run_mode",
    "session_id", "session", "thread_id", "thread", "agent_id", "patch_hash", "candidate_hash",
    "artifact_hash", "hash", "identity_hash", "run_hash", "identity_digest", "trajectory_hash",
    "guard_terminal", "terminal", "guard_status", "terminal_status", "status", "final_status",
    "passed", "passed_to_grading", "rounds", "round", "quality_round", "quality_rounds",
    "changed_files", "diff_files", "files", "contract_failures", "failures", "infrastructure_failures",
    "unresolved_infra", "infra_unresolved", "infra_status", "infra_resolved", "prompt_markers", "mcp_events", "identity",
    "censored", "unverified", "verified", "verification_status", "evidence_status", "result_status", "execution_status",
    "score_status", "common_identity", "identity_envelope", "base_hash", "base_id", "base_commit", "base_commit_sha",
    "base_tree", "base_tree_sha", "source_image_hash", "source_image_id", "image_hash", "image_id", "model_hash",
    "model_id", "model", "prompt_hash", "prompt_id", "grader_hash", "grader_id", "manifest_hash", "manifest_id",
    "dataset_hash", "dataset_id", "runner_hash", "runner_id", "toolchain_hash", "toolchain_id", "pricing_hash",
    "pricing_id", "scoring_hash", "scoring_id", "policy_hash", "policy_id",
}


def _load_object(value: Any, label: str) -> Mapping[str, Any]:
    value = _read_json_value(value, label)
    if not isinstance(value, Mapping):
        raise ContractFailure(f"{label} must be a JSON object")
    return value


def _parse_changed_files(value: Any, *, required: bool, label: str) -> tuple[str, ...]:
    if value is None:
        if required:
            raise MissingEvidenceError(f"{label} is required")
        return ()
    if not isinstance(value, Sequence) or isinstance(value, (str, bytes, bytearray)):
        raise ContractFailure(f"{label} must be an array")
    paths = tuple(_normalise_path(item, label) for item in value)
    if len(set(paths)) != len(paths):
        raise ContractFailure(f"{label} contains duplicate files")
    if required and not paths:
        raise MissingEvidenceError(f"{label} must not be empty")
    return paths


_ROUND_FIELDS = {
    "round", "round_number", "number", "kind", "type", "status", "outcome", "passed", "quality",
    "contract_failure", "contract_failures", "recovered", "evidence_recovered", "resolved",
    "infra_recovered", "failure_kind", "infrastructure_failure", "message",
}
_PASS_STATUSES = {"passed", "pass", "success", "succeeded", "accepted", "recovered", "resolved", "ok", "true"}
_FAIL_STATUSES = {"failed", "fail", "error", "rejected", "false", "contract_failure", "infrastructure_failure", "infra_failure"}


@dataclasses.dataclass(frozen=True)
class RoundEvidence:
    number: int
    kind: str
    passed: bool
    recovered: bool = False
    contract_failure: bool = False
    infrastructure_failure: bool = False


def _normalise_round_kind(value: Any) -> str:
    text = _required_text(value, "round kind").lower().replace("-", "_").replace(" ", "_")
    aliases = {
        "quality_round": "quality",
        "quality": "quality",
        "evidence": "evidence_recovery",
        "evidence_recovery_round": "evidence_recovery",
        "infra": "infra_retry",
        "infrastructure_retry": "infra_retry",
    }
    text = aliases.get(text, text)
    if text not in ROUND_KINDS:
        raise ContractFailure(f"unsupported round kind: {value!r}")
    return text


def _parse_rounds(summary: Mapping[str, Any]) -> tuple[RoundEvidence, ...]:
    raw = summary.get("rounds")
    if raw is None:
        raw = summary.get("quality_rounds")
    if raw is None and "quality_round" in summary:
        raw = [{"round": summary["quality_round"], "kind": "quality", "status": "passed"}]
    if raw is None and "round" in summary:
        raw = [{"round": summary["round"], "kind": "quality", "status": "passed"}]
    if raw is None:
        raise MissingEvidenceError("summary.rounds (or quality_round) is required")
    if not isinstance(raw, Sequence) or isinstance(raw, (str, bytes, bytearray)):
        raise ContractFailure("summary.rounds must be an array")
    parsed: list[RoundEvidence] = []
    for index, item in enumerate(raw, 1):
        if not isinstance(item, Mapping):
            raise ContractFailure(f"summary.rounds[{index}] must be an object")
        _reject_unknown(item, _ROUND_FIELDS, f"summary.rounds[{index}]")
        number = _first(item, "round", "round_number", "number")
        if number is None:
            raise MissingEvidenceError(f"summary.rounds[{index}].round is required")
        number = _as_int(number, f"summary.rounds[{index}].round")
        if number < 1 or number > MAX_QUALITY_ROUNDS:
            raise ContractFailure(f"summary.rounds[{index}].round outside 1..{MAX_QUALITY_ROUNDS}")
        kind = _normalise_round_kind(_first(item, "kind", "type") or "quality")
        status = _first(item, "status", "outcome")
        passed_value = item.get("passed")
        if passed_value is not None:
            passed = _as_bool(passed_value, f"summary.rounds[{index}].passed")
        elif status is not None:
            status_text = _required_text(status, f"summary.rounds[{index}].status").lower().replace("-", "_")
            _reject_unscoreable_status(status_text, f"summary.rounds[{index}].status")
            if status_text in _PASS_STATUSES:
                passed = True
            elif status_text in _FAIL_STATUSES:
                passed = False
            else:
                raise ContractFailure(f"unknown round status: {status!r}")
        else:
            raise MissingEvidenceError(f"summary.rounds[{index}] needs status or passed")
        recovered_value = _first(item, "recovered", "evidence_recovered", "resolved", "infra_recovered")
        recovered = False if recovered_value is None else _as_bool(recovered_value, f"summary.rounds[{index}].recovered")
        failure_value = _first(item, "contract_failure", "contract_failures")
        contract_failure = bool(failure_value) if failure_value is not None else False
        infra_value = _first(item, "infrastructure_failure", "failure_kind")
        infrastructure_failure = bool(infra_value)
        if isinstance(infra_value, str):
            _reject_unscoreable_status(infra_value, f"summary.rounds[{index}].failure_kind")
            infrastructure_failure = infra_value.lower().replace("-", "_") in {
                "infra", "infrastructure", "infra_retry", "infrastructure_failure", "timeout"
            }
        parsed.append(RoundEvidence(number, kind, passed, recovered, contract_failure, infrastructure_failure))
    numbers = [item.number for item in parsed]
    if numbers != list(range(1, len(numbers) + 1)):
        raise ContractFailure("summary.rounds must be sequential without duplicate numbers")
    if len(parsed) > MAX_QUALITY_ROUNDS:
        raise ContractFailure(f"at most {MAX_QUALITY_ROUNDS} rounds are allowed")

    # Contract failures and infrastructure retries are their own channels.
    # A failure is only acceptable when a later recovery is explicit; a final
    # official pass cannot retroactively hide an unresolved contract failure.
    open_contract = False
    open_infra = False
    for item in parsed:
        if item.contract_failure:
            open_contract = True
        if item.infrastructure_failure:
            open_infra = True
        if item.kind == "evidence_recovery" and item.passed and item.recovered:
            open_contract = False
        if item.kind == "infra_retry" and item.passed and item.recovered:
            open_infra = False
    if open_contract:
        raise ContractFailure("contract failure was not recovered")
    if open_infra:
        raise ContractFailure("infrastructure failure was not recovered")
    for item in parsed:
        if item.kind == "evidence_recovery" and not (item.passed and item.recovered):
            raise ContractFailure(f"evidence recovery round {item.number} did not recover evidence")
        if item.kind == "infra_retry" and item.infrastructure_failure and not (item.passed and item.recovered):
            raise ContractFailure(f"infrastructure retry round {item.number} did not recover infrastructure")
    return tuple(parsed)


_TERMINAL_STATUSES = {
    "success", "succeeded", "passed", "completed", "terminal", "failed", "rejected", "max_rounds_reached",
    "completed_with_failure", "terminal_failure",
}


@dataclasses.dataclass(frozen=True)
class SummaryEvidence:
    identity: Identity
    changed_files: tuple[str, ...]
    rounds: tuple[RoundEvidence, ...]
    guard_status: str
    guard_terminal: bool
    passed_hint: bool | None
    passed_to_grading: bool | None
    unresolved_infra: bool
    declared_prompt_markers: tuple[str, ...]
    declared_mcp_events: int | None
    common_identity: CommonIdentity | None = None


def _parse_summary(
    value: Any,
    *,
    question_id: str,
    arm_id: str,
    mode: str,
    identity_metadata: Mapping[str, Any] | None = None,
) -> SummaryEvidence:
    summary = _load_object(value, "summary")
    _reject_unknown(summary, _SUMMARY_FIELDS, "summary")
    schema = summary.get("schema")
    if schema is not None and schema not in {SCHEMA, "super-dolphin/cross-file-ab-summary/v1", "ab-summary/v1"}:
        raise ContractFailure(f"unsupported summary schema: {schema!r}")
    for field in ("verification_status", "evidence_status", "result_status", "execution_status", "score_status"):
        _reject_unscoreable_status(summary.get(field), f"summary.{field}")
    for field in ("censored", "unverified"):
        if field in summary and _as_bool(summary[field], f"summary.{field}"):
            raise ContractFailure(f"summary.{field} evidence is not scoreable")
    if "verified" in summary and not _as_bool(summary["verified"], "summary.verified"):
        raise ContractFailure("summary.verified evidence is not scoreable")
    # The arm label in the input selects the mode, but the persisted arm_id is
    # evidence owned by the runner (it is not required to be a synthesized
    # ``question_id:mode`` string).  We compare that exact ID across all
    # evidence surfaces below.
    identity = _identity_from(summary, label="summary", question_id=question_id, mode=mode)
    assert identity is not None
    changed = _parse_changed_files(
        _first(summary, "changed_files", "diff_files", "files"),
        required=mode == "with_lsp",
        label="summary.changed_files",
    )
    rounds = _parse_rounds(summary)
    guard_status_raw = _first(summary, "guard_status", "terminal_status", "final_status", "status")
    if guard_status_raw is None:
        guard_status = "terminal" if summary.get("guard_terminal") is True or summary.get("terminal") is True else ""
    else:
        guard_status = _required_text(guard_status_raw, "summary.guard_status").lower().replace("-", "_").replace(" ", "_")
    _reject_unscoreable_status(guard_status, "summary.guard_status")
    terminal_value = _first(summary, "guard_terminal", "terminal")
    if terminal_value is None:
        terminal = guard_status in _TERMINAL_STATUSES
    else:
        terminal = _as_bool(terminal_value, "summary.guard_terminal")
    if not terminal or guard_status not in _TERMINAL_STATUSES:
        raise ContractFailure("summary guard must be in a legal terminal state")
    passed_hint_value = summary.get("passed")
    passed_hint = None if passed_hint_value is None else _as_bool(passed_hint_value, "summary.passed")
    passed_to_grading_value = summary.get("passed_to_grading")
    passed_to_grading = None if passed_to_grading_value is None else _as_bool(passed_to_grading_value, "summary.passed_to_grading")
    unresolved = _first(summary, "unresolved_infra", "infra_unresolved")
    unresolved_infra = False if unresolved is None else _as_bool(unresolved, "summary.unresolved_infra")
    if unresolved_infra:
        raise ContractFailure("summary has unresolved infrastructure failure")
    markers = summary.get("prompt_markers", ())
    if not isinstance(markers, Sequence) or isinstance(markers, (str, bytes, bytearray)):
        raise ContractFailure("summary.prompt_markers must be an array")
    declared_markers = tuple(_required_text(item, "summary.prompt_markers item") for item in markers)
    declared_mcp = summary.get("mcp_events")
    if declared_mcp is not None:
        declared_mcp = _as_int(declared_mcp, "summary.mcp_events")
        if declared_mcp < 0:
            raise ContractFailure("summary.mcp_events cannot be negative")
    declared_failures = _first(summary, "contract_failures", "failures")
    if declared_failures is not None:
        if not isinstance(declared_failures, Sequence) or isinstance(declared_failures, (str, bytes, bytearray)):
            raise ContractFailure("summary.contract_failures must be an array")
        for index, failure in enumerate(declared_failures, 1):
            if isinstance(failure, str):
                if not failure.strip():
                    raise ContractFailure(f"summary.contract_failures[{index}] is empty")
            elif isinstance(failure, Mapping):
                _reject_unknown(failure, {"round", "kind", "message", "resolved", "recovered", "status"}, f"summary.contract_failures[{index}]")
                resolved = _first(failure, "resolved", "recovered")
                if resolved is False:
                    raise ContractFailure("contract failure was not recovered")
            else:
                raise ContractFailure(f"summary.contract_failures[{index}] must be text or object")
    common_identity = _merge_common_identity(
        _common_identity_from(summary, label="summary"),
        _common_identity_from(identity_metadata, label="identity metadata"),
        label="summary",
    )
    return SummaryEvidence(
        identity, changed, rounds, guard_status, terminal, passed_hint, passed_to_grading,
        unresolved_infra, declared_markers, declared_mcp, common_identity,
    )


_EVENT_FIELDS = {
    "type", "event", "kind", "tool", "method", "arguments", "params", "result", "response", "output", "content",
    "item", "status", "id", "timestamp", "round", "round_number", "question_id", "arm_id", "arm", "mode", "run_mode",
    "session_id", "session", "thread_id", "thread", "patch_hash", "candidate_hash", "identity_hash", "identity",
    "prompt", "system_prompt", "user_prompt", "instructions", "text", "message", "source", "mcp", "data",
    # Real Codex JSONL transport fields. They are accepted as non-authoritative
    # telemetry; pass/fail and tool coverage still come from the strict
    # evidence surfaces below.
    "usage", "aggregated_output", "exit_code", "command", "duration_ms", "error", "server", "changes", "payload",
    "turn_id", "response_id", "item_id", "cache_write_input_tokens", "reasoning_output_tokens",
}
_MCP_TOOL_NAMES = {"structure", "xref", "diagnostics"}
_PROMPT_KEYS = {"prompt", "system_prompt", "user_prompt", "instructions"}
_LSP_PROMPT_RE = re.compile(r"\b(?:mcp|lsp|structure|xref|diagnostics)\b", re.IGNORECASE)
_LOCATION_RE = re.compile(r"(?<![A-Za-z0-9_])((?:[A-Za-z]:)?[^\s\"'(),]+):(\d+):(\d+)(?!\d)")


def _event_type(event: Mapping[str, Any]) -> str:
    value = _first(event, "type", "event", "kind", "method")
    return "" if value is None else str(value).strip().lower().replace("-", "_").replace(".", "_")


def _event_identity(
    event: Mapping[str, Any],
    *,
    transport: bool = False,
) -> Identity | PartialIdentity | None:
    declarations = _identity_declarations(event, label="agent event")
    if not declarations:
        return None
    normalised = _normalise_identity_declarations(declarations, label="agent event")
    if _REQUIRED_IDENTITY_FIELDS.issubset(normalised):
        return _identity_from(event, label="agent event", require_all=True)
    if not transport:
        missing = sorted(_REQUIRED_IDENTITY_FIELDS - set(normalised))
        raise MissingEvidenceError(
            "agent event identity envelope is incomplete: missing " + ", ".join(missing)
        )
    return PartialIdentity(normalised)


def _flatten_event(event: Mapping[str, Any]) -> tuple[Mapping[str, Any], ...]:
    nested = event.get("item")
    if isinstance(nested, Mapping):
        return (event, nested)
    return (event,)


def _is_mcp_event(event: Mapping[str, Any]) -> bool:
    for item in _flatten_event(event):
        tool = item.get("tool")
        source = str(item.get("source", "")).lower()
        typ = _event_type(item)
        if tool is not None or item.get("mcp") is True or source == "mcp" or "mcp" in typ:
            return True
    return False


def _tool_name(event: Mapping[str, Any]) -> str:
    for item in _flatten_event(event):
        if item.get("tool") is not None:
            return str(item["tool"]).strip().lower().replace("-", "_")
        typ = _event_type(item)
        for name in _MCP_TOOL_NAMES:
            if name in typ:
                return name
    return ""


def _event_args(event: Mapping[str, Any]) -> Mapping[str, Any]:
    for item in reversed(_flatten_event(event)):
        args = _first(item, "arguments", "params")
        if args is not None:
            if not isinstance(args, Mapping):
                raise ContractFailure("MCP event arguments must be an object")
            return args
    return {}


def _event_result(event: Mapping[str, Any]) -> Any:
    for item in reversed(_flatten_event(event)):
        result = _first(item, "result", "response", "output", "content", "data")
        if result is not None:
            return result
    return None


def _prompt_marker_strings(event: Mapping[str, Any]) -> tuple[str, ...]:
    typ = _event_type(event)
    if not any(token in typ for token in ("prompt", "message", "instruction", "system", "user")) and not any(key in event for key in _PROMPT_KEYS):
        return ()
    found: list[str] = []
    def visit(value: Any) -> None:
        if isinstance(value, str) and _LSP_PROMPT_RE.search(value):
            found.append(value)
        elif isinstance(value, Mapping):
            for key, child in value.items():
                if key in _PROMPT_KEYS or key in {"text", "message", "content"}:
                    visit(child)
        elif isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
            for child in value:
                visit(child)
    for key in _PROMPT_KEYS | {"text", "message", "content"}:
        if key in event:
            visit(event[key])
    return tuple(found)


def _path_line_column(value: Mapping[str, Any]) -> tuple[str, int, int] | None:
    path = _first(value, "file_path", "file", "path", "uri", "source")
    line = _first(value, "line", "line_number")
    column = _first(value, "column", "character", "col")
    if path is None or line is None or column is None:
        return None
    try:
        return _normalise_path(path), _as_int(line, "location.line"), _as_int(column, "location.column")
    except (AuditError, TypeError, ValueError):
        return None


def _locations(value: Any) -> set[str]:
    """Extract canonical ``path:line:column`` locations from structure output."""

    found: set[str] = set()
    def visit(item: Any) -> None:
        if isinstance(item, str):
            for match in _LOCATION_RE.finditer(item):
                try:
                    path = _normalise_path(match.group(1), "structure location path")
                    found.add(f"{path}:{int(match.group(2))}:{int(match.group(3))}")
                except AuditError:
                    continue
        elif isinstance(item, Mapping):
            location = _path_line_column(item)
            if location is not None:
                path, line, column = location
                found.add(f"{path}:{line}:{column}")
            # LSP range/start objects commonly use zero-based line/character;
            # preserve explicit string positions as the authoritative form and
            # only inspect nested values when a full path is present.
            for child in item.values():
                visit(child)
        elif isinstance(item, Sequence) and not isinstance(item, (str, bytes, bytearray)):
            for child in item:
                visit(child)
    visit(value)
    return found


def _normalise_position(value: Any) -> str:
    text = _required_text(value, "xref pos").strip()
    match = _LOCATION_RE.search(text)
    if not match:
        raise ContractFailure(f"xref pos is not a path:line:column position: {text!r}")
    path = _normalise_path(match.group(1), "xref pos path")
    return f"{path}:{int(match.group(2))}:{int(match.group(3))}"


@dataclasses.dataclass(frozen=True)
class TrajectoryEvidence:
    events: tuple[Mapping[str, Any], ...]
    mcp_count: int
    prompt_markers: tuple[str, ...]
    structure_count: int
    xref_count: int
    diagnostics_files: tuple[str, ...]
    structure_positions: tuple[str, ...]


def _validate_trajectory(events: tuple[Mapping[str, Any], ...], *, mode: str, changed_files: tuple[str, ...], expected: Identity) -> TrajectoryEvidence:
    mcp_events: list[Mapping[str, Any]] = []
    markers: list[str] = []
    structure_positions: set[str] = set()
    diagnostics_files: set[str] = set()
    structure_count = 0
    xref_count = 0
    for index, event in enumerate(events, 1):
        _reject_unknown(event, _EVENT_FIELDS, f"agent event {index}")
        transport_event = _is_transport_identity_event(event)
        for flattened in _flatten_event(event):
            _reject_unknown(flattened, _EVENT_FIELDS, f"agent event {index}")
            actual = _event_identity(flattened, transport=transport_event)
            _check_identity(expected, actual, f"agent event {index}")
        markers.extend(_prompt_marker_strings(event))
        if not _is_mcp_event(event):
            continue
        mcp_events.append(event)
        if mode == "no_lsp":
            # Do not inspect tool-specific arguments on a forbidden event:
            # the governing violation is the zero-MCP No-LSP contract.
            raise ContractFailure("No-LSP arm must emit zero MCP events")
        tool = _tool_name(event)
        if not tool:
            raise ContractFailure(f"agent event {index} is MCP but has no tool")
        if tool not in _MCP_TOOL_NAMES:
            raise ContractFailure(f"agent event {index} uses unknown MCP tool {tool!r}")
        args = _event_args(event)
        if tool == "structure":
            structure_count += 1
            structure_positions.update(_locations(_event_result(event)))
        elif tool == "xref":
            xref_count += 1
            pos = _first(args, "pos", "position")
            if pos is None:
                raise ContractFailure(f"agent event {index} xref is missing pos")
            normalised = _normalise_position(pos)
            if normalised not in structure_positions:
                raise ContractFailure(
                    f"agent event {index} xref pos {normalised!r} was not returned by a previous structure"
                )
        elif tool == "diagnostics":
            diff_filter = args.get("diff_filter")
            if diff_filter is None:
                raise ContractFailure(f"agent event {index} diagnostics is missing diff_filter")
            if not isinstance(diff_filter, Mapping):
                raise ContractFailure(f"agent event {index} diagnostics diff_filter must be an object")
            if set(diff_filter) != {"mode", "changed_lines"}:
                raise ContractFailure(f"agent event {index} diagnostics diff_filter has invalid fields")
            if diff_filter.get("mode") != "changed_lines":
                raise ContractFailure(f"agent event {index} diagnostics diff_filter mode must be changed_lines")
            changed_lines = diff_filter.get("changed_lines")
            if not isinstance(changed_lines, Mapping) or not changed_lines:
                raise ContractFailure(f"agent event {index} diagnostics changed_lines must be a non-empty object")
            declared_paths: set[str] = set()
            for path, lines in changed_lines.items():
                normalised_path = _normalise_path(path, "diagnostics diff_filter path")
                if not isinstance(lines, Sequence) or isinstance(lines, (str, bytes, bytearray)) or not lines:
                    raise ContractFailure(f"agent event {index} diagnostics changed lines must be a non-empty array")
                if any(isinstance(line, bool) or not isinstance(line, int) or line < 1 for line in lines):
                    raise ContractFailure(f"agent event {index} diagnostics changed lines must be positive integers")
                declared_paths.add(normalised_path)
            file_paths = args.get("file_paths")
            file_path = args.get("file_path")
            if (file_paths is None) == (file_path is None):
                raise ContractFailure(f"agent event {index} diagnostics needs exactly one of file_path or file_paths")
            if file_path is not None:
                requested = {_normalise_path(file_path, "diagnostics file_path")}
            else:
                if not isinstance(file_paths, Sequence) or isinstance(file_paths, (str, bytes, bytearray)):
                    raise ContractFailure(f"agent event {index} diagnostics file_paths must be an array")
                requested = {_normalise_path(path, "diagnostics file_paths") for path in file_paths}
            if requested != declared_paths:
                raise ContractFailure(f"agent event {index} diagnostics file paths do not match changed_lines")
            diagnostics_files.update(declared_paths)

    markers = list(dict.fromkeys(markers))
    if mode == "no_lsp":
        if mcp_events:
            raise ContractFailure("No-LSP arm must emit zero MCP events")
        if markers:
            raise ContractFailure("No-LSP arm contains an LSP/MCP prompt marker")
        return TrajectoryEvidence(events, 0, tuple(markers), 0, 0, (), tuple(sorted(structure_positions)))
    missing = sorted(set(changed_files) - diagnostics_files)
    if missing:
        raise ContractFailure("With-LSP diagnostics diff_filter missing: " + ", ".join(missing))
    if not mcp_events:
        raise ContractFailure("With-LSP arm must contain MCP evidence")
    if structure_count == 0 or xref_count == 0:
        raise ContractFailure("With-LSP arm must contain a structure-to-xref chain")
    return TrajectoryEvidence(
        events,
        len(mcp_events),
        tuple(markers),
        structure_count,
        xref_count,
        tuple(sorted(diagnostics_files)),
        tuple(sorted(structure_positions)),
    )


_GRADER_FIELDS = {
    "schema", "question_id", "question", "task_id", "arm_id", "arm", "mode", "run_mode", "session_id", "session",
    "thread_id", "thread", "patch_hash", "candidate_hash", "artifact_hash", "hash", "identity_hash", "run_hash",
    "identity_digest", "status", "outcome", "passed", "resolved", "failed", "unresolved", "failure_kind", "kind",
    "failure", "failures", "errors", "error_ids", "infrastructure", "infra_failure", "timeout", "tests", "report_id",
    "source_artifact", "source_hash", "summary", "message", "passed_to_grading", "identity",
    "censored", "unverified", "verified", "verification_status", "evidence_status", "result_status", "execution_status",
    "common_identity", "identity_envelope", "base_hash", "base_id", "base_commit", "base_commit_sha", "base_tree",
    "base_tree_sha", "source_image_hash", "source_image_id", "image_hash", "image_id", "model_hash", "model_id", "model",
    "prompt_hash", "prompt_id", "grader_hash", "grader_id", "manifest_hash", "manifest_id", "dataset_hash", "dataset_id",
    "runner_hash", "runner_id", "toolchain_hash", "toolchain_id", "pricing_hash", "pricing_id", "scoring_hash", "scoring_id",
    "policy_hash", "policy_id",
}


@dataclasses.dataclass(frozen=True)
class GraderEvidence:
    identity: Identity
    passed: bool
    status: str
    infrastructure: bool
    common_identity: CommonIdentity | None = None


def _normalise_report_status(value: Any) -> str:
    text = _required_text(value, "official grader status").lower().replace("-", "_").replace(" ", "_")
    aliases = {"pass": "passed", "success": "passed", "ok": "passed", "fail": "failed", "error": "infra", "infrastructure_failure": "infra", "unresolved_infrastructure": "infra"}
    return aliases.get(text, text)


def _parse_grader(value: Any, *, question_id: str, arm_id: str, mode: str, expected: Identity) -> GraderEvidence:
    report = _load_object(value, "official grader evidence")
    _reject_unknown(report, _GRADER_FIELDS, "official grader evidence")
    for field in ("verification_status", "evidence_status", "result_status", "execution_status"):
        _reject_unscoreable_status(report.get(field), f"official grader.{field}")
    for field in ("censored", "unverified"):
        if field in report and _as_bool(report[field], f"official grader.{field}"):
            raise OfficialReportError(f"official grader.{field} evidence is not scoreable")
    if "unresolved" in report and _as_bool(report["unresolved"], "official grader.unresolved"):
        raise OfficialReportError("official grader unresolved evidence is not scoreable")
    if "verified" in report and not _as_bool(report["verified"], "official grader.verified"):
        raise OfficialReportError("official grader.verified evidence is not scoreable")
    schema = report.get("schema")
    if schema is not None and not isinstance(schema, str):
        raise OfficialReportError("official grader schema must be text")
    identity = _identity_from(report, label="official grader", question_id=question_id, arm_id=arm_id, mode=mode)
    assert identity is not None
    _check_identity(expected, identity, "official grader", required=True)
    passed_field = report.get("passed")
    status_field = _first(report, "status", "outcome")
    if passed_field is not None and not isinstance(passed_field, bool):
        raise OfficialReportError("official grader passed must be boolean")
    passed_status: bool | None = None
    status = ""
    if status_field is not None:
        status = _normalise_report_status(status_field)
        _reject_unscoreable_status(status, "official grader.status")
        if status in {"passed"}:
            passed_status = True
        elif status in {"failed", "infra", "unresolved", "infrastructure", "timeout", "error"}:
            passed_status = False
        else:
            raise OfficialReportError(f"unknown official grader status: {status_field!r}")
    if passed_field is None and passed_status is None:
        raise OfficialReportError("official grader needs status or passed; passed_to_grading is not a result")
    if passed_field is not None and passed_status is not None and passed_field != passed_status:
        raise OfficialReportError("official grader report contradicts passed/status")
    passed = bool(passed_field if passed_field is not None else passed_status)

    failure_kind = _first(report, "failure_kind", "kind", "failure")
    if isinstance(failure_kind, Mapping):
        failure_kind = _first(failure_kind, "kind", "type", "failure_kind")
    failure_text = "" if failure_kind is None else str(failure_kind).lower().replace("-", "_")
    infrastructure_value = _first(report, "infrastructure", "infra_failure", "timeout")
    infrastructure = bool(infrastructure_value) if infrastructure_value is not None else False
    if isinstance(infrastructure_value, str):
        infrastructure = infrastructure_value.lower() not in {"", "false", "0", "none", "no"}
    infrastructure = infrastructure or failure_text in {"infra", "infrastructure", "infrastructure_failure", "timeout", "error"}
    for key in ("errors", "error_ids"):
        if key in report and report[key]:
            infrastructure = True
    failures = report.get("failures", report.get("failure"))
    if failures and passed:
        raise OfficialReportError("official grader report says passed but contains failures")
    if infrastructure and passed:
        raise OfficialReportError("official grader report says passed but contains infrastructure failure")
    if status == "passed" and infrastructure:
        raise OfficialReportError("official grader status passed contradicts infrastructure failure")
    if status in {"infra", "unresolved", "infrastructure", "timeout"} and passed:
        raise OfficialReportError("official grader infrastructure status contradicts passed")
    if not passed and status == "" and infrastructure:
        status = "infra"
    elif status == "":
        status = "passed" if passed else "failed"
    return GraderEvidence(identity, passed, status, infrastructure, _common_identity_from(report, label="official grader"))


_COST_FIELDS = {
    "usd", "cost_usd", "total_usd", "amount_usd", "amount", "currency", "input_tokens", "output_tokens",
    "cached_input_tokens", "cache_read_input_tokens", "cache_write_input_tokens", "total_tokens", "tokens", "model", "pricing_hash",
    "pricing_manifest", "pricing", "manifest", "pricing_manifest_hash", "manifest_hash", "declared_usd",
    "reasoning_output_tokens", "usage", "status", "verification_status", "evidence_status", "verified", "unverified", "censored", "question_id", "question", "task_id", "arm_id", "arm", "mode", "run_mode",
    "session_id", "session", "thread_id", "thread", "patch_hash", "candidate_hash", "artifact_hash", "hash", "identity_hash",
    "run_hash", "identity_digest", "identity",
}
_COST_KEYS = ("usd", "cost_usd", "total_usd", "amount_usd", "amount")
_PRICING_FIELDS = {
    "schema", "manifest_id", "version", "model", "currency", "input_usd_per_token", "cached_input_usd_per_token",
    "output_usd_per_token", "input_usd_per_1k", "cached_input_usd_per_1k", "output_usd_per_1k",
    "input_usd_per_million", "cached_input_usd_per_million", "output_usd_per_million", "input_rate",
    "cached_input_rate", "output_rate", "cached_input_discount", "cached_input_multiplier", "rates",
    "hash", "pricing_hash", "manifest_hash", "pricing_manifest_hash",
}
_PRICING_HASH_KEYS = ("pricing_manifest_hash", "manifest_hash", "pricing_hash", "hash")


@dataclasses.dataclass(frozen=True)
class PricingManifestEvidence:
    manifest_hash: str
    input_usd_per_token: Decimal
    output_usd_per_token: Decimal
    cached_input_discount: Decimal


def _as_decimal(value: Any, label: str) -> Decimal:
    try:
        parsed = Decimal(str(value))
    except (InvalidOperation, TypeError, ValueError) as exc:
        raise CostError(f"{label} must be a finite decimal") from exc
    if not parsed.is_finite() or parsed < 0:
        raise CostError(f"{label} must be a finite non-negative decimal")
    return parsed


def _parse_pricing_manifest(value: Any, *, expected_hash: str | None = None, label: str = "pricing manifest") -> PricingManifestEvidence:
    manifest = _load_object(value, label)
    _reject_unknown(manifest, _PRICING_FIELDS, label)
    declared_hash = next((manifest.get(key) for key in _PRICING_HASH_KEYS if manifest.get(key) is not None), None)
    material = dict(manifest)
    for key in _PRICING_HASH_KEYS:
        material.pop(key, None)
    computed_hash = _canonical_hash(material)
    if declared_hash is None and expected_hash is None:
        raise CostError(f"{label} hash is required to lock pricing")
    if declared_hash is not None and _required_text(declared_hash, f"{label}.hash") != computed_hash:
        raise CostError(f"{label} hash does not match manifest contents")
    if expected_hash is not None and _required_text(expected_hash, "pricing_manifest_hash") != computed_hash:
        raise CostError("pricing manifest hash drifted")
    if manifest.get("schema") not in {None, PRICING_MANIFEST_SCHEMA}:
        raise CostError(f"unsupported pricing manifest schema: {manifest.get('schema')!r}")
    currency = manifest.get("currency", "USD")
    if str(currency).upper() != "USD":
        raise CostError("pricing manifest currency must be USD")
    rates = manifest.get("rates")
    if rates is not None:
        if not isinstance(rates, Mapping):
            raise CostError("pricing manifest rates must be an object")
        _reject_unknown(rates, {"input", "cached_input", "output"}, f"{label}.rates")
    input_rate = manifest.get("input_usd_per_token")
    if input_rate is None and isinstance(rates, Mapping):
        input_rate = rates.get("input")
    output_rate = manifest.get("output_usd_per_token")
    if output_rate is None and isinstance(rates, Mapping):
        output_rate = rates.get("output")
    if input_rate is None:
        input_rate = manifest.get("input_rate")
    if output_rate is None:
        output_rate = manifest.get("output_rate")
    input_scale = 1
    output_scale = 1
    if input_rate is None:
        input_rate = manifest.get("input_usd_per_1k")
        input_scale = 1000
    if input_rate is None:
        input_rate = manifest.get("input_usd_per_million")
        input_scale = 1000000
    if output_rate is None:
        output_rate = manifest.get("output_usd_per_1k")
        output_scale = 1000
    if output_rate is None:
        output_rate = manifest.get("output_usd_per_million")
        output_scale = 1000000
    if input_rate is None or output_rate is None:
        raise CostError(f"{label} must lock input and output rates")
    input_rate_decimal = _as_decimal(input_rate, f"{label}.input_usd_per_token") / input_scale
    output_rate_decimal = _as_decimal(output_rate, f"{label}.output_usd_per_token") / output_scale
    discount = _as_decimal(
        manifest.get("cached_input_discount", manifest.get("cached_input_multiplier", CACHE_INPUT_DISCOUNT)),
        f"{label}.cached_input_discount",
    )
    if discount != Decimal(CACHE_INPUT_DISCOUNT):
        raise CostError("cached input pricing must use the locked 0.9 discount")
    cached_rate = manifest.get("cached_input_usd_per_token")
    if cached_rate is None and isinstance(rates, Mapping):
        cached_rate = rates.get("cached_input")
    cached_scale = 1
    if cached_rate is None:
        cached_rate = manifest.get("cached_input_rate")
    if cached_rate is None:
        cached_rate = manifest.get("cached_input_usd_per_1k")
        cached_scale = 1000
    if cached_rate is None:
        cached_rate = manifest.get("cached_input_usd_per_million")
        cached_scale = 1000000
    if cached_rate is not None and _as_decimal(cached_rate, f"{label}.cached_input_usd_per_token") / cached_scale != input_rate_decimal * discount:
        raise CostError("cached input rate contradicts the locked 0.9 discount")
    return PricingManifestEvidence(computed_hash, input_rate_decimal, output_rate_decimal, discount)


@dataclasses.dataclass(frozen=True)
class CostEvidence:
    identity: Identity | None
    usd: Decimal
    input_tokens: int = 0
    cached_input_tokens: int = 0
    cache_write_input_tokens: int = 0
    output_tokens: int = 0
    reasoning_output_tokens: int = 0
    pricing_manifest_hash: str = ""
    declared_usd: Decimal | None = None

    def as_number(self) -> int | float:
        value = float(self.usd)
        return int(value) if value.is_integer() else value


def _token_count(value: Any, label: str) -> int:
    count = _as_int(value, label)
    if count < 0:
        raise CostError(f"{label} must be non-negative")
    return count


def _parse_cost(
    value: Any,
    *,
    expected: Identity,
    locked_pricing_manifest: Mapping[str, Any] | None = None,
    locked_pricing_hash: str | None = None,
) -> CostEvidence:
    cost = _load_object(value, "cost")
    _reject_unknown(cost, _COST_FIELDS, "cost")
    for field in ("status", "verification_status", "evidence_status"):
        _reject_unscoreable_status(cost.get(field), f"cost.{field}")
    for field in ("unverified", "censored"):
        if field in cost and _as_bool(cost[field], f"cost.{field}"):
            raise CostError(f"cost.{field} evidence is not scoreable")
    if "verified" in cost and not _as_bool(cost["verified"], "cost.verified"):
        raise CostError("cost evidence is unverified")
    identity_present = any(key in cost for key in ("question_id", "arm_id", "mode", "session_id", "thread_id", "patch_hash", "identity_hash", "identity"))
    identity = _identity_from(cost, label="cost", require_all=identity_present)
    if identity is not None:
        _check_identity(expected, identity, "cost", required=True)
    raw_tokens = cost.get("tokens")
    if raw_tokens is not None:
        if not isinstance(raw_tokens, Mapping):
            raise CostError("cost.tokens must be an object")
        _reject_unknown(raw_tokens, {"input", "input_tokens", "cached_input", "cached_input_tokens", "cache_read_input_tokens", "cache_write_input_tokens", "output", "output_tokens", "reasoning_output", "reasoning_output_tokens"}, "cost.tokens")
    raw_usage = cost.get("usage")
    if raw_usage is not None:
        if not isinstance(raw_usage, Mapping):
            raise CostError("cost.usage must be an object")
        _reject_unknown(raw_usage, {"input_tokens", "cached_input_tokens", "cache_read_input_tokens", "cache_write_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens"}, "cost.usage")

    def token_value(*keys: str) -> Any:
        for source in (raw_tokens, raw_usage, cost):
            if isinstance(source, Mapping):
                value = _first(source, *keys)
                if value is not None:
                    return value
        return None

    input_raw = token_value("input_tokens", "input")
    cached_raw = token_value("cached_input_tokens", "cache_read_input_tokens", "cached_input")
    cache_write_raw = token_value("cache_write_input_tokens")
    output_raw = token_value("output_tokens", "output")
    reasoning_raw = token_value("reasoning_output_tokens", "reasoning_output")
    if input_raw is None or cached_raw is None or output_raw is None:
        raise CostError("cost must contain raw input, cached_input, and output token counts")
    input_tokens = _token_count(input_raw, "cost.input_tokens")
    cached_input_tokens = _token_count(cached_raw, "cost.cached_input_tokens")
    cache_write_input_tokens = 0 if cache_write_raw is None else _token_count(cache_write_raw, "cost.cache_write_input_tokens")
    output_tokens = _token_count(output_raw, "cost.output_tokens")
    reasoning_output_tokens = 0 if reasoning_raw is None else _token_count(reasoning_raw, "cost.reasoning_output_tokens")
    if cached_input_tokens > input_tokens:
        raise CostError("cached input tokens cannot exceed input tokens")

    manifest_value = _first(cost, "pricing_manifest", "pricing", "manifest")
    if manifest_value is None:
        manifest_value = locked_pricing_manifest
    if manifest_value is None:
        raise CostError("locked pricing manifest is required")
    declared_manifest_hash = _first(cost, "pricing_manifest_hash", "manifest_hash", "pricing_hash")
    if declared_manifest_hash is None:
        declared_manifest_hash = locked_pricing_hash
    manifest = _parse_pricing_manifest(manifest_value, expected_hash=declared_manifest_hash)
    if locked_pricing_hash is not None and manifest.manifest_hash != locked_pricing_hash:
        raise CostError("cost pricing manifest is not the locked run manifest")
    uncached_input_tokens = input_tokens - cached_input_tokens
    amount = (
        Decimal(uncached_input_tokens) * manifest.input_usd_per_token
        + Decimal(cached_input_tokens) * manifest.input_usd_per_token * manifest.cached_input_discount
        + Decimal(output_tokens + reasoning_output_tokens) * manifest.output_usd_per_token
    )
    key = next((name for name in _COST_KEYS if name in cost), None)
    declared_usd: Decimal | None = None
    if key is not None:
        raw = cost[key]
        if key == "amount":
            currency = cost.get("currency")
            if currency is None or str(currency).upper() != "USD":
                raise CostError("cost.amount requires currency=USD")
        declared_usd = _as_decimal(raw, "cost declared USD")
        if declared_usd != amount:
            raise CostError(f"declared USD cost does not match token/pricing recomputation: {declared_usd} != {amount}")
    return CostEvidence(identity, amount, input_tokens, cached_input_tokens, cache_write_input_tokens, output_tokens, reasoning_output_tokens, manifest.manifest_hash, declared_usd)


@dataclasses.dataclass(frozen=True)
class InputIntegrity:
    policy_hash: str
    evidence_hash: str
    pricing_manifest: Mapping[str, Any]
    pricing_manifest_hash: str


def _validate_input_integrity(payload: Mapping[str, Any]) -> InputIntegrity:
    """Validate the pre-run policy lock and the immutable input digest."""

    declared_policy_hash = _declared_integrity_value(payload, "policy_hash")
    if declared_policy_hash is None:
        raise ContractFailure("policy_hash is required before scoring")
    declared_policy_hash = _required_text(declared_policy_hash, "policy_hash")
    policy_value = _declared_integrity_value(payload, "policy")
    policy = APPROVED_POLICY if policy_value is None else _load_object(policy_value, "policy")
    if policy != APPROVED_POLICY:
        raise ContractFailure("input policy differs from the approved business scoring policy")
    if canonical_policy_hash(policy) != declared_policy_hash or declared_policy_hash != POLICY_HASH:
        raise ContractFailure("policy_hash does not match the approved pre-run policy")

    declared_evidence_hash = _declared_integrity_value(payload, "evidence_hash", "input_evidence_hash")
    if declared_evidence_hash is None:
        raise ContractFailure("evidence_hash is required for scoring input")
    declared_evidence_hash = _required_text(declared_evidence_hash, "evidence_hash")
    computed_evidence_hash = canonical_evidence_hash(payload)
    if declared_evidence_hash != computed_evidence_hash:
        raise ContractFailure("evidence_hash does not match submitted evidence")

    manifest_value = _declared_integrity_value(payload, "pricing_manifest", "pricing")
    if manifest_value is None:
        raise CostError("locked run pricing_manifest is required")
    manifest = _load_object(manifest_value, "pricing_manifest")
    declared_pricing_hash = _declared_integrity_value(payload, "pricing_manifest_hash", "manifest_hash")
    if declared_pricing_hash is None:
        declared_pricing_hash = next((manifest.get(key) for key in _PRICING_HASH_KEYS if manifest.get(key) is not None), None)
    pricing = _parse_pricing_manifest(manifest, expected_hash=declared_pricing_hash, label="pricing_manifest")
    return InputIntegrity(declared_policy_hash, declared_evidence_hash, manifest, pricing.manifest_hash)


@dataclasses.dataclass(frozen=True)
class ArmAudit:
    mode: str
    passed: bool
    quality_round: int | None
    quality_attempt: int | None
    quality_score: int
    cost_usd: Decimal
    cost_score: int | None
    score: int
    guard_status: str
    mcp_events: int
    diagnostics_files: tuple[str, ...]
    structure_count: int
    xref_count: int
    patch_hash: str = ""
    changed_files: tuple[str, ...] = ()
    input_tokens: int = 0
    cached_input_tokens: int = 0
    cache_write_input_tokens: int = 0
    output_tokens: int = 0
    reasoning_output_tokens: int = 0
    pricing_manifest_hash: str = ""

    def to_dict(self) -> dict[str, Any]:
        return {
            "mode": self.mode,
            "passed": self.passed,
            "pass": self.passed,
            "fail": not self.passed,
            "round": self.quality_round,
            "quality_round": self.quality_round,
            "quality_attempt": self.quality_attempt,
            "quality_score": self.quality_score,
            "patch_hash": self.patch_hash,
            "changed_files": list(self.changed_files),
            "cost": int(float(self.cost_usd)) if float(self.cost_usd).is_integer() else float(self.cost_usd),
            "charged_usd": str(self.cost_usd),
            "pricing_manifest_hash": self.pricing_manifest_hash,
            "cost_score": self.cost_score,
            "score": self.score,
            "guard_status": self.guard_status,
            "mcp_events": self.mcp_events,
            "diagnostics_files": list(self.diagnostics_files),
            "structure_count": self.structure_count,
            "xref_count": self.xref_count,
            "usage": {
                "input_tokens": self.input_tokens,
                "cached_input_tokens": self.cached_input_tokens,
                "cache_write_input_tokens": self.cache_write_input_tokens,
                "output_tokens": self.output_tokens,
                "reasoning_output_tokens": self.reasoning_output_tokens,
            },
            # Keep the legacy flat fields above, while exposing separate
            # pass/quality/USD layers for consumers that must not conflate
            # business points with statistical evidence.
            "dimensions": {
                "pass": {"passed": self.passed},
                "quality": {
                    "first_passing_round": self.quality_round,
                    "points": self.quality_score,
                },
                "usd": {
                    "charged_usd": str(self.cost_usd),
                    "input_tokens": self.input_tokens,
                    "cached_input_tokens": self.cached_input_tokens,
                    "cache_write_input_tokens": self.cache_write_input_tokens,
                    "output_tokens": self.output_tokens,
                    "reasoning_output_tokens": self.reasoning_output_tokens,
                    "pricing_manifest_hash": self.pricing_manifest_hash,
                },
                "business_points": {
                    "quality": self.quality_score,
                    "cost": self.cost_score,
                    "total": self.score,
                },
            },
        }


@dataclasses.dataclass(frozen=True)
class QuestionAudit:
    question_id: str
    discovery_shape: str
    arms: Mapping[str, ArmAudit]
    eligible: bool = True
    patch_equal: bool = False
    patch_hashes: Mapping[str, str] = dataclasses.field(default_factory=dict)
    changed_files_overlap: tuple[str, ...] = ()
    changed_files_divergence: tuple[str, ...] = ()

    @property
    def passed(self) -> bool:
        return all(arm.passed for arm in self.arms.values())

    @property
    def total_score(self) -> int:
        return sum(arm.score for arm in self.arms.values())

    def to_dict(self) -> dict[str, Any]:
        arm_dict = {mode: self.arms[mode].to_dict() for mode in ARM_MODES}
        return {
            "question_id": self.question_id,
            "discovery_shape": self.discovery_shape,
            "eligible": self.eligible,
            "passed": self.passed,
            "pass": self.passed,
            "fail": not self.passed,
            "round": {mode: arm_dict[mode]["round"] for mode in ARM_MODES},
            "cost": {mode: arm_dict[mode]["cost"] for mode in ARM_MODES},
            "score": {mode: arm_dict[mode]["score"] for mode in ARM_MODES},
            "patch_equal": self.patch_equal,
            "patch_hashes": dict(self.patch_hashes),
            "changed_files": {mode: arm_dict[mode]["changed_files"] for mode in ARM_MODES},
            "changed_files_overlap": list(self.changed_files_overlap),
            "changed_files_divergence": list(self.changed_files_divergence),
            "arms": arm_dict,
            "total_score": self.total_score,
        }


@dataclasses.dataclass(frozen=True)
class AuditResult:
    questions: tuple[QuestionAudit, ...]
    leaderboard: tuple[QuestionAudit, ...]
    coverage_ledger: Mapping[str, Any]
    policy_hash: str = POLICY_HASH
    evidence_hash: str = ""
    pricing_manifest_hash: str = ""

    def _layer_summary(self) -> dict[str, Any]:
        result: dict[str, Any] = {}
        for mode in ARM_MODES:
            arms = [question.arms[mode] for question in self.questions]
            result[mode] = {
                "pass": {
                    "passed": sum(1 for arm in arms if arm.passed),
                    "total": len(arms),
                    "rate": sum(1 for arm in arms if arm.passed) / len(arms),
                },
                "quality": {
                    "points": sum(arm.quality_score for arm in arms),
                    "mean_points": sum(arm.quality_score for arm in arms) / len(arms),
                },
                "usd": {
                    "charged_usd": str(sum((arm.cost_usd for arm in arms), Decimal("0"))),
                    "mean_charged_usd": str(sum((arm.cost_usd for arm in arms), Decimal("0")) / Decimal(len(arms))),
                    "input_tokens": sum(arm.input_tokens for arm in arms),
                    "cached_input_tokens": sum(arm.cached_input_tokens for arm in arms),
                    "cache_write_input_tokens": sum(arm.cache_write_input_tokens for arm in arms),
                    "output_tokens": sum(arm.output_tokens for arm in arms),
                    "reasoning_output_tokens": sum(arm.reasoning_output_tokens for arm in arms),
                    "pricing_manifest_hash": self.pricing_manifest_hash,
                },
                "business_points": {
                    "quality": sum(arm.quality_score for arm in arms),
                    "cost": sum(arm.cost_score or 0 for arm in arms),
                    "total": sum(arm.score for arm in arms),
                },
            }
        return result

    def to_dict(self) -> dict[str, Any]:
        return {
            "schema": SCHEMA,
            "scoring_policy": {
                "quality_points_by_first_passing_round": list(QUALITY_POINTS),
                "cost_points_enabled": False,
                "token_usage_scored": False,
                "evidence_recovery_consumes_quality_round": False,
                "infra_retry_consumes_quality_round": False,
                "cached_input_discount": CACHE_INPUT_DISCOUNT,
                "policy_hash": self.policy_hash,
                "business_points_are_descriptive_only": True,
            },
            "policy_hash": self.policy_hash,
            "evidence_hash": self.evidence_hash,
            "pricing_manifest_hash": self.pricing_manifest_hash,
            "integrity": {
                "policy_hash": self.policy_hash,
                "evidence_hash": self.evidence_hash,
                "pricing_manifest_hash": self.pricing_manifest_hash,
            },
            "dimensions": self._layer_summary(),
            "statistics": {
                "eligible": False,
                "reason": "business_points_are_not_statistical_significance",
            },
            "questions": [question.to_dict() for question in self.questions],
            "leaderboard": [question.to_dict() for question in self.leaderboard],
            "coverage_ledger": self.coverage_ledger,
        }

    def json(self) -> str:
        return json.dumps(self.to_dict(), ensure_ascii=False, sort_keys=True, indent=2) + "\n"


def _parse_arm(
    value: Any,
    *,
    question_id: str,
    mode: str,
    locked_pricing_manifest: Mapping[str, Any],
    locked_pricing_hash: str,
    identity_metadata: Mapping[str, Any] | None = None,
) -> tuple[Identity, SummaryEvidence, TrajectoryEvidence, GraderEvidence, CostEvidence]:
    arm = _load_object(value, f"{mode} arm")
    _reject_unknown(arm, {"mode", "label", "summary", "summary_path", "agent_jsonl", "agent", "trajectory", "grader", "official_grader", "official_grader_evidence", "grader_path", "cost", "cost_path"}, f"{mode} arm")
    declared_mode = arm.get("mode")
    if declared_mode is not None and _normalise_mode(declared_mode, f"{mode} arm.mode") != mode:
        raise IdentityError(f"{mode} arm mode drifted")
    summary_value = arm.get("summary")
    if summary_value is None and "summary_path" in arm:
        summary_value = arm["summary_path"]
    if summary_value is None:
        raise MissingEvidenceError(f"{mode} arm summary is required")
    summary = _parse_summary(
        summary_value,
        question_id=question_id,
        arm_id="",
        mode=mode,
        identity_metadata=identity_metadata,
    )
    agent_value = _first(arm, "agent_jsonl", "agent", "trajectory")
    if agent_value is None:
        raise MissingEvidenceError(f"{mode} arm agent_jsonl is required")
    events = adapt_codex_jsonl(agent_value)
    trajectory = _validate_trajectory(events, mode=mode, changed_files=summary.changed_files, expected=summary.identity)
    if summary.declared_mcp_events is not None and summary.declared_mcp_events != trajectory.mcp_count:
        raise ContractFailure(f"{mode} summary.mcp_events contradicts agent JSONL")
    if summary.declared_prompt_markers and tuple(summary.declared_prompt_markers) != tuple(trajectory.prompt_markers):
        raise ContractFailure(f"{mode} summary.prompt_markers contradicts agent JSONL")
    grader_value = _first(arm, "grader", "official_grader", "official_grader_evidence")
    if grader_value is None and "grader_path" in arm:
        grader_value = arm["grader_path"]
    if grader_value is None:
        raise MissingEvidenceError(f"{mode} arm official grader evidence is required")
    grader = _parse_grader(grader_value, question_id=question_id, arm_id=summary.identity.arm_id, mode=mode, expected=summary.identity)
    cost_value = arm.get("cost")
    if cost_value is None and "cost_path" in arm:
        cost_value = arm["cost_path"]
    if cost_value is None:
        raise MissingEvidenceError(f"{mode} arm cost is required")
    cost = _parse_cost(
        cost_value,
        expected=summary.identity,
        locked_pricing_manifest=locked_pricing_manifest,
        locked_pricing_hash=locked_pricing_hash,
    )
    if summary.passed_hint is not None and summary.passed_hint != grader.passed:
        raise OfficialReportError(f"{mode} summary.passed contradicts official grader")
    if summary.passed_to_grading is True and not grader.passed:
        raise OfficialReportError(f"{mode} passed_to_grading is not an official pass")
    if summary.guard_status in {"success", "succeeded", "passed"} and not grader.passed:
        raise ContractFailure(f"{mode} guard succeeded but official grader failed")
    if summary.guard_status in {"failed", "rejected", "terminal_failure", "completed_with_failure", "max_rounds_reached"} and grader.passed:
        raise ContractFailure(f"{mode} guard failure terminal contradicts official pass")
    if grader.infrastructure:
        raise ContractFailure(f"{mode} official grader has unresolved infrastructure failure")
    return summary.identity, summary, trajectory, grader, cost


def _arm_scores(summary: SummaryEvidence, grader: GraderEvidence, cost: CostEvidence) -> tuple[int | None, int, int]:
    quality = [item for item in summary.rounds if item.kind == "quality"]
    if not quality:
        raise ContractFailure("at least one quality round is required")
    passing = [index for index, item in enumerate(quality, 1) if item.passed]
    if grader.passed and not passing:
        raise OfficialReportError("official grader passed but no quality round passed")
    if not grader.passed and passing:
        raise OfficialReportError("official grader failed but a quality round passed")
    attempt = len(quality) if not passing else passing[0]
    quality_round = quality[passing[0] - 1].number if passing else None
    quality_score = QUALITY_POINTS[passing[0] - 1] if passing else 0
    return quality_round, quality_score, attempt


def _question_identity_metadata(
    payload: Mapping[str, Any],
    *,
    question_id: str,
) -> Mapping[str, Any] | None:
    metadata = payload.get("metadata")
    if not isinstance(metadata, Mapping):
        return None
    adapter_metadata = metadata.get("evidence_adapter")
    if not isinstance(adapter_metadata, Mapping):
        return None
    raw_questions = adapter_metadata.get("questions")
    if not isinstance(raw_questions, Sequence) or isinstance(raw_questions, (str, bytes, bytearray)):
        raise MissingEvidenceError("metadata.evidence_adapter.questions must be an array")
    matches = [item for item in raw_questions if isinstance(item, Mapping) and item.get("question_id") == question_id]
    if len(matches) != 1:
        raise IdentityError(f"metadata identity question mapping is not unique for {question_id}")
    arms = matches[0].get("arms")
    if not isinstance(arms, Mapping):
        raise MissingEvidenceError(f"metadata identity arms are required for {question_id}")
    return arms


def _parse_questions(payload: Any, *, integrity: InputIntegrity | None = None) -> tuple[QuestionAudit, ...]:
    if integrity is None:
        if not isinstance(payload, Mapping):
            raise ContractFailure("audit input must be an object with canonical integrity fields")
        integrity = _validate_input_integrity(payload)
    if isinstance(payload, Mapping):
        _reject_unknown(
            payload,
            {
                "schema", "run_id", "questions", "metadata", "policy", "policy_hash", "evidence_hash",
                "input_evidence_hash", "pricing_manifest", "pricing", "pricing_manifest_hash", "manifest_hash",
            },
            "audit input",
        )
        schema = payload.get("schema")
        if schema is not None and schema != SCHEMA:
            raise ContractFailure(f"unsupported audit schema: {schema!r}")
        raw_questions = payload.get("questions")
    else:
        raise ContractFailure("audit input must be an object with canonical integrity fields")
    if not isinstance(raw_questions, Sequence) or isinstance(raw_questions, (str, bytes, bytearray)):
        raise MissingEvidenceError("audit input.questions must be an array")
    if not raw_questions:
        raise MissingEvidenceError("audit input.questions must not be empty")
    result: list[QuestionAudit] = []
    seen_questions: set[str] = set()
    seen_sessions: dict[str, str] = {}
    for index, raw_question in enumerate(raw_questions, 1):
        if not isinstance(raw_question, Mapping):
            raise ContractFailure(f"question {index} must be an object")
        _reject_unknown(raw_question, {"question_id", "id", "task_id", "discovery_shape", "shape", "arms", "arm_a", "arm_b", "metadata"}, f"question {index}")
        qid = _required_text(_first(raw_question, "question_id", "id", "task_id"), f"question {index}.question_id")
        if qid in seen_questions:
            raise ContractFailure(f"duplicate question_id: {qid}")
        seen_questions.add(qid)
        shape = _required_text(_first(raw_question, "discovery_shape", "shape") or "unspecified", f"question {index}.discovery_shape")
        raw_arms = raw_question.get("arms")
        if raw_arms is None:
            raw_arms = {"A": raw_question.get("arm_a"), "B": raw_question.get("arm_b")}
        arms_by_mode: dict[str, Any] = {}
        if isinstance(raw_arms, Mapping):
            for label, value in raw_arms.items():
                mode = _normalise_mode(label, f"question {qid} arm label") if str(label).lower().replace("-", "_") in {"no_lsp", "with_lsp", "nolsp", "lsp", "withlsp"} else None
                if mode is None:
                    if not isinstance(value, Mapping) or value.get("mode") is None:
                        raise ContractFailure(f"question {qid} arm {label!r} must declare mode")
                    mode = _normalise_mode(value["mode"], f"question {qid} arm mode")
                if mode in arms_by_mode:
                    raise ContractFailure(f"question {qid} has duplicate {mode} arm")
                arms_by_mode[mode] = value
        elif isinstance(raw_arms, Sequence) and not isinstance(raw_arms, (str, bytes, bytearray)):
            for arm in raw_arms:
                if not isinstance(arm, Mapping) or arm.get("mode") is None:
                    raise ContractFailure(f"question {qid} list arm must declare mode")
                mode = _normalise_mode(arm["mode"], f"question {qid} arm mode")
                if mode in arms_by_mode:
                    raise ContractFailure(f"question {qid} has duplicate {mode} arm")
                arms_by_mode[mode] = arm
        else:
            raise ContractFailure(f"question {qid}.arms must be an object or array")
        missing_modes = [mode for mode in ARM_MODES if mode not in arms_by_mode]
        if missing_modes:
            raise MissingEvidenceError(f"question {qid} missing arm(s): {', '.join(missing_modes)}")
        parsed: dict[str, tuple[Identity, SummaryEvidence, TrajectoryEvidence, GraderEvidence, CostEvidence]] = {}
        identity_metadata = _question_identity_metadata(payload, question_id=qid)
        for mode in ARM_MODES:
            parsed[mode] = _parse_arm(
                arms_by_mode[mode],
                question_id=qid,
                mode=mode,
                locked_pricing_manifest=integrity.pricing_manifest,
                locked_pricing_hash=integrity.pricing_manifest_hash,
                identity_metadata=(identity_metadata.get(mode) if identity_metadata is not None else None),
            )
            identity, summary, trajectory, grader, cost = parsed[mode]
            owner = f"{qid}:{mode}"
            if identity.session_id in seen_sessions:
                raise ContractFailure(f"duplicate session_id {identity.session_id!r} ({seen_sessions[identity.session_id]} and {owner})")
            seen_sessions[identity.session_id] = owner
        no_identity = parsed["no_lsp"][0]
        lsp_identity = parsed["with_lsp"][0]
        if no_identity.question_id != lsp_identity.question_id:
            raise IdentityError(f"question {qid} A/B task identity mismatch")
        if no_identity.arm_id == lsp_identity.arm_id:
            raise IdentityError(f"question {qid} A/B arm IDs must be distinct")
        no_summary = parsed["no_lsp"][1]
        lsp_summary = parsed["with_lsp"][1]
        no_grader = parsed["no_lsp"][3]
        lsp_grader = parsed["with_lsp"][3]
        no_common = _bind_policy_identity(
            _merge_common_identity(no_summary.common_identity, no_grader.common_identity, label=f"question {qid} no_lsp"),
            integrity.policy_hash,
        )
        lsp_common = _bind_policy_identity(
            _merge_common_identity(lsp_summary.common_identity, lsp_grader.common_identity, label=f"question {qid} with_lsp"),
            integrity.policy_hash,
        )
        if no_common is None or lsp_common is None:
            raise IdentityError(f"question {qid} A/B common identity is required")
        for mode, common in (("no_lsp", no_common), ("with_lsp", lsp_common)):
            missing_common = sorted(_REQUIRED_COMMON_IDENTITY_COMPONENTS - set(common.components))
            if missing_common:
                raise IdentityError(
                    f"question {qid} {mode} common identity missing: {', '.join(missing_common)}"
                )
        if dict(no_common.components) != dict(lsp_common.components):
            raise IdentityError(f"question {qid} A/B common identity mismatch")
        no_cost = parsed["no_lsp"][4]
        lsp_cost = parsed["with_lsp"][4]
        no_round, no_quality_score, no_attempt = _arm_scores(no_summary, no_grader, no_cost)
        lsp_round, lsp_quality_score, lsp_attempt = _arm_scores(lsp_summary, lsp_grader, lsp_cost)
        no_cost_score = 0
        lsp_cost_score = 0
        no_score = no_quality_score
        lsp_score = lsp_quality_score
        arm_audit = {
            "no_lsp": ArmAudit(
                "no_lsp", no_grader.passed, no_round, no_attempt, no_quality_score, no_cost.usd, no_cost_score,
                no_score, no_summary.guard_status, parsed["no_lsp"][2].mcp_count, parsed["no_lsp"][2].diagnostics_files,
                parsed["no_lsp"][2].structure_count, parsed["no_lsp"][2].xref_count,
                patch_hash=no_identity.patch_hash, changed_files=no_summary.changed_files,
                input_tokens=no_cost.input_tokens, cached_input_tokens=no_cost.cached_input_tokens,
                cache_write_input_tokens=no_cost.cache_write_input_tokens, output_tokens=no_cost.output_tokens,
                reasoning_output_tokens=no_cost.reasoning_output_tokens, pricing_manifest_hash=no_cost.pricing_manifest_hash,
            ),
            "with_lsp": ArmAudit(
                "with_lsp", lsp_grader.passed, lsp_round, lsp_attempt, lsp_quality_score, lsp_cost.usd, lsp_cost_score,
                lsp_score, lsp_summary.guard_status, parsed["with_lsp"][2].mcp_count, parsed["with_lsp"][2].diagnostics_files,
                parsed["with_lsp"][2].structure_count, parsed["with_lsp"][2].xref_count,
                patch_hash=lsp_identity.patch_hash, changed_files=lsp_summary.changed_files,
                input_tokens=lsp_cost.input_tokens, cached_input_tokens=lsp_cost.cached_input_tokens,
                cache_write_input_tokens=lsp_cost.cache_write_input_tokens, output_tokens=lsp_cost.output_tokens,
                reasoning_output_tokens=lsp_cost.reasoning_output_tokens, pricing_manifest_hash=lsp_cost.pricing_manifest_hash,
            ),
        }
        no_changed = set(no_summary.changed_files)
        lsp_changed = set(lsp_summary.changed_files)
        result.append(QuestionAudit(
            qid,
            shape,
            arm_audit,
            patch_equal=no_identity.patch_hash == lsp_identity.patch_hash,
            patch_hashes={"no_lsp": no_identity.patch_hash, "with_lsp": lsp_identity.patch_hash},
            changed_files_overlap=tuple(sorted(no_changed & lsp_changed)),
            changed_files_divergence=tuple(sorted(no_changed ^ lsp_changed)),
        ))
    return tuple(result)


def _coverage_ledger(questions: Sequence[QuestionAudit]) -> dict[str, Any]:
    rows: list[dict[str, Any]] = []
    for question in questions:
        no = question.arms["no_lsp"]
        lsp = question.arms["with_lsp"]
        rows.append({
            "question_id": question.question_id,
            "discovery_shape": question.discovery_shape,
            "no_lsp": {"mcp_events": no.mcp_events, "prompt_markers": 0, "clean": no.mcp_events == 0},
            "with_lsp": {
                "structure": lsp.structure_count,
                "xref": lsp.xref_count,
                "diagnostics_files": list(lsp.diagnostics_files),
                "diagnostics_file_count": len(lsp.diagnostics_files),
                "clean": lsp.structure_count > 0 and lsp.xref_count > 0 and bool(lsp.diagnostics_files),
            },
        })
    return {
        "questions": rows,
        "question_count": len(rows),
        "arm_count": len(rows) * 2,
        "no_lsp_clean": sum(1 for row in rows if row["no_lsp"]["clean"]),
        "with_lsp_chain_complete": sum(1 for row in rows if row["with_lsp"]["clean"]),
        "diagnostics_files_covered": sum(row["with_lsp"]["diagnostics_file_count"] for row in rows),
    }


def audit(payload: Any) -> AuditResult:
    """Validate all evidence and return a complete A/B score report."""

    if not isinstance(payload, Mapping):
        raise ContractFailure("audit input must be an object with canonical integrity fields")
    integrity = _validate_input_integrity(payload)
    questions = _parse_questions(payload, integrity=integrity)
    leaderboard = tuple(sorted(questions, key=lambda item: (-item.total_score, item.question_id)))
    return AuditResult(
        questions,
        leaderboard,
        _coverage_ledger(questions),
        integrity.policy_hash,
        integrity.evidence_hash,
        integrity.pricing_manifest_hash,
    )


def audit_file(path: str | pathlib.Path) -> AuditResult:
    """Load and audit a JSON input document."""

    candidate = pathlib.Path(path)
    try:
        payload = json.loads(candidate.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise AuditError(f"read audit input: {exc}") from exc
    return audit(payload)


# Descriptive aliases for embedding callers and older harnesses.
validate = audit
evaluate = audit
score = audit
build_report = audit


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, help="JSON A/B audit input")
    parser.add_argument("--output", help="optional JSON report path")
    args = parser.parse_args(argv)
    try:
        result = audit_file(args.input)
    except AuditError as exc:
        payload = {"schema": SCHEMA, "ok": False, "error": str(exc)}
        print(json.dumps(payload, ensure_ascii=False, sort_keys=True))
        return 2
    rendered = result.json()
    if args.output:
        try:
            pathlib.Path(args.output).write_text(rendered, encoding="utf-8")
        except OSError as exc:
            print(json.dumps({"schema": SCHEMA, "ok": False, "error": f"write report: {exc}"}, ensure_ascii=False))
            return 2
    else:
        sys.stdout.write(rendered)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
