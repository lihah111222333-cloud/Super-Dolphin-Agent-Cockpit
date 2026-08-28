#!/usr/bin/env python3
"""Durable orchestration for one Cross-File A/B arm.

The closed-loop guard owns the quality-round contract.  This module owns the
runner lifecycle around it: a quality round may have an evidence-recovery
child event and an infrastructure-retry child event, but neither child may
advance the five-round quality budget.  Every transition is persisted before
the caller is allowed to continue, so a process restart can resume a pending
evidence or infrastructure action with the original identity and patch.

The runner intentionally does not start a model, an MCP server, or a grader.
Callers provide those integration boundaries as ``RoundInput`` values and a
grader callback.  This keeps the state machine testable and prevents a
transport failure from being mistaken for a model-quality result.
"""

from __future__ import annotations

import dataclasses
import json
import os
import pathlib
import tempfile
from collections.abc import Callable, Mapping, Sequence
from typing import Any

try:  # Package import when used by repository tests.
    from .cross_file_closed_loop_guard import (
        ClosedLoopGuard,
        FailureKind,
        GraderOutcome,
        RoundDecision,
        RoundInput,
        RoundOutcome,
        RunnerStatus,
    )
    from .grader_feedback import (
        FailureKind as FeedbackFailureKind,
        GraderFeedback,
        GraderFeedbackError,
        Status as FeedbackStatus,
        validate_grader_feedback,
    )
except ImportError:  # Direct ``python scripts/...py`` integration.
    from cross_file_closed_loop_guard import (  # type: ignore[no-redef]
        ClosedLoopGuard,
        FailureKind,
        GraderOutcome,
        RoundDecision,
        RoundInput,
        RoundOutcome,
        RunnerStatus,
    )
    from scripts.grader_feedback import (  # type: ignore[no-redef]
        FailureKind as FeedbackFailureKind,
        GraderFeedback,
        GraderFeedbackError,
        Status as FeedbackStatus,
        validate_grader_feedback,
    )


MAX_QUALITY_ROUNDS = 3
DEFAULT_MAX_INFRA_RETRIES = 1
EVENT_SCHEMA_VERSION = 2
EVENT_KINDS = frozenset(
    {"quality_round", "evidence_recovery", "infra_retry", "grader_result", "terminal"}
)
SCOREABLE_STATUSES = frozenset(
    {RunnerStatus.SUCCEEDED.value, RunnerStatus.MAX_ROUNDS_REACHED.value}
)
INFRA_FEEDBACK_KINDS = frozenset(
    {
        FeedbackFailureKind.AGENT_TRANSPORT_FAILURE.value,
        FeedbackFailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
        FeedbackFailureKind.TEST_HARNESS_FAILURE.value,
        FeedbackFailureKind.PATCH_APPLY_FAILURE.value,
        FeedbackFailureKind.TIMEOUT.value,
        FeedbackFailureKind.INFRASTRUCTURE_ERROR.value,
    }
)
NON_QUALITY_INFRA_FEEDBACK_KINDS = frozenset(
    {
        FeedbackFailureKind.AGENT_TRANSPORT_FAILURE.value,
        FeedbackFailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
        FeedbackFailureKind.TEST_HARNESS_FAILURE.value,
    }
)


def _marker(value: Any) -> bool | None:
    """Parse an explicit boolean marker without guessing from arbitrary data."""

    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        normalized = value.strip().lower()
        if normalized in {"true", "1", "yes"}:
            return True
        if normalized in {"false", "0", "no"}:
            return False
    return None


def _feedback_sources(value: Mapping[str, Any]) -> tuple[Mapping[str, Any], ...]:
    nested = value.get("grader_feedback")
    if isinstance(nested, Mapping):
        return nested, value
    return (value,)


def _source_marker(sources: Sequence[Mapping[str, Any]], key: str) -> bool | None:
    for source in sources:
        if key not in source:
            continue
        parsed = _marker(source[key])
        if parsed is not None:
            return parsed
    return None


def _source_value(
    sources: Sequence[Mapping[str, Any]], key: str, default: Any = None
) -> Any:
    """Return the first explicitly supplied value across nested envelopes."""

    for source in sources:
        if key in source and source[key] is not None:
            return source[key]
    return default


def _source_ids(sources: Sequence[Mapping[str, Any]], key: str) -> set[str] | None:
    for source in sources:
        if key not in source:
            continue
        value = source[key]
        if not isinstance(value, Sequence) or isinstance(value, (str, bytes, bytearray)):
            return None
        if any(not isinstance(item, str) for item in value):
            return None
        return {item.strip() for item in value if item.strip()}
    return None


