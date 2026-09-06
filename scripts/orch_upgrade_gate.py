#!/usr/bin/env python3
"""Validate and render the offline, non-authorizing S0 planning skeleton.
The two JSON files are the only machine inputs.  This tool never evaluates a
release decision and never grants runtime permission; it only checks that the
planning graph, blocker registry, source specification, and documentation
metadata/completeness contract agree before producing a deterministic Markdown
projection.
"""
from __future__ import annotations
import argparse
import hashlib
import json
import os
import re
import sys
import tempfile
from dataclasses import dataclass, field, fields, is_dataclass
from pathlib import Path
from typing import Any, Mapping, Sequence, get_args, get_origin, get_type_hints
GRAPH_RELATIVE = "docs/work/plans/orch-subagent-upgrade/release-gate-dag.json"
REGISTRY_RELATIVE = "docs/work/plans/orch-subagent-upgrade/current-blocker-registry.json"
OUTPUT_RELATIVE = "docs/work/plans/orch-subagent-upgrade/dependency-map.generated.md"
MATRIX_RELATIVE = "docs/work/plans/orch-subagent-upgrade/cross-task-requirements.md"
SPEC_INDEX_RELATIVE = "docs/work/plans/orch-subagent-upgrade/spec-execution-index.json"
MAX_FILE_BYTES = 1024 * 1024
PROVIDERS = ("codex", "claude", "agy", "grok")
CT_CAPABILITIES = {
    "direct_peer_delivery_union",
    "strict_discover_read_send_grant",
    "message_reply_association",
    "room_no_escalation",
    "cursor_resume",
    "idempotency",
    "unknown_outcome",
    "unauthorized_zero_side_effect",
}
CT_SURFACES = {"unified_v2", "room"}
S0_COMPLETENESS_FIELDS = (
    "Owner",
    "Scope",
    "Dependencies",
    "Inputs",
    "Outputs",
    "Failure",
    "Recovery",
    "Field-Chain",
    "Named-Acceptance",
)
_PLACEHOLDER_VALUES = {"none", "n/a", "na", "tbd", "todo", "unknown", "pending"}
_ID = re.compile(r"^[A-Z][A-Z0-9_]*$")
_HEX40 = re.compile(r"^[0-9a-f]{40}$")
_MATRIX_FIXTURE_TOKEN = re.compile(r"\b(?:RED|GREEN)-[A-Z0-9][A-Z0-9_-]*\b")
class GateError(ValueError):
    """A malformed planning input or inconsistent planning projection."""
@dataclass(frozen=True)
class Graph:
    schema_version: int
    authority: str
    source_spec: str
    nodes: list[Node]
    edges: list[Edge]
    cross_task_requirements: list[CrossTaskRequirement]
@dataclass(frozen=True)
class Node:
    id: str
    title: str
    kind: str
    document: str
    status: str
@dataclass(frozen=True)
class Edge:
    from_: str = field(metadata={"json_name": "from"})
    to: str
    kind: str
    providers: list[str]


@dataclass(frozen=True)
class CrossTaskRequirement:
    id: str
    capability: str
    title: str
    canonical_surface: str
    owner_spec_ids: list[str]
    depends_on: list[str]
    ancestor_gates: list[str]
    red_evidence_refs: list[str]
    green_evidence_refs: list[str]
    guard_evidence_refs: list[str]
    status: str
@dataclass(frozen=True)
class Registry:
    schema_version: int
    authority: str
    source_spec: str
    baseline_commit: str
    blockers: list[Blocker]
@dataclass(frozen=True)
class Blocker:
    id: str
    title: str
    priority: str
    scope: str
    source_requirement: str
    closure_requirement: str
    owner_spec_ids: list[str]
    status: str
    evidence_refs: list[str]
    red_evidence_refs: list[str]
    green_evidence_refs: list[str]
    guard_evidence_refs: list[str]
SCHEMA_TYPES = (Graph, Node, Edge, CrossTaskRequirement, Registry, Blocker)
class _DuplicateKey(Exception):
    pass
def _pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise _DuplicateKey(key)
        result[key] = value
    return result
def canonical_json(value: Any) -> str:
    """Return this tool's compact sorted-key JSON form (not JCS)."""
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
def _schema_keys(schema: type[Any]) -> set[str]:
    return {item.metadata.get("json_name", item.name) for item in fields(schema)}

