#!/usr/bin/env python3
"""Fail-closed state and trajectory guard for a cross-file five-round runner.

This module is deliberately independent from benchmark prompts, diagnostics
implementations, and grader write sets.  A runner owns the actual model and
grader calls; it must call :meth:`ClosedLoopGuard.evaluate_round` before a
grader and may invoke the grader only through :meth:`ClosedLoopGuard.run_grader`.
The guard persists the arm/session/thread identity and an append-only audit
record through :class:`JsonStateStore`.

The integration seam is intentionally small::

    guard = ClosedLoopGuard.from_store(state_path)
    decision = guard.evaluate_round(round_input)
    # No grader call is allowed unless this returns grader_allowed=True.
    result = guard.run_grader(decision, grader_callable)

    # On the next round the runner supplies the same arm/session/thread IDs and
    # action="resume".  A new session is never silently accepted as repair.
"""

import argparse
import dataclasses
import enum
import hashlib
import json
import os
import pathlib
import tempfile
from collections.abc import Callable, Iterable, Mapping, Sequence
from typing import Any

try:
    from scripts.cross_file_lsp_receipts import (
        ReceiptError,
        ReceiptChainError,
        validate_lsp_receipts,
        validate_no_lsp_isolation,
        validate_receipts,
    )
except ImportError:  # pragma: no cover - direct module execution fallback.
    from cross_file_lsp_receipts import (  # type: ignore[no-redef]
        ReceiptError,
        ReceiptChainError,
        validate_lsp_receipts,
        validate_no_lsp_isolation,
        validate_receipts,
    )


MAX_ROUNDS = 3
DEFAULT_NO_PROGRESS_LIMIT = 2
MAX_EVIDENCE_RECOVERY_ATTEMPTS = 1
STATE_SCHEMA_VERSION = 2


class GuardError(RuntimeError):
    """Base class for all fail-closed guard errors."""


class ContractFailure(GuardError):
    """The runner supplied evidence that violates a closed-loop contract."""


class SessionIdentityError(ContractFailure):
    """The arm/session/thread identity is missing, changed, or not resumed."""


class RoundSequenceError(ContractFailure):
    """The caller skipped, repeated, or exceeded a round."""


class StateDecodeError(ContractFailure):
    """A persisted state document is not a supported, trustworthy state."""


class GraderBlocked(ContractFailure):
    """A caller attempted to run a grader for a blocked round."""


class RunMode(str, enum.Enum):
    """Whether the arm is allowed to use MCP/LSP tools."""

    NO_LSP = "no_lsp"
    WITH_LSP = "with_lsp"


class FailureKind(str, enum.Enum):
    """Failure channels that must never be conflated."""

    INFRASTRUCTURE = "infrastructure"
    MODEL_QUALITY = "model_quality"


class RoundDecision(str, enum.Enum):
    """Auditable result of the pre-grader round gate."""

    GRADER_ALLOWED = "grader_allowed"
    EVIDENCE_RECOVERY_REQUIRED = "evidence_recovery_required"
    NO_PROGRESS = "no_progress"
    CONTRACT_FAILURE = "contract_failure"
    INFRASTRUCTURE_FAILURE = "infrastructure_failure"
    TERMINAL = "terminal"


class RunnerStatus(str, enum.Enum):
    ACTIVE = "active"
    SUCCEEDED = "succeeded"
    UNVERIFIED = "unverified"
    EVIDENCE_RECOVERY_REQUIRED = "evidence_recovery_required"
    RELOCALIZE_REQUIRED = "relocalize_required"
    CONTRACT_FAILURE = "contract_failure"
    MAX_ROUNDS_REACHED = "max_rounds_reached"
    INFRASTRUCTURE_FAILURE = "infrastructure_failure"


_RUNNER_STATUSES = {item.value for item in RunnerStatus}


def _required_text(value: Any, label: str) -> str:
    text = str(value or "").strip()
    if not text:
        raise ContractFailure(f"{label} is required")
    return text


def _normalise_path(value: Any, label: str = "path") -> str:
    text = _required_text(value, label).replace("\\", "/")
    if text.startswith("file://"):
        text = text[7:]
    while text.startswith("./"):
        text = text[2:]
    if text.endswith("/"):
        text = text.rstrip("/")
    if not text:
        raise ContractFailure(f"{label} is required")
    return pathlib.PurePosixPath(text).as_posix()


def compute_patch_hash(patch: str | bytes) -> str:
    """Return a stable SHA-256 digest for the exact patch bytes/text."""

    if isinstance(patch, str):
        data = patch.encode("utf-8")
    elif isinstance(patch, bytes):
        data = patch
    else:
        raise ContractFailure("patch must be str or bytes")
    return hashlib.sha256(data).hexdigest()


@dataclasses.dataclass(frozen=True)
class FileChange:
    """One changed production file and, when needed, its exemption class."""

    path: str
    production: bool = True
    exemption_kind: str | None = None
    language: str = ""

    def __post_init__(self) -> None:
        object.__setattr__(self, "path", _normalise_path(self.path))
        kind = (self.exemption_kind or "").strip().lower() or None
        if kind not in (None, "generated", "unsupported_language"):
            raise ContractFailure(f"unsupported exemption kind for {self.path}: {kind}")
        object.__setattr__(self, "exemption_kind", kind)
        object.__setattr__(self, "language", str(self.language or "").strip())


@dataclasses.dataclass(frozen=True)
class Exemption:
    """A scoped, justified diagnostics exemption for one changed file."""

    path: str
    kind: str
    reason: str
    language: str = ""
    active_round: int | None = None

    def __post_init__(self) -> None:
        object.__setattr__(self, "path", _normalise_path(self.path, "exemption path"))
        kind = _required_text(self.kind, "exemption kind").lower()
        if kind not in ("generated", "unsupported_language"):
            raise ContractFailure(f"unsupported exemption kind: {kind}")
        object.__setattr__(self, "kind", kind)
        reason = _required_text(self.reason, f"exemption reason for {self.path}")
        object.__setattr__(self, "reason", reason)
        object.__setattr__(self, "language", str(self.language or "").strip())
        if self.active_round is not None and self.active_round < 1:
            raise ContractFailure(f"invalid exemption active_round for {self.path}")


def _coerce_file_change(value: FileChange | Mapping[str, Any] | str) -> FileChange:
    if isinstance(value, FileChange):
        return value
    if isinstance(value, str):
        return FileChange(value)
    if not isinstance(value, Mapping):
        raise ContractFailure(f"invalid production file change: {value!r}")
    path = value.get("path", value.get("file", value.get("file_path")))
    kind = value.get("exemption_kind", value.get("exemption"))
    declared_kind = str(value.get("kind", "")).strip().lower()
    if declared_kind in ("generated", "unsupported_language"):
        kind = declared_kind if not kind else kind
    if value.get("generated") is True:
        kind = "generated"
    if value.get("unsupported_language") is True or value.get("language_supported") is False:
        kind = "unsupported_language"
    return FileChange(
        path=path,
        production=bool(value.get("production", True)),
        exemption_kind=kind,
        language=str(value.get("language", "") or ""),
    )


def _coerce_exemption(value: Exemption | Mapping[str, Any]) -> Exemption:
    if isinstance(value, Exemption):
        return value
    if not isinstance(value, Mapping):
        raise ContractFailure(f"invalid exemption registry entry: {value!r}")
    active_round = value.get("active_round")
    if active_round is not None:
        try:
            active_round = int(active_round)
        except (TypeError, ValueError) as exc:
            raise ContractFailure("invalid exemption active_round") from exc
    return Exemption(
        path=value.get("path", value.get("file", value.get("file_path"))),
        kind=value.get("kind", value.get("exemption_kind", "")),
        reason=value.get("reason", ""),
        language=value.get("language", ""),
        active_round=active_round,
    )


def validate_exemption_registry(
    changed_files: Iterable[FileChange | Mapping[str, Any] | str],
    exemptions: Iterable[Exemption | Mapping[str, Any]] = (),
    *,
    round_number: int,
) -> tuple[FileChange, ...]:
    """Validate generated/unsupported-language exemptions fail-closed.

    A changed file explicitly marked ``generated`` or
    ``unsupported_language`` needs exactly one current registry entry with a
    non-empty reason.  Entries for files not changed in this round are stale;
    entries whose kind does not match are also rejected.  This prevents a
    copied exemption from silently widening diagnostics coverage.
    """

    changes = tuple(_coerce_file_change(item) for item in changed_files)
    production = tuple(item for item in changes if item.production)
    registry = tuple(_coerce_exemption(item) for item in exemptions)
    by_path: dict[str, Exemption] = {}
    for entry in registry:
        if entry.path in by_path:
            raise ContractFailure(f"duplicate diagnostics exemption for {entry.path}")
        if entry.active_round is not None and entry.active_round != round_number:
            raise ContractFailure(f"stale diagnostics exemption for {entry.path}")
        by_path[entry.path] = entry

    production_paths = {item.path for item in production}
    stale = sorted(path for path in by_path if path not in production_paths)
    if stale:
        raise ContractFailure("stale diagnostics exemptions: " + ", ".join(stale))

    effective_changes: list[FileChange] = []
    for change in changes:
        entry = by_path.get(change.path)
        if change.exemption_kind is None:
            if entry is not None:
                # The registry is the canonical classification for callers
                # that only have a changed path.  This keeps the exemption
                # boundary usable for generated artifacts discovered by Git.
                change = dataclasses.replace(change, exemption_kind=entry.kind)
            effective_changes.append(change)
            continue
        if entry is None:
            if change.production:
                raise ContractFailure(f"missing diagnostics exemption for {change.path}")
            effective_changes.append(change)
            continue
        if entry.kind != change.exemption_kind:
            raise ContractFailure(
                f"diagnostics exemption kind mismatch for {change.path}: "
                f"file={change.exemption_kind}, registry={entry.kind}"
            )
        # Exemption.__post_init__ rejects an empty reason.  Keep this explicit
        # for callers supplying a custom Exemption-like mapping in the future.
        if not entry.reason.strip():
            raise ContractFailure(f"empty diagnostics exemption reason for {change.path}")
        effective_changes.append(change)
    return tuple(effective_changes)


