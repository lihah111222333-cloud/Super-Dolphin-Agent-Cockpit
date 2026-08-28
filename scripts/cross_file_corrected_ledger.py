#!/usr/bin/env python3
"""Build a fail-closed corrected ledger for cross-file benchmark evidence.

The runner and scorer own execution and business scoring.  This module only
merges immutable benchmark records from an original ledger and explicitly
linked recovery attempts.  A version number is descriptive metadata; it is
never used to silently replace a record.  Every replacement must identify the
attempt it corrects, the corresponding evidence digest, and a human-readable
reason.

The input can be a record array or the strict envelope::

    {
      "schema": "super-dolphin/cross-file-corrected-ledger-input/v1",
      "raw_records": [{
        "attempt_id": "v082-pytest-with",
        "parent": null,
        "supersedes": [],
        "instance": "pytest-dev-pytest-5840",
        "arm": "with_lsp",
        "version": "v082",
        "classification": "infra",
        "status": "infrastructure_failure",
        "evidence": {"message": "collected_count disagrees"},
        "evidence_hash": "sha256:<64 lowercase hex>",
        "quality_round": null,
        "score": 0,
        "correction_link": null
      }]
    }

The output contains an immutable raw snapshot and hash, explicit current and
corrected views, field-level differences, and corrected final three-round
scores.  It is benchmark evidence only and cannot be used as remote-CI
authority.
"""

from __future__ import annotations

import argparse
import copy
import dataclasses
import hashlib
import json
import pathlib
import re
import sys
from collections import defaultdict
from collections.abc import Mapping, Sequence
from typing import Any


SCHEMA = "super-dolphin/cross-file-corrected-ledger/v1"
INPUT_SCHEMA = "super-dolphin/cross-file-corrected-ledger-input/v1"
AUTHORITY = "BENCHMARK_EVIDENCE_ONLY"
EVIDENCE_DOMAIN = "super-dolphin/cross-file-corrected-ledger-evidence/v1"
RAW_HASH_DOMAIN = "super-dolphin/cross-file-corrected-ledger-raw/v1"
AUTHORITATIVE_HASH_DOMAIN = "super-dolphin/cross-file-corrected-ledger-authoritative/v1"
QUALITY_POINTS = (10, 7, 4)
ARM_MODES = ("no_lsp", "with_lsp")
CLASSIFICATIONS = frozenset(
    {"model", "harness", "collection", "classifier", "infra", "pending"}
)
PASS_STATUSES = frozenset({"pass", "passed", "success", "succeeded", "resolved"})
_SHA256_RE = re.compile(r"^sha256:[0-9a-f]{64}$")

_INPUT_FIELDS = frozenset({"schema", "raw_records", "current_attempt_ids", "raw_ledger_hash"})
_RECORD_FIELDS = frozenset(
    {
        "attempt_id",
        "parent",
        "supersedes",
        "instance",
        "arm",
        "version",
        "classification",
        "status",
        "evidence",
        "evidence_ref",
        "evidence_hash",
        "correction_link",
        "quality_round",
        "round",
        "score",
    }
)
_CORRECTION_FIELDS = frozenset(
    {"from_attempt_id", "relation", "evidence_hash", "reason"}
)


class CorrectedLedgerError(ValueError):
    """Base class for malformed, ambiguous, or unverifiable ledger input."""


class UnknownFieldError(CorrectedLedgerError):
    """An input object contains a field outside the ledger schema."""


class MissingFieldError(CorrectedLedgerError):
    """A required ledger identity or evidence field is absent."""


class EvidenceHashError(CorrectedLedgerError):
    """An evidence digest is missing, malformed, or does not match evidence."""


class EvidenceDriftError(EvidenceHashError):
    """A correction link is not bound to evidence in its correction edge."""


class CorrectionLinkError(CorrectedLedgerError):
    """A recovery attempt does not contain a valid explicit correction edge."""


class AmbiguousCorrectionError(CorrectedLedgerError):
    """More than one unlinked or unresolved candidate could be current."""


class LedgerCycleError(CorrectedLedgerError):
    """The explicit parent/supersedes graph contains a cycle."""


