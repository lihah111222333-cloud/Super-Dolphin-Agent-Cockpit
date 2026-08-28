"""Fail-closed extraction of structured official grader feedback.

The closed-loop runner owns model, prompt, diagnostics, and state decisions.
This module only reads an official report/test-output pair and returns a
bounded, redacted result.  Free-form runner chatter is never promoted to a
failure; an unresolved report without a structured failure becomes
``FEEDBACK_UNAVAILABLE``.

Structured results retain the question/arm/session/thread/patch identity and
bounded failing-test evidence.  A test name without an exception, assertion,
traceback, location, or value pair is not actionable model feedback.
"""

from __future__ import annotations

import dataclasses
import enum
import hashlib
import json
import pathlib
import re
from collections.abc import Iterable, Mapping, Sequence
from typing import Any


SCHEMA = "super-dolphin/grader-feedback/v1"
MAX_INPUT_BYTES = 2 * 1024 * 1024
MAX_LINES = 20_000
MAX_TEST_IDS = 256
MAX_SUMMARY_CHARS = 4096
MAX_EVIDENCE = 32
MAX_FAILURES = 32
MAX_TRACEBACK_LINES = 80


class FailureKind(str, enum.Enum):
    """Mutually exclusive official grader failure categories."""

    AGENT_TRANSPORT_FAILURE = "AGENT_TRANSPORT_FAILURE"
    TEST_COLLECTION_IMPORT_ERROR = "TEST_COLLECTION_IMPORT_ERROR"
    TEST_HARNESS_FAILURE = "TEST_HARNESS_FAILURE"
    INFRASTRUCTURE_ERROR = "INFRASTRUCTURE_ERROR"
    TIMEOUT = "TIMEOUT"
    PATCH_APPLY_FAILURE = "PATCH_APPLY_FAILURE"
    MODEL_TEST_FAILURE = "MODEL_TEST_FAILURE"
    FEEDBACK_UNAVAILABLE = "FEEDBACK_UNAVAILABLE"


class Status(str, enum.Enum):
    PASSED = "PASSED"
    FAILED = "FAILED"
    UNRESOLVED = "UNRESOLVED"


class GraderFeedbackError(RuntimeError):
    """Base class for malformed or unsafe grader evidence."""


class UnknownSchemaError(GraderFeedbackError):
    """The official document has no schema owned by this parser."""


class MissingInputError(GraderFeedbackError):
    """Neither an official report nor official test output was supplied."""


class ContradictoryReportError(GraderFeedbackError):
    """Independent report fields describe incompatible outcomes."""


class MissingFileError(GraderFeedbackError):
    """A caller supplied a report/output path that cannot be read."""


_KNOWN_SCHEMAS = {
    SCHEMA,
    "super-dolphin/grader-report/v1",
    "super-dolphin/grader-test-output/v1",
    "grader-feedback/v1",
    "grader-report/v1",
    "grader-test-output/v1",
    "official-grader-report/v1",
    "official-grader-test-output/v1",
    "swe-bench/grader-report/v2",
}
_SWE_BENCH_V2_SCHEMA = "swe-bench/grader-report/v2"
_SWE_BENCH_OUTCOME_KEYS = {"resolved_ids", "unresolved_ids", "error_ids", "completed_ids"}
_SWE_BENCH_SINGLE_OUTCOME_KEYS = {"resolved", "completed"}
_ANSI_RE = re.compile(r"\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))")
_KEY_RE = re.compile(r"^([A-Za-z][A-Za-z0-9_-]*)\s*[:=]\s*(.*)$")
_SUMMARY_RE = re.compile(r"\b(\d+)\s+(failed|failure|failures|error|errors|passed)\b", re.I)
_FAILURES_COUNT_RE = re.compile(r"\bfailures?\s*=\s*(\d+)\b", re.I)
_TEST_LINE_RE = re.compile(r"^(PASSED|FAILED|ERROR)\s+(.+)$", re.I)
_TEST_SUFFIX_RE = re.compile(r"^(.+?)\s+(PASSED|FAILED|ERROR)(?:\s|$)", re.I)
_FILE_LINE_RE = re.compile(
    r"(?:File\s+\"(?P<quoted_file>[^\"]+)\",\s+line\s+(?P<quoted_line>\d+)|"
    r"(?P<plain_file>(?:[A-Za-z]:)?[^\s:]+\.py):(?P<plain_line>\d+)(?::|\s|$))"
)
_ASSERTION_RE = re.compile(r"\bassert\s+(.+?)\s*==\s*(.+)$", re.I)
_EXCEPTION_RE = re.compile(
    r"^(?:E\s+)?(?P<type>[A-Za-z_][\w.]*(?:Error|Exception|Warning|Exit))"
    r"(?:\s*:\s*(?P<message>.*))?$"
)
_TIMEOUT_RE = re.compile(
    r"(?:\btimed?\s+out\b|"
    r"\btimeout(?:expired|error)?\s*(?::|=|after\b)|"
    r"\b(?:evaluation|request|connection|runner|grader|agent)\s+timeout\b|"
    r"\b(?:context\s+)?deadline exceeded\b)",
    re.I,
)
_AGENT_TRANSPORT_RE = re.compile(
    r"(?:\bturn\.failed\b|stream disconnected|failed to lookup address information|"
    r"request timed out|(?:\bDNS\b|name resolution).*(?:failure|error|unavailable|failed)|"
    r"tls.*(?:handshake|connection).*(?:failure|error|eof|failed)|"
    r"falling back from websockets to https transport|"
    r"error sending request for url .*codex/responses|responses_websocket.*failed to connect)",
    re.I,
)
_COLLECTION_RE = re.compile(r"\bcollected\s+(?P<count>\d+)\s+items?\b", re.I)
_COLLECTION_ERROR_RE = re.compile(r"^ERROR\s+collecting\s+(?P<test>\S+)", re.I)
_RAN_TESTS_RE = re.compile(r"\bRan\s+(?P<count>\d+)\s+tests?\b", re.I)
_RESULT_COUNTS_RE = re.compile(
    r"(?:(?P<passed>\d+)\s+passed|(?P<failed>\d+)\s+failed|"
    r"(?P<errors>\d+)\s+errors?|(?P<skipped>\d+)\s+skipped)",
    re.I,
)
_MISSING_SYMBOL_RE = re.compile(
    r"(?:cannot import name|has no attribute)\s+[\"'](?P<symbol>[A-Za-z_]\w*)[\"']",
    re.I,
)
_MISSING_MODULE_RE = re.compile(r"No module named\s+[\"'](?P<module>[^\"']+)[\"']", re.I)
_IMPORT_ERROR_RE = re.compile(r"\b(?:ImportError|ModuleNotFoundError)\b", re.I)
_HARNESS_RE = re.compile(
    r"(?:ERROR\s+at\s+(?P<phase>setup|teardown)\s+of\b|"
    r"PytestRemovedIn8Warning|Support for nose tests is deprecated|"
    r"using nose-specific method:\s*`?(?P<nose_phase>setup|teardown)|"
    r"warnings?[- ]as[- ]errors?|(?:setup|teardown)[ /-]+(?:error|failure))",
    re.I,
)
_PATCH_RE = re.compile(
    r"(?:patch\s+(?:apply\s+)?(?:failed|failure|error|rejected)|"
    r"apply\s+patch\s+(?:failed|failure|error|rejected)|"
    r"git\s+apply[^\n]*(?:failed|error|rejected)|hunk\s+failed)",
    re.I,
)
_INFRA_RE = re.compile(
    r"(?:infrastructure\s+(?:error|failure)\b|infra(?:structure)?[_ -](?:error|failure)\b|"
    r"runner\s+(?:infrastructure\s+)?(?:error|failure)\b|executor\s+(?:unavailable|failed)|"
    r"container\s+(?:failed|exited|unavailable|not found)|"
    r"docker\s+(?:daemon|engine)\s+(?:error|unavailable)|"
    r"image\s+pull\s+(?:failed|error)|network\s+(?:unavailable|failure)|"
    r"no space left on device|resource\s+(?:exhausted|unavailable)|failed to spawn)",
    re.I,
)
_SECRET_RE = re.compile(
    r"\b(api[_-]?key|token|password|passwd|secret|authorization|cookie)\b\s*[:=]\s*[^\s,;]+",
    re.I,
)
_USER_PATH_RE = re.compile(r"(?:/Users/[^\s]+|/home/[^\s]+|C:\\Users\\[^\s]+)")
_NOISE_LINE_RE = re.compile(
    r"^(?:"
    r"Requirement already satisfied:|"
    r"Collecting\s+|"
    r"Obtaining file://|"
    r"Installing collected packages:|"
    r"Successfully installed\b|"
    r"DEPRECATION:|"
    r"WARNING: Running pip as the 'root' user|"
    r"Looking in indexes:|"
    r"Found (?:\d+ )?existing instance images\.|"
    r"Running \d+ instances\.\.\.|"
    r"Cleaning cached images\."
    r")"
)


_IDENTITY_ALIASES = {
    "question_id": ("question_id", "question", "task_id"),
    "arm_id": ("arm_id", "arm", "variant_id"),
    "session_id": ("session_id", "session"),
    "thread_id": ("thread_id", "thread"),
    "patch_hash": ("patch_hash", "candidate_hash"),
}

_TOP_KEYS = {
    "schema",
    "schema_version",
    "identity",
    "question_id",
    "question",
    "task_id",
    "arm_id",
    "arm",
    "variant_id",
    "session_id",
    "session",
    "thread_id",
    "thread",
    "patch_hash",
    "candidate_hash",
    "status",
    "failure",
    "failures",
    "failure_kind",
    "kind",
    "failing_test_ids",
    "failed_test_ids",
    "test_ids",
    "failing_tests",
    "test_id",
    "tests",
    "exception",
    "exception_type",
    "traceback",
    "traceback_tail",
    "assertion",
    "assertion_summary",
    "summary",
    "message",
    "file",
    "path",
    "line",
    "actual",
    "expected",
    "source",
    "source_artifact",
    "source_hash",
    "artifact",
    "artifact_hash",
    "source_artifact_hash",
    "evidence",
    # Official SWE-bench v2 aggregate reports omit ``schema`` and identify
    # themselves with schema_version=2 plus these four outcome sets.
    "resolved_ids",
    "unresolved_ids",
    "error_ids",
    "completed_ids",
    "resolved",
    "completed",
    "official_resolved",
    "official_completed",
    "official_instance_id",
    "instance_id",
    "run_id",
    "report_id",
    "patch_apply_failed",
    "timeout",
    "infrastructure_error",
    "error",
    "structured",
    "passed",
    "code_repair_eligible",
    "grader_failure_kind",
    "runner_failure_kind",
    "phase",
    "collected_count",
    "executed_count",
    "collected",
    "executed",
    "collected_tests",
    "executed_tests",
    "missing_symbols",
}
_FAILURE_KEYS = _TOP_KEYS - {"schema", "schema_version", "status", "failures", "tests"}
_SOURCE_KEYS = {
    "artifact",
    "hash",
    "sha256",
    "source_artifact",
    "source_hash",
    "artifact_hash",
    "source_artifact_hash",
}
_SWE_BENCH_KEYS = {
    "resolved_ids",
    "unresolved_ids",
    "error_ids",
    "completed_ids",
    "resolved",
    "completed",
    "official_resolved",
    "official_completed",
    "instance_id",
    "run_id",
    "report_id",
    "patch_apply_failed",
    "timeout",
    "infrastructure_error",
}


@dataclasses.dataclass(frozen=True)
class SourceArtifact:
    artifact: str = ""
    hash: str = ""


@dataclasses.dataclass(frozen=True)
class FeedbackIdentity:
    """Identity that binds grader evidence to one question/arm attempt."""

    question_id: str = ""
    arm_id: str = ""
    session_id: str = ""
    thread_id: str = ""
    patch_hash: str = ""

    def is_empty(self) -> bool:
        return not any(
            (self.question_id, self.arm_id, self.session_id, self.thread_id, self.patch_hash)
        )

    def is_complete(self) -> bool:
        return all(
            (self.question_id, self.arm_id, self.session_id, self.thread_id, self.patch_hash)
        )

    def to_dict(self) -> dict[str, str]:
        return dataclasses.asdict(self)


@dataclasses.dataclass(frozen=True)
class FailureEvidence:
    """One bounded, redacted failing-test evidence record."""

    test_id: str = ""
    file: str = ""
    line: int | None = None
    exception: str = ""
    message: str = ""
    actual: str = ""
    expected: str = ""
    traceback_tail: str = ""

    @property
    def location(self) -> str:
        if not self.file:
            return ""
        return f"{self.file}:{self.line}" if self.line is not None else self.file

    def to_dict(self) -> dict[str, Any]:
        result = dataclasses.asdict(self)
        result["location"] = self.location
        return result


@dataclasses.dataclass(frozen=True)
class FeedbackInput:
    report_path: str | pathlib.Path | None = None
    test_output_path: str | pathlib.Path | None = None
    source_artifact: str = ""
    source_hash: str = ""
    source_artifact_path: str | pathlib.Path | None = None
    question_id: str = ""
    arm_id: str = ""
    session_id: str = ""
    thread_id: str = ""
    patch_hash: str = ""
    identity: FeedbackIdentity | Mapping[str, Any] | None = None
    strict_provenance: bool = False


