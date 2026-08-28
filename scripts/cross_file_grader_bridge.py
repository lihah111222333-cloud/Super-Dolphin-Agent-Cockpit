#!/usr/bin/env python3
"""Bind real SWE-bench output to the signed Ubuntu-driver feedback envelope.

The SWE-bench process has four independent output surfaces: its report, test
output, stdout, and stderr.  This module treats all four as evidence and
hashes each one, while only the official report/test-output pair is promoted
to model-quality feedback.  Runner chatter in stdout/stderr can classify an
unresolved run as infrastructure/transport failure, but can never invent a
model test failure.

The file written to ``feedback_output`` is deliberately the exact object
consumed by ``UbuntuExecutionDriver``: ``{"receipt": <signed receipt>}``.
The receipt result carries the four source hashes, so those hashes remain
covered by the Ed25519 signature without expanding the driver's envelope
contract.
"""

from __future__ import annotations

import argparse
import dataclasses
import hashlib
import json
import pathlib
import re
import sys
from collections.abc import Mapping, Sequence
from typing import Any

try:
    from .cross_file_signed_receipts import (
        Ed25519Signer,
        sign_grader_receipt,
    )
    from .grader_feedback import (
        FailureKind,
        GraderFeedback,
        GraderFeedbackError,
        FeedbackIdentity,
        canonical_source_hash,
        parse_grader_feedback,
    )
except ImportError:  # pragma: no cover - direct script invocation support.
    from cross_file_signed_receipts import (  # type: ignore[no-redef]
        Ed25519Signer,
        sign_grader_receipt,
    )
    from grader_feedback import (  # type: ignore[no-redef]
        FailureKind,
        FeedbackIdentity,
        GraderFeedback,
        GraderFeedbackError,
        canonical_source_hash,
        parse_grader_feedback,
    )


SCHEMA = "super-dolphin/grader-bridge/v1"
MAX_SOURCE_BYTES = 2 * 1024 * 1024
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
_TREE_RE = re.compile(r"^[0-9a-f]{40}(?:[0-9a-f]{24})?$")
_AUXILIARY_KINDS = frozenset(
    {
        FailureKind.AGENT_TRANSPORT_FAILURE.value,
        FailureKind.INFRASTRUCTURE_ERROR.value,
        FailureKind.TIMEOUT.value,
        FailureKind.PATCH_APPLY_FAILURE.value,
    }
)


class GraderBridgeError(RuntimeError):
    """The real grader evidence cannot be bound to one signed result."""


class GraderBridgeInputError(GraderBridgeError):
    """A required identity, source, or signing input is malformed."""


class GraderBridgeProvenanceError(GraderBridgeError):
    """The official source pair contradicts the structured feedback."""


@dataclasses.dataclass(frozen=True)
class GraderSource:
    """One bounded raw output surface and its byte hash."""

    name: str
    data: bytes
    present: bool = True

    @property
    def sha256(self) -> str:
        return hashlib.sha256(self.data).hexdigest()

    def to_dict(self) -> dict[str, Any]:
        return {"sha256": self.sha256, "bytes": len(self.data), "present": self.present}


@dataclasses.dataclass(frozen=True)
class GraderArtifact:
    """Hash manifest for every source consumed by the bridge."""

    id: str
    sources: tuple[GraderSource, ...]

    @property
    def sha256(self) -> str:
        material = {
            "schema": "super-dolphin/grader-artifact/v1",
            "sources": {
                source.name: {
                    "sha256": source.sha256,
                    "bytes": len(source.data),
                    "present": source.present,
                }
                for source in self.sources
            },
        }
        encoded = json.dumps(
            material, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False
        ).encode("utf-8")
        return hashlib.sha256(encoded).hexdigest()

    @property
    def binding(self) -> dict[str, str]:
        """Return the compact artifact binding accepted by signed receipts."""

        return {"id": self.id, "sha256": self.sha256}

    def hashes(self) -> dict[str, str]:
        return {source.name: source.sha256 for source in self.sources}

    def to_dict(self) -> dict[str, Any]:
        return {
            **self.binding,
            "sources": {source.name: source.to_dict() for source in self.sources},
        }


def validate_grader_artifact_hashes(
    result: Mapping[str, Any],
    artifact: GraderArtifact,
) -> None:
    """Require signed feedback to carry hashes for the executor's sources."""

    if not isinstance(result, Mapping):
        raise GraderBridgeError("grader receipt result must be an object")
    declared = result.get("grader_artifact_hashes")
    if not isinstance(declared, Mapping):
        raise GraderBridgeError("grader feedback is missing grader_artifact_hashes")
    normalized = {str(key): value for key, value in declared.items()}
    if normalized != artifact.hashes():
        raise GraderBridgeError("grader feedback source hashes drifted from executor artifacts")