def _identity_value(
    sources: Sequence[Mapping[str, Any]], key: str, fallback: str
) -> Any:
    for source in sources:
        identity = source.get("identity")
        if isinstance(identity, Mapping) and key in identity:
            return identity[key]
        if key in source:
            return source[key]
    return fallback


def _completed_unresolved_model_feedback(
    value: Any,
    *,
    question_id: str,
    arm_id: str,
    session_id: str,
    thread_id: str,
    patch_hash: str,
) -> Any:
    """Promote an executed-but-unresolved official result to model feedback.

    SWE-bench-style reports often carry ``completed=true`` and
    ``resolved=false`` without a pytest assertion.  Completion proves that
    the official grader ran; unresolved is still a failed candidate result,
    not missing infrastructure.  Return a typed object so the normal identity
    and outcome validator remains the final authority.
    """

    if not isinstance(value, Mapping):
        return value
    sources = _feedback_sources(value)
    resolved = _source_marker(sources, "resolved")
    completed = _source_marker(sources, "completed")
    question = str(question_id).strip()
    if resolved is None:
        resolved_ids = _source_ids(sources, "resolved_ids")
        unresolved_ids = _source_ids(sources, "unresolved_ids")
        if resolved_ids is not None and question in resolved_ids:
            resolved = True
        elif unresolved_ids is not None and question in unresolved_ids:
            resolved = False
        elif resolved_ids is not None and not resolved_ids:
            resolved = False
    if completed is None:
        completed_ids = _source_ids(sources, "completed_ids")
        if completed_ids is not None and question in completed_ids:
            completed = True
    if completed is not True or resolved is not False:
        return value

    # Explicit grader-side errors outrank the unresolved model outcome.  A
    # completed invocation can still have failed before it produced a valid
    # candidate result (for example timeout or patch application failure).
    for source in sources:
        error_ids = source.get("error_ids")
        if isinstance(error_ids, Sequence) and not isinstance(
            error_ids, (str, bytes, bytearray)
        ) and error_ids:
            return value
        if any(source.get(marker) is True for marker in (
            "infrastructure_error", "timeout", "patch_apply_failed"
        )):
            return value

    declared_kind = _source_marker(sources, "passed")
    if declared_kind is True:
        # Let the ordinary validator report the contradictory pass/unresolved
        # declaration instead of silently overriding official evidence.
        return value
    for source in sources:
        raw_kind = source.get("grader_failure_kind", source.get("failure_kind"))
        if raw_kind not in (None, "", FeedbackFailureKind.FEEDBACK_UNAVAILABLE.value):
            return value
        raw_status = source.get("status")
        if isinstance(raw_status, str) and raw_status.strip().lower() in {
            "passed", "pass", "success", "succeeded", "green"
        }:
            return value

    missing_symbols = _source_value(sources, "missing_symbols", ())
    if isinstance(missing_symbols, Sequence) and not isinstance(
        missing_symbols, (str, bytes, bytearray)
    ):
        missing_symbols = tuple(
            item.strip() for item in missing_symbols if isinstance(item, str) and item.strip()
        )
    else:
        missing_symbols = ()
    failing_test_ids = _source_value(sources, "failing_test_ids", ())
    if isinstance(failing_test_ids, Sequence) and not isinstance(
        failing_test_ids, (str, bytes, bytearray)
    ):
        failing_test_ids = tuple(
            item.strip() for item in failing_test_ids if isinstance(item, str) and item.strip()
        )
    else:
        failing_test_ids = ()
    phase = _source_value(sources, "phase")
    phase = str(phase).strip() if isinstance(phase, str) and phase.strip() else "test"
    schema = _source_value(sources, "schema")
    schema = str(schema).strip() if isinstance(schema, str) and schema.strip() else ""
    return GraderFeedback(
        schema=schema or GraderFeedback.schema,
        status=FeedbackStatus.FAILED.value,
        failure_kind=FeedbackFailureKind.MODEL_TEST_FAILURE.value,
        failing_test_ids=failing_test_ids,
        question_id=_identity_value(sources, "question_id", question_id),
        arm_id=_identity_value(sources, "arm_id", arm_id),
        session_id=_identity_value(sources, "session_id", session_id),
        thread_id=_identity_value(sources, "thread_id", thread_id),
        patch_hash=_identity_value(sources, "patch_hash", patch_hash),
        phase=phase,
        collected_count=_source_value(sources, "collected_count"),
        executed_count=_source_value(sources, "executed_count"),
        missing_symbols=missing_symbols,
        official_resolved=False,
        official_completed=True,
        structured=True,
        passed=False,
        code_repair_eligible=True,
        evidence=("official grader completed with resolved=false",),
    )


