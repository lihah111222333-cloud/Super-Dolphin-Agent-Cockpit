#!/usr/bin/env python3
"""Fail-closed MCP server receipts and No-LSP isolation evidence.

The A/B auditor must consume receipts emitted by the MCP server, not claims
reconstructed by an agent or a runner summary.  A successful receipt binds the
task, arm, session, thread, sequence, request, exact argument/result hashes,
status, and server generation.  This module deliberately has no MCP client;
the runner supplies the immutable receipt stream and the executor supplies the
No-LSP sandbox attestation.
"""

from __future__ import annotations

import dataclasses
import hashlib
import json
import pathlib
import re
from collections.abc import Iterable, Mapping, Sequence
from typing import Any

try:
    from .cross_file_identity_envelope import parse_identity_envelope
except ImportError:  # pragma: no cover - direct module execution fallback.
    from cross_file_identity_envelope import parse_identity_envelope  # type: ignore[no-redef]

try:
    from .cross_file_signed_receipts import (
        GRADER_RECEIPT_KIND,
        MCP_RECEIPT_KIND,
        NO_LSP_ISOLATION_KIND,
        Ed25519Verifier,
        ReceiptReplayError,
        ReceiptReplayGuard,
        SignedReceiptError,
        attach_signature,
        canonical_artifact,
        validate_receipt_context,
        verifier_from,
        verify_signed_record,
    )
except ImportError:  # pragma: no cover - direct module execution fallback.
    from cross_file_signed_receipts import (  # type: ignore[no-redef]
        GRADER_RECEIPT_KIND,
        MCP_RECEIPT_KIND,
        NO_LSP_ISOLATION_KIND,
        Ed25519Verifier,
        ReceiptReplayError,
        ReceiptReplayGuard,
        SignedReceiptError,
        attach_signature,
        canonical_artifact,
        validate_receipt_context,
        verifier_from,
        verify_signed_record,
    )


SCHEMA = "super-dolphin/mcp-server-receipt/v2"
ISOLATION_SCHEMA = "super-dolphin/no-lsp-isolation/v2"
ARM_EVIDENCE_SCHEMA = "super-dolphin/cross-file-arm-evidence/v2"
SUCCESS_STATUS = "success"
LSP_TOOLS = frozenset({"structure", "xref", "diagnostics"})
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
_TREE_HASH_RE = re.compile(r"^[0-9a-f]{40}(?:[0-9a-f]{24})?$")
_POSITION_RE = re.compile(
    r"^(?P<path>[^:\s][^\n]*):(?P<line>[1-9][0-9]*):(?P<column>[1-9][0-9]*)$"
)
_LOCATION_IN_TEXT_RE = re.compile(
    r"(?<![A-Za-z0-9_])(?P<path>[^\s\"'(),]+):(?P<line>[1-9][0-9]*):(?P<column>[1-9][0-9]*)(?![0-9])"
)
_RESTRICTED_PROMPT_MARKER_RE = re.compile(
    r"(?<![A-Za-z0-9])(?:lsp|mcp|structure|xref|diagnostics)(?![A-Za-z0-9])",
    re.IGNORECASE,
)


class ReceiptError(ValueError):
    """Base class for malformed or untrusted server receipt evidence."""


class UnknownFieldError(ReceiptError):
    """A receipt or attestation contains an undeclared field."""


class ReceiptStatusError(ReceiptError):
    """A receipt is not a successful server result."""


class ReceiptIdentityError(ReceiptError):
    """A receipt does not belong to the requested A/B run."""


class ReceiptHashError(ReceiptError):
    """A supplied argument or result hash does not match its payload."""


class ReceiptSequenceError(ReceiptError):
    """A receipt stream is duplicated, reordered, or has a generation gap."""


class ReceiptChainError(ReceiptError):
    """The structure-to-xref or diagnostics chain is not proven."""


class DiffFilterError(ReceiptChainError):
    """A diagnostics diff_filter is not the canonical object shape."""


class IsolationError(ReceiptError):
    """No-LSP network/sidecar isolation evidence is incomplete or contaminated."""


class ReceiptSignatureError(ReceiptError):
    """A canonical evidence item lacks a trusted Ed25519 signature."""


def _required_text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ReceiptError(f"{label} must be a non-empty string")
    return value.strip()