def validate_official_grader_sources(
    report: str | bytes | None,
    test_output: str | bytes | None,
    *,
    stdout: str | bytes | None = None,
    stderr: str | bytes | None = None,
) -> GraderFeedback:
    """Parse official sources for an independent runner-side comparison."""

    try:
        # A test stream without the official report is not enough to establish
        # SWE-bench truth.  Preserve it as unverified instead of promoting a
        # plausible-looking assertion into a model result.
        report_missing = report in (None, "", b"")
        if report_missing:
            return GraderFeedback(
                status="UNRESOLVED",
                failure_kind=FailureKind.FEEDBACK_UNAVAILABLE.value,
                source_artifact="",
                source_hash=(
                    canonical_source_hash(None, test_output)
                    if test_output not in (None, "", b"")
                    else ""
                ),
            )
        report_feedback = parse_grader_feedback(report)
        if report_feedback.passed:
            report_text = report.decode("utf-8") if isinstance(report, bytes) else report
            output_text = test_output.decode("utf-8") if isinstance(test_output, bytes) else test_output
            feedback = dataclasses.replace(
                report_feedback,
                source_artifact="official-grader-report+official-test-output",
                source_hash=canonical_source_hash(report_text, output_text),
            )
        else:
            feedback = parse_grader_feedback(report, test_output, strict_provenance=True)
        if feedback.failure_kind == FailureKind.TEST_HARNESS_FAILURE.value:
            feedback = dataclasses.replace(feedback, phase="harness")
        if stdout is not None or stderr is not None:
            feedback = _auxiliary_feedback(
                feedback,
                _read_source(stdout, "stdout"),
                _read_source(stderr, "stderr"),
            )
        return feedback
    except GraderFeedbackError as exc:
        raise GraderBridgeProvenanceError(
            f"official grader source validation failed: {exc}"
        ) from exc


def _read_source(value: str | bytes | pathlib.Path | None, name: str) -> GraderSource:
    if value is None:
        return GraderSource(name=name, data=b"", present=False)
    if isinstance(value, pathlib.Path):
        try:
            data = value.read_bytes()
        except OSError as exc:
            raise GraderBridgeInputError(f"read {name} failed: {value}") from exc
    elif isinstance(value, bytes):
        data = value
    elif isinstance(value, str):
        data = value.encode("utf-8")
    else:
        raise GraderBridgeInputError(f"{name} must be bytes, text, path, or None")
    if len(data) > MAX_SOURCE_BYTES:
        raise GraderBridgeInputError(f"{name} exceeds {MAX_SOURCE_BYTES} bytes")
    return GraderSource(name=name, data=data, present=True)


def _decode(source: GraderSource, *, required: bool) -> str | None:
    if not source.present:
        if required:
            raise GraderBridgeInputError(f"{source.name} is required")
        return None
    if required and not source.data:
        raise GraderBridgeInputError(f"{source.name} is empty")
    try:
        return source.data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise GraderBridgeInputError(f"{source.name} is not UTF-8") from exc


def _required_text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise GraderBridgeInputError(f"{label} must be a non-empty string")
    return value.strip()


def _digest(value: Any, label: str) -> str:
    text = _required_text(value, label).lower()
    if text.startswith("sha256:"):
        text = text[7:]
    if _SHA256_RE.fullmatch(text) is None:
        raise GraderBridgeInputError(f"{label} must be a lowercase SHA-256 digest")
    return text


def _tree(value: Any) -> str:
    text = _required_text(value, "final_tree_hash").lower()
    if _TREE_RE.fullmatch(text) is None:
        raise GraderBridgeInputError("final_tree_hash must be a lowercase Git tree hash")
    return text