@dataclasses.dataclass(frozen=True)
class GraderFeedback:
    """Bounded, redacted feedback returned to a closed-loop runner."""

    schema: str = SCHEMA
    status: str = Status.UNRESOLVED.value
    failure_kind: str = FailureKind.FEEDBACK_UNAVAILABLE.value
    failing_test_ids: tuple[str, ...] = ()
    exception_summary: str = ""
    traceback_summary: str = ""
    assertion_summary: str = ""
    source_artifact: str = ""
    source_hash: str = ""
    question_id: str = ""
    arm_id: str = ""
    session_id: str = ""
    thread_id: str = ""
    patch_hash: str = ""
    failures: tuple[FailureEvidence, ...] = ()
    phase: str = ""
    collected_count: int | None = None
    executed_count: int | None = None
    missing_symbols: tuple[str, ...] = ()
    # Keep the official per-instance truth separate from the derived runner
    # status.  ``completed`` only says that grading ran; ``resolved`` is the
    # sole success signal.
    official_resolved: bool | None = None
    official_completed: bool | None = None
    official_instance_id: str = ""
    structured: bool = False
    passed: bool = False
    code_repair_eligible: bool = False
    evidence: tuple[str, ...] = ()

    @property
    def kind(self) -> str:
        """Compatibility alias for consumers that call the field ``kind``."""

        return self.failure_kind

    @property
    def repair_eligible(self) -> bool:
        return self.code_repair_eligible

    @property
    def collected_tests(self) -> int | None:
        """Compatibility alias for callers using a test-oriented name."""

        return self.collected_count

    @property
    def executed_tests(self) -> int | None:
        """Compatibility alias for callers using a test-oriented name."""

        return self.executed_count

    def can_enter_code_repair_round(self) -> bool:
        return self.code_repair_eligible and self.failure_kind == FailureKind.MODEL_TEST_FAILURE.value

    def should_inject_code_repair(self) -> bool:
        return self.can_enter_code_repair_round()

    def to_dict(self) -> dict[str, Any]:
        result = dataclasses.asdict(self)
        result["failing_test_ids"] = list(self.failing_test_ids)
        result["evidence"] = list(self.evidence)
        result["failures"] = [item.to_dict() for item in self.failures]
        result["missing_symbols"] = list(self.missing_symbols)
        result["identity"] = {
            "question_id": self.question_id,
            "arm_id": self.arm_id,
            "session_id": self.session_id,
            "thread_id": self.thread_id,
            "patch_hash": self.patch_hash,
        }
        return result

    def as_runner_result(self) -> dict[str, Any]:
        """Return the small result envelope accepted by the closed-loop guard."""

        runner_kind = None
        if self.failure_kind == FailureKind.MODEL_TEST_FAILURE.value:
            runner_kind = "model_quality"
        elif self.failure_kind not in ("", None) and not self.passed:
            runner_kind = "infrastructure"
        return {
            "schema": self.schema,
            "status": self.status,
            "passed": self.passed,
            "failure_kind": runner_kind,
            "grader_failure_kind": self.failure_kind,
            "question_id": self.question_id,
            "arm_id": self.arm_id,
            "session_id": self.session_id,
            "thread_id": self.thread_id,
            "patch_hash": self.patch_hash,
            "phase": self.phase,
            "collected_count": self.collected_count,
            "executed_count": self.executed_count,
            "missing_symbols": list(self.missing_symbols),
            "official_resolved": self.official_resolved,
            "official_completed": self.official_completed,
            "official_instance_id": self.official_instance_id,
            "grader_feedback": self.to_dict(),
            "code_repair_eligible": self.can_enter_code_repair_round(),
        }

    to_runner_result = as_runner_result


@dataclasses.dataclass
class _Parsed:
    status: str = ""
    kind: str = ""
    explicit_kind: bool = False
    structured: bool = False
    passed: bool = False
    ids: list[str] = dataclasses.field(default_factory=list)
    passed_ids: list[str] = dataclasses.field(default_factory=list)
    exception: str = ""
    traceback: str = ""
    assertion: str = ""
    artifact: SourceArtifact = dataclasses.field(default_factory=SourceArtifact)
    source_seen: bool = False
    identity: FeedbackIdentity = dataclasses.field(default_factory=FeedbackIdentity)
    identity_seen: bool = False
    identity_fields: dict[str, str] = dataclasses.field(default_factory=dict)
    evidence: list[str] = dataclasses.field(default_factory=list)
    failures: list[FailureEvidence] = dataclasses.field(default_factory=list)
    current_failure: int | None = None
    traceback_lines: list[str] = dataclasses.field(default_factory=list)
    traceback_active: bool = False
    phase: str = ""
    phase_explicit: bool = False
    collected_count: int | None = None
    executed_count: int | None = None
    missing_symbols: list[str] = dataclasses.field(default_factory=list)
    # Per-instance SWE-bench reports carry an authoritative result which must
    # win over noisy test-output lines when the two surfaces disagree.
    official_resolved: bool | None = None
    official_completed: bool | None = None
    official_outcome_seen: bool = False
    official_model_failure: bool = False
    official_infrastructure: bool = False
    official_instance_id: str = ""
    agent_transport_seen: bool = False
    import_error_seen: bool = False
    collection_marker_seen: bool = False
    collection_import_seen: bool = False
    harness_seen: bool = False
    categories: set[str] = dataclasses.field(default_factory=set)
    schema: str = ""


def extract_grader_feedback(
    input_value: FeedbackInput | str | pathlib.Path | None = None,
    test_output_path: str | pathlib.Path | None = None,
    *,
    source_artifact: str = "",
    source_hash: str = "",
    source_artifact_path: str | pathlib.Path | None = None,
    question_id: str = "",
    arm_id: str = "",
    session_id: str = "",
    thread_id: str = "",
    patch_hash: str = "",
    identity: FeedbackIdentity | Mapping[str, Any] | None = None,
    strict_provenance: bool = False,
) -> GraderFeedback:
    """Read report/output files and extract one authoritative result.

    A string/path first argument is treated as ``report_path``.  A
    ``FeedbackInput`` keeps the API explicit for runner integrations.
    """

    if isinstance(input_value, FeedbackInput):
        request = input_value
    else:
        request = FeedbackInput(
            report_path=input_value,
            test_output_path=test_output_path,
            source_artifact=source_artifact,
            source_hash=source_hash,
            source_artifact_path=source_artifact_path,
            question_id=question_id,
            arm_id=arm_id,
            session_id=session_id,
            thread_id=thread_id,
            patch_hash=patch_hash,
            identity=identity,
            strict_provenance=strict_provenance,
        )
    report = _read_path(request.report_path, "official grader report") if request.report_path else ""
    output = _read_path(request.test_output_path, "official test output") if request.test_output_path else ""
    return parse_grader_feedback(
        report,
        output,
        source_artifact=request.source_artifact,
        source_hash=request.source_hash,
        source_artifact_path=request.source_artifact_path,
        question_id=request.question_id,
        arm_id=request.arm_id,
        session_id=request.session_id,
        thread_id=request.thread_id,
        patch_hash=request.patch_hash,
        identity=request.identity,
        strict_provenance=request.strict_provenance,
    )


def extract_from_files(
    report_path: str | pathlib.Path | None,
    test_output_path: str | pathlib.Path | None,
    **identity: Any,
) -> GraderFeedback:
    return extract_grader_feedback(
        FeedbackInput(report_path=report_path, test_output_path=test_output_path, **identity)
    )


def parse_grader_feedback(
    report: str | bytes | None = None,
    test_output: str | bytes | None = None,
    *,
    source_artifact: str = "",
    source_hash: str = "",
    source_artifact_path: str | pathlib.Path | None = None,
    question_id: str = "",
    arm_id: str = "",
    session_id: str = "",
    thread_id: str = "",
    patch_hash: str = "",
    identity: FeedbackIdentity | Mapping[str, Any] | None = None,
    strict_provenance: bool = False,
) -> GraderFeedback:
    """Combine report and test output blobs using report/test evidence only."""

    report_text = _clean_input(report, "official grader report") if report not in (None, "", b"") else ""
    output_text = _clean_input(test_output, "official test output") if test_output not in (None, "", b"") else ""
    expected_identity = _identity_from_values(
        question_id=question_id,
        arm_id=arm_id,
        session_id=session_id,
        thread_id=thread_id,
        patch_hash=patch_hash,
        label="input identity",
        allow_empty=True,
    )
    if identity is not None:
        supplied_identity = _coerce_identity(identity, label="input identity")
        if not expected_identity.is_empty() and expected_identity != supplied_identity:
            raise ContradictoryReportError("input identity declarations disagree")
        expected_identity = supplied_identity
    if not report_text and not output_text:
        if strict_provenance:
            raise MissingInputError("grader feedback report or test output is required")
        # A missing report is a valid terminal *unverified* observation for a
        # scheduler.  It must not be promoted to either success or a model
        # failure merely because the grader process produced no bytes.
        return GraderFeedback(
            source_artifact=_sanitize(source_artifact),
            source_hash=_normalise_hash(source_hash) if source_hash else "",
            question_id=expected_identity.question_id,
            arm_id=expected_identity.arm_id,
            session_id=expected_identity.session_id,
            thread_id=expected_identity.thread_id,
            patch_hash=expected_identity.patch_hash,
        )
    parts = []
    if report_text:
        report_part = _parse_document(report_text, "official grader report")
        parts.append(report_part)
        authoritative_terminal = report_part.official_outcome_seen and (
            report_part.official_infrastructure
            or
            report_part.official_resolved is True
            or (
                report_part.official_resolved is False
                and report_part.official_completed is True
            )
        )
    else:
        authoritative_terminal = False
    if output_text:
        if authoritative_terminal:
            # Terminal official truth wins.  Best-effort parsing keeps useful
            # phase/count metadata, but malformed/noisy output cannot reverse
            # the already authoritative outcome.
            try:
                parts.append(_parse_document(output_text, "official test output"))
            except GraderFeedbackError:
                pass
        else:
            parts.append(_parse_document(output_text, "official test output"))
    merged = _merge_parts(parts)
    if strict_provenance:
        source = _strict_source_identity(
            report_text,
            output_text,
            source_artifact=source_artifact,
            source_hash=source_hash,
        )
    else:
        source = _merge_source(merged, source_artifact, source_hash, source_artifact_path)
    identity = _merge_identity(merged, expected_identity)
    if (
        merged.official_instance_id
        and identity.question_id
        and merged.official_instance_id != identity.question_id
    ):
        raise ContradictoryReportError("official instance_id drifted from question identity")
    return _finalize(merged, source, identity)


def parse_official_report(
    raw: str | bytes,
    *,
    strict_provenance: bool = False,
    source_artifact: str = "",
    source_hash: str = "",
    **identity: Any,
) -> GraderFeedback:
    return parse_grader_feedback(
        raw,
        None,
        strict_provenance=strict_provenance,
        source_artifact=source_artifact,
        source_hash=source_hash,
        **identity,
    )


def parse_test_output(
    raw: str | bytes,
    *,
    strict_provenance: bool = False,
    source_artifact: str = "",
    source_hash: str = "",
    **identity: Any,
) -> GraderFeedback:
    return parse_grader_feedback(
        None,
        raw,
        strict_provenance=strict_provenance,
        source_artifact=source_artifact,
        source_hash=source_hash,
        **identity,
    )


# Common integration spellings kept as aliases, with one implementation.
extract = extract_grader_feedback
parse_report = parse_official_report
parse_output = parse_test_output
parse_feedback = parse_grader_feedback
extract_feedback = extract_grader_feedback
GraderFeedbackResult = GraderFeedback
ReportInput = FeedbackInput


def validate_grader_feedback(
    value: GraderFeedback | Mapping[str, Any] | bool,
    *,
    question_id: str,
    arm_id: str,
    session_id: str,
    thread_id: str,
    patch_hash: str,
    strict_provenance: bool = False,
    official_report: str | bytes | None = None,
    official_test_output: str | bytes | None = None,
) -> GraderFeedback:
    """Validate one structured grader result and bind it to one round.

    The runner may receive either a :class:`GraderFeedback` instance or the
    JSON-compatible result returned by :meth:`GraderFeedback.as_runner_result`.
    A bare boolean is deliberately rejected: it cannot distinguish model
    quality, infrastructure, unavailable evidence, and unresolved outcomes.
    """

    if isinstance(value, bool):
        raise GraderFeedbackError("structured grader feedback is required; bool is not accepted")
    if isinstance(value, GraderFeedback):
        result = value
    elif isinstance(value, Mapping):
        result = _parse_runner_feedback_mapping(value, question_id, arm_id, session_id, thread_id, patch_hash)
    else:
        raise GraderFeedbackError("grader result must be structured feedback")
    if strict_provenance:
        if isinstance(value, GraderFeedback) and (
            not value.source_artifact or not value.source_hash
        ):
            raise GraderFeedbackError(
                "strict provenance rejects typed feedback without source artifact/hash"
            )
        if official_report in (None, "", b"") and official_test_output in (None, "", b""):
            raise GraderFeedbackError(
                "strict provenance requires official_report or official_test_output"
            )
        source_result = parse_grader_feedback(
            official_report,
            official_test_output,
            question_id=question_id,
            arm_id=arm_id,
            session_id=session_id,
            thread_id=thread_id,
            patch_hash=patch_hash,
            strict_provenance=True,
        )
        if (
            result.status,
            result.failure_kind,
            result.passed,
            result.phase,
            result.collected_count,
            result.executed_count,
            result.missing_symbols,
            result.official_resolved,
            result.official_completed,
            result.official_instance_id,
        ) != (
            source_result.status,
            source_result.failure_kind,
            source_result.passed,
            source_result.phase,
            source_result.collected_count,
            source_result.executed_count,
            source_result.missing_symbols,
            source_result.official_resolved,
            source_result.official_completed,
            source_result.official_instance_id,
        ):
            raise ContradictoryReportError(
                "structured feedback outcome disagrees with official evidence"
            )
        if result.source_artifact and result.source_artifact != source_result.source_artifact:
            raise ContradictoryReportError("structured feedback source artifact drifted")
        if result.source_hash and result.source_hash != source_result.source_hash:
            raise ContradictoryReportError("structured feedback source hash drifted")
        result = dataclasses.replace(
            result,
            source_artifact=source_result.source_artifact,
            source_hash=source_result.source_hash,
        )
    expected = _identity_from_values(
        question_id=question_id,
        arm_id=arm_id,
        session_id=session_id,
        thread_id=thread_id,
        patch_hash=patch_hash,
        label="runner identity",
        allow_empty=False,
    )
    actual = FeedbackIdentity(
        result.question_id,
        result.arm_id,
        result.session_id,
        result.thread_id,
        result.patch_hash,
    )
    if actual != expected:
        raise ContradictoryReportError("grader feedback identity/hash drifted from round")
    _validate_feedback_outcome(result)
    return result