def _promote_candidate_missing_symbol(feedback: GraderFeedback) -> GraderFeedback:
    """Treat a repository-owned missing symbol as repairable model feedback."""

    if not feedback.missing_symbols or feedback.failure_kind not in {
        FeedbackFailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
        FeedbackFailureKind.FEEDBACK_UNAVAILABLE.value,
    }:
        return feedback
    return dataclasses.replace(
        feedback,
        status=FeedbackStatus.FAILED.value,
        failure_kind=FeedbackFailureKind.MODEL_TEST_FAILURE.value,
        phase="test",
        structured=True,
        passed=False,
        code_repair_eligible=True,
    )


def _promote_official_model_feedback(feedback: GraderFeedback) -> GraderFeedback:
    """Apply official resolved/completed truth to an already typed result."""

    if (
        feedback.official_resolved is not False
        or feedback.official_completed is not True
        or feedback.failure_kind == FeedbackFailureKind.MODEL_TEST_FAILURE.value
    ):
        return feedback
    if feedback.failure_kind not in {
        "",
        FeedbackFailureKind.FEEDBACK_UNAVAILABLE.value,
        FeedbackFailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
    }:
        return feedback
    evidence = tuple(feedback.evidence)
    marker = "official grader completed with resolved=false"
    if marker not in evidence:
        evidence = (*evidence, marker)
    return dataclasses.replace(
        feedback,
        status=FeedbackStatus.FAILED.value,
        failure_kind=FeedbackFailureKind.MODEL_TEST_FAILURE.value,
        phase=feedback.phase or "test",
        structured=True,
        passed=False,
        code_repair_eligible=True,
        evidence=evidence,
    )


class RunnerError(RuntimeError):
    """Base class for runner lifecycle errors."""


class RunnerStateError(RunnerError):
    """Persisted runner event state is malformed or incomplete."""


class PendingActionError(RunnerError):
    """The caller must finish a same-round child action first."""


class RunnerProtocolError(RunnerError):
    """The integration boundary omitted a required runner input."""


def _required_text(value: Any, label: str) -> str:
    text = str(value or "").strip()
    if not text:
        raise RunnerStateError(f"{label} is required")
    return text