def _positive_int(value: Any, label: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise GraderBridgeInputError(f"{label} must be a positive integer")
    return value


def _artifact(task: str, request_id: str, sources: Sequence[GraderSource]) -> GraderArtifact:
    # The id is descriptive only; its digest is the immutable signed binding.
    return GraderArtifact(f"swebench/{task}/{request_id}", tuple(sources))


def _auxiliary_feedback(
    base: GraderFeedback,
    stdout: GraderSource,
    stderr: GraderSource,
) -> GraderFeedback:
    """Use stderr/stdout only to classify unresolved runner failures."""

    if base.failure_kind != FailureKind.FEEDBACK_UNAVAILABLE.value:
        return base
    for source in (stderr, stdout):
        if not source.present or not source.data:
            continue
        try:
            candidate = parse_grader_feedback(source.data)
        except GraderFeedbackError:
            continue
        if candidate.failure_kind not in _AUXILIARY_KINDS:
            continue
        return dataclasses.replace(
            candidate,
            source_artifact=base.source_artifact,
            source_hash=base.source_hash,
            question_id=base.question_id,
            arm_id=base.arm_id,
            session_id=base.session_id,
            thread_id=base.thread_id,
            patch_hash=base.patch_hash,
        )
    return base


def build_grader_artifact(
    *,
    task: str,
    request_id: str,
    report: str | bytes | pathlib.Path | None,
    test_output: str | bytes | pathlib.Path | None,
    stdout: str | bytes | pathlib.Path | None,
    stderr: str | bytes | pathlib.Path | None,
) -> GraderArtifact:
    """Read and hash all four real grader output surfaces."""

    task = _required_text(task, "task")
    request_id = _required_text(request_id, "request_id")
    sources = tuple(
        _read_source(value, name)
        for name, value in (
            ("report", report),
            ("test_output", test_output),
            ("stdout", stdout),
            ("stderr", stderr),
        )
    )
    return _artifact(task, request_id, sources)


def build_signed_feedback(
    *,
    task: str,
    arm: str,
    session: str,
    thread: str,
    patch_hash: str,
    final_tree_hash: str,
    request_id: str,
    seq: int,
    signer: Ed25519Signer,
    report: str | bytes | pathlib.Path | None,
    test_output: str | bytes | pathlib.Path | None,
    stdout: str | bytes | pathlib.Path | None = None,
    stderr: str | bytes | pathlib.Path | None = None,
) -> dict[str, Any]:
    """Produce the exact ``{"receipt": ...}`` object read by Ubuntu driver."""

    task = _required_text(task, "task")
    arm = _required_text(arm, "arm")
    session = _required_text(session, "session")
    thread = _required_text(thread, "thread")
    request_id = _required_text(request_id, "request_id")
    patch_hash = _digest(patch_hash, "patch_hash")
    final_tree_hash = _tree(final_tree_hash)
    seq = _positive_int(seq, "seq")
    if not isinstance(signer, Ed25519Signer):
        raise GraderBridgeInputError("signer must be Ed25519Signer")

    report_source = _read_source(report, "report")
    test_source = _read_source(test_output, "test_output")
    stdout_source = _read_source(stdout, "stdout")
    stderr_source = _read_source(stderr, "stderr")
    report_text = _decode(report_source, required=False)
    test_text = _decode(test_source, required=False)
    if report_text is None:
        # No official report means no authoritative model outcome.  The raw
        # process surfaces remain hashed, while the signed result is explicit
        # unverified feedback so the runner can stop the arm safely.
        official_hash = (
            canonical_source_hash(None, test_text)
            if test_text not in (None, "")
            else ""
        )
        identity = FeedbackIdentity(
            question_id=task,
            arm_id=arm,
            session_id=session,
            thread_id=thread,
            patch_hash=patch_hash,
        )
        feedback = GraderFeedback(
            status="UNRESOLVED",
            failure_kind=FailureKind.FEEDBACK_UNAVAILABLE.value,
            source_artifact="",
            source_hash=official_hash,
            question_id=identity.question_id,
            arm_id=identity.arm_id,
            session_id=identity.session_id,
            thread_id=identity.thread_id,
            patch_hash=identity.patch_hash,
            evidence=("official grader report missing",),
        )
        artifact = _artifact(
            task,
            request_id,
            (report_source, test_source, stdout_source, stderr_source),
        )
        result = feedback.to_dict()
        result["grader_artifact_hashes"] = artifact.hashes()
        result["grader_artifact"] = artifact.to_dict()
        result["official_source_hash"] = official_hash
        receipt = sign_grader_receipt(
            result,
            task=task,
            arm=arm,
            session=session,
            thread=thread,
            seq=seq,
            request_id=request_id,
            patch_hash=patch_hash,
            grader_artifact=artifact.binding,
            final_tree_hash=final_tree_hash,
            signer=signer,
        )
        return {"receipt": receipt}
    if not test_text:
        try:
            report_truth = parse_grader_feedback(report_text)
        except GraderFeedbackError as exc:
            raise GraderBridgeProvenanceError(
                f"official grader report validation failed: {exc}"
            ) from exc
        if (
            report_truth.official_resolved is None
            and report_truth.failure_kind != FailureKind.INFRASTRUCTURE_ERROR.value
        ):
            raise GraderBridgeInputError(
                "test_output is required when an official report has no resolved truth"
            )
    identity = FeedbackIdentity(
        question_id=task,
        arm_id=arm,
        session_id=session,
        thread_id=thread,
        patch_hash=patch_hash,
    )
    official_hash = canonical_source_hash(report_text, test_text)
    try:
        report_feedback = parse_grader_feedback(report_text)
        if report_feedback.passed:
            # SWE-bench resolved=true is success truth.  Successful output can
            # contain baseline warnings and source locations; retain its hash
            # without allowing it to reverse the official outcome.
            feedback = dataclasses.replace(
                report_feedback,
                source_artifact="official-grader-report+official-test-output",
                source_hash=official_hash,
            )
        else:
            feedback = parse_grader_feedback(
                report_text,
                test_text,
                source_artifact="official-grader-report+official-test-output",
                source_hash=official_hash,
                strict_provenance=True,
            )
        if feedback.failure_kind == FailureKind.TEST_HARNESS_FAILURE.value:
            feedback = dataclasses.replace(feedback, phase="harness")
        # SWE-bench's aggregate report normally identifies the instance, but
        # does not know the runner's session/thread/patch.  Those values come
        # from the trusted driver request and are bound here after parsing;
        # any identity fields that the official parser did see have already
        # been checked for internal consistency above.
        if feedback.question_id and feedback.question_id != task:
            raise GraderBridgeProvenanceError("official report task identity drifted")
        if feedback.official_instance_id and feedback.official_instance_id != task:
            raise GraderBridgeProvenanceError("official report instance identity drifted")
        feedback = dataclasses.replace(
            feedback,
            question_id=identity.question_id,
            arm_id=identity.arm_id,
            session_id=identity.session_id,
            thread_id=identity.thread_id,
            patch_hash=identity.patch_hash,
        )
        feedback = _auxiliary_feedback(feedback, stdout_source, stderr_source)
    except GraderFeedbackError as exc:
        raise GraderBridgeProvenanceError(f"official grader evidence rejected: {exc}") from exc

    artifact = _artifact(
        task,
        request_id,
        (report_source, test_source, stdout_source, stderr_source),
    )
    result = feedback.to_dict()
    # The parser may retain an empty placeholder for a failing ID when the
    # official report supplies its details in a separate field.  Null line
    # values are not valid structured evidence for the runner; discard only
    # those wholly empty placeholders while retaining real failures.
    failures = result.get("failures")
    if isinstance(failures, list):
        result["failures"] = [
            failure
            for failure in failures
            if isinstance(failure, Mapping)
            and any(
                failure.get(field) not in (None, "")
                for field in (
                    "file", "line", "exception", "message",
                    "actual", "expected", "traceback_tail",
                )
            )
        ]
    result["grader_artifact_hashes"] = artifact.hashes()
    result["grader_artifact"] = artifact.to_dict()
    result["official_source_hash"] = official_hash
    receipt = sign_grader_receipt(
        result,
        task=task,
        arm=arm,
        session=session,
        thread=thread,
        seq=seq,
        request_id=request_id,
        patch_hash=patch_hash,
        grader_artifact=artifact.binding,
        final_tree_hash=final_tree_hash,
        signer=signer,
    )
    return {"receipt": receipt}


def build_driver_feedback_envelope(
    *,
    task: str,
    arm: str,
    session: str,
    thread: str,
    patch_hash: str,
    final_tree_hash: str,
    request_id: str,
    seq: int,
    signer: Ed25519Signer,
    report: str | bytes | pathlib.Path | None,
    test_output: str | bytes | pathlib.Path | None,
    stdout: str | bytes | pathlib.Path | None = None,
    stderr: str | bytes | pathlib.Path | None = None,
) -> dict[str, Any]:
    """Return a full driver result while retaining official source bytes."""

    feedback = build_signed_feedback(
        task=task,
        arm=arm,
        session=session,
        thread=thread,
        patch_hash=patch_hash,
        final_tree_hash=final_tree_hash,
        request_id=request_id,
        seq=seq,
        signer=signer,
        report=report,
        test_output=test_output,
        stdout=stdout,
        stderr=stderr,
    )
    report_bytes = _read_source(report, "report").data
    test_bytes = _read_source(test_output, "test_output").data
    try:
        report_text = report_bytes.decode("utf-8")
        test_text = test_bytes.decode("utf-8")
    except UnicodeDecodeError as exc:
        # ``build_signed_feedback`` already reads these surfaces, but keep
        # this public envelope JSON-compatible and fail closed if a caller
        # changes the source between the two reads.
        raise GraderBridgeInputError("official output is not UTF-8") from exc
    signed_hashes = feedback["receipt"]["result"].get("grader_artifact_hashes")
    if not isinstance(signed_hashes, Mapping):
        raise GraderBridgeError("signed feedback is missing artifact hashes")
    for name, data in (("report", report_bytes), ("test_output", test_bytes)):
        if signed_hashes.get(name) != hashlib.sha256(data).hexdigest():
            raise GraderBridgeError(f"{name} changed while building driver envelope")
    return {
        "feedback": feedback,
        "official_report": report_text,
        "official_test_output": test_text,
    }


# Keep the likely integration spellings intentionally boring and explicit.
build_feedback_envelope = build_driver_feedback_envelope
build_grader_feedback = build_signed_feedback
build_feedback = build_signed_feedback


def build_signed_feedback_from_paths(
    *,
    report_path: str | pathlib.Path,
    test_output_path: str | pathlib.Path,
    stdout_path: str | pathlib.Path | None = None,
    stderr_path: str | pathlib.Path | None = None,
    **kwargs: Any,
) -> dict[str, Any]:
    """Path-explicit adapter used by container entrypoints and tests."""

    return build_signed_feedback(
        report=pathlib.Path(report_path),
        test_output=pathlib.Path(test_output_path),
        stdout=pathlib.Path(stdout_path) if stdout_path is not None else None,
        stderr=pathlib.Path(stderr_path) if stderr_path is not None else None,
        **kwargs,
    )


def build_driver_feedback_envelope_from_paths(
    *,
    report_path: str | pathlib.Path,
    test_output_path: str | pathlib.Path,
    stdout_path: str | pathlib.Path | None = None,
    stderr_path: str | pathlib.Path | None = None,
    **kwargs: Any,
) -> dict[str, Any]:
    """Path-explicit adapter returning the full runner/driver envelope."""

    return build_driver_feedback_envelope(
        report=pathlib.Path(report_path),
        test_output=pathlib.Path(test_output_path),
        stdout=pathlib.Path(stdout_path) if stdout_path is not None else None,
        stderr=pathlib.Path(stderr_path) if stderr_path is not None else None,
        **kwargs,
    )


def write_feedback_file(path: str | pathlib.Path, feedback: Mapping[str, Any]) -> None:
    """Atomically write only the signed feedback object for Ubuntu driver."""

    if not isinstance(feedback, Mapping) or set(feedback) != {"receipt"}:
        raise GraderBridgeError("feedback file must contain exactly one receipt field")
    destination = pathlib.Path(path)
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_name(f".{destination.name}.tmp")
    try:
        temporary.write_text(
            json.dumps(feedback, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n",
            encoding="utf-8",
        )
        temporary.replace(destination)
    except OSError as exc:
        raise GraderBridgeError(f"write feedback file failed: {destination}") from exc


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    required_options = (
        "task", "arm", "session", "thread", "patch-hash", "final-tree-hash",
        "request-id", "private-key", "key-id", "report", "test-output",
        "feedback-output",
    )
    for name in required_options:
        parser.add_argument(f"--{name}", required=True)
    parser.add_argument("--stdout")
    parser.add_argument("--stderr")
    parser.add_argument("--seq", required=True, type=int)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        signer = Ed25519Signer.from_file(args.private_key, key_id=args.key_id)
        feedback = build_signed_feedback(
            task=args.task,
            arm=args.arm,
            session=args.session,
            thread=args.thread,
            patch_hash=args.patch_hash,
            final_tree_hash=args.final_tree_hash,
            request_id=args.request_id,
            seq=args.seq,
            signer=signer,
            report=pathlib.Path(args.report),
            test_output=pathlib.Path(args.test_output),
            stdout=pathlib.Path(args.stdout) if args.stdout else None,
            stderr=pathlib.Path(args.stderr) if args.stderr else None,
        )
        write_feedback_file(args.feedback_output, feedback)
        return 0
    except Exception as exc:  # noqa: BLE001 - CLI must fail closed.
        print(f"grader bridge failed: {type(exc).__name__}: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())


__all__ = [
    "GraderArtifact",
    "GraderBridgeError",
    "GraderBridgeInputError",
    "GraderBridgeProvenanceError",
    "GraderSource",
    "SCHEMA",
    "build_driver_feedback_envelope",
    "build_driver_feedback_envelope_from_paths",
    "build_feedback",
    "build_feedback_envelope",
    "build_grader_artifact",
    "build_grader_feedback",
    "build_signed_feedback",
    "build_signed_feedback_from_paths",
    "main",
    "validate_grader_artifact_hashes",
    "validate_official_grader_sources",
    "write_feedback_file",
]