def _parse_runner_feedback_mapping(
    value: Mapping[str, Any],
    question_id: str,
    arm_id: str,
    session_id: str,
    thread_id: str,
    patch_hash: str,
) -> GraderFeedback:
    payload = value.get("grader_feedback", value)
    if not isinstance(payload, Mapping):
        raise GraderFeedbackError("grader_feedback envelope must be an object")
    payload = dict(payload)
    raw_failure = payload.get("failure")
    if raw_failure is not None and not isinstance(raw_failure, Mapping):
        raise GraderFeedbackError("grader feedback failure must be an object")
    canonical_kind = payload.get("grader_failure_kind") or payload.get("failure_kind")
    if canonical_kind in (None, "") and isinstance(raw_failure, Mapping):
        canonical_kind = raw_failure.get("kind") or raw_failure.get("failure_kind")
    if canonical_kind is None and "grader_failure_kind" in value:
        canonical_kind = value["grader_failure_kind"]
    normalized: dict[str, Any] = {
        "schema": payload.get("schema", SCHEMA),
        "schema_version": 1,
        "status": payload.get("status", Status.UNRESOLVED.value),
        "identity": payload.get("identity", {
            "question_id": payload.get("question_id", value.get("question_id", question_id)),
            "arm_id": payload.get("arm_id", value.get("arm_id", arm_id)),
            "session_id": payload.get("session_id", value.get("session_id", session_id)),
            "thread_id": payload.get("thread_id", value.get("thread_id", thread_id)),
            "patch_hash": payload.get("patch_hash", value.get("patch_hash", patch_hash)),
        }),
    }
    for field in (
        "phase",
        "collected_count",
        "executed_count",
        "collected",
        "executed",
        "collected_tests",
        "executed_tests",
        "missing_symbols",
    ):
        if field in payload and payload[field] not in (None, "", []):
            normalized[field] = payload[field]
    for field in (
        "resolved",
        "completed",
        "official_resolved",
        "official_completed",
        "official_instance_id",
    ):
        if field in payload and payload[field] not in (None, ""):
            normalized[field] = payload[field]
    if canonical_kind not in (None, ""):
        failure_source = raw_failure if isinstance(raw_failure, Mapping) else payload
        normalized["failure"] = {
            "kind": canonical_kind,
            "failing_test_ids": payload.get(
                "failing_test_ids", failure_source.get("failing_test_ids", ())
            ),
            "exception": payload.get(
                "exception_summary", payload.get("exception", failure_source.get("exception", ""))
            ),
            "traceback": payload.get(
                "traceback_summary", payload.get("traceback", failure_source.get("traceback", ""))
            ),
            "assertion": payload.get(
                "assertion_summary", payload.get("assertion", failure_source.get("assertion", ""))
            ),
        }
        for field in (
            "phase",
            "collected_count",
            "executed_count",
            "collected",
            "executed",
            "collected_tests",
            "executed_tests",
            "missing_symbols",
        ):
            if (
                field in failure_source
                and field in normalized
                and failure_source[field] not in (None, "", [])
                and normalized[field] != failure_source[field]
            ):
                raise ContradictoryReportError(
                    f"grader feedback {field} declarations disagree"
                )
            if (
                field in failure_source
                and failure_source[field] is not None
                and failure_source[field] not in ("", [])
                and field not in normalized
            ):
                normalized["failure"][field] = failure_source[field]
    failures = payload.get("failures", ())
    if failures:
        if not isinstance(failures, Sequence) or isinstance(failures, (str, bytes, bytearray)):
            raise GraderFeedbackError("grader feedback failures must be an array")
        allowed = {
            "test_id", "file", "line", "exception", "message", "actual", "expected",
            "traceback", "traceback_tail", "assertion", "assertion_summary",
            "phase", "collected_count", "executed_count", "collected", "executed",
            "collected_tests", "executed_tests", "missing_symbols",
        }
        normalized["failures"] = [
            {key: item[key] for key in item if key in allowed}
            if isinstance(item, Mapping) else item
            for item in failures
        ]
    if payload.get("evidence"):
        normalized["evidence"] = payload["evidence"]
    source: dict[str, str] = {}
    if payload.get("source_artifact"):
        source["artifact"] = str(payload["source_artifact"])
    if payload.get("source_hash"):
        source["hash"] = str(payload["source_hash"])
    if source:
        normalized["source"] = source
    result = parse_grader_feedback(
        json.dumps(normalized, ensure_ascii=False),
        question_id=question_id,
        arm_id=arm_id,
        session_id=session_id,
        thread_id=thread_id,
        patch_hash=patch_hash,
    )
    if "passed" in payload and bool(payload["passed"]) != result.passed:
        raise ContradictoryReportError("grader feedback passed field disagrees with status")
    if "code_repair_eligible" in payload and bool(payload["code_repair_eligible"]) != result.can_enter_code_repair_round():
        raise ContradictoryReportError("grader feedback repair eligibility disagrees with failure kind")
    return result


def _validate_feedback_outcome(result: GraderFeedback) -> None:
    if result.status == Status.UNRESOLVED.value and result.failure_kind in {
        "",
        FailureKind.FEEDBACK_UNAVAILABLE.value,
    }:
        if result.passed or result.code_repair_eligible:
            raise ContradictoryReportError("UNRESOLVED feedback cannot be passed or repairable")
        return
    if result.status == Status.PASSED.value:
        if not result.passed or result.failure_kind:
            raise ContradictoryReportError("PASSED feedback contains failure evidence")
        return
    if result.failure_kind == FailureKind.MODEL_TEST_FAILURE.value:
        if result.status != Status.FAILED.value or result.passed or not result.code_repair_eligible:
            raise ContradictoryReportError("MODEL_TEST_FAILURE feedback is not a repairable failed result")
        return
    if result.failure_kind in {
        FailureKind.AGENT_TRANSPORT_FAILURE.value,
        FailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
        FailureKind.TEST_HARNESS_FAILURE.value,
        FailureKind.PATCH_APPLY_FAILURE.value,
        FailureKind.TIMEOUT.value,
        FailureKind.INFRASTRUCTURE_ERROR.value,
    }:
        if result.status != Status.FAILED.value or result.passed or result.code_repair_eligible:
            raise ContradictoryReportError("infrastructure feedback has inconsistent status")
        return
    if result.failure_kind == FailureKind.FEEDBACK_UNAVAILABLE.value:
        if result.status != Status.UNRESOLVED.value or result.passed or result.code_repair_eligible:
            raise ContradictoryReportError("FEEDBACK_UNAVAILABLE must be unresolved and non-repairable")
        return
    raise GraderFeedbackError(f"unsupported structured grader outcome: {result.failure_kind}")


def _read_path(path: str | pathlib.Path, label: str) -> str:
    candidate = pathlib.Path(path)
    try:
        if not candidate.is_file():
            raise MissingFileError(f"{label} is missing or not a regular file: {candidate}")
        size = candidate.stat().st_size
        if size > MAX_INPUT_BYTES:
            raise GraderFeedbackError(f"{label} exceeds {MAX_INPUT_BYTES} bytes")
        data = candidate.read_bytes()
    except MissingFileError:
        raise
    except OSError as exc:
        raise MissingFileError(f"read {label}: {exc}") from exc
    if len(data) > MAX_INPUT_BYTES:
        raise GraderFeedbackError(f"{label} exceeds {MAX_INPUT_BYTES} bytes")
    return data.decode("utf-8", errors="strict")


def _clean_input(value: str | bytes, label: str) -> str:
    if isinstance(value, bytes):
        if len(value) > MAX_INPUT_BYTES:
            raise GraderFeedbackError(f"{label} exceeds {MAX_INPUT_BYTES} bytes")
        try:
            value = value.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise GraderFeedbackError(f"{label} is not UTF-8") from exc
    if not isinstance(value, str):
        raise GraderFeedbackError(f"{label} must be text or bytes")
    if len(value.encode("utf-8")) > MAX_INPUT_BYTES:
        raise GraderFeedbackError(f"{label} exceeds {MAX_INPUT_BYTES} bytes")
    clean = _ANSI_RE.sub("", value).replace("\r\n", "\n").replace("\r", "\n")
    if clean.count("\n") > MAX_LINES:
        raise GraderFeedbackError(f"{label} exceeds {MAX_LINES} lines")
    return clean


def canonical_source_bytes(
    report: str | bytes | None = None,
    test_output: str | bytes | None = None,
) -> bytes:
    """Return deterministic bytes used for official-source provenance hashing."""

    report_text = _clean_input(report, "official grader report") if report not in (None, "", b"") else ""
    output_text = _clean_input(test_output, "official test output") if test_output not in (None, "", b"") else ""
    parts: list[bytes] = []
    if report_text:
        parts.extend((b"official-grader-report\0", report_text.encode("utf-8"), b"\0"))
    if output_text:
        parts.extend((b"official-test-output\0", output_text.encode("utf-8"), b"\0"))
    if not parts:
        raise MissingInputError("official report or test output is required for provenance")
    return b"".join(parts)


def canonical_source_hash(
    report: str | bytes | None = None,
    test_output: str | bytes | None = None,
) -> str:
    """Hash the canonical, ANSI-free official report/output bytes."""

    return hashlib.sha256(canonical_source_bytes(report, test_output)).hexdigest()


def _canonical_source_artifact(
    report_text: str,
    output_text: str,
    provided: str,
) -> str:
    artifact = _sanitize(provided.strip()) if provided.strip() else ""
    if artifact:
        return artifact
    labels = []
    if report_text:
        labels.append("official-grader-report")
    if output_text:
        labels.append("official-test-output")
    return "+".join(labels)


def _strict_source_identity(
    report_text: str,
    output_text: str,
    *,
    source_artifact: str,
    source_hash: str,
) -> SourceArtifact:
    if not report_text and not output_text:
        raise MissingInputError("official report or test output is required for provenance")
    computed = hashlib.sha256(
        canonical_source_bytes(report_text, output_text)
    ).hexdigest()
    if source_hash:
        supplied = _normalise_hash(source_hash)
        if supplied != computed:
            raise ContradictoryReportError("source hash does not match canonical official evidence")
    artifact = _canonical_source_artifact(report_text, output_text, source_artifact)
    if not artifact:
        raise GraderFeedbackError("source artifact is required for strict provenance")
    return SourceArtifact(artifact, computed)


_PHASE_ALIASES = {
    "agent": "agent_turn",
    "agent_transport": "agent_turn",
    "agent_transport_failure": "agent_turn",
    "agent_turn": "agent_turn",
    "transport": "agent_turn",
    "collection": "collection",
    "collection_import_error": "collection",
    "test_collection": "collection",
    "test_collection_import_error": "collection",
    "call": "test",
    "execution": "test",
    "test": "test",
    "harness": "harness",
    "test_harness_failure": "harness",
    "setup": "setup",
    "teardown": "teardown",
    "patch": "patch",
    "runner": "runner",
}
_PHASE_PRIORITY = {
    "": 0,
    "patch": 10,
    "runner": 20,
    "test": 30,
    "harness": 40,
    "setup": 41,
    "teardown": 41,
    "collection": 50,
    "agent_turn": 60,
}


def _normalise_phase(value: Any, label: str) -> str:
    if not isinstance(value, str):
        raise GraderFeedbackError(f"{label} must be a phase string")
    normal = value.strip().lower().replace("-", "_").replace(" ", "_")
    try:
        return _PHASE_ALIASES[normal]
    except KeyError as exc:
        raise GraderFeedbackError(f"unsupported {label} {value!r}") from exc


def _set_inferred_phase(part: _Parsed, phase: str) -> None:
    if part.phase_explicit:
        return
    if _PHASE_PRIORITY[phase] >= _PHASE_PRIORITY[part.phase]:
        part.phase = phase


def _set_explicit_phase(part: _Parsed, phase: str, label: str) -> None:
    if part.phase_explicit and part.phase != phase:
        raise ContradictoryReportError(f"{label} phase disagrees")
    part.phase = phase
    part.phase_explicit = True


def _set_count(part: _Parsed, field: str, value: Any, label: str) -> None:
    if isinstance(value, bool) or not isinstance(value, (int, str)):
        raise GraderFeedbackError(f"{label} must be a non-negative integer")
    text = str(value).strip()
    if not text.isdigit():
        raise GraderFeedbackError(f"{label} must be a non-negative integer")
    try:
        count = int(text)
    except (TypeError, ValueError) as exc:
        raise GraderFeedbackError(f"{label} must be a non-negative integer") from exc
    old = getattr(part, field)
    if old is not None and old != count:
        raise ContradictoryReportError(f"{label} disagrees")
    setattr(part, field, count)


def _strict_bool(value: Any, label: str) -> bool:
    """Read an official boolean without accepting truthy runner chatter."""

    if not isinstance(value, bool):
        raise GraderFeedbackError(f"{label} must be boolean")
    return value