def _atomic_write(path: pathlib.Path, payload: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(
        prefix=f".{path.name}.", suffix=".tmp", dir=path.parent
    )
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


@dataclasses.dataclass(frozen=True)
class RunnerEvent:
    """One append-only state-machine event.

    ``round_number`` is the quality round number.  Recovery and retry events
    deliberately reuse it and point to their parent event instead of creating
    a synthetic sixth/seventh quality round.
    """

    sequence: int
    kind: str
    round_number: int
    attempt: int
    status: str
    arm_id: str
    question_id: str
    session_id: str
    thread_id: str
    code_agent_id: str
    patch_hash: str
    decision: str | None = None
    grader_started: bool = False
    grader_passed: bool | None = None
    failure_kind: str | None = None
    feedback_failure_kind: str | None = None
    evidence_recovery_succeeded: bool = False
    quality_round_consumed: bool = False
    parent_sequence: int | None = None
    scoreable: bool = False

    def __post_init__(self) -> None:
        if self.sequence < 1:
            raise RunnerStateError("event sequence must be positive")
        if self.kind not in EVENT_KINDS:
            raise RunnerStateError(f"unsupported runner event kind: {self.kind}")
        if self.round_number < 1:
            raise RunnerStateError("event round number must be positive")
        if self.attempt < 0:
            raise RunnerStateError("event attempt cannot be negative")
        for label, value in (
            ("event status", self.status),
            ("event arm id", self.arm_id),
            ("event question id", self.question_id),
            ("event session id", self.session_id),
            ("event thread id", self.thread_id),
            ("event code agent id", self.code_agent_id),
            ("event patch hash", self.patch_hash),
        ):
            _required_text(value, label)

    def to_dict(self) -> dict[str, Any]:
        return dataclasses.asdict(self)

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "RunnerEvent":
        allowed = {field.name for field in dataclasses.fields(cls)}
        unknown = sorted(str(key) for key in value if key not in allowed)
        if unknown:
            raise RunnerStateError("runner event has unknown field(s): " + ", ".join(unknown))
        try:
            return cls(
                sequence=int(value["sequence"]),
                kind=str(value["kind"]),
                round_number=int(value["round_number"]),
                attempt=int(value.get("attempt", 0)),
                status=str(value["status"]),
                arm_id=str(value["arm_id"]),
                question_id=str(value["question_id"]),
                session_id=str(value["session_id"]),
                thread_id=str(value["thread_id"]),
                code_agent_id=str(value["code_agent_id"]),
                patch_hash=str(value["patch_hash"]),
                decision=(None if value.get("decision") is None else str(value["decision"])),
                grader_started=bool(value.get("grader_started", False)),
                grader_passed=value.get("grader_passed"),
                failure_kind=(
                    None if value.get("failure_kind") is None else str(value["failure_kind"])
                ),
                feedback_failure_kind=(
                    None
                    if value.get("feedback_failure_kind") is None
                    else str(value["feedback_failure_kind"])
                ),
                evidence_recovery_succeeded=bool(
                    value.get("evidence_recovery_succeeded", False)
                ),
                quality_round_consumed=bool(value.get("quality_round_consumed", False)),
                parent_sequence=(
                    None
                    if value.get("parent_sequence") is None
                    else int(value["parent_sequence"])
                ),
                scoreable=bool(value.get("scoreable", False)),
            )
        except (KeyError, TypeError, ValueError, RunnerStateError) as exc:
            raise RunnerStateError(f"invalid runner event: {exc}") from exc


class RunnerEventStore:
    """Atomic JSON persistence for the runner's append-only event envelope."""

    def __init__(self, path: str | os.PathLike[str]):
        self.path = pathlib.Path(path)

    def load(self) -> list[RunnerEvent]:
        try:
            raw = json.loads(self.path.read_text(encoding="utf-8"))
        except FileNotFoundError:
            return []
        except (OSError, UnicodeError, json.JSONDecodeError) as exc:
            raise RunnerStateError(f"read runner events: {exc}") from exc
        if not isinstance(raw, Mapping) or raw.get("schema_version") != EVENT_SCHEMA_VERSION:
            raise RunnerStateError("unsupported runner event schema")
        values = raw.get("events")
        if not isinstance(values, Sequence) or isinstance(values, (str, bytes, bytearray)):
            raise RunnerStateError("runner events must be an array")
        events = [RunnerEvent.from_dict(item) for item in values if isinstance(item, Mapping)]
        if len(events) != len(values):
            raise RunnerStateError("runner event entries must be objects")
        expected = tuple(range(1, len(events) + 1))
        actual = tuple(event.sequence for event in events)
        if actual != expected:
            raise RunnerStateError("runner event sequence is not contiguous")
        return events

    def save(self, events: Sequence[RunnerEvent]) -> None:
        payload = json.dumps(
            {
                "schema_version": EVENT_SCHEMA_VERSION,
                "events": [event.to_dict() for event in events],
            },
            ensure_ascii=False,
            sort_keys=True,
            indent=2,
        ) + "\n"
        _atomic_write(self.path, payload)


@dataclasses.dataclass(frozen=True)
class RunnerSnapshot:
    """Score and lifecycle facts exposed to an integration scheduler."""

    status: str
    terminal: bool
    scoreable: bool
    passed: bool
    last_round: int
    quality_rounds_used: int
    max_rounds: int
    pending_action: str | None
    pending_round: int | None
    infra_attempts: int

    def to_dict(self) -> dict[str, Any]:
        return dataclasses.asdict(self)


@dataclasses.dataclass(frozen=True)
class RunnerStep:
    """Result of one runner call and the next required child action."""

    outcome: RoundOutcome | None
    grader: GraderOutcome | None
    event_sequences: tuple[int, ...]
    needs_evidence_recovery: bool
    needs_infra_retry: bool
    terminal: bool
    scoreable: bool
    feedback: GraderFeedback | None = None

    @property
    def status(self) -> str:
        if self.grader is not None:
            return self.grader.runner_status
        if self.outcome is not None:
            if self.outcome.failure_kind == FailureKind.INFRASTRUCTURE.value:
                return RunnerStatus.INFRASTRUCTURE_FAILURE.value
            return RunnerStatus.ACTIVE.value if not self.terminal else (
                RunnerStatus.CONTRACT_FAILURE.value
                if self.outcome.decision == RoundDecision.CONTRACT_FAILURE.value
                else RunnerStatus.RELOCALIZE_REQUIRED.value
            )
        return RunnerStatus.UNVERIFIED.value

    @property
    def code_repair_eligible(self) -> bool:
        """Whether the official result authorizes a next-round code repair."""

        return bool(self.feedback and self.feedback.can_enter_code_repair_round())

    @property
    def repair_eligible(self) -> bool:
        """Compatibility alias for schedulers using the shorter field name."""

        return self.code_repair_eligible

    @property
    def missing_symbols(self) -> tuple[str, ...]:
        """Expose concrete candidate/API symbols from repair feedback."""

        return tuple(self.feedback.missing_symbols) if self.feedback else ()

    @property
    def lsp_self_check(self) -> Mapping[str, Any]:
        """Expose the round's non-authoritative LSP audit to the scheduler."""

        return self.outcome.lsp_self_check if self.outcome is not None else {}


GraderCallable = Callable[[RoundOutcome], Any]


class FiveRoundRunner:
    """Orchestrate one arm without allowing hidden quality-round increments."""

    def __init__(
        self,
        state_path: str | os.PathLike[str],
        *,
        event_path: str | os.PathLike[str] | None = None,
        max_infra_retries: int = DEFAULT_MAX_INFRA_RETRIES,
        guard: ClosedLoopGuard | None = None,
        question_id: str = "",
    ) -> None:
        if max_infra_retries < 0:
            raise RunnerProtocolError("max_infra_retries cannot be negative")
        self.state_path = pathlib.Path(state_path)
        self.question_id = str(question_id or "").strip()
        self.guard = guard or ClosedLoopGuard.from_store(self.state_path)
        if self.guard.state.max_rounds != MAX_QUALITY_ROUNDS:
            raise RunnerProtocolError(
                f"runner requires exactly {MAX_QUALITY_ROUNDS} quality rounds"
            )
        self.max_infra_retries = max_infra_retries
        self.event_store = RunnerEventStore(
            event_path
            or self.state_path.with_name(self.state_path.stem + ".events.json")
        )
        self.events = self.event_store.load()
        if not self.question_id and self.events:
            self.question_id = _required_text(self.events[-1].question_id, "question id")
        if not self.question_id:
            raise RunnerStateError("question id is required")
        if self.guard.state.last_round and not self.events:
            raise RunnerStateError("quality state exists without runner event history")
        if self.events and self.events[-1].status != self.guard.state.status:
            # A grader event may be the last persisted transition; permitting
            # an older event status would make restart classification ambiguous.
            raise RunnerStateError("runner event tail disagrees with guard status")

    @classmethod
    def from_store(
        cls,
        state_path: str | os.PathLike[str],
        **kwargs: Any,
    ) -> "FiveRoundRunner":
        return cls(state_path, **kwargs)

    @property
    def state(self) -> Any:
        """Return the guard state for read-only scheduler inspection."""

        return self.guard.state

    def _pending_action(self) -> str | None:
        state = self.guard.state
        if state.pending_evidence_round is not None:
            return "evidence_recovery"
        if state.status == RunnerStatus.INFRASTRUCTURE_FAILURE.value:
            return "infra_retry"
        return None

    def snapshot(self) -> RunnerSnapshot:
        state = self.guard.state
        record = state.rounds[-1] if state.rounds else None
        status = state.status
        terminal = status in {
            RunnerStatus.SUCCEEDED.value,
            RunnerStatus.UNVERIFIED.value,
            RunnerStatus.CONTRACT_FAILURE.value,
            RunnerStatus.RELOCALIZE_REQUIRED.value,
            RunnerStatus.MAX_ROUNDS_REACHED.value,
        }
        return RunnerSnapshot(
            status=status,
            terminal=terminal,
            scoreable=status in SCOREABLE_STATUSES,
            passed=status == RunnerStatus.SUCCEEDED.value,
            last_round=state.last_round,
            quality_rounds_used=state.quality_rounds_used,
            max_rounds=state.max_rounds,
            pending_action=self._pending_action(),
            pending_round=state.pending_evidence_round,
            infra_attempts=record.infra_attempts if record else 0,
        )

    def summary(self) -> dict[str, Any]:
        """Return an immutable-style score envelope with explicit eligibility."""

        return {
            "schema": "super-dolphin/cross-file-five-round-runner/v1",
            "status": self.snapshot().status,
            "scoreable": self.snapshot().scoreable,
            "passed": self.snapshot().passed,
            "snapshot": self.snapshot().to_dict(),
            "guard_state": self.guard.state.to_dict(),
            "events": [event.to_dict() for event in self.events],
        }

    def _append_event(
        self,
        request: RoundInput,
        *,
        kind: str,
        outcome: RoundOutcome | None = None,
        grader: GraderOutcome | None = None,
        attempt: int = 0,
        parent_sequence: int | None = None,
        quality_round_consumed: bool = False,
        feedback: GraderFeedback | None = None,
        feedback_failure_kind: str | None = None,
    ) -> int:
        state = self.guard.state
        sequence = len(self.events) + 1
        event = RunnerEvent(
            sequence=sequence,
            kind=kind,
            round_number=request.round_number,
            attempt=attempt,
            status=state.status,
            arm_id=str(request.arm_id),
            question_id=self.question_id,
            session_id=str(request.session_id),
            thread_id=str(request.thread_id),
            code_agent_id=str(request.code_agent_id),
            patch_hash=str(request.patch_hash),
            decision=outcome.decision if outcome is not None else None,
            grader_started=bool(grader.started) if grader is not None else False,
            grader_passed=grader.passed if grader is not None else None,
            failure_kind=(
                grader.failure_kind
                if grader is not None
                else (
                    outcome.failure_kind
                    if outcome is not None
                    else (FailureKind.INFRASTRUCTURE.value if kind == "terminal" else None)
                )
            ),
            feedback_failure_kind=(
                feedback.failure_kind
                if feedback is not None
                else feedback_failure_kind
            ),
            evidence_recovery_succeeded=(
                outcome.evidence_recovery_succeeded if outcome is not None else False
            ),
            quality_round_consumed=quality_round_consumed,
            parent_sequence=parent_sequence,
            scoreable=self.snapshot().scoreable,
        )
        self.events.append(event)
        self.event_store.save(self.events)
        return sequence

    def _assert_quality_round_allowed(self) -> None:
        state = self.guard.state
        if state.pending_evidence_round is not None:
            raise PendingActionError(
                f"round {state.pending_evidence_round} requires evidence recovery"
            )
        if state.status == RunnerStatus.INFRASTRUCTURE_FAILURE.value:
            raise PendingActionError("current quality round requires infrastructure retry")
        if state.status in {
            RunnerStatus.SUCCEEDED.value,
            RunnerStatus.UNVERIFIED.value,
            RunnerStatus.CONTRACT_FAILURE.value,
            RunnerStatus.RELOCALIZE_REQUIRED.value,
            RunnerStatus.MAX_ROUNDS_REACHED.value,
        }:
            raise PendingActionError(f"runner is terminal: {state.status}")

    def _mark_infra_exhausted(
        self,
        request: RoundInput,
        *,
        parent_sequence: int,
    ) -> int | None:
        state = self.guard.state
        record = state.rounds[-1] if state.rounds else None
        if state.status != RunnerStatus.INFRASTRUCTURE_FAILURE.value or record is None:
            return None
        max_attempts = 1 + self.max_infra_retries
        if record.infra_attempts < max_attempts:
            return None
        self.guard.mark_unverified(
            f"infrastructure retry budget exhausted at round {record.round_number} "
            f"after {record.infra_attempts} grader attempt(s)",
            source_agent_id="grader-infrastructure",
        )
        return self._append_event(
            request,
            kind="terminal",
            attempt=record.infra_attempts,
            parent_sequence=parent_sequence,
        )

    def record_non_quality_failure(
        self,
        request: RoundInput,
        *,
        failure_kind: str,
        message: str,
    ) -> RunnerStep:
        """Persist an Agent/collection/harness failure without consuming quality.

        These failures happen before a trustworthy model-quality result exists.
        They are explicit infrastructure/non-quality events and therefore
        terminate the arm as unverified instead of manufacturing a failed
        quality round.
        """

        kind = str(failure_kind or "").strip().upper()
        if kind not in NON_QUALITY_INFRA_FEEDBACK_KINDS:
            raise RunnerProtocolError(
                f"unsupported non-quality infrastructure kind: {failure_kind!r}"
            )
        if self.guard.state.status not in {
            RunnerStatus.ACTIVE.value,
            RunnerStatus.INFRASTRUCTURE_FAILURE.value,
        }:
            raise PendingActionError(f"runner is terminal: {self.guard.state.status}")
        self.guard.mark_unverified(
            _required_text(message, "non-quality infrastructure message"),
            source_agent_id="runner-infrastructure",
        )
        sequence = self._append_event(
            request,
            kind="terminal",
            attempt=0,
            feedback_failure_kind=kind,
        )
        snapshot = self.snapshot()
        return RunnerStep(
            outcome=None,
            grader=None,
            event_sequences=(sequence,),
            needs_evidence_recovery=False,
            needs_infra_retry=False,
            terminal=True,
            scoreable=snapshot.scoreable,
        )

    def _grade(
        self,
        request: RoundInput,
        outcome: RoundOutcome,
        grader: GraderCallable,
        *,
        parent_sequence: int,
        kind: str,
        attempt: int,
    ) -> RunnerStep:
        try:
            raw_feedback = grader(outcome)
        except RunnerProtocolError:
            raise
        except Exception as exc:  # noqa: BLE001 - callback transport is infrastructure.
            guard_result = self.guard.run_grader(
                outcome,
                lambda _: (_ for _ in ()).throw(exc),
            )
            sequences = [
                self._append_event(
                    request,
                    kind="grader_result",
                    outcome=outcome,
                    grader=guard_result,
                    attempt=attempt,
                    parent_sequence=parent_sequence,
                    quality_round_consumed=False,
                )
            ]
            terminal_event = self._mark_infra_exhausted(
                request,
                parent_sequence=sequences[-1],
            )
            if terminal_event is not None:
                sequences.append(terminal_event)
            snapshot = self.snapshot()
            return RunnerStep(
                outcome=outcome,
                grader=guard_result,
                event_sequences=tuple(sequences),
                needs_evidence_recovery=False,
                needs_infra_retry=(
                    snapshot.pending_action == "infra_retry" and not snapshot.terminal
                ),
                terminal=snapshot.terminal,
                scoreable=snapshot.scoreable,
            )
        try:
            raw_feedback = _completed_unresolved_model_feedback(
                raw_feedback,
                question_id=self.question_id,
                arm_id=str(request.arm_id),
                session_id=str(request.session_id),
                thread_id=str(request.thread_id),
                patch_hash=str(request.patch_hash),
            )
            feedback = validate_grader_feedback(
                raw_feedback,
                question_id=self.question_id,
                arm_id=str(request.arm_id),
                session_id=str(request.session_id),
                thread_id=str(request.thread_id),
                patch_hash=str(request.patch_hash),
            )
        except (GraderFeedbackError, TypeError, ValueError) as exc:
            raise RunnerProtocolError(f"structured grader feedback rejected: {exc}") from exc

        feedback = _promote_official_model_feedback(feedback)
        feedback = _promote_candidate_missing_symbol(feedback)

        if feedback.failure_kind in INFRA_FEEDBACK_KINDS or (
            feedback.failure_kind == FeedbackFailureKind.FEEDBACK_UNAVAILABLE.value
            or feedback.status == FeedbackStatus.UNRESOLVED.value
        ):
            guard_result = self.guard.run_grader(
                outcome,
                lambda _: {"passed": False, "failure_kind": FailureKind.INFRASTRUCTURE.value},
            )
        else:
            guard_result = self.guard.run_grader(outcome, lambda _: feedback.as_runner_result())
        sequences = [
            self._append_event(
                request,
                kind="grader_result",
                outcome=outcome,
                grader=guard_result,
                attempt=attempt,
                parent_sequence=parent_sequence,
                quality_round_consumed=(
                    guard_result.failure_kind != FailureKind.INFRASTRUCTURE.value
                ),
                feedback=feedback,
            )
        ]
        terminal_event = None
        if (
            feedback.failure_kind == FeedbackFailureKind.FEEDBACK_UNAVAILABLE.value
            or feedback.status == FeedbackStatus.UNRESOLVED.value
        ):
            self.guard.mark_unverified(
                "grader feedback unavailable or unresolved; quality outcome is unverified",
                source_agent_id="grader-feedback",
            )
            terminal_event = self._append_event(
                request,
                kind="terminal",
                grader=guard_result,
                feedback=feedback,
                attempt=attempt,
                parent_sequence=sequences[-1],
            )
        else:
            terminal_event = self._mark_infra_exhausted(
                request,
                parent_sequence=sequences[-1],
            )
        if terminal_event is not None:
            sequences.append(terminal_event)
        snapshot = self.snapshot()
        return RunnerStep(
            outcome=outcome,
            grader=guard_result,
            event_sequences=tuple(sequences),
            needs_evidence_recovery=False,
            needs_infra_retry=(
                snapshot.pending_action == "infra_retry" and not snapshot.terminal
            ),
            terminal=snapshot.terminal,
            scoreable=snapshot.scoreable,
            feedback=feedback,
        )

    def submit_quality_round(
        self,
        request: RoundInput,
        grader: GraderCallable,
    ) -> RunnerStep:
        """Submit exactly one new quality round and grade it if allowed."""

        self._assert_quality_round_allowed()
        if not callable(grader):
            raise RunnerProtocolError("grader callback is required")
        outcome = self.guard.evaluate_round(request)
        gate_sequence = self._append_event(
            request,
            kind="quality_round",
            outcome=outcome,
            attempt=0,
            quality_round_consumed=(
                outcome.decision
                in {
                    RoundDecision.NO_PROGRESS.value,
                    RoundDecision.CONTRACT_FAILURE.value,
                }
            ),
        )
        if outcome.decision == RoundDecision.EVIDENCE_RECOVERY_REQUIRED.value:
            snapshot = self.snapshot()
            return RunnerStep(
                outcome=outcome,
                grader=None,
                event_sequences=(gate_sequence,),
                needs_evidence_recovery=True,
                needs_infra_retry=False,
                terminal=snapshot.terminal,
                scoreable=snapshot.scoreable,
            )
        if not outcome.grader_allowed:
            snapshot = self.snapshot()
            return RunnerStep(
                outcome=outcome,
                grader=None,
                event_sequences=(gate_sequence,),
                needs_evidence_recovery=False,
                needs_infra_retry=(
                    outcome.failure_kind == FailureKind.INFRASTRUCTURE.value
                    and snapshot.pending_action == "infra_retry"
                    and not snapshot.terminal
                ),
                terminal=snapshot.terminal,
                scoreable=snapshot.scoreable,
            )
        return self._grade(
            request,
            outcome,
            grader,
            parent_sequence=gate_sequence,
            kind="quality_round",
            attempt=1,
        )

    def submit_evidence_recovery(
        self,
        request: RoundInput,
        grader: GraderCallable,
    ) -> RunnerStep:
        """Complete the one bounded evidence child event for the pending round."""

        if self.guard.state.pending_evidence_round is None:
            raise PendingActionError("no evidence recovery is pending")
        if not callable(grader):
            raise RunnerProtocolError("grader callback is required")
        attempt = request.evidence_attempt or self.guard.state.pending_evidence_attempts + 1
        recovery_request = dataclasses.replace(
            request,
            action="evidence_recovery",
            evidence_attempt=attempt,
        )
        outcome = self.guard.evaluate_round(recovery_request)
        recovery_sequence = self._append_event(
            recovery_request,
            kind="evidence_recovery",
            outcome=outcome,
            attempt=attempt,
            quality_round_consumed=False,
        )
        if not outcome.grader_allowed:
            snapshot = self.snapshot()
            return RunnerStep(
                outcome=outcome,
                grader=None,
                event_sequences=(recovery_sequence,),
                # A transport failure leaves the guard's evidence gate
                # pending and must remain retryable.  A terminal contract
                # failure clears that gate, so it still reports False.
                needs_evidence_recovery=(
                    snapshot.pending_action == "evidence_recovery"
                    and not snapshot.terminal
                ),
                needs_infra_retry=False,
                terminal=snapshot.terminal,
                scoreable=snapshot.scoreable,
            )
        return self._grade(
            recovery_request,
            outcome,
            grader,
            parent_sequence=recovery_sequence,
            kind="evidence_recovery",
            attempt=1,
        )

    def retry_infrastructure(
        self,
        request: RoundInput,
        grader: GraderCallable,
    ) -> RunnerStep:
        """Retry grading in the same quality round and identity."""

        if self.guard.state.status != RunnerStatus.INFRASTRUCTURE_FAILURE.value:
            raise PendingActionError("no infrastructure retry is pending")
        record = self.guard.state.rounds[-1]
        max_attempts = 1 + self.max_infra_retries
        if record.infra_attempts >= max_attempts:
            raise PendingActionError("infrastructure retry budget is exhausted")
        if not callable(grader):
            raise RunnerProtocolError("grader callback is required")
        retry_request = dataclasses.replace(request, action="infra_recovery")
        outcome = self.guard.evaluate_round(retry_request)
        retry_sequence = self._append_event(
            retry_request,
            kind="infra_retry",
            outcome=outcome,
            attempt=record.infra_attempts + 1,
            quality_round_consumed=False,
        )
        if not outcome.grader_allowed:
            snapshot = self.snapshot()
            return RunnerStep(
                outcome=outcome,
                grader=None,
                event_sequences=(retry_sequence,),
                needs_evidence_recovery=False,
                needs_infra_retry=False,
                terminal=snapshot.terminal,
                scoreable=snapshot.scoreable,
            )
        return self._grade(
            retry_request,
            outcome,
            grader,
            parent_sequence=retry_sequence,
            kind="infra_retry",
            attempt=record.infra_attempts + 1,
        )

    def execute_round(
        self,
        request: RoundInput,
        grader: GraderCallable,
        *,
        evidence_recovery: Callable[[RunnerStep], RoundInput] | None = None,
        infrastructure_retry: Callable[[RunnerStep], RoundInput] | None = None,
    ) -> RunnerStep:
        """Run a round plus explicitly supplied same-round child actions."""

        step = self.submit_quality_round(request, grader)
        if step.needs_evidence_recovery:
            if evidence_recovery is None:
                return step
            step = self.submit_evidence_recovery(evidence_recovery(step), grader)
        while step.needs_infra_retry:
            if infrastructure_retry is None:
                return step
            step = self.retry_infrastructure(infrastructure_retry(step), grader)
        return step


__all__ = [
    "DEFAULT_MAX_INFRA_RETRIES",
    "EVENT_SCHEMA_VERSION",
    "FiveRoundRunner",
    "MAX_QUALITY_ROUNDS",
    "INFRA_FEEDBACK_KINDS",
    "NON_QUALITY_INFRA_FEEDBACK_KINDS",
    "PendingActionError",
    "RunnerError",
    "RunnerEvent",
    "RunnerEventStore",
    "RunnerProtocolError",
    "RunnerSnapshot",
    "RunnerStateError",
    "RunnerStep",
    "SCOREABLE_STATUSES",
]