def _object(value: Any, schema: type[Any], label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise GateError(f"{label} must be an object")
    expected, actual = _schema_keys(schema), set(value)
    if expected != actual:
        raise GateError(f"{label} field mismatch: missing={sorted(expected-actual)}, unknown={sorted(actual-expected)}")
    hints = get_type_hints(schema)
    return {item.metadata.get("json_name", item.name):
            _typed(value[item.metadata.get("json_name", item.name)], hints[item.name], f"{label}.{item.name}")
            for item in fields(schema)}

def _typed(value: Any, shape: Any, label: str) -> Any:
    if is_dataclass(shape):
        return _object(value, shape, label)
    if get_origin(shape) is list:
        if not isinstance(value, list):
            raise GateError(f"{label} must be an array")
        return [_typed(item, get_args(shape)[0], f"{label}[{i}]") for i, item in enumerate(value)]
    if shape is str:
        return _text(value, label)
    if shape is int and type(value) is int:
        return value
    raise GateError(f"{label} has invalid or unsupported type")

def _text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip() or "\x00" in value:
        raise GateError(f"{label} must be non-empty text")
    return value

def _id(value: Any, label: str) -> str:
    value = _text(value, label)
    if _ID.fullmatch(value) is None:
        raise GateError(f"{label} has invalid identifier {value!r}")
    return value

def _header(item: dict[str, Any], label: str) -> None:
    if item["schema_version"] != 1 or item["authority"] != "planning_only":
        raise GateError(f"{label} requires schema_version=1 and authority=planning_only")

def _parse_graph(value: Any) -> dict[str, Any]:
    graph = _object(value, Graph, "graph")
    _header(graph, "graph")
    for node in graph["nodes"]:
        _id(node["id"], "node.id")
        if (node["kind"], node["status"]) not in {("spec", "draft"), ("gate", "pending")}:
            raise GateError("node kind/status combination is invalid")
    for edge in graph["edges"]:
        _id(edge["from"], "edge.from")
        _id(edge["to"], "edge.to")
        if edge["kind"] not in {"requires", "root_requirement", "root_compatibility", "provider_enable"}:
            raise GateError("edge.kind has invalid enum")
        if edge["kind"] == "provider_enable":
            if (edge["from"], edge["to"], tuple(edge["providers"])) != ("BRIDGE", "TOOLS", PROVIDERS):
                raise GateError("provider_enable must be BRIDGE -> TOOLS for exactly codex,claude,agy,grok")
        elif edge["providers"]:
            raise GateError("non-provider edge must have providers=[]")
    requirement_ids: set[str] = set()
    capabilities: set[str] = set()
    for requirement in graph["cross_task_requirements"]:
        _id(requirement["id"], "cross_task_requirement.id")
        if requirement["id"] in requirement_ids:
            raise GateError("cross_task_requirements IDs must be unique")
        requirement_ids.add(requirement["id"])
        capability = requirement["capability"]
        if capability not in CT_CAPABILITIES:
            raise GateError("cross_task_requirement.capability has invalid enum")
        if capability in capabilities:
            raise GateError("cross_task_requirements capabilities must be unique")
        capabilities.add(capability)
        if requirement["canonical_surface"] not in CT_SURFACES:
            raise GateError("cross_task_requirement.canonical_surface has invalid enum")
        expected_surface = "room" if capability == "room_no_escalation" else "unified_v2"
        if requirement["canonical_surface"] != expected_surface:
            raise GateError(
                f"cross_task_requirement {requirement['id']} canonical_surface must be {expected_surface} for {capability}"
            )
        if requirement["status"] not in {"pending", "needs_revalidation"}:
            raise GateError("cross_task_requirement.status must remain pending or needs_revalidation")
        for key in (
            "owner_spec_ids",
            "depends_on",
            "ancestor_gates",
            "red_evidence_refs",
            "green_evidence_refs",
            "guard_evidence_refs",
        ):
            values = requirement[key]
            if not values or len(values) != len(set(values)):
                raise GateError(f"cross_task_requirement.{key} must be nonempty and unique")
        for owner in requirement["owner_spec_ids"]:
            _id(owner, "cross_task_requirement.owner_spec_ids")
        for dependency in requirement["depends_on"]:
            _id(dependency, "cross_task_requirement.depends_on")
        for gate in requirement["ancestor_gates"]:
            _id(gate, "cross_task_requirement.ancestor_gates")
        required_ancestors = {
            "VERIFY_S",
            "ROLLOUT_S",
            "DOMAIN_DONE",
            "VERIFY_DONE",
            "PRECANARY_DONE",
        }
        if not required_ancestors.issubset(requirement["ancestor_gates"]):
            raise GateError(
                f"cross_task_requirement {requirement['id']} ancestor_gates must include VERIFY_S, ROLLOUT_S, DOMAIN_DONE, VERIFY_DONE, PRECANARY_DONE"
            )
    if capabilities != CT_CAPABILITIES:
        missing = sorted(CT_CAPABILITIES - capabilities)
        extra = sorted(capabilities - CT_CAPABILITIES)
        raise GateError(f"cross_task_requirements must cover each capability exactly once: missing={missing}, extra={extra}")
    return graph

def _parse_registry(value: Any) -> dict[str, Any]:
    slots: dict[str, Any] | None = None
    if isinstance(value, dict) and "evidence_slots" in value:
        slots = value["evidence_slots"]
        if not isinstance(slots, dict) or set(slots) != {f"B{i:02d}" for i in range(1, 19)}:
            raise GateError("registry.evidence_slots must cover B01-B18 exactly")
        for blocker_id, slot in slots.items():
            if not isinstance(slot, dict) or set(slot) != {"reproduction_ref", "reproduction_anchor", "closure_ref", "closure_anchor", "status", "red_evidence", "green_evidence", "guard_evidence"}:
                raise GateError(f"registry.evidence_slots.{blocker_id} field mismatch")
            for field_name in ("reproduction_ref", "reproduction_anchor", "closure_ref", "closure_anchor"):
                _text(slot[field_name], f"registry.evidence_slots.{blocker_id}.{field_name}")
            if slot["status"] != "unverified":
                raise GateError(f"registry.evidence_slots.{blocker_id}.status must remain unverified")
            for evidence_name in ("red_evidence", "green_evidence", "guard_evidence"):
                evidence = slot[evidence_name]
                if not isinstance(evidence, dict) or set(evidence) != {"kind", "path", "symbol", "line", "sha256"}:
                    raise GateError(f"registry.evidence_slots.{blocker_id}.{evidence_name} must contain kind/path/symbol/line/sha256")
                if evidence["kind"] not in {"test", "fixture", "guard"}:
                    raise GateError(f"registry.evidence_slots.{blocker_id}.{evidence_name}.kind invalid")
                _text(evidence["path"], f"registry.evidence_slots.{blocker_id}.{evidence_name}.path")
                _text(evidence["symbol"], f"registry.evidence_slots.{blocker_id}.{evidence_name}.symbol")
                if type(evidence["line"]) is not int or evidence["line"] < 1:
                    raise GateError(f"registry.evidence_slots.{blocker_id}.{evidence_name}.line invalid")
                if re.fullmatch(r"[0-9a-f]{64}", str(evidence["sha256"])) is None or set(evidence["sha256"]) == {"0"}:
                    raise GateError(f"registry.evidence_slots.{blocker_id}.{evidence_name}.sha256 invalid")
        value = {key: item for key, item in value.items() if key != "evidence_slots"}
    registry = _object(value, Registry, "registry")
    _header(registry, "registry")
    if _HEX40.fullmatch(registry["baseline_commit"]) is None:
        raise GateError("registry.baseline_commit must be exactly 40 lowercase hex characters")
    for blocker in registry["blockers"]:
        _id(blocker["id"], "blocker.id")
        if blocker["priority"] not in {"P0", "P1", "P2"} or blocker["status"] != "needs_revalidation":
            raise GateError("blocker priority/status invalid; this tool cannot close blockers")
        for key in (
            "owner_spec_ids",
            "evidence_refs",
            "red_evidence_refs",
            "green_evidence_refs",
            "guard_evidence_refs",
        ):
            values = blocker[key]
            if not values:
                raise GateError(f"blocker.{key} must be nonempty and unique")
        for owner in blocker["owner_spec_ids"]:
            _id(owner, "blocker.owner_spec_ids")
    if slots is not None:
        registry["evidence_slots"] = slots
    return registry


def _validate_spec_execution_index(root: Path, graph: dict[str, Any], registry: dict[str, Any], mandatory: bool = True) -> dict[str, Any] | None:
    """Validate the optional machine execution index for the fourteen child specs."""
    path = root / SPEC_INDEX_RELATIVE
    if not path.exists() and mandatory:
        raise GateError(f"required spec execution index is missing: {SPEC_INDEX_RELATIVE}")
    if not path.exists():
        return None
    index = _read_json(_inside_file(root, SPEC_INDEX_RELATIVE, "spec execution index"), "spec execution index")
    if not isinstance(index, dict):
        raise GateError("spec execution index must be an object")
    expected_top = {"schema_version", "authority", "release_decision", "evidence_classification", "specs"}
    if set(index) != expected_top:
        raise GateError(f"spec execution index field mismatch: missing={sorted(expected_top-set(index))}, unknown={sorted(set(index)-expected_top)}")
    if index["schema_version"] != 1 or index["authority"] != "planning_only" or index["release_decision"] != "NO-GO":
        raise GateError("spec execution index must remain planning_only and NO-GO")
    _text(index["evidence_classification"], "spec execution index.evidence_classification")
    specs = index["specs"]
    if not isinstance(specs, list) or len(specs) != 14:
        raise GateError("spec execution index.specs must contain exactly fourteen entries")
    graph_specs = {node["id"]: node for node in graph["nodes"] if node["kind"] == "spec"}
    node_kinds = {node["id"]: node["kind"] for node in graph["nodes"]}
    predecessors: dict[str, set[str]] = {node_id: set() for node_id in node_kinds}
    for edge in graph["edges"]:
        if edge["kind"] != "provider_enable":
            predecessors[edge["to"]].add(edge["from"])
    registry_blockers = {blocker["id"] for blocker in registry["blockers"]}
    seen: set[str] = set()
    for item in specs:
        expected = {"spec_id", "document", "owner", "prerequisite_ids", "ancestor_gate_ids", "entry_criteria", "exit_criteria", "commands", "evidence_classification", "blocker_ids"}
        if not isinstance(item, dict) or set(item) != expected:
            raise GateError("spec execution index entry field mismatch")
        spec_id = _id(item["spec_id"], "spec execution index.spec_id")
        if spec_id in seen or spec_id not in graph_specs:
            raise GateError(f"spec execution index unknown or duplicate spec {spec_id}")
        seen.add(spec_id)
        if item["document"] != graph_specs[spec_id]["document"]:
            raise GateError(f"spec execution index {spec_id} document does not match DAG")
        for text_field in ("owner", "evidence_classification"):
            _text(item[text_field], f"spec execution index {spec_id}.{text_field}")
        incoming = {edge["from"] for edge in graph["edges"] if edge["to"] == spec_id}
        if set(item["prerequisite_ids"]) != incoming:
            raise GateError(f"spec execution index {spec_id}.prerequisite_ids does not match DAG incoming edges")
        for field_name in ("prerequisite_ids", "ancestor_gate_ids", "blocker_ids"):
            values = item[field_name]
            if not isinstance(values, list) or len(values) != len(set(values)):
                raise GateError(f"spec execution index {spec_id}.{field_name} must be a unique array")
            for value in values:
                _id(value, f"spec execution index {spec_id}.{field_name}")
        expected_ancestors: set[str] = set()
        pending = list(predecessors[spec_id])
        while pending:
            candidate = pending.pop()
            if candidate in expected_ancestors:
                continue
            if node_kinds[candidate] == "gate":
                expected_ancestors.add(candidate)
            pending.extend(predecessors[candidate])
        if set(item["ancestor_gate_ids"]) != expected_ancestors:
            raise GateError(f"spec execution index {spec_id}.ancestor_gate_ids does not match transitive DAG ancestors")
        expected_blockers = {blocker["id"] for blocker in registry["blockers"] if spec_id in blocker["owner_spec_ids"]}
        if set(item["blocker_ids"]) != expected_blockers:
            raise GateError(f"spec execution index {spec_id}.blocker_ids does not match blocker owners")
        for field_name in ("entry_criteria", "exit_criteria"):
            values = item[field_name]
            if not isinstance(values, list) or not values or any(not isinstance(value, str) or not value.strip() for value in values):
                raise GateError(f"spec execution index {spec_id}.{field_name} must be non-empty text array")
        commands = item["commands"]
        if not isinstance(commands, list) or not commands:
            raise GateError(f"spec execution index {spec_id}.commands must be non-empty")
        command_ids: set[str] = set()
        for command in commands:
            required_command_fields = {
                "id", "command_id", "purpose", "command", "runner", "argv", "expected_exit",
                "expected_status", "expected_output", "acceptance_tokens", "blocker_ids",
            }
            if not isinstance(command, dict) or set(command) != required_command_fields:
                raise GateError(f"spec execution index {spec_id}.commands entry field mismatch")
            command_id = _text(command["id"], f"spec execution index {spec_id}.command.id")
            if command["command_id"] != command_id:
                raise GateError(f"spec execution index {spec_id}.command.command_id must equal id")
            if re.fullmatch(r"[A-Za-z][A-Za-z0-9_-]*", command_id) is None:
                raise GateError(f"spec execution index {spec_id}.command.id has invalid identifier {command_id!r}")
            if command_id in command_ids:
                raise GateError(f"spec execution index {spec_id}.commands IDs must be unique")
            command_ids.add(command_id)
            for field_name in ("purpose", "command", "runner", "expected_status", "expected_output"):
                _text(command[field_name], f"spec execution index {spec_id}.command.{field_name}")
            if command["expected_status"].casefold() in _PLACEHOLDER_VALUES or command["expected_output"].casefold() in _PLACEHOLDER_VALUES:
                raise GateError(f"spec execution index {spec_id}.command expected status/output cannot be a placeholder")
            runner = command["runner"]
            if Path(runner).is_absolute() or ".." in Path(runner).parts or runner.startswith("<"):
                raise GateError(f"spec execution index {spec_id}.command.runner must be workspace-relative")
            _inside_file(root, runner, f"spec execution index {spec_id}.command.runner")
            argv = command["argv"]
            if not isinstance(argv, list) or not argv or any(not isinstance(arg, str) or not arg.strip() for arg in argv):
                raise GateError(f"spec execution index {spec_id}.command.argv must be non-empty text array")
            if any("<workspace-root>" in arg or "<" in arg or ">" in arg for arg in argv):
                raise GateError(f"spec execution index {spec_id}.command.argv contains placeholder")
            if type(command["expected_exit"]) is not int or command["expected_exit"] < 0 or command["expected_exit"] > 255:
                raise GateError(f"spec execution index {spec_id}.command.expected_exit must be 0..255")
            for field_name in ("acceptance_tokens", "blocker_ids"):
                values = command[field_name]
                if not isinstance(values, list) or any(not isinstance(value, str) or not value.strip() for value in values):
                    raise GateError(f"spec execution index {spec_id}.command.{field_name} must be a text array")
                if len(values) != len(set(values)):
                    raise GateError(f"spec execution index {spec_id}.command.{field_name} must be unique")
                if field_name == "blocker_ids" and any(value not in registry_blockers for value in values):
                    raise GateError(f"spec execution index {spec_id}.command.blocker_ids references unknown blocker")
    if seen != set(graph_specs):
        raise GateError(f"spec execution index coverage drift: missing={sorted(set(graph_specs)-seen)}")
    return index

def _read_json(path: Path, label: str) -> Any:
    try:
        return json.loads(_read_text(path, label), object_pairs_hook=_pairs,
                          parse_constant=lambda name: (_ for _ in ()).throw(ValueError(name)))
    except _DuplicateKey as exc:
        raise GateError(f"{label} contains duplicate JSON key {exc.args[0]!r}") from exc
    except (ValueError, RecursionError) as exc:
        raise GateError(f"{label} is invalid strict JSON: {exc}") from exc

def _root_path(root: str | Path) -> Path:
    path = Path(root)
    if not path.is_absolute():
        raise GateError("--root must be an absolute path")
    if not path.exists() or not path.is_dir():
        raise GateError(f"workspace root is not a directory: {path}")
    return path.resolve()
def _inside_file(root: Path, relative: str, label: str) -> Path:
    text = _text(relative, label)
    candidate = Path(text)
    if candidate.is_absolute():
        raise GateError(f"{label} must be workspace-relative")
    try:
        resolved = (root / candidate).resolve(strict=True)
        resolved.relative_to(root)
    except (OSError, RuntimeError, ValueError) as exc:
        raise GateError(f"{label} escapes workspace or does not exist: {text}") from exc
    if not resolved.is_file():
        raise GateError(f"{label} is not a regular file: {text}")
    return resolved


def _read_bytes(path: Path, label: str) -> bytes:
    try:
        with path.open("rb") as handle:
            payload = handle.read(MAX_FILE_BYTES + 1)
    except OSError as exc:
        raise GateError(f"read {label} {path}: {exc}") from exc
    if len(payload) > MAX_FILE_BYTES:
        raise GateError(f"{label} exceeds the {MAX_FILE_BYTES}-byte limit")
    return payload

def _read_text(path: Path, label: str) -> str:
    payload = _read_bytes(path, label)
    try:
        return payload.decode("utf-8")
    except UnicodeError as exc:
        raise GateError(f"{label} is not valid UTF-8") from exc
def _section(text: str, heading: str) -> str:
    lines = text.splitlines()
    start = next((i for i, line in enumerate(lines) if re.match(heading, line)), None)
    if start is None:
        raise GateError(f"source specification is missing section {heading}")
    heading_match = re.match(r"^(#+)", lines[start])
    level = len(heading_match.group(1)) if heading_match else 1
    end = len(lines)
    for i in range(start + 1, len(lines)):
        match = re.match(r"^(#+)\s+", lines[i])
        if match and len(match.group(1)) <= level:
            end = i
            break
    return "\n".join(lines[start:end])
def _split_markdown_row(line: str, label: str) -> list[str]:
    text = line.strip()
    if not text.startswith("|") or not text.endswith("|"):
        raise GateError(f"{label} must start and end with a table delimiter")
    cells: list[str] = []
    current: list[str] = []
    in_code = False
    escaped = False
    for character in text[1:-1]:
        if escaped:
            current.append(character)
            escaped = False
            continue
        if character == "\\":
            current.append(character)
            escaped = True
            continue
        if character == "`":
            in_code = not in_code
            current.append(character)
            continue
        if character == "|" and not in_code:
            cells.append("".join(current).strip())
            current = []
            continue
        current.append(character)
    if escaped:
        raise GateError(f"{label} ends with an incomplete escape")
    cells.append("".join(current).strip())
    if len(cells) != 5 or any(not cell for cell in cells):
        raise GateError(f"{label} must contain exactly 5 non-empty cells")
    if in_code:
        raise GateError(f"{label} contains an unterminated code span")
    return cells

def _normalize_table_cell(value: str) -> str:
    return " ".join(value.strip().split())

def _source_sets(source: str) -> tuple[set[str], set[str], set[str], set[tuple[str, str, str, tuple[str, ...]]], dict[str, tuple[str, str, str, str, str]]]:
    backlog = _section(source, r"^##\s+19(?:[.\s]|$)")
    gate = _section(backlog, r"^###\s+Gate\s+S0\b")
    specs: dict[int, str] = {}
    for line in gate.splitlines():
        match = re.match(r"^\s*(\d+)\.\s+`([^`]+)`", line)
        if match:
            number, slug = int(match.group(1)), match.group(2)
            if number in specs:
                raise GateError(f"source specification repeats backlog number {number}")
            specs[number] = slug
    expected_numbers = list(range(1, len(specs) + 1))
    if not specs or sorted(specs) != expected_numbers or len(set(specs.values())) != len(specs):
        raise GateError("source §19 Gate S0 backlog numbering or slugs are not contiguous and unique")
    blockers_section = _section(source, r"^###\s+2\.2\b")
    table_lines = [line for line in blockers_section.splitlines() if line.strip().startswith("|")]
    if len(table_lines) < 3:
        raise GateError("source §2.2 blocker table is missing header, separator, or rows")
    header = _split_markdown_row(table_lines[0], "source §2.2 blocker header")
    expected_header = ["Blocker ID", "Priority", "Scope / dimensions", "可达风险与证据", "Gate S0 最小修复与锁定测试"]
    if header != expected_header:
        raise GateError("source §2.2 blocker table header drift")
    separator = _split_markdown_row(table_lines[1], "source §2.2 blocker separator")
    if any(re.fullmatch(r":?-{3,}:?", cell) is None for cell in separator):
        raise GateError("source §2.2 blocker separator is malformed")
    blocker_records: dict[str, tuple[str, str, str, str, str]] = {}
    for index, line in enumerate(table_lines[2:], 3):
        cells = _split_markdown_row(line, f"source §2.2 blocker row {index}")
        normalized = tuple(_normalize_table_cell(cell) for cell in cells)
        blocker_id = normalized[0]
        if re.fullmatch(r"B\d+", blocker_id) is None:
            raise GateError(f"source §2.2 blocker row {index} has invalid ID")
        if blocker_id in blocker_records:
            raise GateError(f"source §2.2 repeats blocker {blocker_id}")
        blocker_records[blocker_id] = normalized
    blockers = set(blocker_records)
    if not blockers:
        raise GateError("source §2.2 blocker table is empty")
    source_nodes, source_edges = _proposal_graph(backlog)
    return set(specs.values()), blockers, source_nodes, source_edges, blocker_records
def _header_values(text: str, pattern: str, label: str) -> list[str]:
    values = [match.group(1).strip() for match in re.finditer(pattern, text, flags=re.MULTILINE)]
    if len(values) != 1:
        raise GateError(f"{label} must contain exactly one metadata line")
    return values
def _dependency_values(value: str, label: str) -> set[str]:
    if value.strip() == "none":
        return set()
    values = [entry.strip() for entry in value.split(",")]
    if not values or any(not entry for entry in values):
        raise GateError(f"{label} has an empty dependency entry")
    result = {_id(entry, label) for entry in values}
    if len(result) != len(values):
        raise GateError(f"{label} contains duplicate dependency IDs")
    return result
def _proposal_graph(section: str) -> tuple[set[str], set[tuple[str, str, str, tuple[str, ...]]]]:
    blocks = re.findall(r"```mermaid\s*\n(.*?)```", section, flags=re.DOTALL)
    if len(blocks) != 1 or blocks[0].splitlines()[0].strip() != "flowchart LR":
        raise GateError("source §19 requires exactly one flowchart LR proposal")
    node = r'([A-Z][A-Z0-9_]*)(?:\["[^"\n]*"\])?'
    pattern = re.compile(node + r'\s*(-->|-\.\s*"[^"]+"\s*\.->)\s*' + node)
    nodes, edges = set(), set()
    kinds = {"root requirement contract": "root_requirement", "root compatibility contract": "root_compatibility",
             "per-provider enable join gate": "provider_enable"}
    for line in blocks[0].splitlines()[1:]:
        if not line.strip():
            continue
        match = pattern.fullmatch(line.strip())
        if match is None:
            raise GateError("source Mermaid has unsupported or malformed syntax")
        source_id, arrow, target_id = match.groups()
        kind = "requires" if arrow == "-->" else kinds.get(arrow.split('"')[1])
        if kind is None:
            raise GateError("source Mermaid has unknown typed edge")
        edge = (source_id, target_id, kind, PROVIDERS if kind == "provider_enable" else ())
        if edge in edges:
            raise GateError("source Mermaid has duplicate edge")
        nodes.update((source_id, target_id))
        edges.add(edge)
    if not edges:
        raise GateError("source Mermaid proposal has no edges")
    return nodes, edges

def _gate_metadata(text: str, gate_ids: set[str]) -> dict[str, set[str]]:
    result: dict[str, set[str]] = {}
    for line in text.splitlines():
        match = re.match(r"^\s*Gate-Dependencies:\s*([^\s]+)\s*<-\s*(.*?)\s*$", line)
        if match:
            target = _id(match.group(1), "Gate-Dependencies target")
            if target not in gate_ids:
                raise GateError(f"README Gate-Dependencies names unknown gate {target}")
            if target in result:
                raise GateError(f"README repeats Gate-Dependencies for {target}")
            result[target] = _dependency_values(match.group(2), f"Gate-Dependencies {target}")
    return result


def _matrix_values(text: str, requirement_ids: set[str]) -> dict[str, dict[str, Any]]:
    """Parse the human-readable cross-task matrix index.

    The JSON DAG remains the machine input, but the matrix is a checked
    planning projection.  Keeping these fields in lockstep prevents a
    regenerated map from silently disagreeing with the review-facing record.
    """
    records: dict[str, dict[str, Any]] = {}
    fixture_tokens: set[str] = set()
    sections = re.findall(r"^###\s+(CT_[A-Z0-9_]+)\s+—\s+.*?(?=^###\s+|\Z)", text, flags=re.MULTILINE | re.DOTALL)
    if len(sections) != len(set(sections)):
        raise GateError("cross-task matrix repeats a requirement section")
    if set(sections) != requirement_ids:
        missing = sorted(requirement_ids - set(sections))
        extra = sorted(set(sections) - requirement_ids)
        raise GateError(f"cross-task matrix requirement coverage drift: missing={missing}, extra={extra}")
    for requirement_id in sections:
        match = re.search(
            rf"^###\s+{re.escape(requirement_id)}\s+—\s+(.*?)\n(.*?)(?=^###\s+|\Z)",
            text,
            flags=re.MULTILINE | re.DOTALL,
        )
        if match is None:
            raise GateError(f"cross-task matrix is missing section {requirement_id}")
        title, body = match.groups()

        def line(pattern: str, label: str) -> str:
            label_lines = re.findall(
                rf"^-\s+\*\*{re.escape(label)}:\*\*.*$", body, flags=re.MULTILINE
            )
            if len(label_lines) != 1:
                raise GateError(f"cross-task matrix {requirement_id} requires exactly one {label} line")
            values = re.findall(pattern, body, flags=re.MULTILINE)
            if len(values) != 1 or not values[0].strip():
                raise GateError(f"cross-task matrix {requirement_id} requires exactly one {label} line")
            return values[0].strip()

        capability_matches = re.findall(
            r"^-\s+\*\*Capability / canonical surface:\*\*\s+`([^`]+)`\s+/\s+`([^`]+)`\s*$",
            body,
            flags=re.MULTILINE,
        )
        if len(capability_matches) != 1:
            raise GateError(
                f"cross-task matrix {requirement_id} requires exactly one Capability / canonical surface line"
            )
        capability_surface = capability_matches[0]
        owners_raw = line(r"^-\s+\*\*Owners:\*\*\s+(.+?)\s*$", "Owners")
        dependencies_raw = line(r"^-\s+\*\*Dependencies:\*\*\s+(.+?)\s*$", "Dependencies")
        ancestors_raw = line(r"^-\s+\*\*Ancestor gates:\*\*\s+(.+?)\s*$", "Ancestor gates")
        required_fields = line(r"^-\s+\*\*Required fields:\*\*\s*(.*?)\s*$", "Required fields")
        red_fixtures = line(r"^-\s+\*\*RED fixtures:\*\*\s*(.*?)\s*$", "RED fixtures")
        green_fixtures = line(r"^-\s+\*\*GREEN fixtures:\*\*\s*(.*?)\s*$", "GREEN fixtures")
        guard_evidence = line(r"^-\s+\*\*Guard/evidence refs:\*\*\s*(.*?)\s*$", "Guard/evidence refs")
        status = line(r"^-\s+\*\*Current status / decision:\*\*\s+`([^`]+)`.*$", "Current status / decision")

        red_tokens = _MATRIX_FIXTURE_TOKEN.findall(red_fixtures)
        if not red_tokens:
            raise GateError(f"cross-task matrix {requirement_id} RED fixtures requires at least one named RED- token")
        green_tokens = _MATRIX_FIXTURE_TOKEN.findall(green_fixtures)
        if not green_tokens:
            raise GateError(f"cross-task matrix {requirement_id} GREEN fixtures requires at least one named GREEN- token")
        section_tokens = red_tokens + green_tokens
        if len(section_tokens) != len(set(section_tokens)):
            raise GateError(f"cross-task matrix {requirement_id} repeats a named RED-/GREEN- fixture token")
        duplicate_tokens = fixture_tokens.intersection(section_tokens)
        if duplicate_tokens:
            raise GateError(
                f"cross-task matrix repeats named RED-/GREEN- fixture token {sorted(duplicate_tokens)[0]}"
            )
        fixture_tokens.update(section_tokens)
        guard_paths = re.findall(r"`([^`]+)`", guard_evidence)
        if not guard_paths:
            raise GateError(
                f"cross-task matrix {requirement_id} Guard/evidence refs requires at least one backtick path"
            )

        def ids(raw: str, label: str) -> list[str]:
            values = [value.strip() for value in raw.split(",")]
            if not values or any(not value for value in values):
                raise GateError(f"cross-task matrix {requirement_id} {label} is empty")
            parsed: list[str] = []
            for value in values:
                if not (value.startswith("`") and value.endswith("`") and len(value) > 2):
                    raise GateError(f"cross-task matrix {requirement_id} {label} must use backtick IDs")
                parsed.append(_id(value[1:-1], f"cross-task matrix {requirement_id} {label}"))
            if len(parsed) != len(set(parsed)):
                raise GateError(f"cross-task matrix {requirement_id} {label} contains duplicate IDs")
            return parsed

        records[requirement_id] = {
            "title": title.strip(),
            "capability": capability_surface[0],
            "canonical_surface": capability_surface[1],
            "owner_spec_ids": ids(owners_raw, "Owners"),
            "depends_on": ids(dependencies_raw, "Dependencies"),
            "ancestor_gates": ids(ancestors_raw, "Ancestor gates"),
            "status": status,
            "required_fields": required_fields,
            "red_fixtures": red_fixtures,
            "green_fixtures": green_fixtures,
            "guard_evidence_refs": guard_paths,
        }
    return records


def _validate_spec_completeness(text: str, spec_id: str, dependencies: set[str]) -> None:
    """Check the human-owned S0 completeness contract without authorizing it.

    These lines are an explicit planning index, not an approval record.  The
    body remains subject to human review and the runtime never consumes this
    metadata as an authorization or capability grant.
    """
    values: dict[str, str] = {}
    fenced = False
    metadata_lines: list[tuple[int, str]] = []
    depends_line = next(
        (index for index, line in enumerate(text.splitlines()) if re.match(r"^[ \t]*Depends-On:\s*", line)),
        None,
    )
    for index, line in enumerate(text.splitlines()):
        if line.strip().startswith("```"):
            fenced = not fenced
            continue
        match = re.match(r"^[ \t]*S0-([A-Za-z][A-Za-z0-9-]*):", line)
        if match is None:
            continue
        if fenced:
            raise GateError(f"spec {spec_id} S0-{match.group(1)} completeness field is inside a fenced block")
        if depends_line is None or index <= depends_line:
            raise GateError(f"spec {spec_id} S0-{match.group(1)} must appear after Depends-On")
        metadata_lines.append((index, match.group(1)))
    known_fields = set(S0_COMPLETENESS_FIELDS)
    for _, field_name in metadata_lines:
        if field_name not in known_fields:
            raise GateError(f"spec {spec_id} has unknown S0-{field_name} completeness field")
    for field_name in S0_COMPLETENESS_FIELDS:
        pattern = rf"^[ \t]*S0-{re.escape(field_name)}:\s*(.*?)\s*$"
        matches = [match.group(1).strip() for match in re.finditer(pattern, text, flags=re.MULTILINE)]
        if len(matches) != 1:
            raise GateError(
                f"spec {spec_id} requires exactly one S0-{field_name} completeness line"
            )
        value = matches[0]
        if not value or (field_name != "Dependencies" and value.casefold() in _PLACEHOLDER_VALUES):
            raise GateError(f"spec {spec_id} S0-{field_name} cannot be empty or a placeholder")
        values[field_name] = value
    declared_dependencies = _dependency_values(values["Dependencies"], f"spec {spec_id} S0-Dependencies")
    if declared_dependencies != dependencies:
        raise GateError(f"spec {spec_id} S0-Dependencies does not equal Depends-On")
    acceptance = values["Named-Acceptance"]
    spec_acceptance = re.escape(spec_id)
    if re.search(
        rf"(?<![A-Za-z0-9_])`?(?:Test[A-Za-z0-9_]+|(?:RED|GREEN)-[A-Za-z0-9_-]+|{spec_acceptance}-[PN]-?[0-9]{{2}})`?(?![A-Za-z0-9_])",
        acceptance,
    ) is None:
        raise GateError(
            f"spec {spec_id} S0-Named-Acceptance must name a Test, RED, or GREEN acceptance"
        )
    body_without_index = re.sub(r"^[ \t]*S0-[A-Za-z][A-Za-z0-9-]*:.*$", "", text, flags=re.MULTILINE)
    acceptance_tokens = re.findall(
        r"(?<![A-Za-z0-9_])`?((?:Test[A-Za-z0-9_]+|(?:RED|GREEN)-[A-Za-z0-9_-]+|"
        rf"{spec_acceptance}-[PN]-?[0-9]{{2}}))`?(?![A-Za-z0-9_])",
        acceptance,
    )
    for token in acceptance_tokens:
        spec_token = re.fullmatch(rf"{spec_acceptance}-([PN])-?(\d{{2}})", token)
        token_pattern = (
            rf"{spec_acceptance}-{spec_token.group(1)}-?{spec_token.group(2)}"
            if spec_token
            else re.escape(token)
        )
        if re.search(rf"(?<![A-Za-z0-9_]){token_pattern}(?![A-Za-z0-9_])", body_without_index) is None:
            raise GateError(
                f"spec {spec_id} S0-Named-Acceptance token {token} is not traceable in the document body"
            )


def _validate_metadata(root: Path, graph: dict[str, Any], expected_specs: set[str]) -> None:
    specs = [node for node in graph["nodes"] if node["kind"] == "spec"]
    gates = [node for node in graph["nodes"] if node["kind"] == "gate"]
    basenames = [Path(node["document"]).name for node in specs]
    if len(set(basenames)) != len(basenames) or set(basenames) != {f"{slug}.md" for slug in expected_specs}:
        raise GateError("spec document basenames do not exactly cover the source backlog")
    for node in specs:
        text = _read_text(_inside_file(root, node["document"], "spec document"), "spec document")
        spec_id = _header_values(text, r"^Spec-ID:\s*(.*?)\s*$", "Spec-ID")[0]
        status = _header_values(text, r"^Status:\s*(.*?)\s*$", "Status")[0]
        if spec_id != node["id"] or status != "Draft":
            raise GateError(f"spec document metadata does not match node {node['id']}")
        dependencies = _dependency_values(_header_values(text, r"^Depends-On:\s*(.*?)\s*$", "Depends-On")[0], node["id"])
        incoming = {edge["from"] for edge in graph["edges"] if edge["to"] == node["id"] and edge["kind"] != "provider_enable"}
        if dependencies != incoming:
            raise GateError(f"spec {node['id']} Depends-On does not equal ordinary incoming edges")
        _validate_spec_completeness(text, node["id"], dependencies)
        joins = re.findall(r"^Provider-Join:\s*(.*?)\s*$", text, flags=re.MULTILINE)
        expected_joins = [edge["from"] for edge in graph["edges"] if edge["to"] == node["id"] and edge["kind"] == "provider_enable"]
        if joins != expected_joins:
            raise GateError(f"spec {node['id']} Provider-Join does not match provider_enable edges")
    gate_documents = {node["document"] for node in gates}
    if len(gate_documents) != 1:
        raise GateError("all gate nodes must use one shared README document")
    readme_path = _inside_file(root, next(iter(gate_documents)), "gate README")
    if readme_path.name != "README.md":
        raise GateError("gate nodes must use a shared README.md")
    gate_ids = {node["id"] for node in gates}
    explicit = _gate_metadata(_read_text(readme_path, "gate README"), gate_ids)
    if set(explicit) != gate_ids:
        raise GateError("README Gate-Dependencies must cover every gate")
    for gate_id in gate_ids:
        incoming = {edge["from"] for edge in graph["edges"] if edge["to"] == gate_id and edge["kind"] != "provider_enable"}
        if explicit[gate_id] != incoming:
            raise GateError(f"gate {gate_id} metadata does not equal ordinary incoming edges")

def _document_snapshot_digest(root: Path, graph: dict[str, Any], registry: dict[str, Any]) -> str:
    """Hash every canonical in-root planning document and evidence reference."""
    forbidden = {
        _inside_file(root, REGISTRY_RELATIVE, "registry input"),
        (root / OUTPUT_RELATIVE).resolve(),
    }
    references = [graph["source_spec"]]
    if (root / SPEC_INDEX_RELATIVE).exists():
        references.append(SPEC_INDEX_RELATIVE)
    references.extend(node["document"] for node in graph["nodes"])
    if (root / "docs/work/plans/orch-subagent-upgrade/acceptance-inventory.json").exists():
        references.append("docs/work/plans/orch-subagent-upgrade/acceptance-inventory.json")
    index_path = root / SPEC_INDEX_RELATIVE
    if index_path.exists():
        try:
            index_payload = json.loads(_read_text(index_path, "spec execution index"))
            references.extend(
                command["runner"]
                for spec in index_payload.get("specs", [])
                for command in spec.get("commands", [])
                if isinstance(command, dict) and isinstance(command.get("runner"), str)
            )
        except (ValueError, KeyError, TypeError) as exc:
            raise GateError("spec execution index is invalid while computing snapshot") from exc
    for requirement in graph["cross_task_requirements"]:
        for field_name in ("red_evidence_refs", "green_evidence_refs", "guard_evidence_refs"):
            references.extend(requirement[field_name])
    for blocker in registry["blockers"]:
        for field_name in (
            "evidence_refs",
            "red_evidence_refs",
            "green_evidence_refs",
            "guard_evidence_refs",
        ):
            references.extend(blocker[field_name])
    files: dict[str, str] = {}
    for index, reference in enumerate(references):
        resolved = _inside_file(root, reference, f"document snapshot reference[{index}]")
        if resolved in forbidden:
            continue
        payload = _read_bytes(resolved, f"document snapshot {reference}")
        relative_name = resolved.relative_to(root).as_posix()
        if relative_name == "docs/work/plans/orch-subagent-upgrade/acceptance-inventory.json":
            try:
                parsed = json.loads(payload, object_pairs_hook=_pairs)
                if isinstance(parsed, dict):
                    parsed.pop("document_snapshot_digest", None)
                    payload = canonical_json(parsed).encode("utf-8")
            except (ValueError, _DuplicateKey) as exc:
                raise GateError("acceptance inventory is invalid while computing snapshot") from exc
        try:
            payload.decode("utf-8")
        except UnicodeError as exc:
            raise GateError(f"document snapshot {reference} is not valid UTF-8") from exc
        relative = resolved.relative_to(root).as_posix()
        files[relative] = hashlib.sha256(payload).hexdigest()
    material = [{"path": path, "sha256": files[path]} for path in sorted(files)]
    return hashlib.sha256(canonical_json(material).encode("utf-8")).hexdigest()

def _validate(root: Path) -> dict[str, dict[str, Any]]:
    graph_path = _inside_file(root, GRAPH_RELATIVE, "graph input")
    registry_path = _inside_file(root, REGISTRY_RELATIVE, "registry input")
    graph = _parse_graph(_read_json(graph_path, "graph input"))
    registry = _parse_registry(_read_json(registry_path, "registry input"))
    if graph["source_spec"] != registry["source_spec"]:
        raise GateError("graph.source_spec and registry.source_spec must match")
    source_path = _inside_file(root, graph["source_spec"], "source_spec")
    source = _read_text(source_path, "source_spec")
    node_ids = [node["id"] for node in graph["nodes"]]
    if not node_ids or len(set(node_ids)) != len(node_ids):
        raise GateError("graph nodes must be non-empty and have unique IDs")
    known = set(node_ids)
    edge_keys: set[tuple[Any, ...]] = set()
    incoming: dict[str, set[str]] = {node_id: set() for node_id in known}
    outgoing: dict[str, set[str]] = {node_id: set() for node_id in known}
    for edge in graph["edges"]:
        key = (edge["from"], edge["to"], edge["kind"], tuple(edge["providers"]))
        if key in edge_keys:
            raise GateError("graph edges must be unique")
        edge_keys.add(key)
        if edge["from"] not in known or edge["to"] not in known:
            raise GateError("graph edge references an unknown node")
        if edge["from"] == edge["to"]:
            raise GateError("graph self-edge is forbidden")
        incoming[edge["to"]].add(edge["from"])
        outgoing[edge["from"]].add(edge["to"])
    isolated = sorted(node_id for node_id in known if not incoming[node_id] and not outgoing[node_id])
    if isolated:
        raise GateError(f"graph has isolated node(s): {', '.join(isolated)}")
    visiting: set[str] = set()
    visited: set[str] = set()
    def visit(node_id: str) -> None:
        if node_id in visiting:
            raise GateError("graph contains a cycle")
        if node_id in visited:
            return
        visiting.add(node_id)
        for child in sorted(outgoing[node_id]):
            visit(child)
        visiting.remove(node_id)
        visited.add(node_id)
    for node_id in sorted(known):
        visit(node_id)
    expected_specs, expected_blockers, source_nodes, source_edges, source_blockers = _source_sets(source)
    if known != source_nodes:
        raise GateError("graph nodes do not exactly match the source Mermaid proposal")
    actual_edges = {(edge["from"], edge["to"], edge["kind"], tuple(edge["providers"])) for edge in graph["edges"]}
    if actual_edges != source_edges:
        raise GateError("graph edges/types do not exactly match the source Mermaid proposal")
    blocker_ids = [blocker["id"] for blocker in registry["blockers"]]
    if not blocker_ids or len(set(blocker_ids)) != len(blocker_ids):
        raise GateError("registry blockers must be non-empty and have unique IDs")
    spec_ids = {node["id"] for node in graph["nodes"] if node["kind"] == "spec"}
    gate_ids = {node["id"] for node in graph["nodes"] if node["kind"] == "gate"}
    matrix_path = root / MATRIX_RELATIVE
    if not matrix_path.exists() and not matrix_path.is_symlink():
        raise GateError(f"required cross-task matrix is missing: {MATRIX_RELATIVE}")
    if matrix_path.exists() or matrix_path.is_symlink():
        matrix = _matrix_values(
            _read_text(_inside_file(root, MATRIX_RELATIVE, "cross-task matrix"), "cross-task matrix"),
            {item["id"] for item in graph["cross_task_requirements"]},
        )
        for requirement in graph["cross_task_requirements"]:
            record = matrix[requirement["id"]]
            for field_name in (
                "capability",
                "canonical_surface",
                "owner_spec_ids",
                "depends_on",
                "ancestor_gates",
                "status",
            ):
                expected = requirement[field_name]
                actual = record[field_name]
                if actual != expected:
                    raise GateError(
                        f"cross-task matrix {requirement['id']} {field_name} does not match DAG"
                    )
            if record["title"] != requirement["title"]:
                raise GateError(f"cross-task matrix {requirement['id']} title does not match DAG")
            matrix_forbidden_paths = {
                _inside_file(root, MATRIX_RELATIVE, "cross-task matrix"),
                _inside_file(root, GRAPH_RELATIVE, "graph input"),
                _inside_file(root, REGISTRY_RELATIVE, "registry input"),
                (root / OUTPUT_RELATIVE).resolve(),
            }
            seen_matrix_paths: set[Path] = set()
            for index, reference in enumerate(record["guard_evidence_refs"]):
                resolved = _inside_file(
                    root,
                    reference,
                    f"cross-task matrix {requirement['id']} Guard/evidence refs[{index}]",
                )
                if resolved in seen_matrix_paths:
                    raise GateError(
                        f"cross-task matrix {requirement['id']} Guard/evidence refs contains duplicate path"
                    )
                seen_matrix_paths.add(resolved)
                if resolved in matrix_forbidden_paths:
                    raise GateError(
                        f"cross-task matrix {requirement['id']} Guard/evidence refs cannot self-reference matrix or inputs"
                    )
    for requirement in graph["cross_task_requirements"]:
        if not set(requirement["owner_spec_ids"]).issubset(spec_ids):
            raise GateError(
                f"cross-task requirement {requirement['id']} owner_spec_ids must name existing spec nodes"
            )
        if not set(requirement["depends_on"]).issubset(known):
            raise GateError(
                f"cross-task requirement {requirement['id']} depends_on must name existing graph nodes"
            )
        if not set(requirement["ancestor_gates"]).issubset(gate_ids):
            raise GateError(
                f"cross-task requirement {requirement['id']} ancestor_gates must name existing gate nodes"
            )
        forbidden_evidence_paths = {
            _inside_file(root, REGISTRY_RELATIVE, "registry input"),
            (root / OUTPUT_RELATIVE).resolve(),
            _inside_file(root, GRAPH_RELATIVE, "graph input"),
        }
        for field_name in ("red_evidence_refs", "green_evidence_refs", "guard_evidence_refs"):
            seen_paths: set[Path] = set()
            for index, reference in enumerate(requirement[field_name]):
                resolved = _inside_file(
                    root,
                    reference,
                    f"cross-task requirement {requirement['id']}.{field_name}[{index}]",
                )
                if resolved in seen_paths:
                    raise GateError(
                        f"cross-task requirement {requirement['id']}.{field_name} contains duplicate canonical evidence path"
                    )
                seen_paths.add(resolved)
                if resolved in forbidden_evidence_paths:
                    raise GateError(
                        f"cross-task requirement {requirement['id']}.{field_name} cannot self-reference graph, registry, or generated output"
                    )
    for blocker in registry["blockers"]:
        if not set(blocker["owner_spec_ids"]).issubset(spec_ids):
            raise GateError(f"blocker {blocker['id']} owner_spec_ids must name existing spec nodes")
        forbidden_evidence_paths = {
            _inside_file(root, REGISTRY_RELATIVE, "registry input"),
            (root / OUTPUT_RELATIVE).resolve(),
        }
        for field_name in (
            "evidence_refs",
            "red_evidence_refs",
            "green_evidence_refs",
            "guard_evidence_refs",
        ):
            seen_paths: set[Path] = set()
            for index, reference in enumerate(blocker[field_name]):
                resolved = _inside_file(root, reference, f"blocker {blocker['id']}.{field_name}[{index}]")
                if resolved in seen_paths:
                    raise GateError(
                        f"blocker {blocker['id']}.{field_name} contains duplicate canonical evidence path"
                    )
                seen_paths.add(resolved)
                if resolved in forbidden_evidence_paths:
                    raise GateError(
                        f"blocker {blocker['id']}.{field_name}[{index}] cannot self-reference registry or generated output"
                    )
        source_row = source_blockers.get(blocker["id"])
        if source_row is None:
            raise GateError(f"registry blocker {blocker['id']} is absent from source §2.2")
        registry_row = (
            blocker["id"],
            blocker["priority"],
            blocker["scope"],
            blocker["source_requirement"],
            blocker["closure_requirement"],
        )
        if tuple(_normalize_table_cell(value) for value in registry_row) != source_row:
            raise GateError(f"registry blocker {blocker['id']} does not match source §2.2 fields")
    slots = registry.get("evidence_slots")
    strict_artifacts = not graph["source_spec"].endswith("test-source.md")
    if slots is None and strict_artifacts:
        raise GateError("registry.evidence_slots is required for B01-B18")
    if slots is not None:
        for blocker_id, slot in slots.items():
            blocker_record = next((item for item in registry["blockers"] if item["id"] == blocker_id), None)
            if blocker_record is None:
                raise GateError(f"registry evidence slot references unknown blocker {blocker_id}")
            for evidence_name, ref_field in (("red_evidence", "red_evidence_refs"), ("green_evidence", "green_evidence_refs"), ("guard_evidence", "guard_evidence_refs")):
                if slot[evidence_name]["path"] not in blocker_record[ref_field]:
                    raise GateError(f"registry evidence slot {blocker_id}.{evidence_name} is not bound by blocker refs")
            for field_name in ("reproduction_ref", "closure_ref"):
                _inside_file(root, slot[field_name], f"registry evidence slot {blocker_id}.{field_name}")
            reproduction = _read_text(
                _inside_file(root, slot["reproduction_ref"], f"registry evidence slot {blocker_id}.reproduction_ref"),
                f"registry evidence slot {blocker_id}.reproduction_ref",
            )
            closure = _read_text(
                _inside_file(root, slot["closure_ref"], f"registry evidence slot {blocker_id}.closure_ref"),
                f"registry evidence slot {blocker_id}.closure_ref",
            )
            if slot["reproduction_anchor"] not in reproduction:
                raise GateError(f"registry evidence slot {blocker_id}.reproduction_anchor is absent from reproduction_ref")
            if slot["closure_anchor"] not in closure:
                raise GateError(f"registry evidence slot {blocker_id}.closure_anchor is absent from closure_ref")
            for evidence_name in ("red_evidence", "green_evidence", "guard_evidence"):
                evidence = slot[evidence_name]
                evidence_path = _inside_file(root, evidence["path"], f"registry evidence slot {blocker_id}.{evidence_name}.path")
                evidence_text = _read_text(evidence_path, f"registry evidence slot {blocker_id}.{evidence_name}.path")
                if evidence["symbol"] not in evidence_text or not re.search(
                    rf"(?<![A-Za-z0-9_]){re.escape(evidence['symbol'])}(?![A-Za-z0-9_])",
                    evidence_text.splitlines()[evidence["line"] - 1] if evidence["line"] <= len(evidence_text.splitlines()) else "",
                ):
                    raise GateError(f"registry evidence slot {blocker_id}.{evidence_name} symbol/line is absent")
                digest = hashlib.sha256(evidence_path.read_bytes()).hexdigest()
                if digest != evidence["sha256"]:
                    raise GateError(f"registry evidence slot {blocker_id}.{evidence_name}.sha256 does not match")
    _validate_metadata(root, graph, expected_specs)
    spec_execution_index = _validate_spec_execution_index(root, graph, registry, mandatory=strict_artifacts)
    if set(blocker_ids) != expected_blockers:
        raise GateError("registry blocker IDs do not exactly cover the dynamic §2.2 table")
    return {
        "graph": graph,
        "registry": registry,
        "document_snapshot_digest": _document_snapshot_digest(root, graph, registry),
        "spec_execution_index": spec_execution_index,
    }
def validate_inputs(root: str | Path) -> dict[str, dict[str, Any]]:
    """Load and validate both fixed inputs beneath an absolute workspace root."""
    return _validate(_root_path(root))
def _md(value: Any) -> str:
    return str(value).replace("|", "\\|").replace("\n", " ").strip()
def render_dependency_map(validated: Mapping[str, Mapping[str, Any]]) -> str:
    """Render a stable projection while retaining every validated JSON field."""
    graph = validated["graph"]
    registry = validated["registry"]
    spec_execution_index = validated.get("spec_execution_index")
    graph_projection = dict(graph, nodes=sorted(graph["nodes"], key=lambda node: node["id"]))
    graph_projection["edges"] = sorted(graph_projection["edges"], key=lambda edge: (edge["from"], edge["to"], edge["kind"], edge["providers"]))
    registry_projection = dict(registry, blockers=sorted(registry["blockers"], key=lambda blocker: blocker["id"]))
    lines = [
        "# S0 dependency map (planning-only)",
        "",
        "> Offline planning skeleton only. This artifact is non-authorizing: it never grants approval, GO, release, enablement, or runtime execution permission.",
        "> The registry `baseline_commit` identifies the historical source-spec snapshot; it is distinct from the active worktree HEAD used for local revalidation.",
        "",
        "## Derived counts",
        "",
        "| Measure | Count |",
        "| --- | ---: |",
        f"| Nodes | {len(graph['nodes'])} |",
        f"| Spec nodes | {sum(node['kind'] == 'spec' for node in graph['nodes'])} |",
        f"| Gate nodes | {sum(node['kind'] == 'gate' for node in graph['nodes'])} |",
        f"| Edges | {len(graph['edges'])} |",
        f"| Blockers | {len(registry['blockers'])} |",
        f"| Cross-task requirements | {len(graph['cross_task_requirements'])} |",
        f"| Cross-task RED planned refs | {sum(len(item['red_evidence_refs']) for item in graph['cross_task_requirements'])} |",
        f"| Cross-task GREEN planned refs | {sum(len(item['green_evidence_refs']) for item in graph['cross_task_requirements'])} |",
        f"| Cross-task Guard planned refs | {sum(len(item['guard_evidence_refs']) for item in graph['cross_task_requirements'])} |",
        f"| RED evidence entries | {sum(len(blocker['red_evidence_refs']) for blocker in registry['blockers'])} |",
        f"| GREEN evidence entries | {sum(len(blocker['green_evidence_refs']) for blocker in registry['blockers'])} |",
        f"| Guard evidence entries | {sum(len(blocker['guard_evidence_refs']) for blocker in registry['blockers'])} |",
        "",
        "> RED/GREEN/Guard counts are planned or observed workspace references only; they are non-authoritative and do not indicate blocker closure or release approval.",
        "",
        "## Child spec execution index (planning-only)",
        "",
        "> Owner, prerequisites, ancestor gates, entry/exit criteria, commands, and blocker IDs are review metadata only. Command output remains LOCAL_NON_AUTHORITATIVE/NOT_VERIFIED; no row grants runtime or release permission.",
        "",
        "| Spec | Owner | Prerequisites | Ancestor gates | Evidence classification | Blockers |",
        "| --- | --- | --- | --- | --- | --- |",
        "",
        "## Cross-task requirements (planning-only)",
        "",
        f"> These {len(graph['cross_task_requirements'])} rows define acceptance planning for cross-task delivery. They do not register tools, grant capability, or enable runtime. `legacy send_message` is explicitly not a canonical surface; canonical rows use only `unified_v2` or `room`.",
        "",
        "| ID | Capability | Canonical surface | Owners | Depends on | Ancestor gates | Status | RED refs | GREEN refs | Guard refs |",
        "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |",
    ]
    if spec_execution_index is not None:
        insert_at = lines.index("## Cross-task requirements (planning-only)")
        if insert_at > 0 and lines[insert_at - 1] == "":
            lines.pop(insert_at - 1)
            insert_at -= 1
        rows = [
            "| " + " | ".join((
                _md(item["spec_id"]), _md(item["owner"]),
                _md(", ".join(item["prerequisite_ids"]) or "none"),
                _md(", ".join(item["ancestor_gate_ids"]) or "none"),
                _md(item["evidence_classification"]),
                _md(", ".join(item["blocker_ids"]) or "none"),
            )) + " |"
            for item in sorted(spec_execution_index["specs"], key=lambda item: item["spec_id"])
        ]
        lines[insert_at:insert_at] = rows
    for requirement in sorted(graph["cross_task_requirements"], key=lambda item: item["id"]):
        lines.append(
            "| " + " | ".join(
                (
                    _md(requirement["id"]),
                    _md(requirement["capability"]),
                    _md(requirement["canonical_surface"]),
                    _md(", ".join(requirement["owner_spec_ids"])),
                    _md(", ".join(requirement["depends_on"])),
                    _md(", ".join(requirement["ancestor_gates"])),
                    _md(requirement["status"]),
                    _md(", ".join(requirement["red_evidence_refs"])),
                    _md(", ".join(requirement["green_evidence_refs"])),
                    _md(", ".join(requirement["guard_evidence_refs"])),
                )
            )
            + " |"
        )
    lines.extend([
        "",
        "## Dependency graph",
        "",
        "```mermaid",
        "flowchart LR",
    ])
    for node in graph_projection["nodes"]:
        label = f"{node['id']} — {node['title']} ({node['kind']}, {node['status']})".replace('"', '\\"')
        lines.append(f"    {node['id']}[\"{label}\"]")
    for edge in graph_projection["edges"]:
        label = edge["kind"]
        if edge["providers"]:
            label += ":" + ",".join(edge["providers"])
        lines.append(f"    {edge['from']} -->|{label}| {edge['to']}")
    lines.extend(["```", "", "## Current blockers (planning registry)", "",
                  "| ID | Title | Priority | Scope | Status | Source requirement | Closure requirement | Owners | Evidence refs | RED entries | GREEN entries | Guard entries |",
                  "| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |"])
    for blocker in registry_projection["blockers"]:
        lines.append("| " + " | ".join((_md(blocker["id"]), _md(blocker["title"]), _md(blocker["priority"]),
                                          _md(blocker["scope"]), _md(blocker["status"]), _md(blocker["source_requirement"]),
                                          _md(blocker["closure_requirement"]), _md(", ".join(blocker["owner_spec_ids"])),
                                          _md(", ".join(blocker["evidence_refs"])), _md(", ".join(blocker["red_evidence_refs"])),
                                          _md(", ".join(blocker["green_evidence_refs"])), _md(", ".join(blocker["guard_evidence_refs"])))) + " |")
    graph_hash = hashlib.sha256(canonical_json(graph).encode("utf-8")).hexdigest()
    registry_hash = hashlib.sha256(canonical_json(registry).encode("utf-8")).hexdigest()
    lines.extend([
        "", "## Input digests", "",
        "The following are this tool's sorted-key compact UTF-8 canonical-JSON SHA-256 values; this is explicitly not JCS and not a signature.",
        "", f"- `{GRAPH_RELATIVE}` canonical-JSON SHA-256: `{graph_hash}`",
        f"- `{REGISTRY_RELATIVE}` canonical-JSON SHA-256: `{registry_hash}`",
        f"- canonical in-root document/evidence snapshot SHA-256: `{validated['document_snapshot_digest']}`",
        "", "## Full validated input projection", "",
        "The appendices preserve every field from the validated planning inputs; their status remains planning-only and non-authorizing.",
        "", "### Graph JSON", "", "```json", json.dumps(graph_projection, ensure_ascii=False, sort_keys=True, indent=2),
        "```", "", "### Registry JSON", "", "```json", json.dumps(registry_projection, ensure_ascii=False, sort_keys=True, indent=2),
        "```",
    ])
    if spec_execution_index is not None:
        lines.extend([
            "", "### Spec execution index JSON", "", "```json",
            json.dumps(spec_execution_index, ensure_ascii=False, sort_keys=True, indent=2),
            "```",
        ])
    lines.extend([
        "", "> Repeated notice: this generated document is a planning projection, not an approval or runtime authorization.", "",
    ])
    return "\n".join(lines)
def run(root: str | Path, mode: str) -> None:
    root_path = _root_path(root)
    validated = validate_inputs(root_path)
    if mode == "check":
        output = _inside_file(root_path, OUTPUT_RELATIVE, "generated output")
        actual = _read_text(output, "generated output")
        expected = render_dependency_map(validated)
        if actual != expected:
            raise GateError(f"generated output drift: {OUTPUT_RELATIVE}")
        return
    if mode != "write":
        raise GateError("mode must be write or check")
    destination = _inside_file(root_path, GRAPH_RELATIVE, "graph input").parent / Path(OUTPUT_RELATIVE).name
    payload = render_dependency_map(validated).encode("utf-8")
    if len(payload) > MAX_FILE_BYTES:
        raise GateError("generated output exceeds bounded file limit")
    temporary: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(mode="wb", dir=destination.parent, prefix=f".{destination.name}.", delete=False) as handle:
            temporary = Path(handle.name)
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, destination)
    except OSError as exc:
        raise GateError(f"write generated output {OUTPUT_RELATIVE}: {exc}") from exc
    finally:
        if temporary is not None:
            temporary.unlink(missing_ok=True)
def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", required=True, help="absolute workspace root")
    modes = parser.add_mutually_exclusive_group(required=True)
    modes.add_argument("--write", action="store_true", help="explicitly write the generated projection")
    modes.add_argument("--check", action="store_true", help="check the existing projection without writing")
    args = parser.parse_args(argv)
    mode = "write" if args.write else "check"
    try:
        run(args.root, mode)
    except GateError as exc:
        print(f"orch_upgrade_gate: {exc}", file=sys.stderr)
        return 1
    print(f"OK {mode} {OUTPUT_RELATIVE}")
    return 0
if __name__ == "__main__":
    raise SystemExit(main())