def canonical_json(value: Any) -> str:
    """Serialize JSON evidence deterministically and without NaN values."""

    try:
        return json.dumps(
            value,
            ensure_ascii=False,
            allow_nan=False,
            sort_keys=True,
            separators=(",", ":"),
        )
    except (TypeError, ValueError) as exc:
        raise CorrectedLedgerError(f"value is not canonical JSON: {exc}") from exc


def sha256_digest(value: Any) -> str:
    """Return a domain-neutral, prefixed SHA-256 digest for JSON or bytes."""

    payload = value if isinstance(value, bytes) else canonical_json(value).encode("utf-8")
    return "sha256:" + hashlib.sha256(payload).hexdigest()


def evidence_digest(value: Any) -> str:
    """Hash one evidence object with a ledger-specific domain separator."""

    return sha256_digest({"domain": EVIDENCE_DOMAIN, "evidence": value})


def _required_text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value or value.strip() != value:
        raise MissingFieldError(f"{label} must be a canonical non-empty string")
    return value


def _digest(value: Any, label: str) -> str:
    text = _required_text(value, label)
    if _SHA256_RE.fullmatch(text) is None:
        raise EvidenceHashError(f"{label} must be sha256:<64 lowercase hex>")
    return text


def _reject_unknown(value: Mapping[str, Any], allowed: frozenset[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise UnknownFieldError(f"{label} has unknown field(s): {', '.join(unknown)}")


def _required_keys(value: Mapping[str, Any], required: frozenset[str], label: str) -> None:
    missing = sorted(required - set(value))
    if missing:
        raise MissingFieldError(f"{label} missing field(s): {', '.join(missing)}")


def _normalise_arm(value: Any, label: str) -> str:
    text = _required_text(value, label).lower().replace("-", "_")
    aliases = {"nolsp": "no_lsp", "lsp": "with_lsp", "withlsp": "with_lsp"}
    text = aliases.get(text, text)
    if text not in ARM_MODES:
        raise CorrectedLedgerError(f"unsupported {label}: {value!r}")
    return text


def _normalise_status(value: Any, label: str) -> str:
    text = _required_text(value, label).lower().replace("-", "_").replace(" ", "_")
    return text


def _key(instance: str, arm: str) -> tuple[str, str]:
    return instance, arm


@dataclasses.dataclass(frozen=True)
class CorrectionLink:
    """An explicit edge proving which earlier attempt is corrected."""

    from_attempt_id: str
    relation: str
    evidence_hash: str
    reason: str

    def to_dict(self) -> dict[str, str]:
        return dataclasses.asdict(self)


@dataclasses.dataclass(frozen=True)
class LedgerRecord:
    """A normalized view of one immutable raw benchmark attempt."""

    attempt_id: str
    parent: str | None
    supersedes: tuple[str, ...]
    instance: str
    arm: str
    version: str
    classification: str
    status: str
    evidence_hash: str
    evidence_present: bool
    evidence_ref: str | None
    correction_link: CorrectionLink | None
    quality_round: int | None
    score: int
    raw_index: int

    @property
    def key(self) -> tuple[str, str]:
        return _key(self.instance, self.arm)

    def to_dict(self) -> dict[str, Any]:
        result: dict[str, Any] = {
            "attempt_id": self.attempt_id,
            "parent": self.parent,
            "supersedes": list(self.supersedes),
            "instance": self.instance,
            "arm": self.arm,
            "version": self.version,
            "classification": self.classification,
            "status": self.status,
            "evidence_hash": self.evidence_hash,
            "evidence_present": self.evidence_present,
            "evidence_ref": self.evidence_ref,
            "correction_link": (
                self.correction_link.to_dict() if self.correction_link is not None else None
            ),
            "quality_round": self.quality_round,
            "score": self.score,
        }
        return result


def _parse_correction_link(value: Any, label: str) -> CorrectionLink | None:
    if value is None:
        return None
    if not isinstance(value, Mapping):
        raise CorrectionLinkError(f"{label} must be an object or null")
    _reject_unknown(value, _CORRECTION_FIELDS, label)
    _required_keys(value, _CORRECTION_FIELDS, label)
    relation = _required_text(value["relation"], f"{label}.relation")
    if relation != "corrects":
        raise CorrectionLinkError(f"{label}.relation must be 'corrects'")
    return CorrectionLink(
        from_attempt_id=_required_text(value["from_attempt_id"], f"{label}.from_attempt_id"),
        relation=relation,
        evidence_hash=_digest(value["evidence_hash"], f"{label}.evidence_hash"),
        reason=_required_text(value["reason"], f"{label}.reason"),
    )


def _parse_round(value: Mapping[str, Any], label: str) -> int | None:
    present = [name for name in ("quality_round", "round") if name in value]
    if len(present) > 1:
        raise CorrectedLedgerError(f"{label} cannot contain both quality_round and round")
    if not present or value[present[0]] is None:
        return None
    number = value[present[0]]
    if isinstance(number, bool) or not isinstance(number, int) or number not in (1, 2, 3):
        raise CorrectedLedgerError(f"{label}.{present[0]} must be an integer from 1 to 3")
    return number


def _parse_score(value: Any, label: str, status: str, quality_round: int | None) -> int:
    if value is None:
        supplied: int | None = None
    elif isinstance(value, bool) or not isinstance(value, int) or not 0 <= value <= 10:
        raise CorrectedLedgerError(f"{label} must be an integer from 0 to 10 or null")
    else:
        supplied = value

    if status in PASS_STATUSES:
        if quality_round is None:
            raise MissingFieldError(f"{label} needs quality_round for a passing attempt")
        expected = QUALITY_POINTS[quality_round - 1]
        if supplied is not None and supplied != expected:
            raise CorrectedLedgerError(f"{label} disagrees with the first passing round: {supplied} != {expected}")
        return expected
    if supplied not in (None, 0):
        raise CorrectedLedgerError(f"{label} must be zero for a non-passing attempt")
    return 0


def _parse_record(value: Any, index: int) -> LedgerRecord:
    label = f"raw_records[{index}]"
    if not isinstance(value, Mapping):
        raise CorrectedLedgerError(f"{label} must be an object")
    _reject_unknown(value, _RECORD_FIELDS, label)
    required = frozenset(
        {
            "attempt_id",
            "parent",
            "supersedes",
            "instance",
            "arm",
            "version",
            "classification",
            "status",
            "evidence_hash",
            "correction_link",
        }
    )
    _required_keys(value, required, label)

    attempt_id = _required_text(value["attempt_id"], f"{label}.attempt_id")
    parent = value["parent"]
    if parent is not None:
        parent = _required_text(parent, f"{label}.parent")

    supersedes_value = value["supersedes"]
    if not isinstance(supersedes_value, Sequence) or isinstance(
        supersedes_value, (str, bytes, bytearray)
    ):
        raise CorrectedLedgerError(f"{label}.supersedes must be an array")
    supersedes: list[str] = []
    for target_index, target in enumerate(supersedes_value):
        target_id = _required_text(target, f"{label}.supersedes[{target_index}]")
        if target_id in supersedes:
            raise CorrectedLedgerError(f"{label}.supersedes contains duplicate {target_id!r}")
        supersedes.append(target_id)

    instance = _required_text(value["instance"], f"{label}.instance")
    arm = _normalise_arm(value["arm"], f"{label}.arm")
    version = _required_text(value["version"], f"{label}.version")
    classification = _required_text(value["classification"], f"{label}.classification").lower()
    if classification not in CLASSIFICATIONS:
        raise CorrectedLedgerError(f"unsupported {label}.classification: {classification!r}")
    status = _normalise_status(value["status"], f"{label}.status")
    evidence_hash = _digest(value["evidence_hash"], f"{label}.evidence_hash")
    evidence_present = "evidence" in value
    if evidence_present and evidence_digest(value["evidence"]) != evidence_hash:
        raise EvidenceHashError(f"{label}.evidence_hash does not match evidence")
    evidence_ref = None
    if "evidence_ref" in value and value["evidence_ref"] is not None:
        evidence_ref = _required_text(value["evidence_ref"], f"{label}.evidence_ref")

    correction_link = _parse_correction_link(value["correction_link"], f"{label}.correction_link")
    quality_round = _parse_round(value, label)
    score = _parse_score(value.get("score"), f"{label}.score", status, quality_round)
    return LedgerRecord(
        attempt_id=attempt_id,
        parent=parent,
        supersedes=tuple(supersedes),
        instance=instance,
        arm=arm,
        version=version,
        classification=classification,
        status=status,
        evidence_hash=evidence_hash,
        evidence_present=evidence_present,
        evidence_ref=evidence_ref,
        correction_link=correction_link,
        quality_round=quality_round,
        score=score,
        raw_index=index,
    )


def _parse_current_attempt_ids(value: Any) -> dict[tuple[str, str], str]:
    """Parse the optional nested current selector without version inference."""

    if value is None:
        return {}
    if not isinstance(value, Mapping) or not value:
        raise CorrectedLedgerError("current_attempt_ids must be a non-empty object")
    result: dict[tuple[str, str], str] = {}
    for instance, arm_values in value.items():
        instance_text = _required_text(instance, "current_attempt_ids instance")
        if not isinstance(arm_values, Mapping):
            raise CorrectedLedgerError(
                "current_attempt_ids must map each instance to an arm-to-attempt object"
            )
        for arm, attempt_id in arm_values.items():
            canonical_arm = _normalise_arm(arm, f"current_attempt_ids[{instance_text}] arm")
            key = _key(instance_text, canonical_arm)
            if key in result:
                raise CorrectedLedgerError(f"duplicate current selector for {key!r}")
            result[key] = _required_text(
                attempt_id,
                f"current_attempt_ids[{instance_text}][{canonical_arm}]",
            )
    return result


def _load_input(value: Any) -> tuple[list[Any], dict[tuple[str, str], str], str | None]:
    if isinstance(value, Mapping):
        _reject_unknown(value, _INPUT_FIELDS, "corrected ledger input")
        if value.get("schema") != INPUT_SCHEMA:
            raise CorrectedLedgerError(
                f"corrected ledger input schema must be {INPUT_SCHEMA!r}"
            )
        raw_records = value.get("raw_records")
        selectors = _parse_current_attempt_ids(value.get("current_attempt_ids"))
        declared_hash = value.get("raw_ledger_hash")
    else:
        raw_records = value
        selectors = {}
        declared_hash = None

    if not isinstance(raw_records, Sequence) or isinstance(
        raw_records, (str, bytes, bytearray)
    ) or not raw_records:
        raise MissingFieldError("raw_records must be a non-empty array")
    if declared_hash is not None:
        declared_hash = _digest(declared_hash, "raw_ledger_hash")
    return copy.deepcopy(list(raw_records)), selectors, declared_hash


def _validate_links(
    records: Sequence[LedgerRecord],
) -> tuple[dict[str, LedgerRecord], dict[str, set[str]], dict[str, set[str]]]:
    by_id: dict[str, LedgerRecord] = {}
    groups: dict[tuple[str, str], list[LedgerRecord]] = defaultdict(list)
    for record in records:
        if record.attempt_id in by_id:
            raise CorrectedLedgerError(f"duplicate attempt_id: {record.attempt_id}")
        by_id[record.attempt_id] = record
        groups[record.key].append(record)

    children: dict[str, set[str]] = defaultdict(set)
    predecessors: dict[str, set[str]] = defaultdict(set)
    for record in records:
        if record.parent is None:
            if record.supersedes or record.correction_link is not None:
                raise CorrectionLinkError(
                    f"root attempt {record.attempt_id} cannot carry supersedes or correction_link"
                )
        else:
            if record.correction_link is None:
                raise CorrectionLinkError(
                    f"{record.attempt_id} has parent but no explicit correction_link"
                )
            if record.parent not in record.supersedes:
                raise CorrectionLinkError(
                    f"{record.attempt_id}.supersedes must include parent {record.parent!r}"
                )
            if record.correction_link.from_attempt_id != record.parent:
                raise CorrectionLinkError(
                    f"{record.attempt_id}.correction_link.from_attempt_id must equal parent"
                )

        targets = list(record.supersedes)
        if record.parent is not None and record.parent not in targets:
            targets.append(record.parent)
        for target in targets:
            if target == record.attempt_id:
                raise LedgerCycleError(f"attempt {record.attempt_id} supersedes itself")
            previous = by_id.get(target)
            if previous is None:
                raise CorrectionLinkError(
                    f"{record.attempt_id} references unknown attempt {target!r}"
                )
            if previous.key != record.key:
                raise CorrectionLinkError(
                    f"{record.attempt_id} crosses instance/arm boundary to {target!r}"
                )
            children[target].add(record.attempt_id)
            predecessors[record.attempt_id].add(target)

        if record.correction_link is not None:
            link_hash = record.correction_link.evidence_hash
            allowed_hashes = {record.evidence_hash}
            allowed_hashes.update(by_id[target].evidence_hash for target in targets if target in by_id)
            if link_hash not in allowed_hashes:
                raise EvidenceDriftError(
                    f"{record.attempt_id}.correction_link.evidence_hash is not bound to the correction edge"
                )

    # Detect cycles over every explicit correction/supersedes edge.
    visiting: set[str] = set()
    visited: set[str] = set()

    def visit(attempt_id: str) -> None:
        if attempt_id in visiting:
            raise LedgerCycleError(f"correction graph contains a cycle at {attempt_id}")
        if attempt_id in visited:
            return
        visiting.add(attempt_id)
        for predecessor in predecessors.get(attempt_id, ()):
            visit(predecessor)
        visiting.remove(attempt_id)
        visited.add(attempt_id)

    for record in records:
        visit(record.attempt_id)
    return by_id, children, predecessors


def _ordered_lineage(
    group: Sequence[LedgerRecord],
    current: LedgerRecord,
    children: Mapping[str, set[str]],
    predecessors: Mapping[str, set[str]],
) -> tuple[LedgerRecord, ...]:
    group_ids = {record.attempt_id for record in group}
    reachable: set[str] = set()
    stack = [current.attempt_id]
    while stack:
        attempt_id = stack.pop()
        if attempt_id in reachable:
            continue
        reachable.add(attempt_id)
        stack.extend(sorted(children.get(attempt_id, ()), reverse=True))
    if reachable != group_ids:
        missing = sorted(group_ids - reachable)
        raise AmbiguousCorrectionError(
            f"unlinked attempts for {current.instance}:{current.arm}: {', '.join(missing)}"
        )

    depth: dict[str, int] = {}

    def get_depth(attempt_id: str) -> int:
        if attempt_id in depth:
            return depth[attempt_id]
        parents = [parent for parent in predecessors.get(attempt_id, ()) if parent in reachable]
        if not parents:
            depth[attempt_id] = 0
            return 0
        value = max(get_depth(parent) + 1 for parent in parents)
        depth[attempt_id] = value
        return value

    for attempt_id in reachable:
        get_depth(attempt_id)
    return tuple(
        sorted(
            (record for record in group if record.attempt_id in reachable),
            key=lambda record: (depth[record.attempt_id], record.raw_index, record.attempt_id),
        )
    )


def _record_view(record: LedgerRecord) -> dict[str, Any]:
    return record.to_dict()


def _score_view(ordered: Sequence[LedgerRecord], sink: LedgerRecord) -> dict[str, Any]:
    latest_by_round: dict[int, LedgerRecord] = {}
    for record in ordered:
        if record.quality_round is not None:
            latest_by_round[record.quality_round] = record

    first_pass: int | None = None
    for number in (1, 2, 3):
        record = latest_by_round.get(number)
        if record is not None and record.status in PASS_STATUSES:
            first_pass = number
            break
    final_score = QUALITY_POINTS[first_pass - 1] if first_pass is not None else 0
    rounds: list[dict[str, Any]] = []
    for number in (1, 2, 3):
        record = latest_by_round.get(number)
        score = 0
        if first_pass is not None and number >= first_pass:
            score = final_score
        rounds.append(
            {
                "round": number,
                "attempt_id": record.attempt_id if record is not None else None,
                "status": record.status if record is not None else "not_recorded",
                "classification": record.classification if record is not None else "pending",
                "evidence_hash": record.evidence_hash if record is not None else None,
                "score": score,
            }
        )
    return {
        "instance": sink.instance,
        "arm": sink.arm,
        "attempt_id": sink.attempt_id,
        "history_attempt_ids": [record.attempt_id for record in ordered],
        "rounds": rounds,
        "first_passing_round": first_pass,
        "final_score": final_score,
    }


def _field_differences(current: Mapping[str, Any], corrected: Mapping[str, Any]) -> dict[str, Any]:
    fields = (
        "attempt_id",
        "parent",
        "supersedes",
        "version",
        "classification",
        "status",
        "evidence_hash",
        "quality_round",
        "score",
        "correction_link",
    )
    result: dict[str, Any] = {}
    for field in fields:
        if current.get(field) != corrected.get(field):
            result[field] = {"current": current.get(field), "corrected": corrected.get(field)}
    return result


@dataclasses.dataclass(frozen=True)
class CorrectedLedgerResult:
    """Immutable corrected-ledger report returned by the builder."""

    payload: Mapping[str, Any]

    def to_dict(self) -> dict[str, Any]:
        return copy.deepcopy(dict(self.payload))

    def json(self) -> str:
        return json.dumps(self.payload, ensure_ascii=False, sort_keys=True, indent=2) + "\n"

    def __getitem__(self, key: str) -> Any:
        # Do not hand callers a mutable reference into the stored report.
        return copy.deepcopy(self.payload[key])


def build_corrected_ledger(value: Any) -> CorrectedLedgerResult:
    """Validate raw records and produce current/corrected views.

    Multiple records for one ``instance``/``arm`` are accepted only when an
    explicit parent/supersedes/correction-link graph identifies one current
    root and one corrected sink.  The function deep-copies raw input before
    parsing and never mutates the caller's object.
    """

    raw_records, selectors, declared_hash = _load_input(value)
    records = tuple(_parse_record(raw, index) for index, raw in enumerate(raw_records))
    by_id, children, predecessors = _validate_links(records)
    raw_material: dict[str, Any] = {"domain": RAW_HASH_DOMAIN, "records": raw_records}
    if selectors:
        raw_material["current_attempt_ids"] = {
            instance: {arm: attempt_id for (item, arm), attempt_id in selectors.items() if item == instance}
            for instance in sorted({item for item, _ in selectors})
        }
    raw_hash = sha256_digest(raw_material)
    if declared_hash is not None and declared_hash != raw_hash:
        raise EvidenceHashError(
            f"raw_ledger_hash drifted: supplied {declared_hash}, expected {raw_hash}"
        )

    groups: dict[tuple[str, str], list[LedgerRecord]] = defaultdict(list)
    for record in records:
        groups[record.key].append(record)
    if selectors and set(selectors) != set(groups):
        missing = sorted(set(groups) - set(selectors))
        extra = sorted(set(selectors) - set(groups))
        details = []
        if missing:
            details.append(f"missing selectors: {missing}")
        if extra:
            details.append(f"unknown selectors: {extra}")
        raise AmbiguousCorrectionError(
            "current_attempt_ids must explicitly cover every instance/arm: "
            + "; ".join(details)
        )

    current_rows: list[dict[str, Any]] = []
    corrected_rows: list[dict[str, Any]] = []
    differences: list[dict[str, Any]] = []
    current_scores: list[dict[str, Any]] = []
    corrected_scores: list[dict[str, Any]] = []

    for key in sorted(groups):
        group = groups[key]
        roots = [record for record in group if record.parent is None]
        selected_id = selectors.get(key)
        if selected_id is None:
            if len(roots) != 1:
                raise AmbiguousCorrectionError(
                    f"{key[0]}:{key[1]} has multiple candidates without explicit correction links"
                )
            current = roots[0]
        else:
            current = by_id.get(selected_id)
            if current is None or current.key != key:
                raise AmbiguousCorrectionError(
                    f"current selector {selected_id!r} does not identify {key[0]}:{key[1]}"
                )
            if current.parent is not None:
                raise AmbiguousCorrectionError(
                    f"current selector {selected_id!r} must identify a root attempt"
                )

        sinks = [record for record in group if not children.get(record.attempt_id)]
        if len(sinks) != 1:
            raise AmbiguousCorrectionError(
                f"{key[0]}:{key[1]} has {len(sinks)} corrected sinks; explicit links are incomplete"
            )
        corrected = sinks[0]
        ordered = _ordered_lineage(group, current, children, predecessors)
        current_view = _record_view(current)
        corrected_view = _record_view(corrected)
        current_rows.append(current_view)
        corrected_rows.append(corrected_view)
        current_score = _score_view((current,), current)
        corrected_score = _score_view(ordered, corrected)
        current_scores.append(current_score)
        corrected_scores.append(corrected_score)
        changed = _field_differences(current_view, corrected_view)
        if changed:
            differences.append(
                {
                    "instance": key[0],
                    "arm": key[1],
                    "current_attempt_id": current.attempt_id,
                    "corrected_attempt_id": corrected.attempt_id,
                    "current_score": current_score["final_score"],
                    "corrected_score": corrected_score["final_score"],
                    "correction_chain": [record.attempt_id for record in ordered],
                    "changed_fields": changed,
                }
            )

    def _counts(rows: Sequence[Mapping[str, Any]], field: str) -> dict[str, int]:
        counts: dict[str, int] = defaultdict(int)
        for row in rows:
            counts[str(row[field])] += 1
        return dict(sorted(counts.items()))

    corrected_round_totals = {
        str(number): sum(row["rounds"][number - 1]["score"] for row in corrected_scores)
        for number in (1, 2, 3)
    }
    # Keep the per-arm corrected rows above, and add an explicit paired view
    # for the A/B result.  Pair scores remain 10/7/4 per arm; the pair total
    # is their sum and never replaces the raw records.
    corrected_by_key = {
        (row["instance"], row["arm"]): row for row in corrected_scores
    }
    pair_scores: list[dict[str, Any]] = []
    for instance in sorted({record.instance for record in records}):
        arms = {
            arm: copy.deepcopy(corrected_by_key[(instance, arm)])
            for arm in ARM_MODES
            if (instance, arm) in corrected_by_key
        }
        pair_scores.append(
            {
                "instance": instance,
                "complete": set(arms) == set(ARM_MODES),
                "arms": arms,
                "quality_points_by_first_passing_round": list(QUALITY_POINTS),
                "round_totals": {
                    str(number): sum(
                        row["rounds"][number - 1]["score"] for row in arms.values()
                    )
                    for number in (1, 2, 3)
                },
                "final_score": sum(row["final_score"] for row in arms.values()),
            }
        )
    raw_snapshot: dict[str, Any] = {
        "schema": INPUT_SCHEMA,
        "ledger_hash": raw_hash,
        "raw_ledger_hash": raw_hash,
        "records": raw_records,
        "raw_records": copy.deepcopy(raw_records),
    }
    if selectors:
        raw_snapshot["current_attempt_ids"] = {
            instance: {
                arm: attempt_id
                for (item, arm), attempt_id in selectors.items()
                if item == instance
            }
            for instance in sorted({item for item, _ in selectors})
        }

    authoritative_hash = sha256_digest(
        {
            "domain": AUTHORITATIVE_HASH_DOMAIN,
            "records": corrected_rows,
            "scores": corrected_scores,
            "pairs": pair_scores,
        }
    )

    payload = {
        "schema": SCHEMA,
        "authority": AUTHORITY,
        "raw_ledger_hash": raw_hash,
        "authoritative_ledger_hash": authoritative_hash,
        "raw": raw_snapshot,
        "raw_records": copy.deepcopy(raw_records),
        "scoring_policy": {
            "quality_points_by_first_passing_round": list(QUALITY_POINTS),
            "evidence_recovery_consumes_quality_round": False,
            "infra_retry_consumes_quality_round": False,
        },
        "current": current_rows,
        "corrected": corrected_rows,
        # ``authoritative`` is an explicit name for the corrected sink view;
        # the older ``corrected`` key remains for consumers already using it.
        "authoritative": {
            "authority": AUTHORITY,
            "ledger_hash": authoritative_hash,
            "records": copy.deepcopy(corrected_rows),
            "scores": copy.deepcopy(corrected_scores),
            "pairs": pair_scores,
        },
        "authoritative_records": copy.deepcopy(corrected_rows),
        "differences": differences,
        "score_comparison": {
            "current": current_scores,
            "corrected": corrected_scores,
            "current_total": sum(row["final_score"] for row in current_scores),
            "corrected_total": sum(row["final_score"] for row in corrected_scores),
            "corrected_round_totals": corrected_round_totals,
        },
        "final_3_round_scores": corrected_scores,
        "pair_final_3_round_scores": pair_scores,
        "final_records": copy.deepcopy(corrected_rows),
        "final": {
            "authority": AUTHORITY,
            "raw_ledger_hash": raw_hash,
            "authoritative_ledger_hash": authoritative_hash,
            "raw": copy.deepcopy(raw_snapshot),
            "authoritative": {
                "ledger_hash": authoritative_hash,
                "records": copy.deepcopy(corrected_rows),
                "scores": copy.deepcopy(corrected_scores),
                "pairs": copy.deepcopy(pair_scores),
            },
        },
        "classification_counts": {
            "current": _counts(current_rows, "classification"),
            "corrected": _counts(corrected_rows, "classification"),
        },
        "status_counts": {
            "current": _counts(current_rows, "status"),
            "corrected": _counts(corrected_rows, "status"),
        },
        "integrity": {
            "raw_ledger_hash": raw_hash,
            "authoritative_ledger_hash": authoritative_hash,
            "raw_record_count": len(raw_records),
            "corrected_key_count": len(corrected_rows),
            "changed_key_count": len(differences),
            "version_only_selection": False,
        },
    }
    return CorrectedLedgerResult(payload)


def load_corrected_ledger(path: str | pathlib.Path) -> CorrectedLedgerResult:
    """Load a JSON raw ledger and build its corrected report."""

    candidate = pathlib.Path(path)
    try:
        value = json.loads(candidate.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise CorrectedLedgerError(f"read corrected ledger input: {exc}") from exc
    return build_corrected_ledger(value)


build_report = build_corrected_ledger
correct = build_corrected_ledger
build = build_corrected_ledger
merge_recoveries = build_corrected_ledger


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, help="raw corrected-ledger JSON")
    parser.add_argument("--output", help="optional corrected-ledger report path")
    args = parser.parse_args(argv)
    try:
        result = load_corrected_ledger(args.input)
        rendered = result.json()
        if args.output:
            pathlib.Path(args.output).write_text(rendered, encoding="utf-8")
        else:
            sys.stdout.write(rendered)
    except CorrectedLedgerError as exc:
        print(json.dumps({"schema": SCHEMA, "ok": False, "error": str(exc)}, ensure_ascii=False))
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())


__all__ = [
    "ARM_MODES",
    "AUTHORITATIVE_HASH_DOMAIN",
    "AUTHORITY",
    "AmbiguousCorrectionError",
    "CLASSIFICATIONS",
    "CorrectionLink",
    "CorrectionLinkError",
    "CorrectedLedgerError",
    "CorrectedLedgerResult",
    "EvidenceDriftError",
    "EvidenceHashError",
    "INPUT_SCHEMA",
    "LedgerCycleError",
    "LedgerRecord",
    "MissingFieldError",
    "QUALITY_POINTS",
    "SCHEMA",
    "UnknownFieldError",
    "build_corrected_ledger",
    "build_report",
    "build",
    "canonical_json",
    "correct",
    "evidence_digest",
    "load_corrected_ledger",
    "merge_recoveries",
    "sha256_digest",
]