def _absorb_official_outcome(
    part: _Parsed,
    value: Mapping[str, Any],
    label: str,
) -> None:
    """Record SWE-bench per-instance truth before parsing auxiliary output."""

    if "instance_id" in value or "official_instance_id" in value:
        candidates = [
            value[key]
            for key in ("instance_id", "official_instance_id")
            if key in value
        ]
        instance_id = _required_string(candidates[0], f"{label}.instance_id")
        if any(
            _required_string(candidate, f"{label}.instance_id") != instance_id
            for candidate in candidates[1:]
        ):
            raise ContradictoryReportError("official instance_id declarations disagree")
        if part.official_instance_id and part.official_instance_id != instance_id:
            raise ContradictoryReportError("official instance_id declarations disagree")
        part.official_instance_id = instance_id
    if "resolved" in value or "official_resolved" in value:
        candidates = [
            value[key]
            for key in ("resolved", "official_resolved")
            if key in value
        ]
        resolved = _strict_bool(candidates[0], f"{label}.resolved")
        if any(_strict_bool(candidate, f"{label}.resolved") != resolved for candidate in candidates[1:]):
            raise ContradictoryReportError("official resolved fields disagree")
        if part.official_resolved is not None and part.official_resolved != resolved:
            raise ContradictoryReportError("official resolved outcome disagrees")
        part.official_resolved = resolved
        part.official_outcome_seen = True
    if "completed" in value or "official_completed" in value:
        candidates = [
            value[key]
            for key in ("completed", "official_completed")
            if key in value
        ]
        completed = _strict_bool(candidates[0], f"{label}.completed")
        if any(_strict_bool(candidate, f"{label}.completed") != completed for candidate in candidates[1:]):
            raise ContradictoryReportError("official completed fields disagree")
        if part.official_completed is not None and part.official_completed != completed:
            raise ContradictoryReportError("official completed outcome disagrees")
        part.official_completed = completed
        part.official_outcome_seen = True
    if not part.official_outcome_seen:
        return
    if part.official_infrastructure:
        part.status = Status.FAILED.value
        part.passed = False
        part.kind = FailureKind.INFRASTRUCTURE_ERROR.value
        part.categories = {FailureKind.INFRASTRUCTURE_ERROR.value}
        part.structured = True
        part.official_model_failure = False
        return
    if part.official_resolved is True:
        if part.official_completed is False:
            raise ContradictoryReportError("resolved=true requires completed=true")
        if part.status and part.status != Status.PASSED.value:
            raise ContradictoryReportError("resolved=true conflicts with a failed status")
        part.status = Status.PASSED.value
        part.passed = True
        return
    if part.official_resolved is False and part.official_completed is True:
        # A completed unresolved SWE-bench instance is an official model
        # failure even when the test process emitted no useful traceback.
        part.status = Status.FAILED.value
        part.official_model_failure = True
        _set_inferred_kind(part, FailureKind.MODEL_TEST_FAILURE.value)
        part.evidence.append("official grader: resolved=false, completed=true")
    elif part.official_resolved is False and part.status == Status.PASSED.value:
        raise ContradictoryReportError("resolved=false conflicts with a passed status")
    elif part.official_resolved is None and part.official_completed is True:
        # Completion without a resolved boolean cannot establish quality.
        if part.status == Status.PASSED.value:
            raise ContradictoryReportError("completed=true without resolved truth conflicts with pass")


def _set_inferred_count(part: _Parsed, field: str, value: Any, label: str) -> None:
    """Merge text-derived counts from nested test sessions conservatively."""

    if isinstance(value, bool) or not str(value).strip().isdigit():
        raise GraderFeedbackError(f"{label} must be a non-negative integer")
    count = int(str(value).strip())
    old = getattr(part, field)
    setattr(part, field, count if old is None else max(old, count))


def _absorb_metadata_mapping(part: _Parsed, value: Mapping[str, Any], label: str) -> None:
    """Read phase/count/symbol metadata from an official structured object."""

    if "phase" in value:
        _set_explicit_phase(part, _normalise_phase(value["phase"], f"{label}.phase"), label)
    count_aliases = {
        "collected_count": "collected_count",
        "collected": "collected_count",
        "collected_tests": "collected_count",
        "executed_count": "executed_count",
        "executed": "executed_count",
        "executed_tests": "executed_count",
    }
    for source, target in count_aliases.items():
        if source in value:
            _set_count(part, target, value[source], f"{label}.{source}")
    if "missing_symbols" in value:
        symbols = _string_list(value["missing_symbols"], f"{label}.missing_symbols")
        for symbol in symbols:
            if symbol not in part.missing_symbols:
                part.missing_symbols.append(symbol)
    _finalize_execution_metadata(part)


def _absorb_execution_metadata(part: _Parsed, line: str) -> None:
    """Classify execution phase and derive bounded test counts from one line."""

    transport = _AGENT_TRANSPORT_RE.search(line)
    if transport:
        part.agent_transport_seen = True
        _set_inferred_phase(part, "agent_turn")
    collection = _COLLECTION_RE.search(line)
    if collection:
        count = int(collection.group("count"))
        _set_inferred_count(part, "collected_count", count, "collected_count")
        if count == 0:
            part.collection_marker_seen = True
            _set_inferred_phase(part, "collection")
    if re.search(r"\bERROR\s+collecting\b", line, re.I):
        part.collection_marker_seen = True
        _set_inferred_phase(part, "collection")
    collection_error = _COLLECTION_ERROR_RE.match(line)
    if collection_error:
        test_id = _sanitize(collection_error.group("test"))
        if test_id and test_id not in part.ids:
            part.ids.append(test_id)
    if _IMPORT_ERROR_RE.search(line):
        part.import_error_seen = True
        _set_inferred_phase(part, "collection" if part.collection_marker_seen else "test")
    for match in _MISSING_SYMBOL_RE.finditer(line):
        symbol = match.group("symbol")
        if symbol not in part.missing_symbols:
            part.missing_symbols.append(symbol)
    for match in _MISSING_MODULE_RE.finditer(line):
        module = match.group("module")
        if module not in part.missing_symbols:
            part.missing_symbols.append(module)
    harness = _HARNESS_RE.search(line)
    if harness:
        part.harness_seen = True
        phase = harness.group("phase") or harness.group("nose_phase") or "harness"
        _set_inferred_phase(part, phase.lower())
    ran = _RAN_TESTS_RE.search(line)
    if ran:
        _set_inferred_count(part, "executed_count", ran.group("count"), "executed_count")
        _set_inferred_phase(part, "test")
    result_counts = {"passed": 0, "failed": 0, "errors": 0}
    found_result = False
    for match in _RESULT_COUNTS_RE.finditer(line):
        found_result = True
        for key in result_counts:
            value = match.group(key)
            if value is not None:
                result_counts[key] += int(value)
    if found_result and part.collected_count != 0:
        _set_inferred_count(
            part,
            "executed_count",
            sum(result_counts.values()),
            "executed_count",
        )
        _set_inferred_phase(part, "test")
    _finalize_execution_metadata(part)


def _finalize_execution_metadata(part: _Parsed) -> None:
    """Apply precedence after all report/output lines have been observed."""

    # A nested pytest invocation may report collection/import errors before
    # the outer suite continues and executes real tests.  Once executed test
    # evidence exists, retract the earlier inferred collection classification;
    # an explicit structured collection kind remains immutable.
    if (part.executed_count or 0) > 0 and not part.explicit_kind:
        part.collection_import_seen = False
        part.categories.discard(FailureKind.TEST_COLLECTION_IMPORT_ERROR.value)
        if part.kind == FailureKind.TEST_COLLECTION_IMPORT_ERROR.value:
            part.kind = ""
        if part.phase == "collection":
            part.phase = "test"

    if part.import_error_seen and (
        part.collection_marker_seen or part.collected_count == 0
    ) and part.executed_count in (None, 0):
        part.collection_import_seen = True
        if part.executed_count is None:
            part.executed_count = 0
    # A concrete missing symbol (``cannot import name X``) is a candidate/API
    # defect, not generic harness infrastructure.  It is actionable feedback
    # for the same Agent in the next quality round.  A missing external module
    # without a symbol remains a collection/environment classification.
    if part.collection_import_seen and part.missing_symbols and not part.explicit_kind:
        part.collection_import_seen = False
        part.categories.discard(FailureKind.TEST_COLLECTION_IMPORT_ERROR.value)
        part.categories.add(FailureKind.MODEL_TEST_FAILURE.value)
        part.kind = FailureKind.MODEL_TEST_FAILURE.value
        part.phase = "test"
    if part.agent_transport_seen:
        preferred = FailureKind.AGENT_TRANSPORT_FAILURE.value
    elif part.collection_import_seen:
        preferred = FailureKind.TEST_COLLECTION_IMPORT_ERROR.value
    elif part.harness_seen:
        preferred = FailureKind.TEST_HARNESS_FAILURE.value
    else:
        preferred = ""
    classification = preferred or (
        part.kind
        if part.kind
        in {
            FailureKind.AGENT_TRANSPORT_FAILURE.value,
            FailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
            FailureKind.TEST_HARNESS_FAILURE.value,
        }
        else ""
    )
    if classification:
        expected_phase = {
            FailureKind.AGENT_TRANSPORT_FAILURE.value: "agent_turn",
            FailureKind.TEST_COLLECTION_IMPORT_ERROR.value: "collection",
            FailureKind.TEST_HARNESS_FAILURE.value: "harness",
        }.get(classification)
        if (
            expected_phase is not None
            and part.phase_explicit
            and part.phase != expected_phase
        ):
            raise ContradictoryReportError(
                f"{classification} requires phase {expected_phase}, got {part.phase}"
            )
        if (
            classification == FailureKind.TEST_HARNESS_FAILURE.value
            and part.phase_explicit
            and part.phase not in {"harness", "setup", "teardown"}
        ):
            raise ContradictoryReportError(
                f"{classification} requires a harness/setup/teardown phase"
            )
        if preferred:
            if part.explicit_kind and part.kind != preferred:
                raise ContradictoryReportError(f"{part.kind} conflicts with {preferred}")
            if not part.explicit_kind:
                part.kind = preferred
            part.categories = {preferred}
        part.structured = True
        if not part.phase:
            part.phase = expected_phase or ""
        if not part.status:
            part.status = Status.FAILED.value
    if part.phase == "" and part.kind == FailureKind.MODEL_TEST_FAILURE.value:
        part.phase = "test"


def _looks_like_agent_jsonl(text: str) -> bool:
    """Recognize Codex-style event JSONL without weakening official JSON parsing."""

    lines = [line.strip() for line in text.splitlines() if line.strip()]
    if not lines or not any(line.startswith("{") for line in lines):
        return False
    return any(_AGENT_TRANSPORT_RE.search(line) for line in lines)


def _event_text(event: Mapping[str, Any]) -> str:
    """Extract only bounded event markers/messages from an agent JSON object."""

    values: list[str] = []
    event_type = event.get("type")
    if isinstance(event_type, str):
        values.append(event_type)
    for key in ("message", "error"):
        value = event.get(key)
        if isinstance(value, str):
            values.append(value)
        elif isinstance(value, Mapping):
            nested = value.get("message")
            if isinstance(nested, str):
                values.append(nested)
    item = event.get("item")
    if isinstance(item, Mapping):
        item_type = item.get("type")
        if isinstance(item_type, str):
            values.append(item_type)
        item_message = item.get("message")
        if isinstance(item_message, str):
            values.append(item_message)
    return " ".join(values)


def _parse_agent_jsonl(text: str, label: str) -> _Parsed:
    """Parse transport markers from agent JSONL while ignoring runner chatter."""

    part = _Parsed()
    saw_event = False
    for line_number, raw_line in enumerate(text.splitlines(), 1):
        line = raw_line.strip()
        if not line:
            continue
        event: Mapping[str, Any] | None = None
        if line.startswith("{"):
            try:
                decoded = json.loads(line)
            except json.JSONDecodeError as exc:
                # Codex logs can append plain diagnostic lines after JSONL.
                # A line that starts as JSON but is malformed is not safe to
                # silently skip, because it could hide the transport result.
                raise GraderFeedbackError(
                    f"parse {label} JSONL line {line_number}: {exc}"
                ) from exc
            if not isinstance(decoded, Mapping):
                raise GraderFeedbackError(f"parse {label} JSONL line {line_number} must be an object")
            event = decoded
            saw_event = True
            event_line = _event_text(event)
        else:
            event_line = line
        _absorb_execution_metadata(part, event_line)
        _absorb_category(part, event_line)
        if _AGENT_TRANSPORT_RE.search(event_line):
            part.exception = _merge_summary(part.exception, "agent transport disconnected")
            part.phase = "agent_turn"
    if not saw_event and not text.strip():
        raise GraderFeedbackError(f"parse {label} JSONL is empty")
    _finish_traceback(part)
    _finalize_execution_metadata(part)
    _finish_identity(part, label)
    if part.kind and not part.status:
        part.status = Status.FAILED.value
    return part


def _parse_document(text: str, label: str) -> _Parsed:
    stripped = text.strip()
    if not stripped:
        raise GraderFeedbackError(f"{label} is empty")
    if _looks_like_agent_jsonl(text):
        return _parse_agent_jsonl(text, label)
    if stripped.startswith("{") or stripped.startswith("["):
        try:
            value = json.loads(stripped)
        except json.JSONDecodeError as exc:
            if _looks_like_agent_jsonl(text):
                return _parse_agent_jsonl(text, label)
            raise GraderFeedbackError(f"parse {label} JSON: {exc}") from exc
        return _parse_json(value, label)
    return _parse_text(text, label)