def _positive_int(value: Any, label: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise ReceiptError(f"{label} must be a positive integer")
    return value


def _sha256(value: Any, label: str) -> str:
    if not isinstance(value, str) or not _SHA256_RE.fullmatch(value):
        raise ReceiptHashError(f"{label} must be a lowercase SHA-256 digest")
    return value


def _tree_hash(value: Any, label: str) -> str:
    text = _required_text(value, label)
    if _TREE_HASH_RE.fullmatch(text) is None:
        raise ReceiptChainError(f"{label} must be a lowercase Git tree hash")
    return text


def canonical_json(value: Any) -> str:
    """Encode JSON payloads deterministically for receipt hash verification."""

    try:
        return json.dumps(
            value,
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
            allow_nan=False,
        )
    except (TypeError, ValueError) as exc:
        raise ReceiptHashError(f"receipt payload is not canonical JSON: {exc}") from exc


def digest_json(value: Any) -> str:
    """Return the SHA-256 digest of :func:`canonical_json`."""

    return hashlib.sha256(canonical_json(value).encode("utf-8")).hexdigest()


def digest_text(value: str) -> str:
    """Return the SHA-256 digest of exact UTF-8 text bytes."""

    if not isinstance(value, str):
        raise ReceiptHashError("text payload must be a string")
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def scan_no_lsp_prompt(value: Any) -> str:
    """Require raw prompt text and reject every LSP/MCP evidence marker."""

    if not isinstance(value, str) or not value.strip():
        raise IsolationError("No-LSP raw_prompt must be non-empty text")
    markers = tuple(sorted({match.group(0).lower() for match in _RESTRICTED_PROMPT_MARKER_RE.finditer(value)}))
    if markers:
        raise IsolationError(
            "No-LSP raw_prompt contains restricted marker(s): " + ", ".join(markers)
        )
    return value


def _reject_unknown(value: Mapping[str, Any], allowed: set[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise UnknownFieldError(f"{label} has unknown field(s): {', '.join(unknown)}")


def _normalise_tool(value: Any) -> str:
    text = _required_text(value, "receipt.tool").lower().replace("/", ".")
    while ".." in text:
        text = text.replace("..", ".")
    tool = text.rsplit(".", 1)[-1].replace("-", "_")
    if tool not in LSP_TOOLS:
        raise ReceiptError(f"unsupported MCP/LSP tool: {value!r}")
    return tool


def _normalise_path(value: Any, label: str = "path") -> str:
    text = _required_text(value, label).replace("\\", "/")
    if text.startswith("file://"):
        text = text[7:]
    if text.startswith("/") or re.match(r"^[A-Za-z]:[/]", text):
        raise ReceiptChainError(f"{label} must be repository-relative")
    parts = [part for part in text.split("/") if part not in ("", ".")]
    if not parts or any(part == ".." for part in parts):
        raise ReceiptChainError(f"{label} escapes the repository")
    return pathlib.PurePosixPath(*parts).as_posix()


def _normalise_position(value: Any, label: str = "xref position") -> str:
    text = _required_text(value, label)
    match = _POSITION_RE.fullmatch(text)
    if match is None:
        raise ReceiptChainError(f"{label} must be path:line:column")
    path = _normalise_path(match.group("path"), f"{label} path")
    return f"{path}:{int(match.group('line'))}:{int(match.group('column'))}"


_RECEIPT_FIELDS = {
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
    "patch_hash",
    "final_tree_hash",
    "tool",
    "arguments",
    "result",
    "signature",
}


@dataclasses.dataclass(frozen=True)
class MCPServerReceipt:
    """One successful, hash-bound MCP server tool receipt."""

    task: str
    arm: str
    session: str
    thread: str
    seq: int
    request_id: str
    args_hash: str
    result_hash: str
    status: str
    server_generation: str
    patch_hash: str
    tool: str
    arguments: Mapping[str, Any]
    result: Any
    signature: Mapping[str, Any]
    final_tree_hash: str | None = None
    schema: str = SCHEMA
    source: str = "mcp-server"

    @classmethod
    def from_mapping(cls, value: Mapping[str, Any]) -> "MCPServerReceipt":
        if not isinstance(value, Mapping):
            raise ReceiptError("MCP server receipt must be an object")
        _reject_unknown(value, _RECEIPT_FIELDS, "MCP server receipt")
        schema = value.get("schema", SCHEMA)
        if schema != SCHEMA:
            raise ReceiptError(f"unsupported receipt schema: {schema!r}")
        source = _required_text(value.get("source"), "receipt.source")
        if source not in {"mcp-server", "mcp-lsp"}:
            raise ReceiptError(f"receipt.source is not a server issuer: {source!r}")
        status = _required_text(value.get("status"), "receipt.status")
        if status != SUCCESS_STATUS:
            raise ReceiptStatusError(
                f"MCP receipt status {status!r} is not successful; errors never count as coverage"
            )
        if "arguments" not in value or not isinstance(value["arguments"], Mapping):
            raise ReceiptError("successful receipt.arguments must be an object")
        if "result" not in value:
            raise ReceiptError("successful receipt.result is required")
        if "signature" not in value:
            raise ReceiptSignatureError("successful MCP receipt.signature is required")
        if not isinstance(value["signature"], Mapping):
            raise ReceiptSignatureError("MCP receipt.signature must be an object")
        arguments = dict(value["arguments"])
        result = value["result"]
        final_tree_hash = (
            _tree_hash(value.get("final_tree_hash"), "receipt.final_tree_hash")
            if "final_tree_hash" in value
            else None
        )
        args_hash = _sha256(value.get("args_hash"), "receipt.args_hash")
        result_hash = _sha256(value.get("result_hash"), "receipt.result_hash")
        if args_hash != digest_json(arguments):
            raise ReceiptHashError("receipt.args_hash does not match arguments")
        if result_hash != digest_json(result):
            raise ReceiptHashError("receipt.result_hash does not match result")
        return cls(
            task=_required_text(value.get("task"), "receipt.task"),
            arm=_required_text(value.get("arm"), "receipt.arm"),
            session=_required_text(value.get("session"), "receipt.session"),
            thread=_required_text(value.get("thread"), "receipt.thread"),
            seq=_positive_int(value.get("seq"), "receipt.seq"),
            request_id=_required_text(value.get("request_id"), "receipt.request_id"),
            args_hash=args_hash,
            result_hash=result_hash,
            status=status,
            server_generation=_required_text(
                value.get("server_generation"), "receipt.server_generation"
            ),
            patch_hash=_required_text(value.get("patch_hash"), "receipt.patch_hash"),
            tool=_normalise_tool(value.get("tool")),
            arguments=arguments,
            result=result,
            signature=dict(value["signature"]),
            final_tree_hash=final_tree_hash,
            schema=schema,
            source=source,
        )

    def to_dict(self) -> dict[str, Any]:
        value: dict[str, Any] = {
            "schema": self.schema,
            "task": self.task,
            "arm": self.arm,
            "session": self.session,
            "thread": self.thread,
            "seq": self.seq,
            "request_id": self.request_id,
            "args_hash": self.args_hash,
            "result_hash": self.result_hash,
            "status": self.status,
            "server_generation": self.server_generation,
            "patch_hash": self.patch_hash,
            "tool": self.tool,
            "arguments": dict(self.arguments),
            "result": self.result,
            "signature": dict(self.signature),
        }
        if self.final_tree_hash is not None:
            value["final_tree_hash"] = self.final_tree_hash
        value["source"] = self.source
        return value


def _resolve_receipt_verifier(
    *,
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
) -> Ed25519Verifier:
    try:
        if identity_envelope is not None:
            identity_envelope = parse_identity_envelope(identity_envelope)
        return verifier_from(
            verifier=verifier,
            identity_envelope=identity_envelope,
            public_key=public_key,
        )
    except (SignedReceiptError, ValueError) as exc:
        raise ReceiptSignatureError(str(exc)) from exc


def validate_server_receipt(
    value: MCPServerReceipt | Mapping[str, Any],
    *,
    task: str | None = None,
    arm: str | None = None,
    session: str | None = None,
    thread: str | None = None,
    server_generation: str | None = None,
    patch_hash: str | None = None,
    grader_artifact: Mapping[str, Any] | None = None,
    final_tree_hash: str | None = None,
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
) -> MCPServerReceipt:
    """Parse and cryptographically verify one server receipt."""

    # Re-parse even an already typed object.  Dataclasses are constructible by
    # callers and must not become an unchecked trust boundary for status/hash
    # or source fields.
    raw = value.to_dict() if isinstance(value, MCPServerReceipt) else value
    receipt = MCPServerReceipt.from_mapping(raw)
    expected = {
        "task": task,
        "arm": arm,
        "session": session,
        "thread": thread,
        "server_generation": server_generation,
    }
    for field, wanted in expected.items():
        if wanted is not None and getattr(receipt, field) != _required_text(wanted, field):
            raise ReceiptIdentityError(f"receipt.{field} drifted from expected run identity")
    trusted = _resolve_receipt_verifier(
        verifier=verifier,
        identity_envelope=identity_envelope,
        public_key=public_key,
    )
    try:
        payload = verify_signed_record(
            raw,
            verifier=trusted,
            kind=MCP_RECEIPT_KIND,
            issuer=receipt.source,
        )
        validate_receipt_context(
            payload,
            task=receipt.task,
            arm=receipt.arm,
            session=receipt.session,
            thread=receipt.thread,
            patch_hash=patch_hash,
            grader_artifact=grader_artifact,
            seq=receipt.seq,
            final_tree_hash=final_tree_hash,
        )
    except SignedReceiptError as exc:
        raise ReceiptSignatureError(str(exc)) from exc
    return receipt


def validate_receipts(
    values: Iterable[MCPServerReceipt | Mapping[str, Any]],
    *,
    task: str,
    arm: str,
    session: str,
    thread: str,
    server_generation: str | None = None,
    require_contiguous: bool = True,
    patch_hash: str | None = None,
    grader_artifact: Mapping[str, Any] | None = None,
    final_tree_hash: str | None = None,
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
) -> tuple[MCPServerReceipt, ...]:
    """Validate an append-only, signed receipt stream for one A/B arm."""

    trusted = _resolve_receipt_verifier(
        verifier=verifier,
        identity_envelope=identity_envelope,
        public_key=public_key,
    )
    receipts = tuple(
        validate_server_receipt(
            value,
            task=task,
            arm=arm,
            session=session,
            thread=thread,
            server_generation=server_generation,
            patch_hash=patch_hash,
            grader_artifact=grader_artifact,
            final_tree_hash=final_tree_hash,
            verifier=trusted,
        )
        for value in values
    )
    if not receipts:
        raise ReceiptSequenceError("receipt stream must not be empty")
    request_ids: set[str] = set()
    replay_guard = ReceiptReplayGuard()
    generations = {item.server_generation for item in receipts}
    if len(generations) != 1:
        raise ReceiptSequenceError("receipt stream mixes server generations")
    if server_generation is not None and generations != {_required_text(server_generation, "server_generation")}:
        raise ReceiptSequenceError("receipt stream server generation drifted")
    previous = 0
    for index, receipt in enumerate(receipts, 1):
        try:
            replay_guard.observe(receipt.to_dict(), receipt.signature)
        except (ReceiptReplayError, SignedReceiptError) as exc:
            raise ReceiptSequenceError(str(exc)) from exc
        if receipt.request_id in request_ids:
            raise ReceiptSequenceError(f"duplicate receipt.request_id at index {index}")
        request_ids.add(receipt.request_id)
        if receipt.seq <= previous:
            raise ReceiptSequenceError("receipt sequence is not strictly increasing")
        if require_contiguous and receipt.seq != (1 if index == 1 else previous + 1):
            raise ReceiptSequenceError("receipt sequence has a gap")
        previous = receipt.seq
    return receipts


def _require_receipt_objects(
    values: Iterable[MCPServerReceipt],
    *,
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
    patch_hash: str | None = None,
    grader_artifact: Mapping[str, Any] | None = None,
    final_tree_hash: str | None = None,
) -> tuple[MCPServerReceipt, ...]:
    raw_receipts = tuple(values)
    if not raw_receipts or any(not isinstance(item, MCPServerReceipt) for item in raw_receipts):
        raise ReceiptChainError("chain validation requires already validated MCPServerReceipt objects")
    try:
        return tuple(
            validate_server_receipt(
                item,
                verifier=verifier,
                identity_envelope=identity_envelope,
                public_key=public_key,
                patch_hash=patch_hash,
                grader_artifact=grader_artifact,
                final_tree_hash=final_tree_hash,
            )
            for item in raw_receipts
        )
    except (ReceiptError, ReceiptSignatureError) as exc:
        raise ReceiptChainError(f"invalid typed receipt in chain: {exc}") from exc


def _locations(value: Any) -> set[str]:
    found: set[str] = set()

    def visit(item: Any) -> None:
        if isinstance(item, str):
            for match in _LOCATION_IN_TEXT_RE.finditer(item):
                try:
                    found.add(
                        _normalise_position(
                            f"{match.group('path')}:{match.group('line')}:{match.group('column')}"
                        )
                    )
                except ReceiptError:
                    continue
        elif isinstance(item, Mapping):
            path = item.get("file_path", item.get("file", item.get("path", item.get("uri"))))
            line = item.get("line", item.get("line_number"))
            column = item.get("column", item.get("character", item.get("col")))
            if path is not None and line is not None and column is not None:
                try:
                    found.add(_normalise_position(f"{path}:{int(line)}:{int(column)}"))
                except (ReceiptError, TypeError, ValueError):
                    pass
            for child in item.values():
                visit(child)
        elif isinstance(item, Sequence) and not isinstance(item, (str, bytes, bytearray)):
            for child in item:
                visit(child)

    visit(value)
    return found


def _paths(value: Any) -> set[str]:
    path_keys = {
        "file",
        "file_path",
        "filepath",
        "path",
        "uri",
        "files",
        "file_paths",
        "filepaths",
        "changed_files",
        "targets",
    }
    nested_keys = {"arguments", "diff_filter", "changed_lines", "params", "request", "input"}
    found: set[str] = set()

    def visit(item: Any, key: str = "") -> None:
        if isinstance(item, Mapping):
            for raw_key, child in item.items():
                child_key = str(raw_key).replace("-", "_").lower()
                if child_key in path_keys:
                    if isinstance(child, str):
                        try:
                            found.add(_normalise_path(child))
                        except ReceiptError:
                            pass
                    elif isinstance(child, Sequence) and not isinstance(child, (str, bytes, bytearray)):
                        for path in child:
                            if isinstance(path, str):
                                try:
                                    found.add(_normalise_path(path))
                                except ReceiptError:
                                    pass
                elif child_key == "changed_lines" and isinstance(child, Mapping):
                    for path in child:
                        if isinstance(path, str):
                            try:
                                found.add(_normalise_path(path))
                            except ReceiptError:
                                pass
                elif child_key == "diff_filter" and isinstance(child, Sequence) and not isinstance(child, (str, bytes, bytearray)):
                    for path in child:
                        if isinstance(path, str):
                            try:
                                found.add(_normalise_path(path))
                            except ReceiptError:
                                pass
                elif child_key in nested_keys:
                    visit(child, child_key)
        elif isinstance(item, Sequence) and not isinstance(item, (str, bytes, bytearray)):
            for child in item:
                visit(child, key)

    visit(value)
    return found


_DIFF_FILTER_FIELDS = frozenset(("mode", "changed_lines"))
_DIAGNOSTICS_ARGUMENT_FIELDS = frozenset(
    ("file_path", "file_paths", "language_id", "diff_filter", "changed_lines", "work_dir")
)


def _normalise_target_files(values: Iterable[Any] | None, label: str) -> tuple[str, ...]:
    """Normalize a target array and reject aliases or duplicate paths."""

    if values is None:
        return ()
    if isinstance(values, (str, bytes, bytearray)):
        raise DiffFilterError(f"{label} must be an array of paths")
    try:
        iterator = iter(values)
    except TypeError as exc:
        raise DiffFilterError(f"{label} must be an array of paths") from exc
    paths: list[str] = []
    seen: set[str] = set()
    for index, value in enumerate(iterator):
        path = _normalise_path(value, f"{label}[{index}]")
        if path in seen:
            raise DiffFilterError(f"duplicate {label}: {path}")
        seen.add(path)
        paths.append(path)
    return tuple(sorted(paths))


def _normalise_positive_lines(value: Any, label: str) -> list[int]:
    """Validate a non-empty array of unique, one-based line numbers."""

    if not isinstance(value, Sequence) or isinstance(value, (str, bytes, bytearray)):
        raise DiffFilterError(f"{label} must be an array of positive integers")
    if not value:
        raise DiffFilterError(f"{label} must contain at least one line number")
    lines: list[int] = []
    seen: set[int] = set()
    for index, line in enumerate(value):
        if isinstance(line, bool) or not isinstance(line, int) or line <= 0:
            raise DiffFilterError(f"{label}[{index}] must be a positive integer")
        if line in seen:
            raise DiffFilterError(f"{label} contains duplicate line {line}")
        seen.add(line)
        lines.append(line)
    return sorted(lines)


def parse_diff_filter(
    value: Any, *, target_files: Iterable[Any] | None = None
) -> dict[str, Any]:
    """Parse the exact object diff_filter contract used by diagnostics.

    The accepted shape is exactly ``{"mode": "changed_lines",
    "changed_lines": {"repo/path": [positive, ...]}}``.  If target files are
    supplied, the mapping keys must equal them after repository-path
    normalization; omissions and extras are both rejected.
    """

    if not isinstance(value, Mapping):
        raise DiffFilterError("diagnostics diff_filter must be an object")
    keys = set(value)
    missing = sorted(_DIFF_FILTER_FIELDS - keys)
    unknown = sorted(keys - _DIFF_FILTER_FIELDS)
    if missing:
        raise DiffFilterError("diagnostics diff_filter missing field(s): " + ", ".join(missing))
    if unknown:
        raise DiffFilterError("diagnostics diff_filter has unknown field(s): " + ", ".join(unknown))
    if value["mode"] != "changed_lines":
        raise DiffFilterError('diagnostics diff_filter.mode must be "changed_lines"')
    raw_lines = value["changed_lines"]
    if not isinstance(raw_lines, Mapping):
        raise DiffFilterError("diagnostics diff_filter.changed_lines must be an object")
    if not raw_lines:
        raise DiffFilterError("diagnostics diff_filter.changed_lines must not be empty")

    normalized_lines: dict[str, list[int]] = {}
    for raw_path, lines in raw_lines.items():
        path = _normalise_path(raw_path, "diagnostics diff_filter path")
        if path in normalized_lines:
            raise DiffFilterError(f"duplicate diagnostics diff_filter path: {path}")
        normalized_lines[path] = _normalise_positive_lines(
            lines, f"diagnostics diff_filter.changed_lines[{path}]"
        )

    if target_files is not None:
        expected = set(_normalise_target_files(target_files, "diagnostics target files"))
        actual = set(normalized_lines)
        missing_paths = sorted(expected - actual)
        extra_paths = sorted(actual - expected)
        if missing_paths:
            raise DiffFilterError(
                "diagnostics diff_filter.changed_lines missing target file(s): "
                + ", ".join(missing_paths)
            )
        if extra_paths:
            raise DiffFilterError(
                "diagnostics diff_filter.changed_lines has extra file(s): "
                + ", ".join(extra_paths)
            )

    return {
        "mode": "changed_lines",
        "changed_lines": {
            path: normalized_lines[path] for path in sorted(normalized_lines)
        },
    }


normalize_diff_filter = parse_diff_filter


def normalize_diagnostics_arguments(
    arguments: Mapping[str, Any],
    *,
    target_files: Iterable[Any] | None = None,
    require_file_paths: bool = False,
) -> dict[str, Any]:
    """Canonicalize diagnostics arguments and enforce exact diff coverage."""

    if not isinstance(arguments, Mapping):
        raise DiffFilterError("diagnostics arguments must be an object")
    unknown = sorted(set(arguments) - _DIAGNOSTICS_ARGUMENT_FIELDS)
    if unknown:
        raise DiffFilterError("diagnostics arguments have unknown field(s): " + ", ".join(unknown))
    has_single = "file_path" in arguments
    has_batch = "file_paths" in arguments
    if require_file_paths and not has_batch:
        raise DiffFilterError("diagnostics requires file_paths for canonical receipt evidence")
    if has_single == has_batch:
        raise DiffFilterError("diagnostics requires exactly one of file_path or file_paths")
    if has_single:
        paths = _normalise_target_files((arguments["file_path"],), "file_path")
    else:
        paths = _normalise_target_files(arguments["file_paths"], "file_paths")
        if not paths:
            raise DiffFilterError("file_paths must contain at least one path")
    expected = _normalise_target_files(target_files, "diagnostics target files")
    if target_files is not None and paths != expected:
        missing = sorted(set(expected) - set(paths))
        extra = sorted(set(paths) - set(expected))
        details: list[str] = []
        if missing:
            details.append("missing=" + ",".join(missing))
        if extra:
            details.append("extra=" + ",".join(extra))
        raise DiffFilterError(
            "diagnostics target files do not match expected files ("
            + "; ".join(details)
            + ")"
        )
    if "changed_lines" in arguments:
        raise DiffFilterError("diagnostics changed_lines shorthand is not accepted; use diff_filter object")
    if "diff_filter" not in arguments:
        raise DiffFilterError("diagnostics diff_filter is required for diff-scoped coverage")
    diff_filter = parse_diff_filter(arguments["diff_filter"], target_files=paths)
    normalized: dict[str, Any] = {
        "file_paths": list(paths),
        "diff_filter": diff_filter,
    }
    for key in ("language_id", "work_dir"):
        if key in arguments:
            normalized[key] = _required_text(arguments[key], f"diagnostics {key}")
    return normalized


normalize_diagnostics_args = normalize_diagnostics_arguments


def _xref_position(arguments: Mapping[str, Any]) -> str:
    value = arguments.get("pos", arguments.get("position"))
    if value is None:
        raise ReceiptChainError("xref receipt is missing arguments.pos")
    return _normalise_position(value)


@dataclasses.dataclass(frozen=True)
class LSPReceiptCoverage:
    """Validated semantic coverage derived from server receipts."""

    structure_positions: tuple[str, ...]
    diagnostics_files: tuple[str, ...]
    structure_count: int
    xref_count: int
    diagnostics_count: int


def validate_structure_xref_chain(
    values: Iterable[MCPServerReceipt],
    *,
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
    patch_hash: str | None = None,
    grader_artifact: Mapping[str, Any] | None = None,
    final_tree_hash: str | None = None,
) -> tuple[str, ...]:
    """Require a real server structure result before every xref position."""

    receipts = _require_receipt_objects(
        values,
        verifier=verifier,
        identity_envelope=identity_envelope,
        public_key=public_key,
        patch_hash=patch_hash,
        grader_artifact=grader_artifact,
        final_tree_hash=final_tree_hash,
    )
    structure_positions: set[str] = set()
    saw_structure = False
    saw_xref = False
    for receipt in receipts:
        if receipt.tool == "structure":
            saw_structure = True
            structure_positions.update(_locations(receipt.result))
        elif receipt.tool == "xref":
            saw_xref = True
            position = _xref_position(receipt.arguments)
            if position not in structure_positions:
                raise ReceiptChainError(
                    f"xref position {position!r} was not returned by a previous structure receipt"
                )
    if not saw_structure or not structure_positions:
        raise ReceiptChainError("receipt stream has no usable structure result")
    if not saw_xref:
        raise ReceiptChainError("receipt stream has no xref receipt")
    return tuple(sorted(structure_positions))


def validate_lsp_receipts(
    values: Iterable[MCPServerReceipt],
    *,
    changed_files: Iterable[str] = (),
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
    patch_hash: str | None = None,
    grader_artifact: Mapping[str, Any] | None = None,
    final_tree_hash: str | None = None,
) -> LSPReceiptCoverage:
    """Validate signed semantic chain and exact diagnostics file coverage."""

    receipts = _require_receipt_objects(
        values,
        verifier=verifier,
        identity_envelope=identity_envelope,
        public_key=public_key,
        patch_hash=patch_hash,
        grader_artifact=grader_artifact,
        final_tree_hash=final_tree_hash,
    )
    positions = validate_structure_xref_chain(
        receipts,
        verifier=verifier,
        identity_envelope=identity_envelope,
        public_key=public_key,
        patch_hash=patch_hash,
        grader_artifact=grader_artifact,
        final_tree_hash=final_tree_hash,
    )
    required = set(_normalise_target_files(changed_files, "changed files"))
    diagnostics_files: set[str] = set()
    diagnostics_count = 0
    for receipt in receipts:
        if receipt.tool != "diagnostics":
            continue
        diagnostics_count += 1
        try:
            normalized = normalize_diagnostics_arguments(
                receipt.arguments,
                require_file_paths=True,
            )
        except DiffFilterError as exc:
            raise ReceiptChainError(f"invalid diagnostics receipt arguments: {exc}") from exc
        diagnostics_files.update(normalized["file_paths"])
    missing = sorted(required - diagnostics_files)
    extra = sorted(diagnostics_files - required) if required else []
    if missing:
        raise ReceiptChainError("diagnostics receipts missing files: " + ", ".join(missing))
    if extra:
        raise ReceiptChainError("diagnostics receipts have extra files: " + ", ".join(extra))
    if diagnostics_count == 0:
        raise ReceiptChainError("receipt stream has no diagnostics receipt")
    return LSPReceiptCoverage(
        structure_positions=positions,
        diagnostics_files=tuple(sorted(diagnostics_files)),
        structure_count=sum(item.tool == "structure" for item in receipts),
        xref_count=sum(item.tool == "xref" for item in receipts),
        diagnostics_count=diagnostics_count,
    )


_ISOLATION_FIELDS = {
    "schema",
    "mode",
    "task",
    "arm",
    "session",
    "thread",
    "issuer",
    "attestation_hash",
    "network",
    "sidecar",
    "ingress",
    "raw_prompt_hash",
    "raw_prompt",
    "patch_hash",
    "final_tree_hash",
    "signature",
}
_ATTESTATION_FIELDS = {"disabled", "status", "policy_hash"}
_INGRESS_FIELDS = {"count", "receipts", "log_hash"}


@dataclasses.dataclass(frozen=True)
class NoLSPIsolationEvidence:
    """Executor attestation proving that a No-LSP arm had no LSP ingress."""

    task: str
    arm: str
    session: str
    thread: str
    issuer: str
    patch_hash: str
    attestation_hash: str
    network_policy_hash: str
    sidecar_policy_hash: str
    ingress_log_hash: str
    raw_prompt_hash: str
    raw_prompt: str
    signature: Mapping[str, Any]
    final_tree_hash: str | None = None

    def to_dict(self) -> dict[str, Any]:
        """Return the normalized attestation included in canonical evidence."""

        result = {
            "schema": ISOLATION_SCHEMA,
            "mode": "no_lsp",
            "task": self.task,
            "arm": self.arm,
            "session": self.session,
            "thread": self.thread,
            "issuer": self.issuer,
            "patch_hash": self.patch_hash,
            "attestation_hash": self.attestation_hash,
            "network": {
                "disabled": True,
                "status": "enforced",
                "policy_hash": self.network_policy_hash,
            },
            "sidecar": {
                "disabled": True,
                "status": "enforced",
                "policy_hash": self.sidecar_policy_hash,
            },
            "ingress": {
                "count": 0,
                "receipts": [],
                "log_hash": self.ingress_log_hash,
            },
            "raw_prompt_hash": self.raw_prompt_hash,
            "raw_prompt": self.raw_prompt,
            "signature": dict(self.signature),
        }
        if self.final_tree_hash is not None:
            result["final_tree_hash"] = self.final_tree_hash
        return result


def _attestation(value: Any, label: str) -> str:
    if not isinstance(value, Mapping):
        raise IsolationError(f"{label} attestation must be an object")
    _reject_unknown(value, _ATTESTATION_FIELDS, label)
    if value.get("disabled") is not True or value.get("status") != "enforced":
        raise IsolationError(f"{label} must be disabled with status=enforced")
    return _sha256(value.get("policy_hash"), f"{label}.policy_hash")


def canonical_isolation_attestation_payload(value: Mapping[str, Any]) -> dict[str, Any]:
    """Return the exact policy/ingress/identity payload covered by attestation_hash."""

    if not isinstance(value, Mapping):
        raise IsolationError("isolation attestation payload must be an object")
    payload = {
        "issuer": _required_text(value.get("issuer"), "isolation.issuer"),
        "identity": {
            "task": _required_text(value.get("task"), "isolation.task"),
            "arm": _required_text(value.get("arm"), "isolation.arm"),
            "session": _required_text(value.get("session"), "isolation.session"),
            "thread": _required_text(value.get("thread"), "isolation.thread"),
        },
        "policy": {
            "network": value.get("network"),
            "sidecar": value.get("sidecar"),
        },
        "ingress": value.get("ingress"),
    }
    payload["patch_hash"] = _sha256(value.get("patch_hash"), "isolation.patch_hash")
    return payload


def validate_no_lsp_isolation(
    value: Mapping[str, Any],
    *,
    task: str | None = None,
    arm: str | None = None,
    session: str | None = None,
    thread: str | None = None,
    patch_hash: str | None = None,
    grader_artifact: Mapping[str, Any] | None = None,
    final_tree_hash: str | None = None,
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
) -> NoLSPIsolationEvidence:
    """Require executor-level network/sidecar disablement and zero ingress.

    Any ingress receipt, including an error or timeout, is contamination.  A
    caller cannot replace the empty receipt list with a claimed count because
    the list and its hash are checked together.
    """

    if not isinstance(value, Mapping):
        raise IsolationError("No-LSP isolation evidence must be an object")
    _reject_unknown(value, _ISOLATION_FIELDS, "No-LSP isolation evidence")
    if value.get("schema") != ISOLATION_SCHEMA:
        raise IsolationError("unsupported No-LSP isolation schema")
    if value.get("mode") != "no_lsp":
        raise IsolationError("No-LSP isolation evidence must declare mode=no_lsp")
    identity = {
        "task": _required_text(value.get("task"), "isolation.task"),
        "arm": _required_text(value.get("arm"), "isolation.arm"),
        "session": _required_text(value.get("session"), "isolation.session"),
        "thread": _required_text(value.get("thread"), "isolation.thread"),
    }
    issuer = _required_text(value.get("issuer"), "isolation.issuer")
    if issuer != "executor":
        raise IsolationError("No-LSP attestation issuer must be executor")
    for field, expected in (("task", task), ("arm", arm), ("session", session), ("thread", thread)):
        if expected is not None and identity[field] != _required_text(expected, field):
            raise ReceiptIdentityError(f"isolation.{field} drifted from expected run identity")
    trusted = _resolve_receipt_verifier(
        verifier=verifier,
        identity_envelope=identity_envelope,
        public_key=public_key,
    )
    try:
        signed_payload = verify_signed_record(
            value,
            verifier=trusted,
            kind=NO_LSP_ISOLATION_KIND,
            issuer=issuer,
        )
    except SignedReceiptError as exc:
        raise IsolationError(str(exc)) from exc
    signed_patch_hash = _sha256(
        signed_payload.get("patch_hash"),
        "isolation.patch_hash",
    )
    if patch_hash is not None and signed_patch_hash != _sha256(patch_hash, "patch_hash"):
        raise ReceiptIdentityError("isolation.patch_hash drifted from expected patch")
    signed_tree_hash = None
    if "final_tree_hash" in signed_payload or final_tree_hash is not None:
        signed_tree_hash = _tree_hash(
            signed_payload.get("final_tree_hash"),
            "isolation.final_tree_hash",
        )
        if final_tree_hash is not None and signed_tree_hash != _tree_hash(
            final_tree_hash,
            "expected final_tree_hash",
        ):
            raise ReceiptIdentityError("isolation.final_tree_hash drifted")
    network_hash = _attestation(value.get("network"), "network")
    sidecar_hash = _attestation(value.get("sidecar"), "sidecar")
    ingress = value.get("ingress")
    if not isinstance(ingress, Mapping):
        raise IsolationError("isolation.ingress must be an object")
    _reject_unknown(ingress, _INGRESS_FIELDS, "isolation.ingress")
    receipts = ingress.get("receipts")
    if not isinstance(receipts, list):
        raise IsolationError("isolation.ingress.receipts must be an array")
    count = ingress.get("count")
    if isinstance(count, bool) or not isinstance(count, int) or count != 0:
        raise IsolationError("No-LSP ingress count must be zero")
    if receipts:
        raise IsolationError("No-LSP arm contains MCP ingress receipts")
    log_hash = _sha256(ingress.get("log_hash"), "isolation.ingress.log_hash")
    if log_hash != digest_json(receipts):
        raise IsolationError("isolation.ingress.log_hash does not match the empty receipt log")
    raw_prompt = scan_no_lsp_prompt(value.get("raw_prompt"))
    try:
        raw_prompt_hash = _sha256(value.get("raw_prompt_hash"), "isolation.raw_prompt_hash")
    except ReceiptError as exc:
        raise IsolationError(str(exc)) from exc
    if raw_prompt_hash != digest_text(raw_prompt):
        raise IsolationError("isolation.raw_prompt_hash does not match raw_prompt")
    try:
        attestation_hash = _sha256(value.get("attestation_hash"), "isolation.attestation_hash")
    except ReceiptError as exc:
        raise IsolationError(str(exc)) from exc
    if attestation_hash != digest_json(canonical_isolation_attestation_payload(value)):
        raise IsolationError(
            "isolation.attestation_hash does not match policy/ingress/identity canonical payload"
        )
    return NoLSPIsolationEvidence(
        task=identity["task"],
        arm=identity["arm"],
        session=identity["session"],
        thread=identity["thread"],
        issuer=issuer,
        patch_hash=signed_patch_hash,
        attestation_hash=attestation_hash,
        network_policy_hash=network_hash,
        sidecar_policy_hash=sidecar_hash,
        ingress_log_hash=log_hash,
        raw_prompt_hash=raw_prompt_hash,
        raw_prompt=raw_prompt,
        signature=dict(value["signature"]),
        final_tree_hash=signed_tree_hash,
    )


_ARM_EVIDENCE_FIELDS = {
    "schema",
    "mode",
    "task",
    "arm",
    "session",
    "thread",
    "receipts",
    "isolation",
}


@dataclasses.dataclass(frozen=True)
class CanonicalArmEvidence:
    """Validated receipt/isolation evidence for one canonical A/B arm."""

    mode: str
    task: str
    arm: str
    session: str
    thread: str
    receipts: tuple[MCPServerReceipt, ...]
    isolation: NoLSPIsolationEvidence | None
    coverage: LSPReceiptCoverage | None
    schema: str = ARM_EVIDENCE_SCHEMA

    @property
    def evidence_hash(self) -> str:
        """Hash only normalized, independently validated evidence fields."""

        return digest_json(self.to_dict())

    def to_dict(self) -> dict[str, Any]:
        return {
            "schema": self.schema,
            "mode": self.mode,
            "task": self.task,
            "arm": self.arm,
            "session": self.session,
            "thread": self.thread,
            "receipts": [item.to_dict() for item in self.receipts],
            "isolation": None if self.isolation is None else self.isolation.to_dict(),
        }


def validate_canonical_arm_evidence(
    value: Mapping[str, Any],
    *,
    task: str | None = None,
    arm: str | None = None,
    session: str | None = None,
    thread: str | None = None,
    changed_files: Iterable[str] = (),
    server_generation: str | None = None,
    patch_hash: str | None = None,
    final_tree_hash: str | None = None,
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
) -> CanonicalArmEvidence:
    """Validate and normalize the receipt/isolation evidence for one arm.

    ``with_lsp`` arms must carry a complete successful server receipt stream;
    ``no_lsp`` arms must carry no receipts and a verified executor isolation
    attestation.  Coverage and hashes are derived here, never trusted from a
    caller-supplied summary.
    """

    if not isinstance(value, Mapping):
        raise ReceiptError("canonical arm evidence must be an object")
    _reject_unknown(value, _ARM_EVIDENCE_FIELDS, "canonical arm evidence")
    missing = sorted(_ARM_EVIDENCE_FIELDS - set(value))
    if missing:
        raise ReceiptError("canonical arm evidence missing field(s): " + ", ".join(missing))
    if value.get("schema") != ARM_EVIDENCE_SCHEMA:
        raise ReceiptError("unsupported canonical arm evidence schema")
    mode = _required_text(value.get("mode"), "canonical arm mode")
    if mode not in {"no_lsp", "with_lsp"}:
        raise ReceiptError(f"unsupported canonical arm mode: {mode!r}")
    identity = {
        "task": _required_text(value.get("task"), "canonical arm task"),
        "arm": _required_text(value.get("arm"), "canonical arm id"),
        "session": _required_text(value.get("session"), "canonical arm session"),
        "thread": _required_text(value.get("thread"), "canonical arm thread"),
    }
    for field, expected in (("task", task), ("arm", arm), ("session", session), ("thread", thread)):
        if expected is not None and identity[field] != _required_text(expected, f"expected {field}"):
            raise ReceiptIdentityError(f"canonical arm {field} drifted from expected identity")
    trusted = _resolve_receipt_verifier(
        verifier=verifier,
        identity_envelope=identity_envelope,
        public_key=public_key,
    )
    expected_patch_hash = patch_hash
    expected_final_tree_hash = final_tree_hash
    if identity_envelope is not None:
        envelope_value = (
            identity_envelope.to_dict()
            if hasattr(identity_envelope, "to_dict")
            else identity_envelope
        )
        if isinstance(envelope_value, Mapping) and isinstance(
            envelope_value.get("bindings"), Mapping
        ):
            patch_binding = envelope_value["bindings"].get("patch")
            if expected_patch_hash is None and isinstance(patch_binding, Mapping):
                expected_patch_hash = patch_binding.get("id")
            final_tree_binding = envelope_value["bindings"].get("final_tree")
            if expected_final_tree_hash is None and isinstance(final_tree_binding, Mapping):
                expected_final_tree_hash = final_tree_binding.get("tree_sha")

    raw_receipts = value.get("receipts")
    if not isinstance(raw_receipts, list):
        raise ReceiptError("canonical arm receipts must be an array")
    raw_isolation = value.get("isolation")
    changed = tuple(changed_files)
    if mode == "with_lsp":
        if not changed:
            raise ReceiptChainError("with_lsp canonical evidence requires changed_files")
        if raw_isolation is not None:
            raise ReceiptChainError("with_lsp canonical evidence cannot contain No-LSP isolation")
        receipts = validate_receipts(
            raw_receipts,
            task=identity["task"],
            arm=identity["arm"],
            session=identity["session"],
            thread=identity["thread"],
            server_generation=server_generation,
            patch_hash=expected_patch_hash,
            final_tree_hash=expected_final_tree_hash,
            verifier=trusted,
        )
        coverage = validate_lsp_receipts(
            receipts,
            changed_files=changed,
            verifier=trusted,
            patch_hash=expected_patch_hash,
            final_tree_hash=expected_final_tree_hash,
        )
        isolation = None
    else:
        if raw_receipts:
            raise IsolationError("no_lsp canonical evidence must contain zero receipts")
        if raw_isolation is None:
            raise IsolationError("no_lsp canonical evidence requires isolation attestation")
        isolation = validate_no_lsp_isolation(
            raw_isolation,
            task=identity["task"],
            arm=identity["arm"],
            session=identity["session"],
            thread=identity["thread"],
            patch_hash=expected_patch_hash,
            final_tree_hash=expected_final_tree_hash,
            verifier=trusted,
        )
        receipts = ()
        coverage = None
    return CanonicalArmEvidence(
        mode=mode,
        task=identity["task"],
        arm=identity["arm"],
        session=identity["session"],
        thread=identity["thread"],
        receipts=receipts,
        isolation=isolation,
        coverage=coverage,
    )


def build_canonical_arm_evidence(
    *,
    mode: str,
    task: str,
    arm: str,
    session: str,
    thread: str,
    receipts: Iterable[MCPServerReceipt | Mapping[str, Any]] = (),
    isolation: Mapping[str, Any] | None = None,
    changed_files: Iterable[str] = (),
    server_generation: str | None = None,
    patch_hash: str | None = None,
    final_tree_hash: str | None = None,
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
) -> CanonicalArmEvidence:
    """Build the canonical envelope from adapter-owned receipt/attestation parts."""

    return validate_canonical_arm_evidence(
        {
            "schema": ARM_EVIDENCE_SCHEMA,
            "mode": mode,
            "task": task,
            "arm": arm,
            "session": session,
            "thread": thread,
            "receipts": list(receipts),
            "isolation": isolation,
        },
        changed_files=changed_files,
        server_generation=server_generation,
        patch_hash=patch_hash,
        final_tree_hash=final_tree_hash,
        verifier=verifier,
        identity_envelope=identity_envelope,
        public_key=public_key,
    )


# Descriptive aliases for runner integrations.
parse_receipt = validate_server_receipt
validate_receipt_stream = validate_receipts
validate_lsp_receipt_stream = validate_lsp_receipts
validate_no_lsp = validate_no_lsp_isolation
validate_arm_evidence = validate_canonical_arm_evidence
canonical_arm_evidence = validate_canonical_arm_evidence