def _mapping_value(event: Mapping[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in event:
            return event[key]
    return None


@dataclasses.dataclass(frozen=True)
class TrajectoryEvent:
    """Typed convenience wrapper accepted by the trajectory parser."""

    tool: str
    arguments: Mapping[str, Any] = dataclasses.field(default_factory=dict)
    result: Any = None
    source: str = "mcp"
    event_type: str = "mcp_tool_call"

    def as_dict(self) -> dict[str, Any]:
        return dataclasses.asdict(self)


def _event_mapping(event: Any) -> Mapping[str, Any] | None:
    if isinstance(event, Mapping):
        item = event.get("item")
        if event.get("type") == "item.completed" and isinstance(item, Mapping):
            return item
        return event
    if isinstance(event, TrajectoryEvent):
        return event.as_dict()
    return None


def _event_tool_name(event: Mapping[str, Any]) -> str:
    nested = event.get("tool_call")
    if isinstance(nested, Mapping):
        value = _mapping_value(nested, "tool", "tool_name", "name", "method")
        if value:
            return str(value).strip().lower()
    value = _mapping_value(event, "tool", "tool_name", "name", "method")
    return str(value or "").strip().lower()


def _is_mcp_event(event: Any) -> bool:
    event = _event_mapping(event)
    if event is None:
        return False
    source = str(_mapping_value(event, "source", "origin", "producer") or "").strip().lower()
    if (
        source == "mcp"
        or source.startswith("mcp/")
        or source.startswith("mcp-")
        or source.startswith("mcp_")
    ):
        return True
    event_type = str(_mapping_value(event, "type", "event_type", "kind") or "").strip().lower()
    if "mcp" in event_type:
        return True
    tool = _event_tool_name(event)
    if tool.startswith("mcp") or ".diagnostics" in tool or tool.endswith("diagnostics"):
        return True
    return tool in {"structure", "xref", "grep", "inspect", "file_search", "diagnostics"}


def _is_diagnostics_event(event: Mapping[str, Any]) -> bool:
    if not _is_mcp_event(event):
        return False
    tool = _event_tool_name(event).replace("/", ".")
    return tool == "diagnostics" or tool.endswith(".diagnostics") or tool.endswith("_diagnostics")


def _normalise_lsp_tool_name(event: Mapping[str, Any]) -> str:
    """Return the terminal LSP tool name used by an MCP trajectory event."""

    tool = _event_tool_name(event).strip().lower().replace("/", ".")
    tool = tool.replace("__", ".")
    while ".." in tool:
        tool = tool.replace("..", ".")
    return tool.rsplit(".", 1)[-1]


_LSP_EVIDENCE_TOOLS = frozenset({"structure", "xref", "diagnostics"})


def _is_lsp_evidence_event(event: Mapping[str, Any]) -> bool:
    """Whether an event is one of the three permitted recovery tools."""

    return _is_mcp_event(event) and _normalise_lsp_tool_name(event) in _LSP_EVIDENCE_TOOLS


def _validate_mcp_tool_events(trajectory: Iterable[Any]) -> None:
    """Reject MCP events that are outside the exposed LSP tool contract.

    With-LSP does not require the Agent to call a tool.  When it does emit an
    MCP event, however, the event must still describe one of the three tools
    exposed by the MCP tools gate.  Keeping this check separate from coverage
    means optional diagnostics can be audited without silently accepting a
    second, ungoverned MCP surface.
    """

    for event in mcp_trajectory_events(trajectory):
        tool = _normalise_lsp_tool_name(event)
        if tool not in _LSP_EVIDENCE_TOOLS:
            raise ContractFailure(
                "With-LSP trajectory contains unsupported MCP tool: "
                + (_event_tool_name(event) or "unknown_tool")
            )


def _trajectory_non_lsp_tools(trajectory: Iterable[Any]) -> tuple[str, ...]:
    """Find tool events that are not allowed during evidence-only recovery.

    Non-tool shell/progress events may remain in a captured envelope, but any
    tool call in a recovery trajectory must be an MCP structure/xref/diagnostics
    call.  This prevents a recovery request from smuggling a code-agent or
    grader action into the evidence-only state transition.
    """

    invalid: set[str] = set()
    for raw_event in trajectory:
        mapping = _event_mapping(raw_event)
        if mapping is None:
            continue
        has_tool_shape = any(
            key in mapping for key in ("tool", "tool_name", "name", "method", "tool_call")
        )
        if has_tool_shape and not _is_lsp_evidence_event(mapping):
            invalid.add(_event_tool_name(mapping) or "unknown_tool")
    return tuple(sorted(invalid))


_PATH_KEYS = {
    "file",
    "filepath",
    "file_path",
    "path",
    "uri",
    "files",
    "filepaths",
    "file_paths",
    "changed_files",
    "targets",
}
_NESTED_TRACE_KEYS = {
    "arguments",
    "params",
    "input",
    "request",
    "result",
    "output",
    "rows",
    "data",
    "items",
    "tool_call",
}


def _append_path_value(value: Any, paths: set[str]) -> None:
    if isinstance(value, str):
        try:
            paths.add(_normalise_path(value))
        except ContractFailure:
            return
    elif isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
        for item in value:
            _append_path_value(item, paths)
    elif isinstance(value, Mapping):
        for item in value.values():
            _append_path_value(item, paths)


def _diagnostics_paths(event: Mapping[str, Any]) -> set[str]:
    paths: set[str] = set()

    def visit(value: Any, key: str = "") -> None:
        if isinstance(value, Mapping):
            for child_key, child in value.items():
                normal = str(child_key).replace("-", "_").lower()
                if normal in _PATH_KEYS:
                    _append_path_value(child, paths)
                elif normal in _NESTED_TRACE_KEYS:
                    visit(child, normal)
        elif isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
            for child in value:
                visit(child, key)

    visit(event)
    return paths


def mcp_trajectory_events(trajectory: Iterable[Any]) -> tuple[Mapping[str, Any], ...]:
    """Return the MCP events in a trajectory without trusting caller claims."""

    events: list[Mapping[str, Any]] = []
    for event in trajectory:
        mapping = _event_mapping(event)
        if mapping is not None and _is_mcp_event(mapping):
            events.append(mapping)
    return tuple(events)


def validate_diagnostics_coverage(
    changed_files: Iterable[FileChange | Mapping[str, Any] | str],
    mode: RunMode | str,
    trajectory: Iterable[Any],
    exemptions: Iterable[Exemption | Mapping[str, Any]] = (),
    *,
    round_number: int,
) -> tuple[str, ...]:
    """Audit diagnostics evidence directly from MCP trajectory events.

    ``no_lsp`` remains strict: every MCP event is a contract violation.  In
    ``with_lsp`` mode localization calls remain optional, but changed supported
    production files require completion evidence before the grader may start.
    This function returns the observed diagnostics path set; ``evaluate_round``
    turns any missing completion scope into the bounded evidence-recovery gate.
    """

    mode_value = mode.value if isinstance(mode, RunMode) else str(mode).strip().lower()
    mode_value = {
        "no-lsp": RunMode.NO_LSP.value,
        "nolsp": RunMode.NO_LSP.value,
        "with-lsp": RunMode.WITH_LSP.value,
        "lsp": RunMode.WITH_LSP.value,
    }.get(mode_value, mode_value)
    if mode_value not in (RunMode.NO_LSP.value, RunMode.WITH_LSP.value):
        raise ContractFailure(f"unsupported run mode: {mode}")
    changes = validate_exemption_registry(changed_files, exemptions, round_number=round_number)
    trajectory_values = tuple(trajectory)
    mcp_events = mcp_trajectory_events(trajectory_values)
    if mode_value == RunMode.NO_LSP.value:
        if mcp_events:
            raise ContractFailure("No-LSP arm must emit zero MCP events")
        return ()

    _validate_mcp_tool_events(trajectory_values)
    covered: set[str] = set()
    for event in mcp_events:
        if _is_diagnostics_event(event):
            covered.update(_diagnostics_paths(event))
    return tuple(sorted(covered))


def _diagnostics_coverage_observation(
    changes: Sequence[FileChange],
    mode: RunMode | str,
    trajectory: Iterable[Any],
    exemptions: Iterable[Exemption | Mapping[str, Any]] = (),
    *,
    round_number: int,
) -> tuple[tuple[str, ...], tuple[str, ...], tuple[str, ...]]:
    """Return observed coverage, uncovered paths, and hard contract errors.

    ``missing`` is authoritative completion scope for With-LSP quality rounds.
    It is audit-only for No-LSP, which intentionally has no MCP capability.
    """

    try:
        covered = validate_diagnostics_coverage(
            changes,
            mode,
            trajectory,
            exemptions,
            round_number=round_number,
        )
        effective_changes = validate_exemption_registry(
            changes, exemptions, round_number=round_number
        )
        required = {
            change.path
            for change in effective_changes
            if change.production and change.exemption_kind is None
        }
        aligned = _align_diagnostics_paths(covered, required)
        return aligned, tuple(sorted(required - set(aligned))), ()
    except ContractFailure as exc:
        # Missing diagnostics is now represented by ``missing`` only when the
        # observation itself succeeded.  Every other error (including an
        # unsupported MCP tool or an exemption-contract violation) remains a
        # hard fail-closed contract error.
        return (), (), (str(exc),)


def _align_diagnostics_paths(
    covered: Iterable[str], required: Iterable[str]
) -> tuple[str, ...]:
    """Map absolute worktree paths to one unambiguous required repo path."""

    required_set = {_normalise_path(path) for path in required}
    aligned: set[str] = set()
    for raw in covered:
        path = _normalise_path(raw)
        matches = {
            candidate
            for candidate in required_set
            if path == candidate or path.endswith("/" + candidate)
        }
        aligned.add(next(iter(matches)) if len(matches) == 1 else path)
    return tuple(sorted(aligned))


def _legacy_semantic_completion_errors(
    changes: Sequence[FileChange],
    mode: RunMode | str,
    trajectory: Iterable[Any],
    exemptions: Iterable[Exemption | Mapping[str, Any]],
    *,
    round_number: int,
) -> tuple[str, ...]:
    """Fail closed when the legacy trajectory lacks Cross-File semantic closure.

    Production Ubuntu runs use signed canonical receipts.  The trajectory seam
    remains for tests and migration callers, but it must not offer a weaker
    acceptance path: any supported production change in this Cross-File suite
    requires observed structure and xref calls in addition to diagnostics.
    """

    mode_value = mode.value if isinstance(mode, RunMode) else str(mode).strip().lower()
    mode_value = mode_value.replace("-", "_")
    if mode_value not in (RunMode.WITH_LSP.value, "lsp"):
        return ()
    try:
        effective = validate_exemption_registry(changes, exemptions, round_number=round_number)
    except ContractFailure:
        # The diagnostics/exemption observation reports this contract error.
        # Do not mask it by raising from the secondary semantic audit.
        return ()
    required = tuple(
        item.path
        for item in effective
        if item.production and item.exemption_kind is None
    )
    if not required:
        return ()
    tools = {
        str(_event_tool_name(event) or "").strip().lower().split(".")[-1]
        for event in mcp_trajectory_events(trajectory)
    }
    missing = [tool for tool in ("structure", "xref") if tool not in tools]
    if not missing:
        return ()
    return (
        "legacy With-LSP completion evidence missing semantic tool(s): "
        + ", ".join(missing),
    )


def _lsp_self_check(
    changes: Sequence[FileChange],
    trajectory: Iterable[Any],
    covered: Iterable[str],
    exemptions: Iterable[Exemption | Mapping[str, Any]] = (),
    *,
    round_number: int,
) -> dict[str, Any]:
    """Build the compact, non-authoritative LSP audit emitted by a round.

    The self-check intentionally records observations rather than declaring a
    result.  Evidence tokens are paths/positions only; raw MCP payloads are
    not copied into durable state and therefore cannot smuggle secrets or
    unbounded output into the audit trail.
    """

    try:
        effective = validate_exemption_registry(
            changes, exemptions, round_number=round_number
        )
    except ContractFailure:
        effective = tuple(changes)
    production = tuple(sorted({item.path for item in effective if item.production}))
    exempt = tuple(
        sorted(
            {
                item.path
                for item in effective
                if item.production and item.exemption_kind is not None
            }
        )
    )
    structure: set[str] = set()
    xref: set[str] = set()
    for raw_event in trajectory:
        event = _event_mapping(raw_event)
        if event is None or not _is_mcp_event(event):
            continue
        tool = _normalise_lsp_tool_name(event)
        if tool == "structure":
            paths = _diagnostics_paths(event)
            structure.update(paths or {"structure"})
        elif tool == "xref":
            arguments = event.get("arguments", event.get("params", {}))
            position = arguments.get("pos", arguments.get("position")) if isinstance(arguments, Mapping) else None
            if position is not None:
                xref.add(str(position).strip())
            else:
                xref.add("xref")
    return {
        "changed_production_files": list(production),
        "structure_evidence": sorted(item for item in structure if item),
        "xref_evidence": sorted(item for item in xref if item),
        "diagnostics_coverage": sorted(set(str(item) for item in covered)),
        "exemptions": list(exempt),
    }


def _required_evidence_actions(missing_files: Iterable[str]) -> tuple[str, ...]:
    """Return the bounded action set exposed to the recovery caller."""

    if not tuple(missing_files):
        return ()
    return tuple(sorted(_LSP_EVIDENCE_TOOLS))


@dataclasses.dataclass(frozen=True)
class FailureRecord:
    """One failure routed to a dedicated audit channel."""

    kind: FailureKind | str
    message: str
    source_agent_id: str
    feedback_target_agent_id: str | None = None
    channel: str | None = None

    def __post_init__(self) -> None:
        kind = self.kind.value if isinstance(self.kind, FailureKind) else str(self.kind).strip().lower()
        kind = {
            "infra": FailureKind.INFRASTRUCTURE.value,
            "infra_failure": FailureKind.INFRASTRUCTURE.value,
            "model-quality": FailureKind.MODEL_QUALITY.value,
            "quality": FailureKind.MODEL_QUALITY.value,
            "quality_failure": FailureKind.MODEL_QUALITY.value,
        }.get(kind, kind)
        if kind not in (FailureKind.INFRASTRUCTURE.value, FailureKind.MODEL_QUALITY.value):
            raise ContractFailure(f"unsupported failure kind: {kind}")
        object.__setattr__(self, "kind", kind)
        object.__setattr__(self, "message", _required_text(self.message, "failure message"))
        object.__setattr__(self, "source_agent_id", _required_text(self.source_agent_id, "failure source agent"))
        if self.feedback_target_agent_id is not None:
            object.__setattr__(
                self,
                "feedback_target_agent_id",
                _required_text(self.feedback_target_agent_id, "feedback target agent"),
            )
        channel = str(self.channel or "").strip().lower() or None
        expected = "infra" if kind == FailureKind.INFRASTRUCTURE.value else "model_quality"
        if channel is not None and channel != expected:
            raise ContractFailure(f"failure channel {channel!r} does not match {kind}")
        object.__setattr__(self, "channel", expected)


def _coerce_failure(value: FailureRecord | Mapping[str, Any]) -> FailureRecord:
    if isinstance(value, FailureRecord):
        return value
    if not isinstance(value, Mapping):
        raise ContractFailure(f"invalid failure record: {value!r}")
    return FailureRecord(
        kind=value.get("kind", value.get("failure_kind", value.get("failure_type", ""))),
        message=value.get("message", ""),
        source_agent_id=value.get("source_agent_id", value.get("agent_id", "")),
        feedback_target_agent_id=value.get("feedback_target_agent_id", value.get("feedback_target")),
        channel=value.get("channel"),
    )


def validate_failure_separation(
    failures: Iterable[FailureRecord | Mapping[str, Any]],
    *,
    code_agent_id: str,
) -> tuple[FailureRecord, ...]:
    """Reject mixed infrastructure/model-quality feedback channels.

    Infrastructure failures cannot originate from or target the code agent,
    and one agent cannot be the source of both failure classes in a round.
    Model-quality feedback may target the code agent, but remains explicitly
    classified so it cannot be mistaken for an infrastructure failure.
    """

    code_agent = _required_text(code_agent_id, "code agent")
    records = tuple(_coerce_failure(item) for item in failures)
    kinds_by_agent: dict[str, set[str]] = {}
    for record in records:
        kinds_by_agent.setdefault(record.source_agent_id, set()).add(str(record.kind))
        if record.kind == FailureKind.INFRASTRUCTURE.value:
            if record.source_agent_id == code_agent:
                raise ContractFailure("infrastructure failure cannot use the code agent channel")
            if record.feedback_target_agent_id == code_agent:
                raise ContractFailure("infrastructure failure cannot target the code agent")
            if record.channel != "infra":
                raise ContractFailure("infrastructure failure has a non-infra channel")
    mixed = sorted(agent for agent, kinds in kinds_by_agent.items() if len(kinds) > 1)
    if mixed:
        raise ContractFailure("failure source mixes infrastructure and model quality: " + ", ".join(mixed))
    return records


def _receipt_transport_status(values: Sequence[Any] | None) -> bool:
    """Detect server-reported transport failure before strict receipt parsing."""

    if values is None:
        return False
    for value in values:
        if isinstance(value, Mapping):
            raw_status = value.get("status", "")
        else:
            raw_status = getattr(value, "status", "")
        status = str(raw_status).strip().lower().replace("-", "_")
        if status in {
            "error",
            "failed",
            "timeout",
            "timed_out",
            "transport_closed",
            "transport_error",
            "infrastructure_failure",
        }:
            return True
    return False


def _transport_failure_declared(request: "RoundInput") -> bool:
    """Classify an absent/failed MCP stream as infrastructure when declared."""

    if request.transport_failed or not request.turn_completed:
        return True
    if _receipt_transport_status(request.server_receipts):
        return True
    for raw_failure in request.failures:
        try:
            if _coerce_failure(raw_failure).kind == FailureKind.INFRASTRUCTURE.value:
                return True
        except ContractFailure:
            # The normal failure-separation pass reports malformed records as a
            # contract failure; do not let an invalid record masquerade as infra.
            continue
    return False


def _canonical_receipt_coverage_observation(
    request: "RoundInput",
    changes: Sequence[FileChange],
) -> tuple[tuple[str, ...], tuple[str, ...], tuple[str, ...], str | None]:
    """Observe canonical server receipts without trusting agent trajectories.

    The final item is an infrastructure message.  It is deliberately separate
    from contract errors so a missing stream caused by transport loss can be
    retried without consuming a quality round.
    """

    mode_value = request.mode.value if isinstance(request.mode, RunMode) else str(request.mode).strip().lower().replace("-", "_")
    if mode_value == "lsp":
        mode_value = RunMode.WITH_LSP.value
    if mode_value == "no-lsp":
        mode_value = RunMode.NO_LSP.value
    try:
        effective_changes = validate_exemption_registry(
            changes,
            request.exemptions,
            round_number=request.round_number,
        )
    except ContractFailure as exc:
        return (), (), (str(exc),), None
    required = tuple(
        sorted(
            {
                item.path
                for item in effective_changes
                if item.production and item.exemption_kind is None
            }
        )
    )
    if request.server_generation is not None and (
        not isinstance(request.server_generation, str) or not request.server_generation.strip()
    ):
        return (), required, ("canonical receipt server_generation must be non-empty text",), None

    if mode_value == RunMode.NO_LSP.value:
        if not isinstance(request.task_id, str) or not request.task_id.strip():
            return (), required, ("canonical receipt task_id is required",), None
        task_id = request.task_id.strip()
        if request.server_receipts:
            return (), required, ("No-LSP canonical arm contains server receipts",), None
        if mcp_trajectory_events(request.effective_trajectory()):
            return (), required, ("No-LSP canonical arm contains agent MCP events",), None
        if request.isolation_evidence is None:
            return (), required, ("No-LSP canonical arm requires isolation evidence",), None
        try:
            validate_no_lsp_isolation(
                request.isolation_evidence,
                task=task_id,
                arm=request.arm_id,
                session=request.session_id,
                thread=request.thread_id,
                patch_hash=request.patch_hash,
                verifier=request.receipt_verifier,
                identity_envelope=request.identity_envelope,
                public_key=request.receipt_public_key,
            )
        except ReceiptError as exc:
            return (), required, (f"invalid No-LSP isolation evidence: {exc}",), None
        return (), (), (), None

    if mode_value != RunMode.WITH_LSP.value:
        return (), required, (f"unsupported run mode: {request.mode}",), None
    if request.isolation_evidence is not None:
        return (), required, ("With-LSP canonical arm cannot contain No-LSP isolation evidence",), None
    # Empty canonical evidence remains a valid observation, but changed
    # supported production files below make it incomplete and therefore open
    # the bounded evidence-recovery gate.  Non-empty receipts require the full
    # task identity before they can influence the audit.
    if not request.server_receipts:
        if _transport_failure_declared(request):
            return (), required, (), "MCP server transport failed before canonical receipts were captured"
        return (), required, (), None
    if not isinstance(request.task_id, str) or not request.task_id.strip():
        return (), required, ("canonical receipt task_id is required",), None
    task_id = request.task_id.strip()
    if _transport_failure_declared(request):
        return (), required, (), "MCP server transport failed before canonical receipts were captured"
    raw_receipts = request.server_receipts
    if raw_receipts is None or not raw_receipts:
        return (), (), (), None
    try:
        receipts = validate_receipts(
            raw_receipts,
            task=task_id,
            arm=request.arm_id,
            session=request.session_id,
            thread=request.thread_id,
            server_generation=request.server_generation,
            patch_hash=request.patch_hash,
            verifier=request.receipt_verifier,
            identity_envelope=request.identity_envelope,
            public_key=request.receipt_public_key,
        )
    except ReceiptError as exc:
        if _receipt_transport_status(raw_receipts):
            return (), required, (), f"MCP server transport receipt failed: {exc}"
        return (), required, (f"invalid canonical MCP server receipt: {exc}",), None
    try:
        # Ask the receipt module to validate the full semantic chain, then
        # compare the result against the current production file scope here.
        coverage = validate_lsp_receipts(
            receipts,
            changed_files=(),
            patch_hash=request.patch_hash,
            verifier=request.receipt_verifier,
            identity_envelope=request.identity_envelope,
            public_key=request.receipt_public_key,
        )
    except ReceiptChainError as exc:
        if "no diagnostics receipt" in str(exc):
            # Preserve usable semantic receipts as audit evidence, but return
            # the complete required scope as missing so the grader stays
            # blocked until final diff-scoped diagnostics is captured.
            return (), required, (), None
        return (), required, (f"invalid canonical MCP receipt chain: {exc}",), None
    covered = tuple(sorted(set(coverage.diagnostics_files)))
    covered_set = set(covered)
    required_set = set(required)
    missing = tuple(sorted(required_set - covered_set))
    extra = tuple(sorted(covered_set - required_set))
    if extra:
        return covered, missing, (
            "canonical diagnostics receipts have extra files: " + ", ".join(extra),
        ), None
    return covered, missing, (), None


def _canonical_receipt_generation(values: Sequence[Any] | None) -> str | None:
    """Extract one receipt generation after the stream has passed validation."""

    if not values:
        return None
    generations: set[str] = set()
    for value in values:
        if isinstance(value, Mapping):
            generation = value.get("server_generation")
        else:
            generation = getattr(value, "server_generation", None)
        if isinstance(generation, str) and generation.strip():
            generations.add(generation.strip())
    if len(generations) == 1:
        return next(iter(generations))
    return None


@dataclasses.dataclass(frozen=True)
class RoundInput:
    """The auditable facts supplied by a runner for one arm round."""

    round_number: int
    arm_id: str
    session_id: str
    thread_id: str
    patch_hash: str
    changed_files: Sequence[FileChange | Mapping[str, Any] | str] = ()
    trajectory: Sequence[Any] = ()
    mode: RunMode | str = RunMode.WITH_LSP
    action: str = "start"
    exemptions: Sequence[Exemption | Mapping[str, Any]] = ()
    failures: Sequence[FailureRecord | Mapping[str, Any]] = ()
    production_file_changes: Sequence[FileChange | Mapping[str, Any] | str] | None = None
    mcp_trajectory: Sequence[Any] | None = None
    resume: bool | None = None
    code_agent_id: str = "code-agent"
    evidence_attempt: int | None = None
    # When supplied (including an explicit empty list), this is the only
    # authoritative With-LSP coverage source.  ``None`` keeps the legacy
    # trajectory seam usable while callers migrate to server receipts.
    server_receipts: Sequence[Any] | None = None
    isolation_evidence: Mapping[str, Any] | None = None
    identity_envelope: Mapping[str, Any] | None = None
    receipt_public_key: Mapping[str, Any] | None = None
    receipt_verifier: Any = None
    task_id: str | None = None
    server_generation: str | None = None
    transport_failed: bool = False
    turn_completed: bool = True

    def effective_changed_files(self) -> Sequence[FileChange | Mapping[str, Any] | str]:
        return self.production_file_changes if self.production_file_changes is not None else self.changed_files

    def effective_trajectory(self) -> Sequence[Any]:
        return self.mcp_trajectory if self.mcp_trajectory is not None else self.trajectory

    def is_resume(self) -> bool:
        if self.resume is not None:
            return self.resume
        return self.action.strip().lower() == "resume"

    def is_evidence_recovery(self) -> bool:
        action = self.action.strip().lower().replace("-", "_").replace(" ", "_")
        return self.evidence_attempt is not None or action in {
            "evidence_recovery",
            "recover_evidence",
            "evidence_recover",
            "lsp_recovery",
            "recovery",
        }

    def has_canonical_receipt_source(self) -> bool:
        """Whether this request opted into canonical receipt/isolation authority."""

        return self.server_receipts is not None or self.isolation_evidence is not None


@dataclasses.dataclass
class EvidenceRecoveryRecord:
    """One persisted attempt to fill diagnostics evidence for a quality round."""

    round_number: int
    evidence_attempt: int
    arm_id: str
    session_id: str
    thread_id: str
    code_agent_id: str
    patch_hash: str
    changed_production_files: tuple[str, ...]
    diagnostics_covered_files: tuple[str, ...]
    missing_files: tuple[str, ...]
    required_actions: tuple[str, ...]
    decision: str
    contract_failures: tuple[str, ...] = ()

    def to_dict(self) -> dict[str, Any]:
        return dataclasses.asdict(self)

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "EvidenceRecoveryRecord":
        try:
            return cls(
                round_number=int(value["round_number"]),
                evidence_attempt=int(value["evidence_attempt"]),
                arm_id=_required_text(value["arm_id"], "evidence arm id"),
                session_id=_required_text(value["session_id"], "evidence session id"),
                thread_id=_required_text(value["thread_id"], "evidence thread id"),
                code_agent_id=_required_text(value["code_agent_id"], "evidence code agent"),
                patch_hash=_required_text(value["patch_hash"], "evidence patch hash"),
                changed_production_files=tuple(value.get("changed_production_files", ())),
                diagnostics_covered_files=tuple(value.get("diagnostics_covered_files", ())),
                missing_files=tuple(value.get("missing_files", ())),
                required_actions=tuple(value.get("required_actions", ())),
                decision=_required_text(value["decision"], "evidence decision"),
                contract_failures=tuple(value.get("contract_failures", ())),
            )
        except (KeyError, TypeError, ValueError, ContractFailure) as exc:
            raise StateDecodeError(f"invalid evidence recovery record: {exc}") from exc


@dataclasses.dataclass
class RoundRecord:
    round_number: int
    arm_id: str
    session_id: str
    thread_id: str
    patch_hash: str
    changed_production_files: tuple[str, ...]
    diagnostics_covered_files: tuple[str, ...]
    decision: str
    grader_allowed: bool
    grader_started: bool = False
    grader_passed: bool | None = None
    failure_kind: str | None = None
    no_progress: bool = False
    no_progress_streak: int = 0
    contract_failures: tuple[str, ...] = ()
    quality_accepted: bool = False
    infra_attempts: int = 0
    lsp_self_check: Mapping[str, Any] = dataclasses.field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return dataclasses.asdict(self)

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "RoundRecord":
        try:
            raw_lsp_self_check = value.get("lsp_self_check", {})
            if not isinstance(raw_lsp_self_check, Mapping):
                raise StateDecodeError("round lsp_self_check must be an object")
            return cls(
                round_number=int(value["round_number"]),
                arm_id=str(value["arm_id"]),
                session_id=str(value["session_id"]),
                thread_id=str(value["thread_id"]),
                patch_hash=str(value["patch_hash"]),
                changed_production_files=tuple(value.get("changed_production_files", ())),
                diagnostics_covered_files=tuple(value.get("diagnostics_covered_files", ())),
                decision=str(value["decision"]),
                grader_allowed=bool(value["grader_allowed"]),
                grader_started=bool(value.get("grader_started", False)),
                grader_passed=value.get("grader_passed"),
                failure_kind=value.get("failure_kind"),
                no_progress=bool(value.get("no_progress", False)),
                no_progress_streak=int(value.get("no_progress_streak", 0)),
                contract_failures=tuple(value.get("contract_failures", ())),
                quality_accepted=bool(value.get("quality_accepted", False)),
                infra_attempts=int(value.get("infra_attempts", 0)),
                lsp_self_check=dict(raw_lsp_self_check),
            )
        except (KeyError, TypeError, ValueError) as exc:
            raise StateDecodeError(f"invalid round record: {exc}") from exc


@dataclasses.dataclass
class RunnerState:
    """Durable identity, bounded-round state, and append-only audit history."""

    arm_id: str | None = None
    session_id: str | None = None
    thread_id: str | None = None
    code_agent_id: str | None = None
    max_rounds: int = MAX_ROUNDS
    no_progress_limit: int = DEFAULT_NO_PROGRESS_LIMIT
    last_round: int = 0
    last_patch_hash: str | None = None
    last_quality_patch_hash: str | None = None
    quality_rounds_used: int = 0
    no_progress_streak: int = 0
    status: str = RunnerStatus.ACTIVE.value
    rounds: list[RoundRecord] = dataclasses.field(default_factory=list)
    evidence_attempts: list[EvidenceRecoveryRecord] = dataclasses.field(default_factory=list)
    failures: list[FailureRecord] = dataclasses.field(default_factory=list)
    pending_evidence_round: int | None = None
    pending_evidence_patch_hash: str | None = None
    pending_evidence_files: tuple[str, ...] = ()
    pending_evidence_required_files: tuple[str, ...] = ()
    pending_evidence_covered_files: tuple[str, ...] = ()
    pending_evidence_attempts: int = 0
    # Keep the recovery authority bound to the original quality round.  A
    # pending server-receipt round cannot be resumed from agent claims or a
    # different MCP server generation.
    pending_evidence_source: str | None = None
    pending_evidence_task_id: str | None = None
    pending_evidence_server_generation: str | None = None

    def __post_init__(self) -> None:
        if self.max_rounds != MAX_ROUNDS:
            raise ContractFailure(f"closed-loop runner must allow exactly {MAX_ROUNDS} rounds")
        if self.no_progress_limit < 1:
            raise ContractFailure("no_progress_limit must be positive")
        if self.last_round < 0 or self.last_round > self.max_rounds:
            raise StateDecodeError("invalid persisted last_round")
        if self.quality_rounds_used < 0 or self.quality_rounds_used > self.last_round:
            raise StateDecodeError("invalid persisted quality_rounds_used")
        if self.pending_evidence_attempts < 0 or self.pending_evidence_attempts > MAX_EVIDENCE_RECOVERY_ATTEMPTS:
            raise StateDecodeError("invalid persisted evidence recovery attempt count")
        if self.pending_evidence_round is None:
            if any(
                value
                for value in (
                    self.pending_evidence_patch_hash,
                    self.pending_evidence_files,
                    self.pending_evidence_required_files,
                    self.pending_evidence_covered_files,
                    self.pending_evidence_attempts,
                    self.pending_evidence_source,
                    self.pending_evidence_task_id,
                    self.pending_evidence_server_generation,
                )
            ):
                raise StateDecodeError("orphaned persisted evidence recovery fields")
        else:
            if self.pending_evidence_round != self.last_round + 1:
                raise StateDecodeError("persisted evidence recovery is not for the next quality round")
            if not self.arm_id or not self.session_id or not self.thread_id or not self.code_agent_id:
                raise StateDecodeError("persisted evidence recovery is missing identity")
            if not self.pending_evidence_patch_hash:
                raise StateDecodeError("persisted evidence recovery is missing patch hash")
            if not self.pending_evidence_files or not self.pending_evidence_required_files:
                raise StateDecodeError("persisted evidence recovery is missing file scope")
            if self.status != RunnerStatus.EVIDENCE_RECOVERY_REQUIRED.value:
                raise StateDecodeError("persisted evidence recovery has an invalid runner status")
            source = self.pending_evidence_source
            if source not in {"trajectory", "server_receipts"}:
                raise StateDecodeError("persisted evidence recovery has an invalid evidence source")
            if source == "server_receipts":
                if not self.pending_evidence_task_id:
                    raise StateDecodeError("persisted receipt recovery is missing task id")
            elif self.pending_evidence_task_id or self.pending_evidence_server_generation:
                raise StateDecodeError("trajectory recovery cannot carry receipt identity")

    @classmethod
    def empty(cls, *, max_rounds: int = MAX_ROUNDS, no_progress_limit: int = DEFAULT_NO_PROGRESS_LIMIT) -> "RunnerState":
        return cls(max_rounds=max_rounds, no_progress_limit=no_progress_limit)

    def to_dict(self) -> dict[str, Any]:
        return {
            "schema_version": STATE_SCHEMA_VERSION,
            "arm_id": self.arm_id,
            "session_id": self.session_id,
            "thread_id": self.thread_id,
            "code_agent_id": self.code_agent_id,
            "max_rounds": self.max_rounds,
            "no_progress_limit": self.no_progress_limit,
            "last_round": self.last_round,
            "last_patch_hash": self.last_patch_hash,
            "last_quality_patch_hash": self.last_quality_patch_hash,
            "quality_rounds_used": self.quality_rounds_used,
            "no_progress_streak": self.no_progress_streak,
            "status": self.status,
            "rounds": [record.to_dict() for record in self.rounds],
            "evidence_attempts": [record.to_dict() for record in self.evidence_attempts],
            "failures": [dataclasses.asdict(record) for record in self.failures],
            "pending_evidence_round": self.pending_evidence_round,
            "pending_evidence_patch_hash": self.pending_evidence_patch_hash,
            "pending_evidence_files": list(self.pending_evidence_files),
            "pending_evidence_required_files": list(self.pending_evidence_required_files),
            "pending_evidence_covered_files": list(self.pending_evidence_covered_files),
            "pending_evidence_attempts": self.pending_evidence_attempts,
            "pending_evidence_source": self.pending_evidence_source,
            "pending_evidence_task_id": self.pending_evidence_task_id,
            "pending_evidence_server_generation": self.pending_evidence_server_generation,
        }

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "RunnerState":
        if value.get("schema_version") != STATE_SCHEMA_VERSION:
            raise StateDecodeError("unsupported closed-loop state schema")
        try:
            rounds = [RoundRecord.from_dict(item) for item in value.get("rounds", ())]
            evidence_attempts = [
                EvidenceRecoveryRecord.from_dict(item)
                for item in value.get("evidence_attempts", ())
            ]
            failures = [_coerce_failure(item) for item in value.get("failures", ())]
            pending_evidence_round_value = value.get("pending_evidence_round")
            state = cls(
                arm_id=value.get("arm_id"),
                session_id=value.get("session_id"),
                thread_id=value.get("thread_id"),
                code_agent_id=value.get("code_agent_id"),
                max_rounds=int(value.get("max_rounds", MAX_ROUNDS)),
                no_progress_limit=int(value.get("no_progress_limit", DEFAULT_NO_PROGRESS_LIMIT)),
                last_round=int(value.get("last_round", 0)),
                last_patch_hash=value.get("last_patch_hash"),
                last_quality_patch_hash=value.get("last_quality_patch_hash"),
                quality_rounds_used=int(value.get("quality_rounds_used", 0)),
                no_progress_streak=int(value.get("no_progress_streak", 0)),
                status=str(value.get("status", RunnerStatus.ACTIVE.value)),
                rounds=rounds,
                evidence_attempts=evidence_attempts,
                failures=failures,
                pending_evidence_round=(
                    None
                    if pending_evidence_round_value is None
                    else int(str(pending_evidence_round_value))
                ),
                pending_evidence_patch_hash=value.get("pending_evidence_patch_hash"),
                pending_evidence_files=tuple(value.get("pending_evidence_files", ())),
                pending_evidence_required_files=tuple(
                    value.get("pending_evidence_required_files", ())
                ),
                pending_evidence_covered_files=tuple(
                    value.get("pending_evidence_covered_files", ())
                ),
                pending_evidence_attempts=int(value.get("pending_evidence_attempts", 0)),
                pending_evidence_source=(
                    value.get("pending_evidence_source")
                    if pending_evidence_round_value is not None
                    else None
                )
                or ("trajectory" if pending_evidence_round_value is not None else None),
                pending_evidence_task_id=value.get("pending_evidence_task_id"),
                pending_evidence_server_generation=value.get(
                    "pending_evidence_server_generation"
                ),
            )
        except (TypeError, ValueError, ContractFailure, StateDecodeError) as exc:
            raise StateDecodeError(f"invalid closed-loop state: {exc}") from exc
        if len(state.rounds) != state.last_round:
            raise StateDecodeError("persisted round history does not match last_round")
        if state.status not in _RUNNER_STATUSES:
            raise StateDecodeError(f"unsupported persisted runner status: {state.status}")
        if state.last_round:
            if not state.arm_id or not state.session_id or not state.thread_id or not state.code_agent_id:
                raise StateDecodeError("persisted round state is missing identity")
            expected_rounds = tuple(range(1, state.last_round + 1))
            actual_rounds = tuple(record.round_number for record in state.rounds)
            if actual_rounds != expected_rounds:
                raise StateDecodeError("persisted round history is not sequential")
            if state.last_patch_hash != state.rounds[-1].patch_hash:
                raise StateDecodeError("persisted last_patch_hash does not match audit history")
            if state.last_quality_patch_hash is None and state.quality_rounds_used:
                raise StateDecodeError("persisted quality rounds are missing last patch hash")
        if state.status == RunnerStatus.INFRASTRUCTURE_FAILURE.value:
            if not state.rounds or state.rounds[-1].grader_started:
                raise StateDecodeError("persisted infrastructure failure is not retryable")
        if state.status == RunnerStatus.CONTRACT_FAILURE.value and state.pending_evidence_round is not None:
            raise StateDecodeError("contract failure cannot retain pending evidence recovery")
        return state


class JsonStateStore:
    """Atomic stdlib JSON persistence for the arm identity and audit trail."""

    def __init__(self, path: str | os.PathLike[str]):
        self.path = pathlib.Path(path)

    def load(self) -> RunnerState:
        try:
            data = json.loads(self.path.read_text(encoding="utf-8"))
        except FileNotFoundError:
            return RunnerState.empty()
        except (OSError, json.JSONDecodeError) as exc:
            raise StateDecodeError(f"read closed-loop state: {exc}") from exc
        if not isinstance(data, Mapping):
            raise StateDecodeError("closed-loop state must be a JSON object")
        return RunnerState.from_dict(data)

    def save(self, state: RunnerState) -> None:
        payload = json.dumps(state.to_dict(), ensure_ascii=False, sort_keys=True, indent=2) + "\n"
        parent = self.path.parent
        try:
            parent.mkdir(parents=True, exist_ok=True)
            fd, temp_name = tempfile.mkstemp(prefix=self.path.name + ".", suffix=".tmp", dir=parent)
            try:
                with os.fdopen(fd, "w", encoding="utf-8") as handle:
                    handle.write(payload)
                    handle.flush()
                    os.fsync(handle.fileno())
                os.replace(temp_name, self.path)
            finally:
                if os.path.exists(temp_name):
                    os.unlink(temp_name)
        except OSError as exc:
            raise GuardError(f"persist closed-loop state: {exc}") from exc


@dataclasses.dataclass(frozen=True)
class RoundOutcome:
    round_number: int
    decision: str
    grader_allowed: bool
    no_progress: bool
    no_progress_streak: int
    relocalization_required: bool
    terminal: bool
    diagnostics_covered_files: tuple[str, ...] = ()
    contract_failures: tuple[str, ...] = ()
    missing_files: tuple[str, ...] = ()
    required_actions: tuple[str, ...] = ()
    evidence_attempt: int | None = None
    evidence_recovery_succeeded: bool = False
    failure_kind: str | None = None
    lsp_self_check: Mapping[str, Any] = dataclasses.field(default_factory=dict)


@dataclasses.dataclass(frozen=True)
class GraderOutcome:
    round_number: int
    started: bool
    passed: bool | None
    failure_kind: str | None
    quality_accepted: bool
    runner_status: str


class ClosedLoopGuard:
    """Stateful five-round guard that owns no model, prompt, or grader data."""

    def __init__(
        self,
        state: RunnerState | None = None,
        *,
        state_store: JsonStateStore | None = None,
        no_progress_limit: int = DEFAULT_NO_PROGRESS_LIMIT,
    ) -> None:
        self.state_store = state_store
        self.state = state if state is not None else (state_store.load() if state_store else RunnerState.empty())
        if self.state.no_progress_limit != no_progress_limit and self.state.last_round == 0:
            self.state.no_progress_limit = no_progress_limit

    @classmethod
    def from_store(cls, path: str | os.PathLike[str]) -> "ClosedLoopGuard":
        return cls(state_store=JsonStateStore(path))

    def _persist(self) -> None:
        if self.state_store is not None:
            self.state_store.save(self.state)

    def _validate_identity(self, request: RoundInput) -> None:
        arm_id = _required_text(request.arm_id, "arm id")
        session_id = _required_text(request.session_id, "session id")
        thread_id = _required_text(request.thread_id, "thread id")
        code_agent_id = _required_text(request.code_agent_id, "code agent")
        if self.state.arm_id is None:
            if request.round_number != 1 or request.is_resume():
                raise SessionIdentityError("first round must start a new arm identity")
            self.state.arm_id = arm_id
            self.state.session_id = session_id
            self.state.thread_id = thread_id
            self.state.code_agent_id = code_agent_id
            return
        if arm_id != self.state.arm_id:
            raise SessionIdentityError("arm id drifted from persisted identity")
        if request.round_number == 1:
            if self.state.last_round == 0 and request.is_resume():
                if session_id != self.state.session_id or thread_id != self.state.thread_id:
                    raise SessionIdentityError("session/thread id drifted; resume original identity")
                if code_agent_id != self.state.code_agent_id:
                    raise SessionIdentityError("code agent id drifted from persisted identity")
                return
            raise SessionIdentityError("round 1 cannot be replayed on an existing arm")
        if not request.is_resume():
            raise SessionIdentityError("round >1 must resume the original session/thread")
        if session_id != self.state.session_id or thread_id != self.state.thread_id:
            raise SessionIdentityError("session/thread id drifted; resume original identity")
        if code_agent_id != self.state.code_agent_id:
            raise SessionIdentityError("code agent id drifted from persisted identity")

    def _validate_recovery_identity(self, request: RoundInput) -> None:
        """Validate the immutable owner identity for evidence-only recovery."""

        arm_id = _required_text(request.arm_id, "arm id")
        session_id = _required_text(request.session_id, "session id")
        thread_id = _required_text(request.thread_id, "thread id")
        code_agent_id = _required_text(request.code_agent_id, "code agent")
        if self.state.arm_id is None:
            raise SessionIdentityError("evidence recovery has no persisted arm identity")
        if arm_id != self.state.arm_id:
            raise SessionIdentityError("evidence recovery arm id drifted")
        if session_id != self.state.session_id or thread_id != self.state.thread_id:
            raise SessionIdentityError("evidence recovery session/thread id drifted")
        if code_agent_id != self.state.code_agent_id:
            raise SessionIdentityError("evidence recovery code agent id drifted")

    @staticmethod
    def _action_name(request: RoundInput) -> str:
        return request.action.strip().lower().replace("-", "_").replace(" ", "_")

    def _is_infrastructure_retry(self, request: RoundInput) -> bool:
        if self.state.status != RunnerStatus.INFRASTRUCTURE_FAILURE.value:
            return False
        if not self.state.rounds:
            return False
        record = self.state.rounds[-1]
        if record.grader_started or record.grader_passed is not None:
            return False
        try:
            patch_hash = _required_text(request.patch_hash, "patch hash")
        except ContractFailure:
            return False
        return request.round_number == record.round_number and patch_hash == record.patch_hash

    def _validate_sequence(self, request: RoundInput) -> None:
        if request.round_number < 1:
            raise RoundSequenceError("round number must be positive")
        if request.round_number > self.state.max_rounds:
            raise RoundSequenceError(f"closed-loop runner allows at most {self.state.max_rounds} rounds")
        if self.state.status == RunnerStatus.INFRASTRUCTURE_FAILURE.value:
            if self._is_infrastructure_retry(request):
                return
            raise RoundSequenceError("infrastructure failure must be retried in the same quality round")
        if request.round_number != self.state.last_round + 1:
            raise RoundSequenceError(
                f"expected round {self.state.last_round + 1}, got {request.round_number}"
            )
        if self.state.status in (
            RunnerStatus.SUCCEEDED.value,
            RunnerStatus.UNVERIFIED.value,
            RunnerStatus.CONTRACT_FAILURE.value,
            RunnerStatus.EVIDENCE_RECOVERY_REQUIRED.value,
            RunnerStatus.RELOCALIZE_REQUIRED.value,
            RunnerStatus.MAX_ROUNDS_REACHED.value,
        ):
            raise RoundSequenceError(f"runner is terminal: {self.state.status}")

    def _clear_pending_evidence(self) -> None:
        self.state.pending_evidence_round = None
        self.state.pending_evidence_patch_hash = None
        self.state.pending_evidence_files = ()
        self.state.pending_evidence_required_files = ()
        self.state.pending_evidence_covered_files = ()
        self.state.pending_evidence_attempts = 0
        self.state.pending_evidence_source = None
        self.state.pending_evidence_task_id = None
        self.state.pending_evidence_server_generation = None

    def _contract_outcome(
        self,
        *,
        round_number: int,
        message: str,
        covered: Iterable[str] = (),
        missing: Iterable[str] = (),
        required_actions: Iterable[str] = (),
        evidence_attempt: int | None = None,
        lsp_self_check: Mapping[str, Any] | None = None,
        terminal: bool = True,
    ) -> RoundOutcome:
        """Persist a terminal recovery contract failure without authorizing a grader."""

        failures = (str(message),)
        self._clear_pending_evidence()
        self.state.status = RunnerStatus.CONTRACT_FAILURE.value
        self._persist()
        return RoundOutcome(
            round_number=round_number,
            decision=RoundDecision.CONTRACT_FAILURE.value,
            grader_allowed=False,
            no_progress=False,
            no_progress_streak=self.state.no_progress_streak,
            relocalization_required=False,
            terminal=terminal,
            diagnostics_covered_files=tuple(sorted(set(covered))),
            contract_failures=failures,
            missing_files=tuple(sorted(set(missing))),
            required_actions=tuple(required_actions),
            evidence_attempt=evidence_attempt,
            lsp_self_check=dict(lsp_self_check or {}),
        )

    def _patch_changed_during_recovery(self, round_number: int, message: str) -> RoundOutcome:
        """Close only the recovery gate so the caller can submit a fresh quality round."""

        self._clear_pending_evidence()
        self.state.status = RunnerStatus.ACTIVE.value
        self._persist()
        return RoundOutcome(
            round_number=round_number,
            decision=RoundDecision.CONTRACT_FAILURE.value,
            grader_allowed=False,
            no_progress=False,
            no_progress_streak=self.state.no_progress_streak,
            relocalization_required=False,
            terminal=False,
            contract_failures=(message,),
        )

    def _start_evidence_recovery(
        self,
        request: RoundInput,
        changes: Sequence[FileChange],
        covered: Sequence[str],
        missing: Sequence[str],
        *,
        failure_kind: str | None = None,
        failure_message: str | None = None,
        lsp_self_check: Mapping[str, Any] | None = None,
    ) -> RoundOutcome:
        if self.state.pending_evidence_round is not None:
            return self._contract_outcome(
                round_number=request.round_number,
                message="evidence recovery already pending; only one recovery is allowed",
                covered=covered,
                missing=missing,
                required_actions=_required_evidence_actions(missing),
            )
        if request.round_number != self.state.last_round + 1:
            raise RoundSequenceError(
                f"expected quality round {self.state.last_round + 1}, got {request.round_number}"
            )
        effective = validate_exemption_registry(
            changes, request.exemptions, round_number=request.round_number
        )
        production_paths = tuple(sorted({item.path for item in effective if item.production}))
        exempt_paths = {
            item.path for item in effective if item.production and item.exemption_kind is not None
        }
        required_files = tuple(sorted(set(production_paths) - exempt_paths))
        if not required_files:
            return self._contract_outcome(
                round_number=request.round_number,
                message="evidence recovery requested without required production files",
            )
        patch_hash = _required_text(request.patch_hash, "patch hash")
        self.state.pending_evidence_round = request.round_number
        self.state.pending_evidence_patch_hash = patch_hash
        self.state.pending_evidence_files = production_paths
        self.state.pending_evidence_required_files = required_files
        self.state.pending_evidence_covered_files = tuple(sorted(set(covered)))
        self.state.pending_evidence_attempts = 0
        canonical_source = request.has_canonical_receipt_source()
        self.state.pending_evidence_source = "server_receipts" if canonical_source else "trajectory"
        task_id = request.task_id
        if canonical_source and not (isinstance(task_id, str) and task_id.strip()):
            self._clear_pending_evidence()
            return self._contract_outcome(
                round_number=request.round_number,
                message="canonical evidence recovery requires task id",
            )
        self.state.pending_evidence_task_id = task_id.strip() if isinstance(task_id, str) else None
        self.state.pending_evidence_server_generation = (
            request.server_generation or _canonical_receipt_generation(request.server_receipts)
            if canonical_source
            else None
        )
        if failure_kind == FailureKind.INFRASTRUCTURE.value:
            self.state.failures.append(
                FailureRecord(
                    kind=FailureKind.INFRASTRUCTURE,
                    message=failure_message or "MCP server transport failed before evidence capture",
                    source_agent_id="mcp-transport",
                )
            )
        self.state.status = RunnerStatus.EVIDENCE_RECOVERY_REQUIRED.value
        self._persist()
        actions = _required_evidence_actions(missing)
        return RoundOutcome(
            round_number=request.round_number,
            decision=RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value,
            grader_allowed=False,
            no_progress=False,
            no_progress_streak=self.state.no_progress_streak,
            relocalization_required=False,
            terminal=False,
            diagnostics_covered_files=tuple(sorted(set(covered))),
            missing_files=tuple(sorted(set(missing))),
            required_actions=actions,
            evidence_attempt=0,
            failure_kind=failure_kind,
            lsp_self_check=dict(lsp_self_check or {}),
        )

    def _record_quality_round(
        self,
        request: RoundInput,
        *,
        patch_hash: str,
        changes: Sequence[FileChange],
        covered: Sequence[str],
        missing: Sequence[str] = (),
        contract_errors: Sequence[str] = (),
        validated_failures: Sequence[FailureRecord] = (),
        evidence_attempt: int | None = None,
        evidence_recovery_succeeded: bool = False,
        lsp_self_check: Mapping[str, Any] | None = None,
    ) -> RoundOutcome:
        production_paths = tuple(sorted({item.path for item in changes if item.production}))
        contract_failure_tuple = tuple(str(item) for item in contract_errors)
        if validated_failures:
            self.state.failures.extend(validated_failures)

        no_progress = not production_paths or patch_hash == self.state.last_quality_patch_hash
        next_streak = self.state.no_progress_streak + 1 if no_progress else 0
        if contract_failure_tuple:
            decision = RoundDecision.CONTRACT_FAILURE.value
            grader_allowed = False
            terminal = True
            relocalize = False
            self.state.status = RunnerStatus.CONTRACT_FAILURE.value
        elif no_progress:
            decision = RoundDecision.NO_PROGRESS.value
            grader_allowed = False
            terminal = next_streak >= self.state.no_progress_limit
            relocalize = terminal
            self.state.status = (
                RunnerStatus.RELOCALIZE_REQUIRED.value if terminal else RunnerStatus.ACTIVE.value
            )
        else:
            decision = RoundDecision.GRADER_ALLOWED.value
            grader_allowed = True
            terminal = False
            relocalize = False
            self.state.status = RunnerStatus.ACTIVE.value

        record = RoundRecord(
            round_number=request.round_number,
            arm_id=self.state.arm_id or request.arm_id,
            session_id=self.state.session_id or request.session_id,
            thread_id=self.state.thread_id or request.thread_id,
            patch_hash=patch_hash,
            changed_production_files=production_paths,
            diagnostics_covered_files=tuple(sorted(set(covered))),
            decision=decision,
            grader_allowed=grader_allowed,
            no_progress=no_progress,
            no_progress_streak=next_streak,
            contract_failures=contract_failure_tuple,
            lsp_self_check=dict(lsp_self_check or {}),
        )
        self.state.rounds.append(record)
        self.state.last_round = request.round_number
        self.state.last_patch_hash = patch_hash
        self.state.no_progress_streak = next_streak
        # The patch is remembered for the same-patch no-progress rule even
        # while a grader is waiting to run.  The quality-round budget itself is
        # advanced only by a no-progress/contract decision or a grader result.
        self.state.last_quality_patch_hash = patch_hash
        if not grader_allowed:
            self.state.quality_rounds_used += 1
        if request.round_number == self.state.max_rounds and not grader_allowed:
            if not terminal:
                self.state.status = RunnerStatus.MAX_ROUNDS_REACHED.value
            terminal = True
        self._persist()
        return RoundOutcome(
            round_number=request.round_number,
            decision=decision,
            grader_allowed=grader_allowed,
            no_progress=no_progress,
            no_progress_streak=next_streak,
            relocalization_required=relocalize,
            terminal=terminal,
            diagnostics_covered_files=tuple(sorted(set(covered))),
            contract_failures=contract_failure_tuple,
            missing_files=tuple(sorted(set(missing))),
            evidence_attempt=evidence_attempt,
            evidence_recovery_succeeded=evidence_recovery_succeeded,
            lsp_self_check=dict(lsp_self_check or {}),
        )

    def _record_infrastructure_round(
        self,
        request: RoundInput,
        *,
        patch_hash: str,
        changes: Sequence[FileChange],
        covered: Sequence[str] = (),
        missing: Sequence[str] = (),
        message: str,
        lsp_self_check: Mapping[str, Any] | None = None,
    ) -> RoundOutcome:
        """Open a retryable same-round slot for pre-grader infrastructure.

        An MCP transport failure is not a diagnostics coverage failure.  It
        still means the round cannot be graded until the infrastructure is
        retried, so persist a normal quality-round record with zero quality
        consumption and expose the existing ``infra_retry`` child action.
        """

        if self.state.rounds and self.state.rounds[-1].round_number == request.round_number:
            raise RoundSequenceError("infrastructure round is already recorded")
        production_paths = tuple(sorted({item.path for item in changes if item.production}))
        record = RoundRecord(
            round_number=request.round_number,
            arm_id=self.state.arm_id or request.arm_id,
            session_id=self.state.session_id or request.session_id,
            thread_id=self.state.thread_id or request.thread_id,
            patch_hash=patch_hash,
            changed_production_files=production_paths,
            diagnostics_covered_files=tuple(sorted(set(covered))),
            decision=RoundDecision.INFRASTRUCTURE_FAILURE.value,
            grader_allowed=False,
            failure_kind=FailureKind.INFRASTRUCTURE.value,
            lsp_self_check=dict(lsp_self_check or {}),
        )
        self.state.rounds.append(record)
        self.state.last_round = request.round_number
        self.state.last_patch_hash = patch_hash
        self.state.status = RunnerStatus.INFRASTRUCTURE_FAILURE.value
        self.state.failures.append(
            FailureRecord(
                kind=FailureKind.INFRASTRUCTURE,
                message=_required_text(message, "infrastructure failure message"),
                source_agent_id="mcp-transport",
            )
        )
        self._persist()
        return RoundOutcome(
            round_number=request.round_number,
            decision=RoundDecision.INFRASTRUCTURE_FAILURE.value,
            grader_allowed=False,
            no_progress=False,
            no_progress_streak=self.state.no_progress_streak,
            relocalization_required=False,
            terminal=False,
            diagnostics_covered_files=tuple(sorted(set(covered))),
            failure_kind=FailureKind.INFRASTRUCTURE.value,
            missing_files=tuple(sorted(set(missing))),
            lsp_self_check=dict(lsp_self_check or {}),
        )

    def _infrastructure_recovery_outcome(
        self,
        *,
        round_number: int,
        covered: Iterable[str],
        missing: Iterable[str],
        message: str,
    ) -> RoundOutcome:
        """Keep an evidence gate retryable when the MCP transport failed."""

        self.state.failures.append(
            FailureRecord(
                kind=FailureKind.INFRASTRUCTURE,
                message=message,
                source_agent_id="mcp-transport",
            )
        )
        self._persist()
        missing_files = tuple(sorted(set(missing)))
        return RoundOutcome(
            round_number=round_number,
            decision=RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value,
            grader_allowed=False,
            no_progress=False,
            no_progress_streak=self.state.no_progress_streak,
            relocalization_required=False,
            terminal=False,
            diagnostics_covered_files=tuple(sorted(set(covered))),
            missing_files=missing_files,
            required_actions=_required_evidence_actions(missing_files),
            evidence_attempt=self.state.pending_evidence_attempts,
            failure_kind=FailureKind.INFRASTRUCTURE.value,
        )

    def _evaluate_evidence_recovery(self, request: RoundInput) -> RoundOutcome:
        pending_round = self.state.pending_evidence_round
        if pending_round is None:
            return self._contract_outcome(
                round_number=request.round_number,
                message="evidence recovery was requested without a pending evidence gate",
            )
        self._validate_recovery_identity(request)
        patch_hash = _required_text(request.patch_hash, "patch hash")
        if patch_hash != self.state.pending_evidence_patch_hash:
            return self._patch_changed_during_recovery(
                round_number=pending_round,
                message="evidence recovery patch hash changed; submit a new quality round",
            )
        if request.round_number != pending_round:
            return self._contract_outcome(
                round_number=pending_round,
                message="evidence recovery must keep the pending quality round number",
            )
        attempt = (
            request.evidence_attempt
            if request.evidence_attempt is not None
            else self.state.pending_evidence_attempts + 1
        )
        if attempt != self.state.pending_evidence_attempts + 1 or attempt > MAX_EVIDENCE_RECOVERY_ATTEMPTS:
            return self._contract_outcome(
                round_number=pending_round,
                message="evidence recovery attempt limit exceeded",
                evidence_attempt=attempt,
        )
        changes = tuple(_coerce_file_change(item) for item in request.effective_changed_files())
        try:
            effective_changes = validate_exemption_registry(
                changes,
                request.exemptions,
                round_number=request.round_number,
            )
        except ContractFailure as exc:
            return self._contract_outcome(
                round_number=pending_round,
                message=str(exc),
                evidence_attempt=attempt,
            )
        production_paths = tuple(sorted({item.path for item in effective_changes if item.production}))
        required_paths = tuple(
            sorted(
                {
                    item.path
                    for item in effective_changes
                    if item.production and item.exemption_kind is None
                }
            )
        )
        if (
            production_paths != self.state.pending_evidence_files
            or required_paths != self.state.pending_evidence_required_files
        ):
            return self._patch_changed_during_recovery(
                round_number=pending_round,
                message="evidence recovery changed production file scope; submit a new quality round",
            )
        mode_value = (
            request.mode.value
            if isinstance(request.mode, RunMode)
            else str(request.mode).strip().lower().replace("-", "_")
        )
        if mode_value not in (RunMode.WITH_LSP.value, "lsp"):
            return self._contract_outcome(
                round_number=pending_round,
                message="evidence recovery requires with_lsp mode",
                evidence_attempt=attempt,
            )

        pending_source = self.state.pending_evidence_source or "trajectory"
        if pending_source == "server_receipts":
            executor_projection = (
                not request.has_canonical_receipt_source()
                and request.mcp_trajectory is not None
            )
            if not request.has_canonical_receipt_source() and not executor_projection:
                return self._contract_outcome(
                    round_number=pending_round,
                    message=(
                        "receipt-backed evidence recovery requires canonical receipts "
                        "or executor-attested MCP projection"
                    ),
                    evidence_attempt=attempt,
                )
            if request.task_id != self.state.pending_evidence_task_id:
                return self._contract_outcome(
                    round_number=pending_round,
                    message="evidence recovery task id changed from canonical receipt source",
                    evidence_attempt=attempt,
                )
            if not executor_projection:
                current_generation = _canonical_receipt_generation(request.server_receipts)
                if not current_generation:
                    return self._contract_outcome(
                        round_number=pending_round,
                        message="evidence recovery receipts are missing server generation",
                        evidence_attempt=attempt,
                    )
                if request.server_generation != current_generation:
                    return self._contract_outcome(
                        round_number=pending_round,
                        message="evidence recovery request/receipt server generation drifted",
                        evidence_attempt=attempt,
                    )
        elif request.has_canonical_receipt_source():
            return self._contract_outcome(
                round_number=pending_round,
                message="trajectory-backed evidence recovery cannot switch to server receipts",
                evidence_attempt=attempt,
            )

        if request.has_canonical_receipt_source():
            canonical_covered, missing, contract_errors, infrastructure_error = (
                _canonical_receipt_coverage_observation(request, changes)
            )
            if infrastructure_error:
                return self._infrastructure_recovery_outcome(
                    round_number=pending_round,
                    covered=canonical_covered,
                    missing=missing,
                    message=infrastructure_error,
                )
            if contract_errors:
                return self._contract_outcome(
                    round_number=pending_round,
                    message=contract_errors[0],
                    covered=canonical_covered,
                    missing=missing,
                    evidence_attempt=attempt,
                )
            covered = set(self.state.pending_evidence_covered_files) | set(canonical_covered)
            missing = tuple(sorted(set(self.state.pending_evidence_required_files) - covered))
            actions = _required_evidence_actions(missing)
            self.state.pending_evidence_attempts = attempt
        else:
            trajectory = request.effective_trajectory()
            invalid_tools = _trajectory_non_lsp_tools(trajectory)
            if invalid_tools:
                return self._contract_outcome(
                    round_number=pending_round,
                    message="evidence recovery permits only structure/xref/diagnostics: "
                    + ", ".join(invalid_tools),
                    evidence_attempt=attempt,
                )
            covered_now: set[str] = set()
            for event in mcp_trajectory_events(trajectory):
                if _is_diagnostics_event(event):
                    covered_now.update(_diagnostics_paths(event))
            covered_now = set(
                _align_diagnostics_paths(
                    covered_now, self.state.pending_evidence_required_files
                )
            )
            covered = set(self.state.pending_evidence_covered_files) | covered_now
            missing = tuple(sorted(set(self.state.pending_evidence_required_files) - covered))
            actions = _required_evidence_actions(missing)
            self.state.pending_evidence_attempts = attempt
            semantic_errors = _legacy_semantic_completion_errors(
                effective_changes,
                request.mode,
                trajectory,
                request.exemptions,
                round_number=request.round_number,
            )
            if semantic_errors:
                return self._contract_outcome(
                    round_number=pending_round,
                    message=semantic_errors[0],
                    covered=covered,
                    missing=missing,
                    required_actions=actions,
                    evidence_attempt=attempt,
                )
        if missing:
            record = EvidenceRecoveryRecord(
                round_number=pending_round,
                evidence_attempt=attempt,
                arm_id=self.state.arm_id or request.arm_id,
                session_id=self.state.session_id or request.session_id,
                thread_id=self.state.thread_id or request.thread_id,
                code_agent_id=self.state.code_agent_id or request.code_agent_id,
                patch_hash=patch_hash,
                changed_production_files=production_paths,
                diagnostics_covered_files=tuple(sorted(covered)),
                missing_files=missing,
                required_actions=actions,
                decision=RoundDecision.CONTRACT_FAILURE.value,
                contract_failures=(
                    "evidence recovery diagnostics coverage still missing for: "
                    + ", ".join(missing),
                ),
            )
            self.state.evidence_attempts.append(record)
            return self._contract_outcome(
                round_number=pending_round,
                message=record.contract_failures[0],
                covered=covered,
                missing=missing,
                required_actions=actions,
                evidence_attempt=attempt,
            )
        try:
            validated_failures = validate_failure_separation(
                request.failures, code_agent_id=request.code_agent_id
            )
        except ContractFailure as exc:
            validated_failures = ()
            record = EvidenceRecoveryRecord(
                round_number=pending_round,
                evidence_attempt=attempt,
                arm_id=self.state.arm_id or request.arm_id,
                session_id=self.state.session_id or request.session_id,
                thread_id=self.state.thread_id or request.thread_id,
                code_agent_id=self.state.code_agent_id or request.code_agent_id,
                patch_hash=patch_hash,
                changed_production_files=production_paths,
                diagnostics_covered_files=tuple(sorted(covered)),
                missing_files=(),
                required_actions=(),
                decision=RoundDecision.CONTRACT_FAILURE.value,
                contract_failures=(str(exc),),
            )
            self.state.evidence_attempts.append(record)
            return self._contract_outcome(
                round_number=pending_round,
                message=str(exc),
                covered=covered,
                evidence_attempt=attempt,
            )

        self.state.evidence_attempts.append(
            EvidenceRecoveryRecord(
                round_number=pending_round,
                evidence_attempt=attempt,
                arm_id=self.state.arm_id or request.arm_id,
                session_id=self.state.session_id or request.session_id,
                thread_id=self.state.thread_id or request.thread_id,
                code_agent_id=self.state.code_agent_id or request.code_agent_id,
                patch_hash=patch_hash,
                changed_production_files=production_paths,
                diagnostics_covered_files=tuple(sorted(covered)),
                missing_files=(),
                required_actions=(),
                decision="evidence_recovery_succeeded",
            )
        )
        self._clear_pending_evidence()
        return self._record_quality_round(
            request,
            patch_hash=patch_hash,
            changes=changes,
            covered=tuple(sorted(covered)),
            validated_failures=validated_failures,
            evidence_attempt=attempt,
            evidence_recovery_succeeded=True,
            lsp_self_check=_lsp_self_check(
                changes,
                request.server_receipts
                if request.has_canonical_receipt_source()
                and request.server_receipts is not None
                else request.effective_trajectory(),
                covered,
                request.exemptions,
                round_number=pending_round,
            ),
        )

    def evaluate_round(self, request: RoundInput) -> RoundOutcome:
        """Gate one quality round or one evidence-only recovery attempt."""

        explicit_recovery = request.is_evidence_recovery()
        pending_same_round = (
            self.state.pending_evidence_round is not None
            and request.round_number == self.state.pending_evidence_round
            and not request.action.strip().lower().startswith("start")
        )
        if explicit_recovery or pending_same_round:
            try:
                return self._evaluate_evidence_recovery(request)
            except SessionIdentityError:
                self.state.status = RunnerStatus.CONTRACT_FAILURE.value
                self._clear_pending_evidence()
                self._persist()
                raise

        infra_retry = self._is_infrastructure_retry(request)
        self._validate_sequence(request)
        if infra_retry:
            self._validate_recovery_identity(request)
        else:
            self._validate_identity(request)
        patch_hash = _required_text(request.patch_hash, "patch hash")
        if infra_retry:
            record = self.state.rounds[-1]
            record.grader_allowed = True
            self.state.status = RunnerStatus.ACTIVE.value
            self._persist()
            return RoundOutcome(
                round_number=record.round_number,
                decision=record.decision,
                grader_allowed=True,
                no_progress=record.no_progress,
                no_progress_streak=record.no_progress_streak,
                relocalization_required=False,
                terminal=False,
                diagnostics_covered_files=record.diagnostics_covered_files,
                contract_failures=record.contract_failures,
                lsp_self_check=dict(record.lsp_self_check),
            )

        changes = tuple(_coerce_file_change(item) for item in request.effective_changed_files())
        infrastructure_error: str | None = None
        audit_trajectory: Sequence[Any] = tuple(request.effective_trajectory())
        if request.has_canonical_receipt_source():
            audit_trajectory = tuple(request.server_receipts or ())
            (
                covered,
                missing,
                coverage_errors,
                infrastructure_error,
            ) = _canonical_receipt_coverage_observation(request, changes)
            if infrastructure_error:
                coverage_errors = (*coverage_errors, infrastructure_error)
        else:
            covered, missing, coverage_errors = _diagnostics_coverage_observation(
                changes,
                request.mode,
                audit_trajectory,
                request.exemptions,
                round_number=request.round_number,
            )

        if infrastructure_error is None and not request.has_canonical_receipt_source():
            if _transport_failure_declared(request) and not coverage_errors:
                infrastructure_error = "runner transport failed before the quality round completed"

        if infrastructure_error:
            return self._record_infrastructure_round(
                request,
                patch_hash=patch_hash,
                changes=changes,
                covered=covered,
                missing=missing,
                message=infrastructure_error,
                lsp_self_check=_lsp_self_check(
                    changes,
                    audit_trajectory,
                    covered,
                    request.exemptions,
                    round_number=request.round_number,
                ),
            )

        self_check = _lsp_self_check(
            changes,
            audit_trajectory,
            covered,
            request.exemptions,
            round_number=request.round_number,
        )
        contract_errors = list(coverage_errors)
        semantic_errors: tuple[str, ...] = ()
        if not request.has_canonical_receipt_source():
            semantic_errors = _legacy_semantic_completion_errors(
                changes,
                request.mode,
                audit_trajectory,
                request.exemptions,
                round_number=request.round_number,
            )
        try:
            validated_failures = validate_failure_separation(
                request.failures, code_agent_id=request.code_agent_id
            )
        except ContractFailure as exc:
            validated_failures = ()
            contract_errors.append(str(exc))
        mode_value = (
            request.mode.value
            if isinstance(request.mode, RunMode)
            else str(request.mode).strip().lower().replace("-", "_")
        )
        if (
            mode_value in (RunMode.WITH_LSP.value, "lsp")
            and (missing or semantic_errors)
            and not contract_errors
        ):
            return self._start_evidence_recovery(
                request,
                changes,
                covered,
                missing,
                lsp_self_check=self_check,
            )
        return self._record_quality_round(
            request,
            patch_hash=patch_hash,
            changes=changes,
            covered=covered,
            missing=missing,
            contract_errors=contract_errors,
            validated_failures=validated_failures,
            lsp_self_check=self_check,
        )

    # Short aliases make the integration seam easy to discover without
    # creating a second state machine.
    check_round = evaluate_round
    process_round = evaluate_round

    def run_grader(self, outcome: RoundOutcome, grader: Callable[[RoundOutcome], Any]) -> GraderOutcome:
        """Run a grader only for an explicitly allowed round.

        A grader exception is recorded as infrastructure failure and is never
        turned into model-quality feedback.  A false grader result is the
        model-quality channel and does not mark the round accepted.
        """

        if not outcome.grader_allowed:
            raise GraderBlocked(
                f"grader blocked for round {outcome.round_number}: {outcome.decision}"
            )
        if outcome.round_number != self.state.last_round:
            raise GraderBlocked("grader outcome is not for the current round")
        record = self.state.rounds[-1]
        if record.grader_started:
            raise GraderBlocked(f"grader already started for round {outcome.round_number}")
        record.grader_started = True
        record.infra_attempts += 1
        try:
            raw = grader(outcome)
        except Exception as exc:  # noqa: BLE001 - boundary must classify, not leak channels.
            # Keep the quality round open.  The same outcome (or an explicit
            # same-round infrastructure retry) may be graded again without
            # consuming another quality-round budget slot.
            record.grader_started = False
            record.failure_kind = FailureKind.INFRASTRUCTURE.value
            record.grader_passed = None
            self.state.status = RunnerStatus.INFRASTRUCTURE_FAILURE.value
            self.state.failures.append(
                FailureRecord(
                    kind=FailureKind.INFRASTRUCTURE,
                    message=str(exc) or exc.__class__.__name__,
                    source_agent_id="grader-infrastructure",
                )
            )
            self._persist()
            return GraderOutcome(
                round_number=outcome.round_number,
                started=True,
                passed=None,
                failure_kind=FailureKind.INFRASTRUCTURE.value,
                quality_accepted=False,
                runner_status=self.state.status,
            )
        try:
            passed, failure_kind = _parse_grader_result(raw)
        except ContractFailure as exc:
            record.grader_started = False
            record.failure_kind = FailureKind.INFRASTRUCTURE.value
            record.grader_passed = None
            self.state.status = RunnerStatus.INFRASTRUCTURE_FAILURE.value
            self.state.failures.append(
                FailureRecord(
                    kind=FailureKind.INFRASTRUCTURE,
                    message=str(exc),
                    source_agent_id="grader-infrastructure",
                )
            )
            self._persist()
            return GraderOutcome(
                round_number=outcome.round_number,
                started=True,
                passed=None,
                failure_kind=FailureKind.INFRASTRUCTURE.value,
                quality_accepted=False,
                runner_status=self.state.status,
            )
        if failure_kind == FailureKind.INFRASTRUCTURE.value:
            record.failure_kind = failure_kind
            record.grader_started = False
            record.grader_passed = None
            self.state.status = RunnerStatus.INFRASTRUCTURE_FAILURE.value
            self.state.failures.append(
                FailureRecord(
                    kind=FailureKind.INFRASTRUCTURE,
                    message="grader reported infrastructure failure",
                    source_agent_id="grader-infrastructure",
                )
            )
            self._persist()
            return GraderOutcome(
                outcome.round_number,
                True,
                None,
                failure_kind,
                False,
                self.state.status,
            )
        if passed:
            record.quality_accepted = True
            record.failure_kind = None
            record.grader_started = True
            record.grader_passed = True
            self.state.quality_rounds_used += 1
            self.state.last_quality_patch_hash = record.patch_hash
            self.state.status = RunnerStatus.SUCCEEDED.value
        else:
            record.failure_kind = FailureKind.MODEL_QUALITY.value
            record.grader_started = True
            record.grader_passed = False
            self.state.quality_rounds_used += 1
            self.state.last_quality_patch_hash = record.patch_hash
            self.state.status = (
                RunnerStatus.MAX_ROUNDS_REACHED.value
                if record.round_number == self.state.max_rounds
                else RunnerStatus.ACTIVE.value
            )
            self.state.failures.append(
                FailureRecord(
                    kind=FailureKind.MODEL_QUALITY,
                    message="grader rejected the production patch",
                    source_agent_id="grader-quality",
                    feedback_target_agent_id=self.state.code_agent_id,
                )
            )
        self._persist()
        return GraderOutcome(
            round_number=outcome.round_number,
            started=True,
            passed=passed,
            failure_kind=FailureKind.MODEL_QUALITY.value if not passed else None,
            quality_accepted=passed,
            runner_status=self.state.status,
        )

    def mark_unverified(self, message: str, *, source_agent_id: str = "runner") -> None:
        """Close an unresolved execution without making it scoreable.

        Infrastructure exhaustion is neither a model-quality failure nor a
        successful quality result.  The runner uses this terminal state after
        the bounded same-round retry budget is exhausted so a later scorer can
        fail closed instead of treating the arm as an ordinary failed patch.
        """

        if self.state.status not in (
            RunnerStatus.INFRASTRUCTURE_FAILURE.value,
            RunnerStatus.ACTIVE.value,
        ):
            raise RoundSequenceError(f"cannot mark runner unverified from {self.state.status}")
        self.state.status = RunnerStatus.UNVERIFIED.value
        self.state.failures.append(
            FailureRecord(
                kind=FailureKind.INFRASTRUCTURE,
                message=_required_text(message, "unverified reason"),
                source_agent_id=_required_text(source_agent_id, "unverified source agent"),
            )
        )
        self._persist()


def _parse_grader_result(value: Any) -> tuple[bool, str | None]:
    if isinstance(value, Mapping):
        if "passed" not in value:
            raise ContractFailure("grader result missing passed")
        passed = bool(value["passed"])
        failure = value.get("failure_kind", value.get("kind"))
        if failure is not None:
            failure = str(failure).strip().lower()
            if failure not in (FailureKind.INFRASTRUCTURE.value, FailureKind.MODEL_QUALITY.value):
                raise ContractFailure(f"unsupported grader failure kind: {failure}")
        return passed, failure
    if isinstance(value, bool):
        return value, None
    raise ContractFailure("grader must return bool or {passed, failure_kind}")


def _load_request(path: str) -> RoundInput:
    try:
        value = json.loads(pathlib.Path(path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise GuardError(f"read round request: {exc}") from exc
    if not isinstance(value, Mapping):
        raise GuardError("round request must be a JSON object")
    allowed = {field.name for field in dataclasses.fields(RoundInput)}
    payload = {key: value[key] for key in value if key in allowed}
    missing = [key for key in ("round_number", "arm_id", "session_id", "thread_id", "patch_hash") if key not in payload]
    if missing:
        raise GuardError("round request missing: " + ", ".join(missing))
    return RoundInput(**payload)


def main(argv: Sequence[str] | None = None) -> int:
    """Small audit-only integration CLI; it never starts a grader."""

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state", required=True, help="durable JSON state path")
    parser.add_argument("--request", required=True, help="JSON RoundInput object")
    args = parser.parse_args(argv)
    guard = ClosedLoopGuard.from_store(args.state)
    try:
        outcome = guard.evaluate_round(_load_request(args.request))
    except GuardError as exc:
        print(json.dumps({"ok": False, "error": str(exc)}, ensure_ascii=False))
        return 2
    print(json.dumps(dataclasses.asdict(outcome), ensure_ascii=False, sort_keys=True))
    return 0 if outcome.grader_allowed else 1


if __name__ == "__main__":
    raise SystemExit(main())