def _parse_json(value: Any, label: str) -> _Parsed:
    if not isinstance(value, dict):
        raise GraderFeedbackError(f"parse {label} JSON: object is required")
    unknown = sorted(set(value) - _TOP_KEYS)
    if unknown:
        raise GraderFeedbackError(f"parse {label} JSON: unknown field(s): {', '.join(unknown)}")
    schema = value.get("schema")
    is_swe_bench_v2 = (
        schema in (None, _SWE_BENCH_V2_SCHEMA)
        and value.get("schema_version") == 2
        and _SWE_BENCH_OUTCOME_KEYS <= set(value)
    )
    is_swe_bench_single = (
        schema in (None, _SWE_BENCH_V2_SCHEMA)
        and bool(_SWE_BENCH_SINGLE_OUTCOME_KEYS & set(value))
        and value.get("schema_version", 2) == 2
    )
    is_untyped_infrastructure = (
        schema is None
        and value.get("error") not in (None, "", False)
    )
    if is_swe_bench_v2:
        schema = _SWE_BENCH_V2_SCHEMA
    elif is_swe_bench_single:
        schema = _SWE_BENCH_V2_SCHEMA
    elif is_untyped_infrastructure:
        schema = _SWE_BENCH_V2_SCHEMA
    elif not isinstance(schema, str) or schema not in _KNOWN_SCHEMAS:
        raise UnknownSchemaError(f"parse {label} JSON: unknown schema {schema!r}")
    if not is_swe_bench_v2 and _SWE_BENCH_KEYS & set(value):
        if _SWE_BENCH_KEYS & set(value) <= {
            "instance_id", "resolved", "completed", "official_resolved",
            "official_completed", "patch_apply_failed", "timeout",
            "infrastructure_error",
        }:
            pass
        else:
            fields = ", ".join(sorted(_SWE_BENCH_KEYS & set(value)))
            raise UnknownSchemaError(f"parse {label} JSON: SWE-bench v2 fields require schema_version=2: {fields}")
    version = value.get("schema_version")
    expected_versions = {2} if is_swe_bench_v2 or is_swe_bench_single else {1}
    if version is not None and version not in expected_versions:
        raise UnknownSchemaError(f"parse {label} JSON: unsupported schema_version {version!r}")
    part = _Parsed(schema=schema)
    _parse_json_identity(part, value, label)
    _absorb_metadata_mapping(part, value, label)
    if (
        is_swe_bench_v2
        or is_swe_bench_single
        or _SWE_BENCH_SINGLE_OUTCOME_KEYS & set(value)
        or {"official_resolved", "official_completed"} & set(value)
        or value.get("official_instance_id") not in (None, "")
    ):
        _absorb_official_outcome(part, value, label)
    if "status" in value:
        part.status = _normalise_status(value["status"])
    if is_swe_bench_v2 or is_swe_bench_single:
        _absorb_swe_bench_v2(part, value)
    _parse_json_source(part, value)
    if "evidence" in value:
        part.evidence.extend(
            item for item in _string_list(value["evidence"], "evidence")
            if not _NOISE_LINE_RE.match(item)
        )
    entries: list[dict[str, Any]] = []
    if "failure" in value:
        entries.append(_failure_object(value["failure"], label))
    if "failures" in value:
        if not isinstance(value["failures"], list):
            raise GraderFeedbackError(f"parse {label} JSON: failures must be an array")
        entries.extend(_failure_object(item, label) for item in value["failures"])
    if "tests" in value:
        if not isinstance(value["tests"], dict):
            raise GraderFeedbackError(f"parse {label} JSON: tests must be an object")
        tests = value["tests"]
        unknown_tests = sorted(set(tests) - {
            "failing_test_ids", "failed_test_ids", "test_ids", "failing_tests",
            "summary", "exit_code", "status",
        })
        if unknown_tests:
            raise GraderFeedbackError(
                f"parse {label} JSON: unknown tests field(s): {', '.join(unknown_tests)}"
            )
        part.ids.extend(_first_string_list(
            tests, ("failing_test_ids", "failed_test_ids", "test_ids", "failing_tests")
        ))
        if "status" in tests:
            test_status = _normalise_status(tests["status"])
            if part.status and part.status != test_status:
                raise ContradictoryReportError("test status disagrees")
            part.status = test_status
    if value.get("error") not in (None, "", False):
        error_value = value["error"]
        if isinstance(error_value, Mapping):
            error_value = error_value.get("message") or error_value.get("error") or str(error_value)
        if isinstance(error_value, (str, list)):
            message = _string_or_lines(error_value, "error")
        else:
            message = "official grader error"
        _absorb_execution_metadata(part, message)
        part.exception = _merge_summary(part.exception, message)
        _set_inferred_kind(part, FailureKind.INFRASTRUCTURE_ERROR.value)
        part.status = Status.FAILED.value
    direct = {key: value[key] for key in _FAILURE_KEYS if key in value and key not in {
        "source", "evidence", "resolved", "completed", "official_resolved",
        "official_completed", "instance_id", "error",
    }}
    if direct:
        entries.append(direct)
    for entry in entries:
        _absorb_failure_object(part, entry)
    _validate_part(part)
    return part


def _parse_json_identity(part: _Parsed, value: Mapping[str, Any], label: str) -> None:
    nested = value.get("identity")
    identity = FeedbackIdentity()
    if nested is not None:
        if not isinstance(nested, Mapping):
            raise GraderFeedbackError(f"parse {label} JSON: identity must be an object")
        identity = _identity_from_mapping(nested, label=f"{label}.identity")
    allowed = {alias for aliases in _IDENTITY_ALIASES.values() for alias in aliases}
    flat = _identity_from_mapping(
        {key: value[key] for key in value if key in allowed},
        label=label,
        allow_empty=True,
    )
    if not flat.is_empty():
        if not identity.is_empty() and identity != flat:
            raise ContradictoryReportError(f"{label} identity fields disagree")
        identity = flat
    if not identity.is_empty():
        part.identity = identity
        part.identity_seen = True


def _identity_from_values(
    *,
    question_id: str,
    arm_id: str,
    session_id: str,
    thread_id: str,
    patch_hash: str,
    label: str,
    allow_empty: bool,
) -> FeedbackIdentity:
    values = {
        "question_id": question_id,
        "arm_id": arm_id,
        "session_id": session_id,
        "thread_id": thread_id,
        "patch_hash": patch_hash,
    }
    present = {key: str(value or "").strip() for key, value in values.items()}
    if not any(present.values()):
        if allow_empty:
            return FeedbackIdentity()
        raise GraderFeedbackError(f"{label} is required")
    if not all(present.values()):
        missing = ", ".join(key for key, value in present.items() if not value)
        raise GraderFeedbackError(f"{label} is incomplete; missing {missing}")
    return FeedbackIdentity(
        question_id=present["question_id"],
        arm_id=present["arm_id"],
        session_id=present["session_id"],
        thread_id=present["thread_id"],
        patch_hash=_normalise_hash(present["patch_hash"]),
    )


def _coerce_identity(value: FeedbackIdentity | Mapping[str, Any], *, label: str) -> FeedbackIdentity:
    if isinstance(value, FeedbackIdentity):
        if not value.is_complete():
            raise GraderFeedbackError(f"{label} is incomplete")
        return FeedbackIdentity(
            question_id=_required_string(value.question_id, f"{label}.question_id"),
            arm_id=_required_string(value.arm_id, f"{label}.arm_id"),
            session_id=_required_string(value.session_id, f"{label}.session_id"),
            thread_id=_required_string(value.thread_id, f"{label}.thread_id"),
            patch_hash=_normalise_hash(value.patch_hash),
        )
    if isinstance(value, Mapping):
        return _identity_from_mapping(value, label=label)
    raise GraderFeedbackError(f"{label} must be an identity object")


def _identity_from_mapping(
    value: Mapping[str, Any], *, label: str, allow_empty: bool = False
) -> FeedbackIdentity:
    allowed = {alias for aliases in _IDENTITY_ALIASES.values() for alias in aliases}
    unknown_identity = set(value) - allowed
    if unknown_identity:
        raise GraderFeedbackError(
            f"{label} contains unsupported identity field(s): {', '.join(sorted(unknown_identity))}"
        )
    resolved: dict[str, str] = {}
    for canonical, aliases in _IDENTITY_ALIASES.items():
        seen: list[str] = []
        for alias in aliases:
            if alias in value:
                seen.append(_required_string(value[alias], f"{label}.{alias}"))
        if seen and len(set(seen)) != 1:
            raise ContradictoryReportError(f"{label}.{canonical} aliases disagree")
        if seen:
            resolved[canonical] = seen[0]
    return _identity_from_values(
        question_id=resolved.get("question_id", ""),
        arm_id=resolved.get("arm_id", ""),
        session_id=resolved.get("session_id", ""),
        thread_id=resolved.get("thread_id", ""),
        patch_hash=resolved.get("patch_hash", ""),
        label=label,
        allow_empty=allow_empty,
    )


def _merge_identity(part: _Parsed, expected: FeedbackIdentity) -> FeedbackIdentity:
    actual = part.identity if part.identity_seen else FeedbackIdentity()
    if expected.is_empty():
        return actual
    if actual.is_empty():
        raise ContradictoryReportError("feedback identity is missing from official evidence")
    if actual != expected:
        raise ContradictoryReportError("feedback identity/hash drifted")
    return actual


def _absorb_swe_bench_v2(part: _Parsed, value: Mapping[str, Any]) -> None:
    """Read the real aggregate outcome sets without guessing free-form logs."""

    if _SWE_BENCH_SINGLE_OUTCOME_KEYS & set(value) or {
        "official_resolved", "official_completed"
    } & set(value):
        _absorb_official_outcome(part, value, "official grader")
        infra_marker = any(
            value.get(field) is True
            for field in ("patch_apply_failed", "timeout", "infrastructure_error")
        )
        if infra_marker and part.official_resolved is True:
            raise ContradictoryReportError(
                "resolved=true conflicts with an infrastructure failure"
            )
        if infra_marker and part.kind == FailureKind.MODEL_TEST_FAILURE.value:
            part.kind = ""
            part.categories.clear()
            part.official_model_failure = False
        if infra_marker:
            part.official_infrastructure = True
        if value.get("patch_apply_failed") is True:
            _set_inferred_kind(part, FailureKind.PATCH_APPLY_FAILURE.value)
            part.status = Status.FAILED.value
        if value.get("timeout") is True:
            _set_inferred_kind(part, FailureKind.TIMEOUT.value)
            part.status = Status.FAILED.value
        if value.get("infrastructure_error") is True:
            _set_inferred_kind(part, FailureKind.INFRASTRUCTURE_ERROR.value)
            part.status = Status.FAILED.value
        for field in ("error",):
            if field in value and value[field] not in (None, "", False):
                if part.official_resolved is True:
                    raise ContradictoryReportError(
                        "resolved=true conflicts with an infrastructure error"
                    )
                if part.kind == FailureKind.MODEL_TEST_FAILURE.value:
                    part.kind = ""
                    part.categories.clear()
                    part.official_model_failure = False
                part.official_infrastructure = True
                error_value = value[field]
                if isinstance(error_value, Mapping):
                    error_value = error_value.get("message") or error_value.get("error") or str(error_value)
                message = _string_or_lines(error_value, field) if isinstance(error_value, (str, list)) else "official grader error"
                _absorb_execution_metadata(part, message)
                part.exception = _merge_summary(part.exception, message)
                _set_inferred_kind(part, FailureKind.INFRASTRUCTURE_ERROR.value)
                part.status = Status.FAILED.value
        return

    resolved = _string_list(value.get("resolved_ids", []), "resolved_ids")
    completed = _string_list(value.get("completed_ids", []), "completed_ids")
    unresolved = _string_list(value.get("unresolved_ids", []), "unresolved_ids")
    errors = _string_list(value.get("error_ids", []), "error_ids")
    # SWE-bench completed_ids only proves that grading ran to completion; it
    # does not mean the instance resolved.  Only resolved_ids is success truth.
    part.passed_ids.extend(resolved)
    part.ids.extend(unresolved + errors)
    part.passed = bool(resolved) and not unresolved and not errors
    if resolved and not unresolved and not errors:
        # The aggregate's all-resolved result is the same success truth as a
        # singleton ``resolved=true`` report.  Mark it authoritative so a
        # noisy test stream cannot reverse a successful official run.
        part.official_resolved = True
        part.official_completed = True if completed else None
        part.official_outcome_seen = True
    elif errors:
        # ``error_ids`` are official infrastructure outcomes, not candidate
        # test failures.  Preserve that precedence when output is noisy.
        part.official_completed = True if completed else None
        part.official_outcome_seen = True
        part.official_infrastructure = True
    if value.get("patch_apply_failed") is True:
        _set_inferred_kind(part, FailureKind.PATCH_APPLY_FAILURE.value)
    if value.get("timeout") is True:
        _set_inferred_kind(part, FailureKind.TIMEOUT.value)
    if value.get("infrastructure_error") is True:
        _set_inferred_kind(part, FailureKind.INFRASTRUCTURE_ERROR.value)
    if errors and not part.kind:
        # error_ids are an authoritative runner/grader error channel; test
        # output cannot silently turn it into model-quality feedback.
        _set_inferred_kind(part, FailureKind.INFRASTRUCTURE_ERROR.value)
    if not part.status:
        if part.kind:
            part.status = Status.FAILED.value
        elif unresolved:
            part.status = Status.UNRESOLVED.value
        elif resolved and not unresolved:
            part.status = Status.PASSED.value
        elif completed:
            part.status = Status.UNRESOLVED.value
    for field in ("error",):
        if field in value:
            message = _string_or_lines(value[field], field)
            _absorb_execution_metadata(part, message)
            part.exception = _merge_summary(part.exception, message)
            _absorb_category(part, message)


