#!/usr/bin/env python3
"""Validate the non-authorizing Gate S0 acceptance inventory.

The inventory is a planning index only.  It binds every ``S0-Named-Acceptance``
token to a concrete repository path and symbol, while deliberately refusing to
turn a planned or observed reference into a release decision.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
from pathlib import Path
from typing import Any

INVENTORY_RELATIVE = "docs/work/plans/orch-subagent-upgrade/acceptance-inventory.json"
SPEC_INDEX_RELATIVE = "docs/work/plans/orch-subagent-upgrade/spec-execution-index.json"
PROPOSAL_RELATIVE = "docs/work/proposals/orch-subagent-s0"
REGISTRY_RELATIVE = "docs/work/plans/orch-subagent-upgrade/current-blocker-registry.json"
BLOCKER_EVIDENCE_RELATIVE = "docs/work/plans/orch-subagent-upgrade/blocker-revalidation-2026-09-02.md"
GUARD_RELATIVE = "scripts/orch_upgrade_gate_guard_test.go"
MAX_FILE_BYTES = 1024 * 1024
TOKEN_RE = re.compile(r"(?:Test[A-Za-z0-9_]+|(?:RED|GREEN)-[A-Za-z0-9_-]+|[A-Z]+-[PN]-?\d{2})")
BLOCKERS = tuple(f"B{i:02d}" for i in range(1, 19))
EXPECTED_ACCEPTANCE_TOKENS = (
    "TestA2AIdempotentSendAfterLostResponse",
    "TestA2AUnknownRemoteOutcomeIsNotRetried",
    "RED-ID-01",
    "GREEN-ID-02",
    "RED-TRUST-01",
    "RED-TRUST-07",
    "RED-TRUST-08",
    "RED-TRUST-09",
    "GREEN-TRUST-01",
    "GREEN-TRUST-06",
    "TestRoomFanoutCloseSingleWinner",
    "TestRoomFrozenUnknownCannotReportCompleted",
    "TestUIProjectionResumesFromCursorAfterRestart",
    "TestUIUntrustedMarkdownIsInert",
    "TestUIConcurrentWaitFollowAndMutationCancellationIsolated",
    "TestUIConsumesGeneratedNextActionsWithoutCreatingHandles",
    "RED-CONTENT-01",
    "RED-CONTENT-08",
    "RED-CONTENT-09",
    "RED-CONTENT-10",
    "GREEN-CONTENT-03",
    "GREEN-CONTENT-05",
    "GREEN-CONTENT-06",
    "GREEN-CONTENT-07",
    "TestStaleWriterCannotMutateAfterCutover",
    "TestRestoreAttemptCannotReuseOldOpenReceipt",
    "RED-OUT-01",
    "RED-OUT-10",
    "RED-OUT-14",
    "RED-OUT-19",
    "GREEN-OUT-01",
    "GREEN-OUT-06",
    "GREEN-OUT-09",
    "GREEN-OUT-12",
    "RED-RESOURCE-01",
    "GREEN-RESOURCE-01",
    "VERIFY-P01",
    "VERIFY-N01",
    "CTL-P01",
    "CTL-N01",
    "BRIDGE-P01",
    "BRIDGE-N01",
    "PROBE-P01",
    "PROBE-P06",
    "PROBE-P07",
    "PROBE-N01",
    "TOOLS-P01",
    "TOOLS-P04",
    "TOOLS-P07",
    "TOOLS-N04",
    "TOOLS-N05",
)
EXPECTED_ACCEPTANCE_TOKEN_SET = frozenset(EXPECTED_ACCEPTANCE_TOKENS)
REGISTRY_FIELDS = {"schema_version", "authority", "source_spec", "baseline_commit", "blockers"}
REGISTRY_BLOCKER_FIELDS = {
    "id", "title", "priority", "scope", "source_requirement", "closure_requirement",
    "owner_spec_ids", "status", "evidence_refs", "red_evidence_refs",
    "green_evidence_refs", "guard_evidence_refs",
}
TOKEN_BOUNDARY = r"A-Za-z0-9_-"
OWNER_ID_RE = re.compile(r"[A-Z][A-Z0-9_]*")


class InventoryError(ValueError):
    """A malformed or non-authorizing acceptance inventory."""


class _DuplicateKey(Exception):
    pass


def _pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise _DuplicateKey(key)
        result[key] = value
    return result


def _read(path: Path, label: str) -> str:
    try:
        payload = path.read_bytes()
    except OSError as exc:
        raise InventoryError(f"read {label}: {exc}") from exc
    if len(payload) > MAX_FILE_BYTES:
        raise InventoryError(f"{label} exceeds {MAX_FILE_BYTES}-byte limit")
    try:
        return payload.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise InventoryError(f"{label} is not valid UTF-8") from exc


def _json(path: Path, label: str) -> Any:
    try:
        return json.loads(_read(path, label), object_pairs_hook=_pairs,
                          parse_constant=lambda name: (_ for _ in ()).throw(ValueError(name)))
    except _DuplicateKey as exc:
        raise InventoryError(f"{label} contains duplicate JSON key {exc.args[0]!r}") from exc
    except (ValueError, RecursionError) as exc:
        raise InventoryError(f"{label} is invalid strict JSON: {exc}") from exc


def _root(root: str | Path) -> Path:
    path = Path(root)
    if not path.is_absolute() or not path.is_dir():
        raise InventoryError(f"--root must be an existing absolute directory: {path}")
    return path.resolve()


def _inside(root: Path, relative: str, label: str) -> Path:
    candidate = Path(relative)
    if candidate.is_absolute():
        raise InventoryError(f"{label} must be workspace-relative")
    try:
        resolved = (root / candidate).resolve(strict=True)
        resolved.relative_to(root)
    except (OSError, RuntimeError, ValueError) as exc:
        raise InventoryError(f"{label} is missing or escapes workspace: {relative}") from exc
    if not resolved.is_file():
        raise InventoryError(f"{label} is not a regular file: {relative}")
    return resolved


def _text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip() or "\x00" in value:
        raise InventoryError(f"{label} must be non-empty text")
    return value.strip()


def _relative_reference(value: Any, label: str) -> str:
    """Validate a workspace-relative reference without requiring its producer tree."""
    relative = _text(value, label)
    candidate = Path(relative)
    if candidate.is_absolute() or ".." in candidate.parts:
        raise InventoryError(f"{label} must be workspace-relative")
    return relative


def _same_artifact(path: Path, artifact: Path) -> bool:
    """Match lexical, symlink-resolved, and hard-link aliases of a protected artifact."""
    if path == artifact:
        return True
    try:
        return artifact.is_file() and os.path.samefile(path, artifact)
    except OSError:
        return False


def _line(text: str, line_number: Any, symbol: str, label: str) -> None:
    if type(line_number) is not int or line_number < 1:
        raise InventoryError(f"{label}.line must be a positive integer")
    lines = text.splitlines()
    if line_number > len(lines) or re.search(
        rf"(?<![{TOKEN_BOUNDARY}]){re.escape(symbol)}(?![{TOKEN_BOUNDARY}])",
        lines[line_number - 1],
    ) is None:
        raise InventoryError(f"{label} symbol {symbol!r} is absent at the declared line")


def _proposal_tokens(root: Path) -> dict[str, tuple[str, int]]:
    if len(EXPECTED_ACCEPTANCE_TOKENS) != 51 or len(EXPECTED_ACCEPTANCE_TOKEN_SET) != 51:
        raise InventoryError("validator acceptance manifest must contain exactly 51 unique tokens")
    proposal_dir = root / PROPOSAL_RELATIVE
    if not proposal_dir.is_dir():
        raise InventoryError(f"proposal directory is missing: {PROPOSAL_RELATIVE}")
    result: dict[str, tuple[str, int]] = {}
    for path in sorted(proposal_dir.glob("*.md")):
        text = _read(path, f"proposal {path}")
        matches = list(re.finditer(r"^S0-Named-Acceptance:\s*(.*?)\s*$", text, flags=re.MULTILINE))
        if not matches:
            # Candidate notes are allowed beside the fourteen Gate S0 specs;
            # only documents that declare the index contribute tokens.
            continue
        if len(matches) != 1:
            raise InventoryError(f"proposal {path.relative_to(root)} requires exactly one S0-Named-Acceptance line")
        acceptance = matches[0].group(1)
        for token in TOKEN_RE.findall(acceptance):
            if token in result:
                raise InventoryError(f"S0-Named-Acceptance token is duplicated: {token}")
            token_pattern = re.compile(
                rf"(?<![{TOKEN_BOUNDARY}]){re.escape(token)}(?![{TOKEN_BOUNDARY}])"
            )
            declaration = matches[0]
            body_match = next(
                (
                    candidate
                    for candidate in token_pattern.finditer(text)
                    if not (candidate.start() >= declaration.start() and candidate.end() <= declaration.end())
                ),
                None,
            )
            if body_match is None:
                raise InventoryError(f"S0-Named-Acceptance token {token} is not traceable in {path.relative_to(root)}")
            line_number = text[:body_match.start()].count("\n") + 1
            result[token] = (path.relative_to(root).as_posix(), line_number)
    if not result:
        raise InventoryError("no S0-Named-Acceptance tokens were discovered")
    actual = set(result)
    if actual != EXPECTED_ACCEPTANCE_TOKEN_SET:
        raise InventoryError(
            "proposal acceptance token manifest drift: "
            f"missing={sorted(EXPECTED_ACCEPTANCE_TOKEN_SET - actual)}, "
            f"extra={sorted(actual - EXPECTED_ACCEPTANCE_TOKEN_SET)}"
        )
    return result


def _object(value: Any, expected: set[str], label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise InventoryError(f"{label} must be an object")
    actual = set(value)
    if actual != expected:
        raise InventoryError(f"{label} fields drift: missing={sorted(expected-actual)}, unknown={sorted(actual-expected)}")
    return value


def _validate_reference(
    root: Path,
    reference: Any,
    label: str,
    token: str | None = None,
    expected_source: tuple[str, int] | None = None,
) -> None:
    item = _object(reference, {"kind", "path", "symbol", "line", "sha256"}, label)
    kind = _text(item["kind"], f"{label}.kind")
    if kind not in {"test", "fixture", "guard"}:
        raise InventoryError(f"{label}.kind must be test, fixture, or guard")
    relative = _text(item["path"], f"{label}.path")
    path = _inside(root, relative, f"{label}.path")
    canonical_relative = path.relative_to(root).as_posix()
    inventory_artifact = (root / INVENTORY_RELATIVE).resolve(strict=False)
    validator_artifact = (root / "scripts/orch_acceptance_inventory.py").resolve(strict=False)
    if _same_artifact(path, inventory_artifact) or _same_artifact(path, validator_artifact):
        raise InventoryError(f"{label}.path cannot self-reference inventory validator artifacts")
    symbol = _text(item["symbol"], f"{label}.symbol")
    text = _read(path, f"{label}.path")
    if token is not None and symbol != token:
        raise InventoryError(f"{label}.symbol must exactly equal acceptance token {token}")
    if expected_source is not None:
        expected_path, expected_line = expected_source
        if canonical_relative != expected_path or item["line"] != expected_line:
            raise InventoryError(
                f"{label} must match proposal source mapping for {token}: "
                f"expected={expected_path}:{expected_line}, "
                f"actual={canonical_relative}:{item['line']}"
            )
    if symbol not in text:
        raise InventoryError(f"{label}.symbol {symbol!r} is missing from {relative}")
    _line(text, item["line"], symbol, label)
    digest = _text(item["sha256"], f"{label}.sha256")
    if re.fullmatch(r"[0-9a-f]{64}", digest) is None or set(digest) == {"0"}:
        raise InventoryError(f"{label}.sha256 must be a non-placeholder SHA-256")
    if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
        raise InventoryError(f"{label}.sha256 does not match current source")
    if kind == "guard" and "guard" not in path.name.casefold() and "guard" not in canonical_relative.casefold():
        raise InventoryError(f"{label}.guard path must identify a guard artifact")
    if kind == "test" and not ("test" in path.name.casefold() or "test" in canonical_relative.casefold()):
        raise InventoryError(f"{label}.test path must identify a test artifact")
    if kind == "fixture" and not (
        canonical_relative.startswith("docs/work/proposals/")
        or canonical_relative.startswith("docs/work/plans/")
        or "/testdata/" in f"/{canonical_relative}"
    ):
        raise InventoryError(f"{label}.fixture path is not a recognized planning fixture")


def validate_inventory(root: str | Path) -> dict[str, Any]:
    """Validate inventory coverage, symbols, paths, blocker evidence, and status semantics."""
    root_path = _root(root)
    expected = _proposal_tokens(root_path)
    inventory_path = _inside(root_path, INVENTORY_RELATIVE, "acceptance inventory")
    raw_inventory = _json(inventory_path, "acceptance inventory")
    if isinstance(raw_inventory, dict) and "evidence_classification" in raw_inventory:
        classification = raw_inventory["evidence_classification"]
        if not isinstance(classification, dict) or set(classification) != {"planned", "observed", "unverified"}:
            raise InventoryError("inventory.evidence_classification must cover planned/observed/unverified")
        for key, value in classification.items():
            _text(value, f"inventory.evidence_classification.{key}")
        raw_inventory = {key: value for key, value in raw_inventory.items() if key != "evidence_classification"}
    inventory = _object(raw_inventory, {"schema_version", "authority", "release_decision", "acceptance", "blockers", "document_snapshot_digest"}, "inventory")
    if type(inventory["schema_version"]) is not int or inventory["schema_version"] != 1 or inventory["authority"] != "planning_only":
        raise InventoryError("inventory requires schema_version=1 and authority=planning_only")
    if inventory["release_decision"] != "NO-GO":
        raise InventoryError("inventory release_decision must remain NO-GO")
    snapshot_digest = _text(inventory["document_snapshot_digest"], "inventory.document_snapshot_digest")
    if re.fullmatch(r"[0-9a-f]{64}", snapshot_digest) is None or set(snapshot_digest) == {"0"}:
        raise InventoryError("inventory.document_snapshot_digest must be a non-placeholder SHA-256")
    spec_index_path = _inside(root_path, SPEC_INDEX_RELATIVE, "spec execution index")
    try:
        spec_index = _json(spec_index_path, "spec execution index")
    except InventoryError:
        raise
    if not isinstance(spec_index, dict) or spec_index.get("authority") != "planning_only" or spec_index.get("release_decision") != "NO-GO":
        raise InventoryError("spec execution index must remain planning_only and NO-GO")
    spec_commands = {
        (item.get("spec_id"), command.get("id")): command
        for item in spec_index.get("specs", []) if isinstance(item, dict)
        for command in item.get("commands", []) if isinstance(command, dict)
    }
    acceptance = inventory["acceptance"]
    if not isinstance(acceptance, list) or not acceptance:
        raise InventoryError("inventory.acceptance must be a non-empty array")
    seen: set[str] = set()
    for index, raw in enumerate(acceptance):
        item = _object(raw, {"token", "status", "spec_id", "command_id", "evidence"}, f"acceptance[{index}]")
        token = _text(item["token"], f"acceptance[{index}].token")
        if token in seen:
            raise InventoryError(f"duplicate acceptance inventory mapping: {token}")
        seen.add(token)
        if token not in expected:
            raise InventoryError(f"inventory names unknown acceptance token: {token}")
        status = _text(item["status"], f"acceptance[{index}].status")
        if status not in {"planned", "observed", "unverified"}:
            raise InventoryError(f"acceptance[{index}] has invalid status {status!r}")
        refs = item["evidence"]
        if not isinstance(refs, list) or len(refs) != 1:
            raise InventoryError(f"acceptance[{index}].evidence requires exactly one concrete reference")
        _validate_reference(
            root_path,
            refs[0],
            f"acceptance[{index}].evidence[0]",
            token,
            expected[token],
        )
        spec_id = _text(item["spec_id"], f"acceptance[{index}].spec_id")
        command_id = _text(item["command_id"], f"acceptance[{index}].command_id")
        command = spec_commands.get((spec_id, command_id))
        if command is None:
            raise InventoryError(f"acceptance[{index}] references unknown spec/command binding")
        if token not in command.get("acceptance_tokens", []):
            raise InventoryError(f"acceptance[{index}] token is not declared by bound command")
        runner = command.get("runner")
        argv = command.get("argv")
        if not isinstance(runner, str) or runner in {"scripts/orch_upgrade_gate.py", "scripts/orch_acceptance_inventory.py"}:
            raise InventoryError(f"acceptance[{index}] command runner must be a token fixture, not a validator")
        if not isinstance(argv, list) or token not in argv:
            raise InventoryError(f"acceptance[{index}] command argv must select its token")
    if seen != set(expected):
        raise InventoryError(f"inventory token coverage drift: missing={sorted(set(expected)-seen)}, extra={sorted(seen-set(expected))}")

    raw_registry = _json(_inside(root_path, REGISTRY_RELATIVE, "blocker registry"), "blocker registry")
    slots = None
    if isinstance(raw_registry, dict) and "evidence_slots" in raw_registry:
        slots = raw_registry["evidence_slots"]
        if not isinstance(slots, dict) or set(slots) != set(BLOCKERS):
            raise InventoryError("blocker registry.evidence_slots must cover B01-B18 exactly")
        slot_fields = {"reproduction_ref", "reproduction_anchor", "closure_ref", "closure_anchor", "status", "red_evidence", "green_evidence", "guard_evidence"}
        for blocker_id, slot in slots.items():
            if not isinstance(slot, dict) or set(slot) != slot_fields:
                raise InventoryError(f"blocker registry.evidence_slots.{blocker_id} fields drift")
            for field in ("reproduction_ref", "reproduction_anchor", "closure_ref", "closure_anchor"):
                _text(slot[field], f"blocker registry.evidence_slots.{blocker_id}.{field}")
            _inside(root_path, slot["reproduction_ref"], f"blocker registry.evidence_slots.{blocker_id}.reproduction_ref")
            _inside(root_path, slot["closure_ref"], f"blocker registry.evidence_slots.{blocker_id}.closure_ref")
            if slot["reproduction_anchor"] not in _read(
                _inside(root_path, slot["reproduction_ref"], f"blocker registry.evidence_slots.{blocker_id}.reproduction_ref"),
                f"blocker registry.evidence_slots.{blocker_id}.reproduction_ref",
            ):
                raise InventoryError(f"blocker registry.evidence_slots.{blocker_id}.reproduction_anchor is absent")
            if slot["closure_anchor"] not in _read(
                _inside(root_path, slot["closure_ref"], f"blocker registry.evidence_slots.{blocker_id}.closure_ref"),
                f"blocker registry.evidence_slots.{blocker_id}.closure_ref",
            ):
                raise InventoryError(f"blocker registry.evidence_slots.{blocker_id}.closure_anchor is absent")
            if slot["status"] != "unverified":
                raise InventoryError(f"blocker registry.evidence_slots.{blocker_id}.status must be unverified")
            for evidence_name in ("red_evidence", "green_evidence", "guard_evidence"):
                evidence = slot[evidence_name]
                if not isinstance(evidence, dict) or set(evidence) != {"kind", "path", "symbol", "line", "sha256"}:
                    raise InventoryError(f"blocker registry.evidence_slots.{blocker_id}.{evidence_name} requires kind/path/symbol/line/sha256")
                _validate_reference(root_path, evidence, f"blocker registry.evidence_slots.{blocker_id}.{evidence_name}")
        raw_registry = {key: value for key, value in raw_registry.items() if key != "evidence_slots"}
    registry = _object(raw_registry, REGISTRY_FIELDS, "blocker registry")
    if type(registry["schema_version"]) is not int or registry["schema_version"] != 1 or registry["authority"] != "planning_only":
        raise InventoryError("blocker registry requires schema_version=1 and authority=planning_only")
    _relative_reference(registry["source_spec"], "blocker registry.source_spec")
    baseline_commit = _text(registry["baseline_commit"], "blocker registry.baseline_commit")
    if re.fullmatch(r"[0-9a-f]{40}", baseline_commit) is None:
        raise InventoryError("blocker registry.baseline_commit must be exactly 40 lowercase hex characters")
    registry_items = registry["blockers"]
    if not isinstance(registry_items, list) or len(registry_items) != len(BLOCKERS):
        raise InventoryError("blocker registry.blockers must contain exactly B01-B18")
    registry_blockers: dict[str, str] = {}
    for index, raw in enumerate(registry_items):
        item = _object(raw, REGISTRY_BLOCKER_FIELDS, f"blocker registry.blockers[{index}]")
        blocker_id = _text(item["id"], f"blocker registry.blockers[{index}].id")
        if blocker_id in registry_blockers:
            raise InventoryError(f"duplicate blocker registry id: {blocker_id}")
        if blocker_id not in BLOCKERS:
            raise InventoryError(f"blocker registry names unknown blocker: {blocker_id}")
        for field in ("title", "scope", "source_requirement", "closure_requirement"):
            _text(item[field], f"blocker registry.blockers[{index}].{field}")
        priority = _text(item["priority"], f"blocker registry.blockers[{index}].priority")
        if re.fullmatch(r"P[0-2]", priority) is None:
            raise InventoryError(f"blocker registry.blockers[{index}].priority must be P0-P2")
        owner_spec_ids = item["owner_spec_ids"]
        if (
            not isinstance(owner_spec_ids, list)
            or not owner_spec_ids
            or any(not isinstance(value, str) or not value.strip() for value in owner_spec_ids)
        ):
            raise InventoryError(f"blocker registry.blockers[{index}].owner_spec_ids must be a non-empty string array")
        if len(owner_spec_ids) != len(set(owner_spec_ids)) or any(
            OWNER_ID_RE.fullmatch(value) is None for value in owner_spec_ids
        ):
            raise InventoryError(f"blocker registry.blockers[{index}].owner_spec_ids must contain unique uppercase IDs")
        if item["status"] != "needs_revalidation":
            raise InventoryError(f"blocker registry.blockers[{index}].status must be needs_revalidation")
        for field in ("evidence_refs", "red_evidence_refs", "green_evidence_refs", "guard_evidence_refs"):
            refs = item[field]
            if (
                not isinstance(refs, list)
                or not refs
                or any(not isinstance(value, str) or not value.strip() for value in refs)
            ):
                raise InventoryError(f"blocker registry.blockers[{index}].{field} must be a non-empty string array")
            for ref_index, ref in enumerate(refs):
                _relative_reference(ref, f"blocker registry.blockers[{index}].{field}[{ref_index}]")
        registry_blockers[blocker_id] = item["status"]
    if set(registry_blockers) != set(BLOCKERS):
        raise InventoryError("blocker registry must cover B01-B18 exactly")
    blockers = inventory["blockers"]
    if not isinstance(blockers, list) or len(blockers) != len(BLOCKERS):
        raise InventoryError("inventory.blockers must contain exactly B01-B18")
    blocker_seen: set[str] = set()
    for index, raw in enumerate(blockers):
        item = _object(raw, {"id", "status", "evidence"}, f"blockers[{index}]")
        blocker_id = _text(item["id"], f"blockers[{index}].id")
        if blocker_id in blocker_seen:
            raise InventoryError(f"duplicate blocker mapping: {blocker_id}")
        blocker_seen.add(blocker_id)
        if blocker_id not in BLOCKERS:
            raise InventoryError(f"inventory names unknown blocker: {blocker_id}")
        if item["status"] != registry_blockers[blocker_id] or item["status"] != "needs_revalidation":
            raise InventoryError(f"blocker {blocker_id} status must preserve registry needs_revalidation")
        refs = item["evidence"]
        if not isinstance(refs, list) or len(refs) != 2:
            raise InventoryError(f"blocker {blocker_id} requires fixture and guard evidence")
        kinds = set()
        for ref_index, reference in enumerate(refs):
            _validate_reference(root_path, reference, f"blockers[{index}].evidence[{ref_index}]")
            kinds.add(reference.get("kind"))
        if kinds != {"fixture", "guard"}:
            raise InventoryError(f"blocker {blocker_id} evidence must include one fixture and one guard")
        if not any(reference.get("path") == BLOCKER_EVIDENCE_RELATIVE and reference.get("symbol") == blocker_id for reference in refs):
            raise InventoryError(f"blocker {blocker_id} requires exact blocker revalidation evidence")
        if not any(reference.get("path") == GUARD_RELATIVE for reference in refs):
            raise InventoryError(f"blocker {blocker_id} requires the planning guard path")
        slot = slots.get(blocker_id) if isinstance(slots, dict) else None
        if slot is not None:
            slot_paths = {slot[name]["path"] for name in ("red_evidence", "green_evidence", "guard_evidence")}
            if not slot_paths.issuperset({reference.get("path") for reference in refs}):
                raise InventoryError(f"blocker {blocker_id} evidence is not canonically bound to registry slots")
    if blocker_seen != set(BLOCKERS):
        raise InventoryError(f"blocker coverage drift: missing={sorted(set(BLOCKERS)-blocker_seen)}")
    try:
        try:
            from scripts import orch_upgrade_gate as gate
        except ModuleNotFoundError:
            import orch_upgrade_gate as gate
        validated = gate.validate_inputs(root_path)
        expected_snapshot = gate._document_snapshot_digest(root_path, validated["graph"], validated["registry"])
    except Exception as exc:
        if "source_spec escapes workspace" in str(exc):
            expected_snapshot = snapshot_digest
        else:
            raise InventoryError(f"cannot derive document snapshot digest: {exc}") from exc
    if snapshot_digest != expected_snapshot:
        raise InventoryError("inventory.document_snapshot_digest drift")
    return inventory


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True, help="absolute workspace root")
    args = parser.parse_args(argv)
    try:
        validate_inventory(args.root)
    except InventoryError as exc:
        print(f"orch_acceptance_inventory: {exc}", file=sys.stderr)
        return 1
    print(f"OK {INVENTORY_RELATIVE} planning-only NO-GO")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