def _failure_object(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise GraderFeedbackError(f"parse {label} JSON: failure must be an object")
    unknown = sorted(set(value) - _FAILURE_KEYS)
    if unknown:
        raise GraderFeedbackError(f"parse {label} JSON: unknown failure field(s): {', '.join(unknown)}")
    return value


def _parse_json_source(part: _Parsed, value: dict[str, Any]) -> None:
    nested = value.get("source")
    source = SourceArtifact()
    seen = False
    if nested is not None:
        if not isinstance(nested, dict):
            raise GraderFeedbackError("source must be an object")
        if set(nested) - _SOURCE_KEYS:
            unknown = sorted(set(nested) - _SOURCE_KEYS)
            raise GraderFeedbackError(f"unknown source field(s): {', '.join(unknown)}")
        source, seen = _source_from_mapping(nested)
    top, top_seen = _source_from_mapping(value)
    if top_seen:
        if seen and source != top:
            raise ContradictoryReportError("source fields disagree")
        source, seen = top, True
    if seen:
        part.artifact, part.source_seen = source, True


def _source_from_mapping(value: Mapping[str, Any]) -> tuple[SourceArtifact, bool]:
    artifact = _first_string(value, ("artifact", "source_artifact"))
    digest = _first_string(value, ("hash", "sha256", "source_hash", "artifact_hash", "source_artifact_hash"))
    if artifact is None and digest is None:
        return SourceArtifact(), False
    return SourceArtifact(_sanitize(artifact or ""), _normalise_hash(digest or "") if digest else ""), True


def _absorb_failure_object(part: _Parsed, entry: Mapping[str, Any]) -> None:
    if not entry:
        raise ContradictoryReportError("failure object is empty")
    _absorb_metadata_mapping(part, entry, "failure")
    for key in ("kind", "failure_kind"):
        if key in entry:
            _set_kind(part, entry[key], explicit=True)
    if "status" in entry:
        status = _normalise_status(entry["status"])
        if part.status and part.status != status:
            raise ContradictoryReportError("failure status disagrees")
        part.status = status
    ids = _first_string_list(entry, ("failing_test_ids", "failed_test_ids", "test_ids", "failing_tests"))
    if "test_id" in entry:
        ids.append(_required_string(entry["test_id"], "test_id"))
    part.ids.extend(ids)
    exception = ""
    message = ""
    for key in ("exception", "exception_type"):
        if key in entry:
            value = _string_or_lines(entry[key], key)
            _absorb_execution_metadata(part, value)
            exception = _merge_summary(exception, value)
            part.exception = _merge_summary(part.exception, value)
    for key in ("summary", "message"):
        if key in entry:
            value = _string_or_lines(entry[key], key)
            _absorb_execution_metadata(part, value)
            message = _merge_summary(message, value)
            part.exception = _merge_summary(part.exception, value)
    exception_match = _EXCEPTION_RE.match(exception)
    if exception_match and exception_match.group("message") and not message:
        exception = exception_match.group("type") or exception
        message = exception_match.group("message") or ""
    traceback = ""
    for key in ("traceback", "traceback_tail"):
        if key in entry:
            value = _tail_text(_string_or_lines(entry[key], key))
            _absorb_execution_metadata(part, value)
            traceback = _merge_summary(traceback, value)
            part.traceback = _merge_summary(part.traceback, value)
    actual = _string_or_lines(entry["actual"], "actual") if "actual" in entry else ""
    expected = _string_or_lines(entry["expected"], "expected") if "expected" in entry else ""
    for key in ("assertion", "assertion_summary"):
        if key in entry:
            value = _string_or_lines(entry[key], key)
            part.assertion = _merge_summary(part.assertion, value)
            parsed_actual, parsed_expected = _parse_actual_expected(value)
            actual = actual or parsed_actual
            expected = expected or parsed_expected
    file_name = _string_or_lines(entry["file"], "file") if "file" in entry else ""
    line_number = _line_number(entry.get("line"), "line") if "line" in entry else None
    if file_name:
        file_name = _sanitize(file_name)
    if ids or exception or traceback or actual or expected or file_name or line_number is not None:
        failure_ids = ids or [""]
        for test_id in failure_ids:
            part.failures.append(FailureEvidence(
                test_id=_sanitize(test_id),
                file=file_name,
                line=line_number,
                exception=_summary_text(exception),
                message=_summary_text(message),
                actual=_summary_text(actual),
                expected=_summary_text(expected),
                traceback_tail=_tail_text(traceback),
            ))
        part.current_failure = len(part.failures) - 1
    if "evidence" in entry:
        part.evidence.extend(
            item for item in _string_list(entry["evidence"], "evidence")
            if not _NOISE_LINE_RE.match(item)
        )
    if not part.kind and (ids or exception or traceback or actual or expected or file_name or line_number is not None):
        _set_inferred_kind(part, FailureKind.MODEL_TEST_FAILURE.value)
    part.structured = part.kind not in ("", FailureKind.FEEDBACK_UNAVAILABLE.value)


def _parse_text(text: str, label: str) -> _Parsed:
    part = _Parsed()
    for raw_line in text.splitlines():
        line = raw_line.strip()
        if not line:
            continue
        if _NOISE_LINE_RE.match(line):
            continue
        _absorb_execution_metadata(part, line)
        _absorb_text_line_details(part, line)
        key_value = _key_value(line)
        if key_value is not None:
            key, value = key_value
            _absorb_text_field(part, key, value)
            _absorb_category(part, line)
            continue
        _absorb_test_line(part, line)
        _absorb_category(part, line)
    _finish_traceback(part)
    _finalize_execution_metadata(part)
    _finish_identity(part, label)
    if len(part.categories) > 1:
        raise ContradictoryReportError(f"parse {label}: multiple failure categories")
    _check_passed_failed_ids(part)
    if part.kind:
        part.structured = part.kind != FailureKind.FEEDBACK_UNAVAILABLE.value
    return part


def _finish_identity(part: _Parsed, label: str) -> None:
    if not part.identity_seen:
        return
    part.identity = _identity_from_values(
        question_id=part.identity_fields.get("question_id", ""),
        arm_id=part.identity_fields.get("arm_id", ""),
        session_id=part.identity_fields.get("session_id", ""),
        thread_id=part.identity_fields.get("thread_id", ""),
        patch_hash=part.identity_fields.get("patch_hash", ""),
        label=f"{label} identity",
        allow_empty=False,
    )


def _absorb_text_line_details(part: _Parsed, line: str) -> None:
    """Capture bounded failure details without treating runner chatter as evidence."""

    if line.startswith("Traceback (most recent call last):"):
        _finish_traceback(part)
        part.traceback_active = True
        part.traceback_lines = [line]
        _ensure_failure(part)
        return
    if part.traceback_active:
        if _TEST_LINE_RE.match(line) or line.startswith(("====", "--- ")):
            _finish_traceback(part)
        else:
            part.traceback_lines.append(line)
            if len(part.traceback_lines) >= MAX_TRACEBACK_LINES:
                _finish_traceback(part)
    file_match = _FILE_LINE_RE.search(line)
    if file_match:
        file_name = file_match.group("quoted_file") or file_match.group("plain_file") or ""
        line_text = file_match.group("quoted_line") or file_match.group("plain_line") or ""
        _update_current_failure(part, file=_sanitize(file_name), line=_line_number(line_text, "line"))
    exception_match = _EXCEPTION_RE.match(line)
    if exception_match:
        exception_type = exception_match.group("type") or ""
        message = exception_match.group("message") or ""
        _ensure_failure(part)
        _update_current_failure(
            part,
            exception=_summary_text(exception_type),
            message=_summary_text(message),
        )
        part.exception = _merge_summary(part.exception, _summary_text(
            f"{exception_type}: {message}".rstrip(": ")
        ))
        if part.traceback_active:
            _finish_traceback(part)
    assertion = _ASSERTION_RE.search(line)
    if assertion:
        actual, expected = assertion.groups()
        _ensure_failure(part)
        _update_current_failure(
            part,
            actual=_summary_text(actual),
            expected=_summary_text(expected),
        )
        part.assertion = _merge_summary(part.assertion, _sanitize(line))


def _ensure_failure(part: _Parsed, test_id: str = "") -> None:
    test_id = _sanitize(test_id)
    if part.current_failure is None or (
        test_id and part.failures[part.current_failure].test_id
        and part.failures[part.current_failure].test_id != test_id
    ):
        part.failures.append(FailureEvidence(test_id=test_id))
        part.current_failure = len(part.failures) - 1
    elif test_id and not part.failures[part.current_failure].test_id:
        _update_current_failure(part, test_id=test_id)


def _update_current_failure(part: _Parsed, **updates: Any) -> None:
    _ensure_failure(part)
    current = part.failures[part.current_failure or 0]
    clean_updates = {
        key: (_sanitize(value) if isinstance(value, str) else value)
        for key, value in updates.items()
        if value not in (None, "")
    }
    part.failures[part.current_failure or 0] = dataclasses.replace(current, **clean_updates)


def _finish_traceback(part: _Parsed) -> None:
    if not part.traceback_lines:
        part.traceback_active = False
        return
    tail = _tail_text("\n".join(part.traceback_lines))
    part.traceback = _merge_summary(part.traceback, tail)
    _update_current_failure(part, traceback_tail=tail)
    part.traceback_lines = []
    part.traceback_active = False


def _key_value(line: str) -> tuple[str, str] | None:
    match = _KEY_RE.match(line)
    if not match or match.group(1).upper() in {"FAIL", "FAILED", "ERROR", "PASSED"}:
        return None
    if line.startswith(("===", "---", "#")):
        return None
    return match.group(1), match.group(2).strip()


def _absorb_text_field(part: _Parsed, key: str, value: str) -> None:
    normal = key.strip().lower().replace("-", "_")
    if normal == "instance_id":
        instance_id = _required_string(value, "instance_id")
        if part.official_instance_id and part.official_instance_id != instance_id:
            raise ContradictoryReportError("official instance_id declarations disagree")
        part.official_instance_id = instance_id
    elif normal in {"question_id", "question", "task_id", "arm_id", "arm", "variant_id", "session_id", "session", "thread_id", "thread", "patch_hash", "candidate_hash"}:
        values = dict(part.identity_fields)
        canonical = next(
            name for name, aliases in _IDENTITY_ALIASES.items() if normal in aliases
        )
        candidate = _sanitize(value)
        if values.get(canonical) and values[canonical] != candidate:
            raise ContradictoryReportError(f"text identity field {canonical} disagrees")
        values[canonical] = candidate
        part.identity_fields = values
        part.identity_seen = True
    elif normal == "schema":
        _validate_schema(value)
        part.schema = value
    elif normal == "schema_version":
        if value.strip() != "1":
            raise UnknownSchemaError(f"unsupported schema_version {value!r}")
    elif normal == "phase":
        _set_explicit_phase(part, _normalise_phase(value, "phase"), "text evidence")
    elif normal in {
        "collected_count",
        "collected",
        "collected_tests",
        "executed_count",
        "executed",
        "executed_tests",
    }:
        target = "collected_count" if normal.startswith("collected") else "executed_count"
        _set_count(part, target, value, normal)
        _finalize_execution_metadata(part)
    elif normal == "missing_symbols":
        for symbol in _split_ids(value):
            if symbol not in part.missing_symbols:
                part.missing_symbols.append(symbol)
        _finalize_execution_metadata(part)
    elif normal in {"status", "report_status", "result_status"}:
        status = _normalise_status(value)
        if part.status and part.status != status:
            raise ContradictoryReportError("status disagrees")
        part.status = status
    elif normal in {"resolved", "official_resolved", "completed", "official_completed"}:
        boolean = _strict_bool(value.strip().lower() == "true" if value.strip().lower() in {"true", "false"} else value, normal)
        values = {
            "resolved": boolean,
        } if normal in {"resolved", "official_resolved"} else {
            "completed": boolean,
        }
        _absorb_official_outcome(part, values, "text evidence")
    elif normal in {"failure_kind", "kind"}:
        _set_kind(part, value, explicit=True)
    elif normal in {"failing_test_id", "failed_test_id", "test_id", "failing_test_ids", "failed_test_ids", "test_ids", "failing_tests"}:
        part.ids.extend(_split_ids(value))
    elif normal in {"exception", "exception_type", "message", "summary"}:
        part.exception = _merge_summary(part.exception, value)
    elif normal == "traceback":
        part.traceback = _merge_summary(part.traceback, value)
        _update_current_failure(part, traceback_tail=_tail_text(value))
    elif normal in {"assertion", "assertion_summary"}:
        part.assertion = _merge_summary(part.assertion, value)
        actual, expected = _parse_actual_expected(value)
        _update_current_failure(part, actual=actual, expected=expected)
    elif normal in {"actual", "expected"}:
        _update_current_failure(part, **{normal: _summary_text(value)})
    elif normal in {"file", "path"}:
        _update_current_failure(part, file=_sanitize(value))
    elif normal == "line":
        # Some test reporters use line=0 as an explicit "unknown location"
        # sentinel.  Preserve fail-fast validation for structured envelopes,
        # but do not let this text-only sentinel discard the whole official
        # test output.
        line_value = str(value).strip()
        if line_value.isdigit() and line_value != "0":
            _update_current_failure(part, line=_line_number(line_value, "line"))
    elif normal in {"source", "source_artifact", "artifact"}:
        _absorb_text_artifact(part, value, nested=normal == "source")
    elif normal in {"source_hash", "artifact_hash", "source_artifact_hash", "hash", "sha256"}:
        _absorb_text_hash(part, value)
    elif normal == "evidence":
        part.evidence.append(_sanitize(value))
    elif "schema" in normal:
        raise UnknownSchemaError(f"unknown schema field {key!r}")


def _absorb_text_artifact(part: _Parsed, value: str, *, nested: bool) -> None:
    if nested and "=" in value:
        for token in value.split():
            key, separator, token_value = token.partition("=")
            if separator:
                _absorb_text_field(part, key, token_value)
        return
    artifact = _sanitize(value)
    if part.source_seen and part.artifact.artifact and part.artifact.artifact != artifact:
        raise ContradictoryReportError("source artifact disagrees")
    part.artifact = dataclasses.replace(part.artifact, artifact=artifact)
    part.source_seen = True


def _absorb_text_hash(part: _Parsed, value: str) -> None:
    digest = _normalise_hash(value)
    if part.source_seen and part.artifact.hash and part.artifact.hash != digest:
        raise ContradictoryReportError("source hash disagrees")
    part.artifact = dataclasses.replace(part.artifact, hash=digest)
    part.source_seen = True


def _absorb_test_line(part: _Parsed, line: str) -> None:
    match = _TEST_LINE_RE.match(line)
    if match is None:
        suffix_match = _TEST_SUFFIX_RE.match(line)
        if suffix_match and suffix_match.group(1).strip().lower().startswith(("collected ", "ran ")):
            suffix_match = None
        if suffix_match:
            match = _TEST_LINE_RE.match(f"{suffix_match.group(2)} {suffix_match.group(1).strip()}")
    if match:
        state, value = match.groups()
        test_id = value.split(" - ", 1)[0].strip()
        if state.upper() == "PASSED":
            part.passed = True
            if test_id:
                part.passed_ids.append(_sanitize(test_id))
            return
        if (
            FailureKind.TEST_COLLECTION_IMPORT_ERROR.value in part.categories
            and (part.collected_count or 0) > 0
            and not part.explicit_kind
        ):
            part.categories.discard(FailureKind.TEST_COLLECTION_IMPORT_ERROR.value)
            part.collection_import_seen = False
            part.kind = ""
            part.phase = "test"
        if (
            FailureKind.TEST_HARNESS_FAILURE.value in part.categories
            and (part.collected_count or 0) > 0
            and not part.explicit_kind
        ):
            part.categories.discard(FailureKind.TEST_HARNESS_FAILURE.value)
            part.harness_seen = False
            part.kind = ""
            part.phase = "test"
        if _has_category_marker(line, part):
            return
        if test_id:
            part.ids.append(_sanitize(test_id))
        _ensure_failure(part, test_id)
        detail = value.split(" - ", 1)[1] if " - " in value else ""
        if detail:
            exception_match = _EXCEPTION_RE.match(detail.strip())
            if exception_match:
                _update_current_failure(
                    part,
                    exception=_summary_text(exception_match.group("type") or ""),
                    message=_summary_text(exception_match.group("message") or ""),
                )
            actual, expected = _parse_actual_expected(detail)
            _update_current_failure(part, actual=actual, expected=expected)
        _set_inferred_kind(part, FailureKind.MODEL_TEST_FAILURE.value)
        return
    upper = line.upper()
    if upper.startswith("FAIL:") or upper.startswith("ERROR:"):
        if _has_category_marker(line, part):
            return
        value = line.split(":", 1)[1].strip()
        if value and (
            "::" in value
            or re.search(r"(?:^|[ /])(?:test_[^ /]+|[^ /]+_test)\.py(?:\s|$)", value)
        ):
            part.ids.append(_sanitize(value))
            _ensure_failure(part, value)
            _set_inferred_kind(part, FailureKind.MODEL_TEST_FAILURE.value)
    kind, has_failure, has_pass = _summary(line)
    if has_failure:
        _set_inferred_kind(part, kind)
    elif has_pass:
        part.passed = True
    if line.startswith("E   ") or line.startswith("Traceback (most recent call last):") or line.startswith("AssertionError:"):
        detail = line.removeprefix("E   ").strip()
        if "assert" in detail.lower() or "AssertionError" in detail:
            part.assertion = _merge_summary(part.assertion, detail)
        if "Error" in detail and not detail.startswith("Traceback"):
            part.exception = _merge_summary(part.exception, detail)
        if detail.startswith("Traceback"):
            part.traceback = _merge_summary(part.traceback, detail)
        _ensure_failure(part)
        _set_inferred_kind(part, FailureKind.MODEL_TEST_FAILURE.value)


def _summary(line: str) -> tuple[str, bool, bool]:
    has_failure = False
    has_pass = False
    for count_text, state in _SUMMARY_RE.findall(line):
        count = int(count_text)
        if state.lower() == "passed":
            has_pass = has_pass or count >= 0
        elif count > 0:
            has_failure = True
    match = _FAILURES_COUNT_RE.search(line)
    if match and int(match.group(1)) > 0:
        has_failure = True
    return FailureKind.MODEL_TEST_FAILURE.value, has_failure, has_pass


def _absorb_category(part: _Parsed, line: str) -> None:
    # One runner line may say both "runner error" and "timed out".  The
    # specific terminal condition wins; generic infrastructure wording must
    # not turn a timeout into a contradictory two-category report.
    if _AGENT_TRANSPORT_RE.search(line):
        part.categories.add(FailureKind.AGENT_TRANSPORT_FAILURE.value)
        _set_inferred_phase(part, "agent_turn")
    elif _PATCH_RE.search(line):
        part.categories.add(FailureKind.PATCH_APPLY_FAILURE.value)
        _set_inferred_phase(part, "patch")
    elif _TIMEOUT_RE.search(line):
        part.categories.add(FailureKind.TIMEOUT.value)
        _set_inferred_phase(part, "runner")
    elif _INFRA_RE.search(line):
        part.categories.add(FailureKind.INFRASTRUCTURE_ERROR.value)
        _set_inferred_phase(part, "runner")
    if FailureKind.AGENT_TRANSPORT_FAILURE.value in part.categories:
        part.categories.discard(FailureKind.PATCH_APPLY_FAILURE.value)
        part.categories.discard(FailureKind.TIMEOUT.value)
        part.categories.discard(FailureKind.INFRASTRUCTURE_ERROR.value)
    elif FailureKind.PATCH_APPLY_FAILURE.value in part.categories:
        part.categories.discard(FailureKind.TIMEOUT.value)
        part.categories.discard(FailureKind.INFRASTRUCTURE_ERROR.value)
    elif FailureKind.TIMEOUT.value in part.categories:
        part.categories.discard(FailureKind.INFRASTRUCTURE_ERROR.value)
    inferred_preferred = next(iter(part.categories), "")
    if inferred_preferred and part.kind != inferred_preferred and not part.explicit_kind:
        part.kind = ""
    if len(part.categories) > 1:
        raise ContradictoryReportError("multiple failure categories")
    for kind in part.categories:
        _set_inferred_kind(part, kind)


def _has_category_marker(line: str, part: _Parsed) -> bool:
    before = set(part.categories)
    _absorb_category(part, line)
    return bool(part.categories - before) or bool(part.categories)


def _set_kind(part: _Parsed, value: Any, *, explicit: bool) -> None:
    kind = _normalise_kind(value)
    if part.kind and part.kind != kind:
        raise ContradictoryReportError(f"{part.kind} conflicts with {kind}")
    part.kind = kind
    part.explicit_kind = part.explicit_kind or explicit
    part.structured = kind != FailureKind.FEEDBACK_UNAVAILABLE.value


def _set_inferred_kind(part: _Parsed, kind: str) -> None:
    specialized = {
        FailureKind.AGENT_TRANSPORT_FAILURE.value,
        FailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
        FailureKind.TEST_HARNESS_FAILURE.value,
    }
    if kind == FailureKind.MODEL_TEST_FAILURE.value and part.kind in specialized and not part.explicit_kind:
        return
    if part.kind and part.kind != kind:
        raise ContradictoryReportError(f"{part.kind} conflicts with {kind}")
    part.kind = kind
    part.categories.add(kind)
    part.structured = kind != FailureKind.FEEDBACK_UNAVAILABLE.value


def _merge_execution_metadata(target: _Parsed, source: _Parsed) -> None:
    """Merge report/output execution facts without inventing missing values."""

    if source.phase:
        if target.phase_explicit and source.phase_explicit and target.phase != source.phase:
            raise ContradictoryReportError("execution phase disagrees across evidence")
        if source.phase_explicit and not target.phase_explicit:
            target.phase = source.phase
            target.phase_explicit = True
        elif not target.phase_explicit and _PHASE_PRIORITY[source.phase] > _PHASE_PRIORITY[target.phase]:
            target.phase = source.phase
    for field in ("collected_count", "executed_count"):
        source_value = getattr(source, field)
        target_value = getattr(target, field)
        if source_value is None:
            continue
        if target_value is not None and target_value != source_value:
            raise ContradictoryReportError(f"{field} disagrees across evidence")
        setattr(target, field, source_value)
    for symbol in source.missing_symbols:
        if symbol not in target.missing_symbols:
            target.missing_symbols.append(symbol)
    target.agent_transport_seen = target.agent_transport_seen or source.agent_transport_seen
    target.import_error_seen = target.import_error_seen or source.import_error_seen
    target.collection_marker_seen = target.collection_marker_seen or source.collection_marker_seen
    target.collection_import_seen = target.collection_import_seen or source.collection_import_seen
    target.harness_seen = target.harness_seen or source.harness_seen


def _merge_parts(parts: Iterable[_Parsed]) -> _Parsed:
    merged = _Parsed()
    for part in parts:
        if part.official_outcome_seen:
            if merged.official_outcome_seen:
                if (
                    merged.official_resolved != part.official_resolved
                    or merged.official_completed != part.official_completed
                ):
                    raise ContradictoryReportError(
                        "official resolved/completed outcome disagrees across evidence"
                    )
            else:
                merged.official_resolved = part.official_resolved
                merged.official_completed = part.official_completed
                merged.official_outcome_seen = True
            merged.official_model_failure = (
                merged.official_model_failure or part.official_model_failure
            )
            merged.official_infrastructure = (
                merged.official_infrastructure or part.official_infrastructure
            )
        if part.official_instance_id:
            if (
                merged.official_instance_id
                and merged.official_instance_id != part.official_instance_id
            ):
                raise ContradictoryReportError(
                    "official instance_id disagrees across evidence"
                )
            merged.official_instance_id = part.official_instance_id
        authoritative = merged.official_outcome_seen
        if part.status:
            if merged.status and merged.status != part.status:
                # Official SWE-bench summaries use UNRESOLVED for every
                # non-passing task, while test output supplies the concrete
                # FAILED phase/kind. Those facts are complementary. Only a
                # genuine PASSED versus FAILED disagreement is contradictory.
                if (
                    {merged.status, part.status} == {Status.PASSED.value, Status.FAILED.value}
                    and not (authoritative and not part.official_outcome_seen)
                ):
                    raise ContradictoryReportError("status conflicts across report and output")
            if not merged.status or (
                part.status == Status.FAILED.value
                and not (authoritative and not part.official_outcome_seen)
            ):
                merged.status = part.status
        if part.kind:
            if merged.kind and merged.kind != part.kind:
                if not (authoritative and not part.official_outcome_seen):
                    raise ContradictoryReportError("failure kind conflicts across report and output")
            elif not (authoritative and not part.official_outcome_seen):
                merged.kind = part.kind
            merged.explicit_kind = merged.explicit_kind or part.explicit_kind
        merged.structured = merged.structured or part.structured
        _merge_execution_metadata(merged, part)
        if not (authoritative and not part.official_outcome_seen):
            merged.passed = merged.passed or part.passed
        merged.ids.extend(part.ids)
        merged.passed_ids.extend(part.passed_ids)
        merged.exception = _merge_summary(merged.exception, part.exception)
        merged.traceback = _merge_summary(merged.traceback, part.traceback)
        merged.assertion = _merge_summary(merged.assertion, part.assertion)
        merged.evidence.extend(part.evidence)
        if part.identity_seen:
            if merged.identity_seen and merged.identity != part.identity:
                raise ContradictoryReportError("feedback identity/hash drifted across evidence")
            merged.identity = part.identity
            merged.identity_seen = True
        merged.failures.extend(part.failures)
        if not (authoritative and not part.official_outcome_seen):
            merged.categories.update(part.categories)
        if part.source_seen:
            if merged.source_seen and merged.artifact != part.artifact:
                raise ContradictoryReportError("source identity disagrees")
            merged.artifact, merged.source_seen = part.artifact, True
        if part.schema:
            if merged.schema and merged.schema != part.schema:
                raise ContradictoryReportError("schema disagrees")
            merged.schema = part.schema
    if len(merged.categories) > 1:
        raise ContradictoryReportError("multiple failure categories")
    _finalize_execution_metadata(merged)
    _apply_official_outcome(merged)
    if (
        merged.status == Status.UNRESOLVED.value
        and merged.kind
        and merged.kind != FailureKind.FEEDBACK_UNAVAILABLE.value
    ):
        # SWE-bench uses unresolved for an executed-but-not-resolved patch.
        # A structured pytest failure supplies the missing quality category.
        merged.status = Status.FAILED.value
    _check_passed_failed_ids(merged)
    merged.failures = _unique_failures(merged.failures)
    return merged


def _merge_source(part: _Parsed, artifact: str, digest: str, artifact_path: str | pathlib.Path | None) -> SourceArtifact:
    provided = SourceArtifact(_sanitize(artifact), _normalise_hash(digest) if digest else "")
    if artifact_path:
        try:
            data = pathlib.Path(artifact_path).read_bytes()
        except OSError as exc:
            raise MissingFileError(f"read source artifact: {exc}") from exc
        if len(data) > MAX_INPUT_BYTES:
            raise GraderFeedbackError(f"source artifact exceeds {MAX_INPUT_BYTES} bytes")
        derived = hashlib.sha256(data).hexdigest()
        if provided.hash and provided.hash != derived:
            raise ContradictoryReportError("source artifact hash mismatch")
        provided = SourceArtifact(_sanitize(str(artifact_path)), derived)
    if provided.artifact or provided.hash:
        if part.source_seen and part.artifact != provided:
            raise ContradictoryReportError("input source identity disagrees")
        return provided
    return part.artifact if part.source_seen else SourceArtifact()


def _finalize(part: _Parsed, source: SourceArtifact, identity: FeedbackIdentity) -> GraderFeedback:
    _finalize_execution_metadata(part)
    _apply_official_outcome(part)
    if part.kind == FailureKind.TEST_HARNESS_FAILURE.value:
        part.phase = "harness"
    ids = _unique_ids(part.ids)
    evidence = _unique_bounded(part.evidence, MAX_EVIDENCE, MAX_SUMMARY_CHARS)
    failures = tuple(_unique_failures(part.failures))
    metadata = _feedback_metadata(part)
    if not part.kind:
        if part.status == Status.PASSED.value or part.passed:
            return GraderFeedback(
                status=Status.PASSED.value,
                failure_kind="",
                passed=True,
                source_artifact=source.artifact,
                source_hash=source.hash,
                # Passing SWE-bench truth is authoritative.  File:line text in
                # successful test output is context, not failure evidence, and
                # must never create a blank test_id in the signed result.
                failures=(),
                evidence=evidence,
                question_id=identity.question_id,
                arm_id=identity.arm_id,
                session_id=identity.session_id,
                thread_id=identity.thread_id,
                patch_hash=identity.patch_hash,
                **metadata,
            )
        return GraderFeedback(
            source_artifact=source.artifact,
            source_hash=source.hash,
            failures=failures,
            evidence=evidence,
            question_id=identity.question_id,
            arm_id=identity.arm_id,
            session_id=identity.session_id,
            thread_id=identity.thread_id,
            patch_hash=identity.patch_hash,
            **metadata,
        )
    if part.kind == FailureKind.FEEDBACK_UNAVAILABLE.value:
        return GraderFeedback(
            source_artifact=source.artifact,
            source_hash=source.hash,
            failing_test_ids=ids,
            failures=failures,
            evidence=evidence,
            question_id=identity.question_id,
            arm_id=identity.arm_id,
            session_id=identity.session_id,
            thread_id=identity.thread_id,
            patch_hash=identity.patch_hash,
            **metadata,
        )
    if part.status in {Status.PASSED.value, Status.UNRESOLVED.value}:
        raise ContradictoryReportError(f"failure kind {part.kind} has status {part.status}")
    if part.kind == FailureKind.MODEL_TEST_FAILURE.value and not _has_failure_evidence(
        part, ids, failures
    ):
        # A failing test name without an exception/assertion is not actionable
        # model feedback.  Keep the identity and IDs, but fail closed.
        return GraderFeedback(
            status=Status.UNRESOLVED.value,
            failure_kind=FailureKind.FEEDBACK_UNAVAILABLE.value,
            failing_test_ids=ids,
            source_artifact=source.artifact,
            source_hash=source.hash,
            failures=failures,
            evidence=evidence,
            question_id=identity.question_id,
            arm_id=identity.arm_id,
            session_id=identity.session_id,
            thread_id=identity.thread_id,
            patch_hash=identity.patch_hash,
            **metadata,
        )
    return GraderFeedback(
        status=Status.FAILED.value,
        failure_kind=part.kind,
        failing_test_ids=ids,
        exception_summary=_summary_text(part.exception),
        traceback_summary=_summary_text(part.traceback),
        assertion_summary=_summary_text(part.assertion),
        source_artifact=source.artifact,
        source_hash=source.hash,
        failures=failures,
        question_id=identity.question_id,
        arm_id=identity.arm_id,
        session_id=identity.session_id,
        thread_id=identity.thread_id,
        patch_hash=identity.patch_hash,
        **metadata,
        structured=True,
        code_repair_eligible=part.kind == FailureKind.MODEL_TEST_FAILURE.value,
        evidence=evidence,
    )


def _feedback_metadata(part: _Parsed) -> dict[str, Any]:
    return {
        "phase": part.phase,
        "collected_count": part.collected_count,
        "executed_count": part.executed_count,
        "missing_symbols": tuple(sorted(set(part.missing_symbols))),
        "official_resolved": part.official_resolved,
        "official_completed": part.official_completed,
        "official_instance_id": part.official_instance_id,
    }


def _has_failure_evidence(
    part: _Parsed, ids: tuple[str, ...], failures: tuple[FailureEvidence, ...]
) -> bool:
    if part.official_model_failure:
        return True
    if not ids:
        return False
    if any(
        item.exception or item.message or item.actual or item.expected or item.traceback_tail
        or item.file or item.line is not None
        for item in failures
    ):
        return True
    return bool(part.exception or part.traceback or part.assertion)


def _validate_part(part: _Parsed) -> None:
    if part.status == Status.PASSED.value and part.kind:
        raise ContradictoryReportError("passed report contains failure kind")
    if part.status == Status.UNRESOLVED.value and part.kind and part.kind != FailureKind.FEEDBACK_UNAVAILABLE.value:
        raise ContradictoryReportError("unresolved report contains failure kind")
    if part.status == Status.FAILED.value and not part.kind:
        # A failed status without a typed failure is not actionable evidence.
        # Preserve the report as unresolved instead of inventing a model bug.
        part.status = Status.UNRESOLVED.value
        part.kind = FailureKind.FEEDBACK_UNAVAILABLE.value
        part.structured = False


def _check_passed_failed_ids(part: _Parsed) -> None:
    passed = {_canonical_id(item) for item in part.passed_ids}
    overlap = sorted(passed & {_canonical_id(item) for item in part.ids})
    if overlap:
        raise ContradictoryReportError(f"test is both PASSED and failed: {overlap[0]}")


def _normalise_status(value: Any) -> str:
    if not isinstance(value, str):
        raise GraderFeedbackError(f"unsupported grader status {value!r}")
    value = value.strip().lower()
    if value in {"pass", "passed", "success", "succeeded", "green"}:
        return Status.PASSED.value
    if value in {"fail", "failed", "error", "red"}:
        return Status.FAILED.value
    if value in {"unresolved", "unknown", "indeterminate", "pending", "not_verified"}:
        return Status.UNRESOLVED.value
    raise GraderFeedbackError(f"unsupported grader status {value!r}")


def _validate_schema(value: str) -> None:
    if value not in _KNOWN_SCHEMAS:
        raise UnknownSchemaError(f"unknown grader schema {value!r}")


def _normalise_kind(value: Any) -> str:
    if not isinstance(value, str):
        raise GraderFeedbackError(f"unsupported grader failure kind {value!r}")
    value = value.strip().lower().replace("-", "_").replace(" ", "_")
    aliases = {
        "agent_transport": FailureKind.AGENT_TRANSPORT_FAILURE.value,
        "agent_transport_failure": FailureKind.AGENT_TRANSPORT_FAILURE.value,
        "transport_failure": FailureKind.AGENT_TRANSPORT_FAILURE.value,
        "test_collection_import": FailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
        "test_collection_import_error": FailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
        "collection_import_error": FailureKind.TEST_COLLECTION_IMPORT_ERROR.value,
        "test_harness": FailureKind.TEST_HARNESS_FAILURE.value,
        "test_harness_failure": FailureKind.TEST_HARNESS_FAILURE.value,
        "harness_failure": FailureKind.TEST_HARNESS_FAILURE.value,
        "infrastructure": FailureKind.INFRASTRUCTURE_ERROR.value,
        "infra": FailureKind.INFRASTRUCTURE_ERROR.value,
        "infrastructure_error": FailureKind.INFRASTRUCTURE_ERROR.value,
        "infra_error": FailureKind.INFRASTRUCTURE_ERROR.value,
        "timeout": FailureKind.TIMEOUT.value,
        "timed_out": FailureKind.TIMEOUT.value,
        "deadline_exceeded": FailureKind.TIMEOUT.value,
        "patch_apply": FailureKind.PATCH_APPLY_FAILURE.value,
        "patch_apply_failure": FailureKind.PATCH_APPLY_FAILURE.value,
        "patch_failure": FailureKind.PATCH_APPLY_FAILURE.value,
        "apply_patch_failure": FailureKind.PATCH_APPLY_FAILURE.value,
        "model_test": FailureKind.MODEL_TEST_FAILURE.value,
        "model_test_failure": FailureKind.MODEL_TEST_FAILURE.value,
        "test_failure": FailureKind.MODEL_TEST_FAILURE.value,
        "test_error": FailureKind.MODEL_TEST_FAILURE.value,
        "feedback_unavailable": FailureKind.FEEDBACK_UNAVAILABLE.value,
        "unavailable": FailureKind.FEEDBACK_UNAVAILABLE.value,
    }
    try:
        return aliases[value]
    except KeyError as exc:
        raise GraderFeedbackError(f"unsupported grader failure kind {value!r}") from exc


def _normalise_hash(value: str) -> str:
    value = value.strip()
    if value.lower().startswith("sha256:"):
        value = value[7:]
    if not re.fullmatch(r"[0-9a-fA-F]{64}", value):
        raise GraderFeedbackError("source hash must be a SHA-256 digest")
    return value.lower()


def _first_string(value: Mapping[str, Any], keys: Iterable[str]) -> str | None:
    found: str | None = None
    for key in keys:
        if key not in value:
            continue
        candidate = _required_string(value[key], key)
        if found is not None and found != candidate:
            raise ContradictoryReportError(f"{key} fields disagree")
        found = candidate
    return found


def _required_string(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise GraderFeedbackError(f"{field} must be a non-empty string")
    return value.strip()


def _first_string_list(value: Mapping[str, Any], keys: Iterable[str]) -> list[str]:
    result: list[str] = []
    for key in keys:
        if key in value:
            result.extend(_string_list(value[key], key))
    return result


def _string_list(value: Any, field: str) -> list[str]:
    if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
        raise GraderFeedbackError(f"{field} must be a string array")
    return [item.strip() for item in value if item.strip()]


def _string_or_lines(value: Any, field: str) -> str:
    if isinstance(value, str):
        return value
    if isinstance(value, list) and all(isinstance(item, str) for item in value):
        return "\n".join(value)
    raise GraderFeedbackError(f"{field} must be a string or string array")


def _split_ids(value: str) -> list[str]:
    return [_sanitize(item.strip()) for item in re.split(r"[,\n\t]", value) if item.strip()]


def _unique_ids(values: Iterable[str]) -> tuple[str, ...]:
    result: dict[str, str] = {}
    for value in values:
        value = _sanitize(value.strip())
        if value and set(value) <= {"_"}:
            continue
        if value:
            result.setdefault(_canonical_id(value), value)
        if len(result) > MAX_TEST_IDS:
            raise GraderFeedbackError(f"grader feedback contains more than {MAX_TEST_IDS} failing test IDs")
    return tuple(sorted(result.values()))


def _unique_bounded(values: Iterable[str], limit: int, item_limit: int) -> tuple[str, ...]:
    result: list[str] = []
    seen: set[str] = set()
    for value in values:
        value = _summary_text(value)
        if value and value not in seen:
            seen.add(value)
            result.append(value[:item_limit])
        if len(result) >= limit:
            break
    return tuple(result)


def _unique_failures(values: Iterable[FailureEvidence]) -> list[FailureEvidence]:
    result: list[FailureEvidence] = []
    seen: set[tuple[Any, ...]] = set()
    for item in values:
        clean = FailureEvidence(
            test_id=_sanitize(item.test_id),
            file=_sanitize(item.file),
            line=item.line,
            exception=_summary_text(item.exception),
            message=_summary_text(item.message),
            actual=_summary_text(item.actual),
            expected=_summary_text(item.expected),
            traceback_tail=_tail_text(item.traceback_tail),
        )
        if not clean.test_id:
            # File/line context without an authoritative test identifier is
            # retained in top-level summaries and raw test output, but cannot
            # be emitted as structured per-test failure evidence.
            continue
        if set(clean.test_id) <= {"_"}:
            continue
        key = dataclasses.astuple(clean)
        if key in seen:
            continue
        seen.add(key)
        result.append(clean)
        if len(result) >= MAX_FAILURES:
            break
    return result


def _canonical_id(value: str) -> str:
    return value.strip().lower()


def _merge_summary(left: str, right: str) -> str:
    left, right = _summary_text(left), _summary_text(right)
    if not left:
        return right
    if not right or right == left or right in left:
        return left
    if left in right:
        return right
    return _summary_text(f"{left}\n{right}")


def _apply_official_outcome(part: _Parsed) -> None:
    """Apply report truth after merging the independent output surface."""

    if not part.official_outcome_seen:
        return
    if part.official_infrastructure:
        part.status = Status.FAILED.value
        part.passed = False
        part.kind = FailureKind.INFRASTRUCTURE_ERROR.value
        part.categories = {FailureKind.INFRASTRUCTURE_ERROR.value}
        part.structured = True
        part.official_model_failure = False
        return
    if part.official_resolved is True:
        # A resolved instance is success even if stdout/test-output contains
        # stale failing lines from a nested or baseline test invocation.
        part.status = Status.PASSED.value
        part.passed = True
        part.kind = ""
        part.categories.clear()
        part.structured = True
        return
    if part.official_resolved is False and part.official_completed is True:
        # Completion is the boundary that lets ``resolved=false`` become a
        # model result.  Without completion the parser remains unverified.
        part.status = Status.FAILED.value
        part.passed = False
        part.kind = FailureKind.MODEL_TEST_FAILURE.value
        part.categories = {FailureKind.MODEL_TEST_FAILURE.value}
        part.structured = True
        part.official_model_failure = True
        return
    part.status = Status.UNRESOLVED.value
    part.passed = False
    if not part.explicit_kind:
        part.kind = ""
        part.categories.clear()


def _summary_text(value: str) -> str:
    value = _sanitize(value)
    return value[:MAX_SUMMARY_CHARS]


def _tail_text(value: str) -> str:
    """Keep the end of a traceback, where the exception and assertion live."""

    value = _sanitize(value)
    if len(value) > MAX_SUMMARY_CHARS:
        value = value[-MAX_SUMMARY_CHARS:]
    return value


def _line_number(value: Any, field: str) -> int:
    try:
        line = int(value)
    except (TypeError, ValueError) as exc:
        raise GraderFeedbackError(f"{field} must be a positive integer") from exc
    if line < 1:
        raise GraderFeedbackError(f"{field} must be a positive integer")
    return line


def _parse_actual_expected(value: str) -> tuple[str, str]:
    match = _ASSERTION_RE.search(_sanitize(value))
    if match is None:
        return "", ""
    return _summary_text(match.group(1)), _summary_text(match.group(2))


def _sanitize(value: str) -> str:
    value = _ANSI_RE.sub("", value)
    value = "".join(character for character in value if character in "\n\t" or ord(character) >= 0x20)
    value = _SECRET_RE.sub(r"\1=[REDACTED]", value)
    return _USER_PATH_RE.sub("[PATH]", value)
