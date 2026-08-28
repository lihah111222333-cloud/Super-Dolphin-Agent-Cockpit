#!/usr/bin/env python3
"""Build a fail-closed, diff-scoped MCP diagnostics request.

The closed-loop runner receives two related but deliberately separate values
from this module:

``changed_files`` and ``exemptions``
    The values can be passed directly to
    :mod:`cross_file_closed_loop_guard`.

``diagnostics_args``
    The exact JSON arguments for the MCP ``diagnostics`` tool.  A batch
    request always contains one changed-line mapping per target file.

This module does not invoke an LSP.  In ``no_lsp`` mode it returns no
diagnostics call at all.  Parsing is intentionally strict: accepting a patch
whose path or hunk shape is ambiguous would make a missing diagnostics call
look like coverage.
"""

from __future__ import annotations

import argparse
import ast
import dataclasses
import fnmatch
import json
import pathlib
import re
import shlex
import sys
from collections.abc import Iterable, Mapping, Sequence
from typing import Any


class CoverageError(ValueError):
    """Base class for malformed input or an unsafe coverage plan."""


class DiffParseError(CoverageError):
    """The supplied unified diff is not a supported, unambiguous patch."""


class RegistryError(CoverageError):
    """The language or exemption registry violates the coverage contract."""


_HUNK_RE = re.compile(
    r"^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?:.*)$"
)
_DRIVE_RE = re.compile(r"^[A-Za-z]:([/\\]|$)")
_NULL = "/dev/null"
_EXEMPTION_KINDS = {"generated", "unsupported_language"}
_KIND_ALIASES = {
    "generated": "generated",
    "unsupported": "unsupported_language",
    "unsupported-language": "unsupported_language",
    "unsupported_language": "unsupported_language",
    "unsupported language": "unsupported_language",
}
_LANGUAGE_EXTENSION_HINTS = {
    "c": (".c", ".h"),
    "cpp": (".cc", ".cpp", ".cxx", ".hh", ".hpp"),
    "go": (".go",),
    "java": (".java",),
    "javascript": (".js", ".jsx",),
    "js": (".js", ".jsx",),
    "python": (".py",),
    "py": (".py",),
    "ruby": (".rb",),
    "rust": (".rs",),
    "typescript": (".ts", ".tsx",),
    "ts": (".ts", ".tsx",),
}
_DIFF_FILTER_KEYS = frozenset(("mode", "changed_lines"))
_DIAGNOSTICS_ARGUMENT_KEYS = frozenset(
    ("file_path", "file_paths", "language_id", "changed_lines", "diff_filter", "work_dir")
)


def _text(value: Any, label: str) -> str:
    if not isinstance(value, str):
        raise CoverageError(f"{label} must be text")
    result = value.strip()
    if not result:
        raise CoverageError(f"{label} is required")
    return result


def _normalise_path(value: Any, label: str = "path") -> str:
    """Normalize a repository-relative path and reject traversal/escaping."""

    if not isinstance(value, str):
        raise CoverageError(f"{label} must be text")
    raw = value.strip()
    if not raw:
        raise CoverageError(f"{label} is required")
    if "\x00" in raw:
        raise CoverageError(f"{label} contains NUL")
    raw = raw.replace("\\", "/")
    if raw.startswith("file://"):
        raise CoverageError(f"{label} must be repository-relative")
    if raw.startswith("/") or _DRIVE_RE.match(raw):
        raise CoverageError(f"{label} escapes the repository: {value!r}")
    parts = [part for part in raw.split("/") if part not in ("", ".")]
    if not parts or any(part == ".." for part in parts):
        raise CoverageError(f"{label} escapes the repository: {value!r}")
    # A path containing a colon in a first component is not a portable Git
    # path and is commonly an unrecognised drive/URI form.
    if ":" in parts[0]:
        raise CoverageError(f"{label} is not a repository path: {value!r}")
    return "/".join(parts)


def _normalise_target_paths(
    values: Iterable[Any] | None, label: str = "diagnostics target files"
) -> tuple[str, ...]:
    """Return canonical target paths and reject aliases or duplicates."""

    if values is None:
        return ()
    if isinstance(values, (str, bytes, bytearray)):
        values = [values.decode() if isinstance(values, bytes) else values]
    paths: list[str] = []
    seen: set[str] = set()
    for index, value in enumerate(values):
        path = _normalise_path(value, f"{label}[{index}]")
        if path in seen:
            raise CoverageError(f"duplicate {label}: {path}")
        seen.add(path)
        paths.append(path)
    return tuple(sorted(paths))


def _normalise_positive_lines(value: Any, label: str) -> list[int]:
    """Validate a JSON array of positive, one-based line numbers."""

    if not isinstance(value, Sequence) or isinstance(value, (str, bytes, bytearray)):
        raise CoverageError(f"{label} must be an array of positive integers")
    if not value:
        raise CoverageError(f"{label} must contain at least one line number")
    lines: list[int] = []
    seen: set[int] = set()
    for index, line in enumerate(value):
        if isinstance(line, bool) or not isinstance(line, int) or line <= 0:
            raise CoverageError(f"{label}[{index}] must be a positive integer")
        if line in seen:
            raise CoverageError(f"{label} contains duplicate line {line}")
        seen.add(line)
        lines.append(line)
    return sorted(lines)


def parse_diff_filter(
    value: Any, *, target_files: Iterable[Any] | None = None
) -> dict[str, Any]:
    """Parse the exact diagnostics ``diff_filter`` object schema.

    The only accepted shape is ``{"mode": "changed_lines", "changed_lines":
    {"path": [positive, ...]}}``.  When ``target_files`` is supplied, the
    mapping must contain exactly those normalized paths: omissions and extra
    entries are both rejected rather than being silently treated as covered.
    """

    if not isinstance(value, Mapping):
        raise CoverageError("diagnostics diff_filter must be an object")
    keys = set(value)
    missing = sorted(_DIFF_FILTER_KEYS - keys)
    unknown = sorted(keys - _DIFF_FILTER_KEYS)
    if missing:
        raise CoverageError("diagnostics diff_filter missing field(s): " + ", ".join(missing))
    if unknown:
        raise CoverageError("diagnostics diff_filter has unknown field(s): " + ", ".join(unknown))
    if value["mode"] != "changed_lines":
        raise CoverageError('diagnostics diff_filter.mode must be "changed_lines"')
    raw_lines = value["changed_lines"]
    if not isinstance(raw_lines, Mapping):
        raise CoverageError("diagnostics diff_filter.changed_lines must be an object")
    if not raw_lines:
        raise CoverageError("diagnostics diff_filter.changed_lines must not be empty")

    normalized_lines: dict[str, list[int]] = {}
    for raw_path, lines in raw_lines.items():
        path = _normalise_path(raw_path, "diagnostics diff_filter path")
        if path in normalized_lines:
            raise CoverageError(f"duplicate diagnostics diff_filter path: {path}")
        normalized_lines[path] = _normalise_positive_lines(
            lines, f"diagnostics diff_filter.changed_lines[{path}]"
        )

    expected = _normalise_target_paths(target_files)
    if target_files is not None:
        actual = set(normalized_lines)
        expected_set = set(expected)
        missing_paths = sorted(expected_set - actual)
        extra_paths = sorted(actual - expected_set)
        if missing_paths:
            raise CoverageError(
                "diagnostics diff_filter.changed_lines missing target file(s): "
                + ", ".join(missing_paths)
            )
        if extra_paths:
            raise CoverageError(
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
    arguments: Mapping[str, Any], *, target_files: Iterable[Any] | None = None
) -> dict[str, Any]:
    """Canonicalize a diagnostics call and enforce its diff-scoped contract."""

    if not isinstance(arguments, Mapping):
        raise CoverageError("diagnostics arguments must be an object")
    keys = set(arguments)
    unknown = sorted(keys - _DIAGNOSTICS_ARGUMENT_KEYS)
    if unknown:
        raise CoverageError("diagnostics arguments have unknown field(s): " + ", ".join(unknown))
    has_single = "file_path" in arguments
    has_batch = "file_paths" in arguments
    if has_single == has_batch:
        raise CoverageError("diagnostics requires exactly one of file_path or file_paths")
    if has_single:
        paths = _normalise_target_paths([arguments["file_path"]], "file_path")
    else:
        paths = _normalise_target_paths(arguments["file_paths"], "file_paths")
        if not paths:
            raise CoverageError("file_paths must contain at least one path")
    expected = _normalise_target_paths(target_files)
    if target_files is not None and paths != expected:
        missing = sorted(set(expected) - set(paths))
        extra = sorted(set(paths) - set(expected))
        details: list[str] = []
        if missing:
            details.append("missing=" + ",".join(missing))
        if extra:
            details.append("extra=" + ",".join(extra))
        raise CoverageError("diagnostics target files do not match expected files (" + "; ".join(details) + ")")
    if "changed_lines" in arguments:
        raise CoverageError("diagnostics changed_lines shorthand is not accepted; use diff_filter object")
    if "diff_filter" not in arguments:
        raise CoverageError("diagnostics diff_filter is required for diff-scoped coverage")
    diff_filter = parse_diff_filter(arguments["diff_filter"], target_files=paths)
    normalized: dict[str, Any] = {
        "file_paths": list(paths),
        "diff_filter": diff_filter,
    }
    for key in ("language_id", "work_dir"):
        if key in arguments:
            normalized[key] = _text(arguments[key], f"diagnostics {key}")
    return normalized


normalize_diagnostics_args = normalize_diagnostics_arguments


@dataclasses.dataclass(frozen=True)
class DiagnosticsCacheKey:
    """Stable identity for a diagnostics result."""

    normalized_args: str
    file_digest: str
    server_generation: str


@dataclasses.dataclass(frozen=True)
class DiagnosticsCacheDecision:
    """Pure cache decision with a machine-readable reason."""

    reuse: bool
    reason: str


def _canonical_file_digest(value: Any) -> str:
    if isinstance(value, str):
        return _text(value, "file digest")
    if not isinstance(value, Mapping) or not value:
        raise CoverageError("file digest must be non-empty text or a path-to-digest object")
    normalized: dict[str, str] = {}
    for raw_path, raw_digest in value.items():
        path = _normalise_path(raw_path, "file digest path")
        if path in normalized:
            raise CoverageError(f"duplicate file digest path: {path}")
        normalized[path] = _text(raw_digest, f"file digest for {path}")
    return json.dumps(normalized, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


def build_diagnostics_cache_key(
    arguments: Mapping[str, Any],
    file_digest: str | Mapping[str, Any],
    server_generation: Any,
    *,
    target_files: Iterable[Any] | None = None,
) -> DiagnosticsCacheKey:
    """Build a cache key from normalized args, file digest, and server generation."""

    normalized = normalize_diagnostics_arguments(arguments, target_files=target_files)
    return DiagnosticsCacheKey(
        normalized_args=json.dumps(normalized, ensure_ascii=False, sort_keys=True, separators=(",", ":")),
        file_digest=_canonical_file_digest(file_digest),
        server_generation=_text(server_generation, "server generation"),
    )


def decide_diagnostics_cache(
    cached_key: DiagnosticsCacheKey | None, requested_key: DiagnosticsCacheKey
) -> DiagnosticsCacheDecision:
    """Decide reuse without I/O, mutation, or implicit fallback."""

    if not isinstance(requested_key, DiagnosticsCacheKey):
        raise CoverageError("requested diagnostics cache key has invalid type")
    if cached_key is None:
        return DiagnosticsCacheDecision(False, "no_cached_result")
    if not isinstance(cached_key, DiagnosticsCacheKey):
        raise CoverageError("cached diagnostics cache key has invalid type")
    if cached_key.normalized_args != requested_key.normalized_args:
        return DiagnosticsCacheDecision(False, "normalized_args_changed")
    if cached_key.file_digest != requested_key.file_digest:
        return DiagnosticsCacheDecision(False, "file_digest_changed")
    if cached_key.server_generation != requested_key.server_generation:
        return DiagnosticsCacheDecision(False, "server_generation_changed")
    return DiagnosticsCacheDecision(True, "cache_hit")


diagnostics_cache_decision = decide_diagnostics_cache


def should_reuse_diagnostics(
    cached_key: DiagnosticsCacheKey | None, requested_key: DiagnosticsCacheKey
) -> bool:
    """Return only the reuse bit for callers that do not need the reason."""

    return decide_diagnostics_cache(cached_key, requested_key).reuse


def _decode_git_token(token: str, label: str) -> str:
    token = token.strip()
    if not token:
        raise DiffParseError(f"{label} is empty")
    if token.startswith('"'):
        if not token.endswith('"') or len(token) < 2:
            raise DiffParseError(f"malformed quoted {label}")
        try:
            decoded = ast.literal_eval(token)
        except (SyntaxError, ValueError) as exc:
            raise DiffParseError(f"malformed quoted {label}") from exc
        if not isinstance(decoded, str):
            raise DiffParseError(f"malformed quoted {label}")
        token = decoded
    return token


def _strip_git_prefix(path: str, prefix: str, label: str) -> str:
    path = _decode_git_token(path, label)
    if path == _NULL:
        return path
    expected = prefix + "/"
    if not path.startswith(expected):
        raise DiffParseError(f"{label} lacks Git {prefix}/ prefix: {path!r}")
    return _normalise_path(path[len(expected) :], label)


def _parse_header_paths(line: str) -> tuple[str, str]:
    if not line.startswith("diff --git "):
        raise DiffParseError("expected a diff --git header")
    try:
        tokens = shlex.split(line, posix=True)
    except ValueError as exc:
        raise DiffParseError("malformed diff --git header") from exc
    if len(tokens) != 4 or tokens[0] != "diff" or tokens[1] != "--git":
        raise DiffParseError("diff --git must contain exactly two paths")
    old = _strip_git_prefix(tokens[2], "a", "diff old path")
    new = _strip_git_prefix(tokens[3], "b", "diff new path")
    return old, new


def _parse_marker_path(line: str, marker: str, *, git_prefix: str | None) -> str:
    if not line.startswith(marker):
        raise DiffParseError(f"expected {marker.strip()} file marker")
    payload = line[len(marker) :]
    # Git emits no timestamp.  Supporting the conventional tab-separated
    # timestamp keeps the parser useful for a standard unified diff without
    # making spaces in a quoted path ambiguous.
    if "\t" in payload:
        payload = payload.split("\t", 1)[0]
    payload = _decode_git_token(payload, marker.strip() + " path")
    if payload == _NULL:
        return payload
    if git_prefix is None:
        return _normalise_path(payload, marker.strip() + " path")
    return _strip_git_prefix(payload, git_prefix, marker.strip() + " path")


@dataclasses.dataclass(frozen=True)
class Hunk:
    """One unified-diff hunk, with the new-side range preserved."""

    old_start: int
    old_count: int
    new_start: int
    new_count: int

    @property
    def new_lines(self) -> tuple[int, ...]:
        if self.new_count == 0:
            return ()
        return tuple(range(self.new_start, self.new_start + self.new_count))


@dataclasses.dataclass(frozen=True)
class DiffFile:
    """A parsed Git diff section."""

    path: str
    old_path: str | None
    new_path: str | None
    status: str
    hunks: tuple[Hunk, ...] = ()

    @property
    def changed_lines(self) -> tuple[int, ...]:
        lines: set[int] = set()
        for hunk in self.hunks:
            lines.update(hunk.new_lines)
        return tuple(sorted(lines))

    @property
    def has_new_file(self) -> bool:
        return self.new_path is not None


def _parse_hunk(lines: Sequence[str], index: int) -> tuple[Hunk, int]:
    match = _HUNK_RE.match(lines[index])
    if match is None:
        raise DiffParseError(f"malformed hunk header at line {index + 1}")
    old_start = int(match.group(1))
    old_count = int(match.group(2) or "1")
    new_start = int(match.group(3))
    new_count = int(match.group(4) or "1")
    if old_count < 0 or new_count < 0:
        raise DiffParseError("hunk counts cannot be negative")
    if old_count and old_start < 1:
        raise DiffParseError("non-empty old hunk must start at line 1 or later")
    if new_count and new_start < 1:
        raise DiffParseError("non-empty new hunk must start at line 1 or later")

    old_remaining = old_count
    new_remaining = new_count
    cursor = index + 1
    saw_body = False
    while old_remaining or new_remaining:
        if cursor >= len(lines):
            raise DiffParseError("hunk body ended before its declared counts")
        body = lines[cursor]
        if body == r"\ No newline at end of file":
            if not saw_body:
                raise DiffParseError("newline marker cannot start a hunk body")
            cursor += 1
            continue
        if not body:
            raise DiffParseError(f"hunk body line {cursor + 1} lacks a prefix")
        prefix = body[0]
        if prefix == " ":
            if old_remaining == 0 or new_remaining == 0:
                raise DiffParseError("context line exceeds hunk counts")
            old_remaining -= 1
            new_remaining -= 1
        elif prefix == "-":
            if old_remaining == 0:
                raise DiffParseError("deletion line exceeds old hunk count")
            old_remaining -= 1
        elif prefix == "+":
            if new_remaining == 0:
                raise DiffParseError("addition line exceeds new hunk count")
            new_remaining -= 1
        else:
            raise DiffParseError(
                f"unknown hunk body prefix {prefix!r} at line {cursor + 1}"
            )
        saw_body = True
        cursor += 1
    return Hunk(old_start, old_count, new_start, new_count), cursor


_ALLOWED_METADATA_PREFIXES = (
    "index ",
    "old mode ",
    "new mode ",
    "new file mode ",
    "deleted file mode ",
    "similarity index ",
    "dissimilarity index ",
    "rename from ",
    "rename to ",
    "copy from ",
    "copy to ",
)


def parse_unified_diff(patch: str | bytes) -> tuple[DiffFile, ...]:
    """Parse a Git unified diff and return deterministic file records.

    ``-U0`` is recommended but not required.  Hunk ranges, rather than only
    literal ``+`` body lines, are used because the diagnostics contract is
    expressed in new-file-side hunk ranges.
    """

    if isinstance(patch, bytes):
        try:
            patch = patch.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise DiffParseError("binary or non-UTF-8 patch is unsupported") from exc
    elif not isinstance(patch, str):
        raise DiffParseError("patch must be text or UTF-8 bytes")
    if "\x00" in patch:
        raise DiffParseError("binary patch is unsupported")
    if not patch.strip():
        return ()
    lines = patch.splitlines()
    files: list[DiffFile] = []
    cursor = 0
    while cursor < len(lines):
        if not lines[cursor].strip():
            cursor += 1
            continue
        if not lines[cursor].startswith("diff --git "):
            raise DiffParseError(
                f"unknown diff format at line {cursor + 1}: {lines[cursor]!r}"
            )
        header_old, header_new = _parse_header_paths(lines[cursor])
        cursor += 1
        old_marker: str | None = None
        new_marker: str | None = None
        rename_from: str | None = None
        rename_to: str | None = None
        hunks: list[Hunk] = []
        binary = False
        while cursor < len(lines) and not lines[cursor].startswith("diff --git "):
            line = lines[cursor]
            if line.startswith("Binary files ") or line == "GIT binary patch":
                binary = True
                cursor += 1
                # Consume the section so the error points to the file rather
                # than to a later base64 payload line.
                while cursor < len(lines) and not lines[cursor].startswith("diff --git "):
                    cursor += 1
                break
            if line.startswith("@@ "):
                hunk, cursor = _parse_hunk(lines, cursor)
                hunks.append(hunk)
                continue
            if line.startswith("--- "):
                if old_marker is not None:
                    raise DiffParseError("duplicate --- file marker")
                old_marker = _parse_marker_path(line, "--- ", git_prefix="a")
                cursor += 1
                continue
            if line.startswith("+++ "):
                if new_marker is not None:
                    raise DiffParseError("duplicate +++ file marker")
                new_marker = _parse_marker_path(line, "+++ ", git_prefix="b")
                cursor += 1
                continue
            if line.startswith("rename from "):
                if rename_from is not None:
                    raise DiffParseError("duplicate rename from metadata")
                rename_from = _normalise_path(line[len("rename from ") :], "rename from")
                cursor += 1
                continue
            if line.startswith("rename to "):
                if rename_to is not None:
                    raise DiffParseError("duplicate rename to metadata")
                rename_to = _normalise_path(line[len("rename to ") :], "rename to")
                cursor += 1
                continue
            if not line or line.startswith(_ALLOWED_METADATA_PREFIXES):
                cursor += 1
                continue
            raise DiffParseError(
                f"unknown diff metadata at line {cursor + 1}: {line!r}"
            )
        if binary:
            raise DiffParseError(f"binary diff is unsupported for {header_new}")

        old_path = old_marker if old_marker is not None else header_old
        new_path = new_marker if new_marker is not None else header_new
        if rename_from is not None:
            old_path = rename_from
        if rename_to is not None:
            new_path = rename_to
        if old_path == _NULL:
            old_path = None
        if new_path == _NULL:
            new_path = None
        if old_marker is not None and old_marker != _NULL and old_marker != header_old:
            raise DiffParseError("--- path disagrees with diff --git path")
        if new_marker is not None and new_marker != _NULL and new_marker != header_new:
            raise DiffParseError("+++ path disagrees with diff --git path")
        if (rename_from is None) != (rename_to is None):
            raise DiffParseError("rename metadata must contain both paths")
        if old_path is None and new_path is None:
            raise DiffParseError("diff section has no file path")
        if rename_from is not None:
            status = "renamed" if not hunks else "renamed_modified"
        elif old_path is None:
            status = "added"
        elif new_path is None:
            status = "deleted"
        else:
            status = "modified"
        path = new_path or old_path
        assert path is not None
        files.append(DiffFile(path, old_path, new_path, status, tuple(hunks)))

    seen: set[str] = set()
    for item in files:
        if item.path in seen:
            raise DiffParseError(f"duplicate diff section for {item.path}")
        seen.add(item.path)
    return tuple(files)


def _first(value: Mapping[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in value:
            return value[key]
    return None


def _canonical_kind(value: Any, label: str = "exemption kind") -> str | None:
    if value is None:
        return None
    if not isinstance(value, str):
        raise RegistryError(f"{label} must be text")
    text = value.strip().lower()
    if not text:
        return None
    result = _KIND_ALIASES.get(text)
    if result is None:
        raise RegistryError(f"unsupported {label}: {value!r}")
    return result


def _coerce_changed_file(value: Any) -> dict[str, Any]:
    if isinstance(value, str):
        path = _normalise_path(value)
        return {"path": path, "production": True}
    if not isinstance(value, Mapping):
        raise CoverageError(f"changed file must be a path or object: {value!r}")
    raw_path = _first(value, "path", "file", "file_path", "new_path", "newFilePath")
    if raw_path is None:
        raw_path = _first(value, "old_path", "oldPath")
    path = _normalise_path(raw_path, "changed file path")
    raw_kind = _first(value, "exemption_kind", "exemption")
    # Codex file_change uses ``kind=update|add|delete`` for the operation.
    # Only the explicit exemption field, or one of the two canonical values
    # in ``kind``, may classify a diagnostics exemption.
    if raw_kind is None and isinstance(value.get("kind"), str):
        candidate = value["kind"].strip().lower()
        if candidate in _KIND_ALIASES:
            raw_kind = candidate
    kind = _canonical_kind(raw_kind, f"exemption kind for {path}")
    if value.get("generated") is True:
        kind = "generated"
    if value.get("unsupported_language") is True or value.get("language_supported") is False:
        kind = "unsupported_language"
    result: dict[str, Any] = {
        "path": path,
        "production": bool(value.get("production", True)),
    }
    if kind is not None:
        result["exemption_kind"] = kind
    for key in ("language", "status", "old_path", "new_path"):
        if key in value and value[key] is not None:
            result[key] = value[key]
    if "old_path" in result:
        result["old_path"] = _normalise_path(result["old_path"], f"old path for {path}")
    if "new_path" in result:
        result["new_path"] = _normalise_path(result["new_path"], f"new path for {path}")
    return result


def _coerce_changed_files(values: Iterable[Any] | Mapping[str, Any] | None) -> list[dict[str, Any]]:
    if values is None:
        return []
    if isinstance(values, Mapping):
        # Accept both {"path": ..., ...} and {"src/a.py": metadata} forms.
        if any(key in values for key in ("path", "file", "file_path", "new_path", "old_path")):
            raw_values: list[Any] = [values]
        else:
            raw_values = []
            for key, metadata in values.items():
                if isinstance(metadata, Mapping):
                    entry = dict(metadata)
                    entry.setdefault("path", key)
                    raw_values.append(entry)
                elif isinstance(metadata, bool):
                    raw_values.append({"path": key, "production": metadata})
                else:
                    raw_values.append({"path": key, "language": metadata})
    else:
        if isinstance(values, (str, bytes, bytearray)):
            raw_values = [values.decode() if isinstance(values, bytes) else values]
        else:
            raw_values = list(values)
    result = [_coerce_changed_file(value) for value in raw_values]
    seen: set[str] = set()
    for item in result:
        if item["path"] in seen:
            raise CoverageError(f"duplicate changed file: {item['path']}")
        seen.add(item["path"])
    return result


@dataclasses.dataclass(frozen=True)
class _Support:
    supported: bool
    language: str = ""
    generated: bool = False


class LanguageSupport:
    """Normalize common language-registry shapes without guessing silently."""

    def __init__(self, registry: Any = None):
        self._path: dict[str, _Support] = {}
        self._patterns: list[tuple[str, _Support]] = []
        self._extensions: dict[str, _Support] = {}
        self._basenames: dict[str, _Support] = {}
        if registry is None:
            # A missing registry is still useful for a runner that only has
            # standard source files.  Unknown extensions remain unsupported
            # and therefore require an explicit exemption.
            registry = {
                ".c": "c", ".cc": "cpp", ".cpp": "cpp", ".go": "go",
                ".h": "c", ".hpp": "cpp", ".java": "java", ".js": "javascript",
                ".jsx": "javascript", ".py": "python", ".rs": "rust",
                ".ts": "typescript", ".tsx": "typescript", ".rb": "ruby",
            }
        self._load(registry)

    @staticmethod
    def _support_from(value: Any, *, default_language: str = "") -> _Support:
        if isinstance(value, bool):
            return _Support(value, default_language if value else "")
        if isinstance(value, str):
            language = value.strip()
            if not language:
                raise RegistryError("language registry value is empty")
            lowered = language.lower()
            if lowered in {"false", "unsupported", "disabled", "no"}:
                return _Support(False)
            return _Support(True, language)
        if isinstance(value, Mapping):
            supported = value.get("supported", value.get("language_supported", value.get("enabled", True)))
            if not isinstance(supported, bool):
                raise RegistryError("registry supported flag must be boolean")
            language = value.get("language", value.get("language_id", default_language))
            if language is None:
                language = ""
            if not isinstance(language, str):
                raise RegistryError("registry language must be text")
            generated = value.get("generated", False)
            if not isinstance(generated, bool):
                raise RegistryError("registry generated flag must be boolean")
            return _Support(supported, language.strip(), generated)
        raise RegistryError(f"unsupported language registry value: {value!r}")

    def _put(self, key: str, support: _Support) -> None:
        key = key.strip()
        if not key:
            raise RegistryError("language registry key is empty")
        if "/" in key or key.startswith("./"):
            path = _normalise_path(key, "language registry path")
            if path in self._path:
                raise RegistryError(f"duplicate language registry path: {path}")
            self._path[path] = support
        elif key.startswith("."):
            ext = key.lower()
            if ext in self._extensions:
                raise RegistryError(f"duplicate language registry extension: {key}")
            self._extensions[ext] = support
        elif "*" in key or "?" in key:
            self._patterns.append((key, support))
        else:
            if key.lower() in self._basenames:
                raise RegistryError(f"duplicate language registry basename: {key}")
            self._basenames[key.lower()] = support

    def _load(self, registry: Any) -> None:
        if isinstance(registry, (str, pathlib.Path)):
            path = pathlib.Path(registry)
            try:
                registry = json.loads(path.read_text(encoding="utf-8"))
            except OSError as exc:
                raise RegistryError(f"cannot read language registry {path}") from exc
            except json.JSONDecodeError as exc:
                raise RegistryError(f"invalid language registry JSON {path}") from exc
        if isinstance(registry, Sequence) and not isinstance(registry, (str, bytes, bytearray)):
            for item in registry:
                if not isinstance(item, str):
                    raise RegistryError("language registry list entries must be strings")
                language = item.lstrip(".")
                extensions = _LANGUAGE_EXTENSION_HINTS.get(language.lower(), ())
                if extensions:
                    for extension in extensions:
                        self._put(extension, _Support(True, language))
                else:
                    self._put(item, _Support(True, language))
            return
        if not isinstance(registry, Mapping):
            raise RegistryError("language support registry must be an object or list")

        # Structured registries commonly use one of these containers.
        for key in ("extensions", "supported_extensions", "languages"):
            nested = registry.get(key)
            if nested is None:
                continue
            if key == "languages" and isinstance(nested, Mapping):
                for language, descriptor in nested.items():
                    if isinstance(descriptor, Mapping) and "extensions" in descriptor:
                        support = self._support_from(descriptor, default_language=str(language))
                        extensions = descriptor["extensions"]
                        if not isinstance(extensions, Sequence) or isinstance(extensions, (str, bytes, bytearray)):
                            raise RegistryError("language extensions must be a list")
                        for extension in extensions:
                            if not isinstance(extension, str):
                                raise RegistryError("language extension must be text")
                            self._put(extension, support)
                    elif isinstance(descriptor, Sequence) and not isinstance(descriptor, (str, bytes, bytearray)):
                        support = _Support(True, str(language))
                        for extension in descriptor:
                            if not isinstance(extension, str):
                                raise RegistryError("language extension must be text")
                            self._put(extension, support)
                    else:
                        self._put(str(language), self._support_from(descriptor, default_language=str(language)))
            elif isinstance(nested, Mapping):
                for item_key, item_value in nested.items():
                    self._put(str(item_key), self._support_from(item_value))
            elif isinstance(nested, Sequence) and not isinstance(nested, (str, bytes, bytearray)):
                for extension in nested:
                    if not isinstance(extension, str):
                        raise RegistryError("language extension must be text")
                    self._put(extension, _Support(True, extension.lstrip(".")))
            else:
                raise RegistryError(f"language registry {key} must be an object or list")

        for key in ("unsupported", "unsupported_extensions", "unsupported_languages", "unsupported_language"):
            values = registry.get(key)
            if values is None:
                continue
            if not isinstance(values, Sequence) or isinstance(values, (str, bytes, bytearray)):
                raise RegistryError(f"language registry {key} must be a list")
            for item in values:
                if not isinstance(item, str):
                    raise RegistryError(f"language registry {key} entries must be text")
                extensions = _LANGUAGE_EXTENSION_HINTS.get(item.lstrip(".").lower(), ())
                if extensions:
                    for extension in extensions:
                        self._put(extension, _Support(False))
                else:
                    self._put(item, _Support(False))

        supported_values = registry.get("supported")
        if supported_values is not None:
            if not isinstance(supported_values, Sequence) or isinstance(supported_values, (str, bytes, bytearray)):
                raise RegistryError("language registry supported must be a list")
            for item in supported_values:
                if not isinstance(item, str):
                    raise RegistryError("language registry supported entries must be text")
                self._put(item, _Support(True, item.lstrip(".")))

        for key in ("generated", "generated_files"):
            values = registry.get(key)
            if values is None:
                continue
            if not isinstance(values, Sequence) or isinstance(values, (str, bytes, bytearray)):
                raise RegistryError(f"language registry {key} must be a list")
            for item in values:
                if not isinstance(item, str):
                    raise RegistryError(f"language registry {key} entries must be text")
                path = _normalise_path(item, "generated registry path")
                self._path[path] = _Support(True, "", True)

        # A flat object is also accepted.  Reserved structural keys are not
        # reinterpreted as file names.
        reserved = {"extensions", "supported_extensions", "languages", "unsupported", "unsupported_extensions", "unsupported_languages", "unsupported_language", "supported", "generated", "generated_files"}
        for key, value in registry.items():
            if key in reserved:
                continue
            if not isinstance(key, str):
                raise RegistryError("language registry keys must be text")
            if isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
                support = _Support(True, key)
                for extension in value:
                    if not isinstance(extension, str):
                        raise RegistryError("language extension must be text")
                    self._put(extension, support)
            elif isinstance(value, Mapping) and "extensions" in value:
                support = self._support_from(value, default_language=key)
                extensions = value["extensions"]
                if not isinstance(extensions, Sequence) or isinstance(extensions, (str, bytes, bytearray)):
                    raise RegistryError("language extensions must be a list")
                for extension in extensions:
                    if not isinstance(extension, str):
                        raise RegistryError("language extension must be text")
                    self._put(extension, support)
            else:
                hinted_extensions = _LANGUAGE_EXTENSION_HINTS.get(key.lower(), ())
                if hinted_extensions and "/" not in key and not key.startswith("."):
                    support = self._support_from(value, default_language=key)
                    for extension in hinted_extensions:
                        self._put(extension, support)
                else:
                    self._put(key, self._support_from(value))

    def classify(self, path: str) -> _Support:
        path = _normalise_path(path)
        exact = self._path.get(path)
        if exact is not None:
            return exact
        for pattern, support in self._patterns:
            if fnmatch.fnmatchcase(path, pattern):
                return support
        basename = pathlib.PurePosixPath(path).name.lower()
        by_basename = self._basenames.get(basename)
        if by_basename is not None:
            return by_basename
        suffix = pathlib.PurePosixPath(path).suffix.lower()
        return self._extensions.get(suffix, _Support(False))


def _coerce_exemptions(values: Iterable[Any] | Mapping[str, Any] | None) -> list[dict[str, str]]:
    if values is None:
        return []
    if isinstance(values, Mapping):
        if any(key in values for key in ("path", "file", "file_path")):
            raw_values: list[Any] = [values]
        else:
            raw_values = []
            for path, descriptor in values.items():
                if not isinstance(descriptor, Mapping):
                    raise RegistryError(f"exemption for {path!r} must be an object")
                item = dict(descriptor)
                item.setdefault("path", path)
                raw_values.append(item)
    elif isinstance(values, (str, bytes, bytearray)):
        raise RegistryError("exemptions must be a list or object")
    else:
        raw_values = list(values)
    result: list[dict[str, str]] = []
    seen: set[str] = set()
    for raw in raw_values:
        if not isinstance(raw, Mapping):
            raise RegistryError(f"exemption must be an object: {raw!r}")
        path = _normalise_path(_first(raw, "path", "file", "file_path"), "exemption path")
        if path in seen:
            raise RegistryError(f"duplicate diagnostics exemption for {path}")
        seen.add(path)
        kind = _canonical_kind(_first(raw, "kind", "exemption_kind"), f"exemption kind for {path}")
        if kind is None:
            raise RegistryError(f"exemption kind for {path} is required")
        fields: dict[str, str] = {"path": path, "kind": kind}
        for field in ("reason", "evidence", "owner"):
            value = raw.get(field)
            if not isinstance(value, str) or not value.strip():
                raise RegistryError(f"empty exemption {field} for {path}")
            fields[field] = value.strip()
        result.append(fields)
    return result


@dataclasses.dataclass(frozen=True)
class CoveragePlan:
    """Runner-ready output from :func:`build_coverage`."""

    mode: str
    changed_files: tuple[dict[str, Any], ...]
    exemptions: tuple[dict[str, str], ...]
    diagnostics_args: dict[str, Any] | None
    all_changed_files: tuple[dict[str, Any], ...] = ()
    deleted_files: tuple[str, ...] = ()
    zero_line_files: tuple[str, ...] = ()

    @property
    def diagnostics_call(self) -> dict[str, Any] | None:
        """Alias used by runners that treat a call as optional."""

        return self.diagnostics_args

    def to_runner_payload(self) -> dict[str, Any]:
        return {
            "changed_files": [dict(item) for item in self.changed_files],
            "exemptions": [dict(item) for item in self.exemptions],
            "diagnostics_args": None if self.diagnostics_args is None else _deepcopy_json(self.diagnostics_args),
        }

    as_runner_payload = to_runner_payload

    def to_dict(self) -> dict[str, Any]:
        result = self.to_runner_payload()
        result.update(
            {
                "mode": self.mode,
                "all_changed_files": [dict(item) for item in self.all_changed_files],
                "deleted_files": list(self.deleted_files),
                "zero_line_files": list(self.zero_line_files),
            }
        )
        return result

    as_dict = to_dict

    def __getitem__(self, key: str) -> Any:
        return self.to_dict()[key]

    def get(self, key: str, default: Any = None) -> Any:
        return self.to_dict().get(key, default)


def _deepcopy_json(value: Any) -> Any:
    return json.loads(json.dumps(value))


def _reconcile_changes(
    diff_files: tuple[DiffFile, ...], supplied: Iterable[Any] | Mapping[str, Any] | None
) -> list[dict[str, Any]]:
    parsed = {item.path: item for item in diff_files}
    if supplied is None:
        return [
            {
                "path": item.path,
                "production": True,
                "status": item.status,
                **({"old_path": item.old_path} if item.old_path else {}),
                **({"new_path": item.new_path} if item.new_path else {}),
            }
            for item in diff_files
        ]
    values = _coerce_changed_files(supplied)
    result: list[dict[str, Any]] = []
    consumed: set[str] = set()
    for value in values:
        path = value["path"]
        item = parsed.get(path)
        # A Codex rename can report the old path in file_change metadata.  The
        # new side is canonical for diagnostics and guard coverage.
        if item is None:
            rename_matches = [candidate for candidate in diff_files if candidate.old_path == path and candidate.new_path]
            if len(rename_matches) == 1:
                item = rename_matches[0]
                path = item.path
                value = dict(value)
                value["path"] = path
        if item is None:
            raise CoverageError(f"changed file has no matching diff section: {value['path']}")
        if path in consumed:
            raise CoverageError(f"duplicate changed file after rename normalization: {path}")
        consumed.add(path)
        merged = dict(value)
        merged.setdefault("status", item.status)
        if item.old_path:
            merged.setdefault("old_path", item.old_path)
        if item.new_path:
            merged.setdefault("new_path", item.new_path)
        result.append(merged)
    missing = sorted(path for path in parsed if path not in consumed)
    if missing:
        raise CoverageError("diff files missing from changed_files: " + ", ".join(missing))
    return result


def _mode(value: Any) -> str:
    if not isinstance(value, str):
        raise CoverageError("mode must be text")
    normalized = value.strip().lower().replace("-", "_")
    if normalized in {"with_lsp", "lsp"}:
        return "with_lsp"
    if normalized in {"no_lsp", "nolsp"}:
        return "no_lsp"
    raise CoverageError(f"unsupported diagnostics mode: {value!r}")


def build_coverage(
    patch: str | bytes,
    changed_files: Iterable[Any] | Mapping[str, Any] | None = None,
    language_support: Any = None,
    exemptions: Iterable[Any] | Mapping[str, Any] | None = None,
    mode: str = "with_lsp",
) -> CoveragePlan:
    """Build a complete runner payload from a Git diff.

    ``changed_files`` may be omitted when the patch itself is authoritative.
    It is still validated against every parsed diff section when supplied.
    ``language_support`` accepts a flat extension/path map or a structured
    registry (see :class:`LanguageSupport`).
    """

    run_mode = _mode(mode)
    diff_files = parse_unified_diff(patch)
    all_changes = _reconcile_changes(diff_files, changed_files)
    support = LanguageSupport(language_support)
    explicit_exemptions = _coerce_exemptions(exemptions)
    explicit_by_path = {item["path"]: item for item in explicit_exemptions}
    diff_by_path = {item.path: item for item in diff_files}

    effective: list[dict[str, Any]] = []
    generated_or_unsupported: set[str] = set()
    for change in all_changes:
        path = change["path"]
        parsed = diff_by_path[path]
        production = bool(change.get("production", True))
        language = str(change.get("language", "") or "").strip()
        classified = support.classify(path)
        if not language:
            language = classified.language
        kind = _canonical_kind(change.get("exemption_kind"), f"exemption kind for {path}")
        registered_exemption = explicit_by_path.get(path)
        if registered_exemption is not None:
            registered_kind = registered_exemption["kind"]
            if kind is not None and kind != registered_kind:
                raise RegistryError(f"diagnostics exemption kind mismatch for {path}")
            kind = registered_kind
        if production and classified.generated:
            if kind is not None and kind != "generated":
                raise RegistryError(f"diagnostics exemption kind mismatch for {path}")
            kind = "generated"
        if production and not classified.supported:
            if kind is not None and kind != "unsupported_language":
                raise RegistryError(f"diagnostics exemption kind mismatch for {path}")
            kind = "unsupported_language" if kind is None else kind
        if kind is not None:
            generated_or_unsupported.add(path)
        merged = dict(change)
        merged["path"] = path
        merged["language"] = language
        if kind is not None:
            merged["exemption_kind"] = kind
        effective.append(merged)

    actual_paths = {item["path"] for item in effective}
    stale = sorted(path for path in explicit_by_path if path not in actual_paths)
    if stale:
        raise RegistryError("stale diagnostics exemptions: " + ", ".join(stale))
    for path in generated_or_unsupported:
        entry = explicit_by_path.get(path)
        if entry is None:
            raise RegistryError(f"missing diagnostics exemption for {path}")
        if entry["kind"] != effective[next(i for i, item in enumerate(effective) if item["path"] == path)].get("exemption_kind"):
            raise RegistryError(f"diagnostics exemption kind mismatch for {path}")
    for path, entry in explicit_by_path.items():
        if path not in generated_or_unsupported:
            raise RegistryError(f"diagnostics exemption is not applicable to supported file {path}")

    # Entries for generated/unsupported files with no live new-side lines are
    # still valid guard exemptions when that file remains in guard_changes.
    # Keep the emitted registry exactly scoped to those direct guard inputs;
    # an exemption for an omitted deleted supported file would be stale to the
    # guard and therefore could not be called a runner-ready payload.
    changed_output: list[dict[str, Any]] = []
    deleted_files: list[str] = []
    zero_line_files: list[str] = []
    changed_lines: dict[str, list[int]] = {}
    for change in effective:
        path = change["path"]
        parsed = diff_by_path[path]
        lines = list(parsed.changed_lines)
        if parsed.new_path is None:
            deleted_files.append(path)
        if not lines:
            zero_line_files.append(path)
        # The guard's production set must contain files for which a live
        # diagnostics target exists.  Deleted/empty-new-side files are kept in
        # all_changed_files for auditability but cannot be valid MCP targets.
        if not parsed.new_path or not lines:
            if change.get("production", True) and path in generated_or_unsupported:
                changed_output.append(dict(change))
            continue
        changed_output.append(dict(change))
        if change.get("production", True) and path not in generated_or_unsupported:
            changed_lines[path] = lines

    diagnostics_args: dict[str, Any] | None = None
    if run_mode == "with_lsp" and changed_lines:
        file_paths = sorted(changed_lines)
        diagnostics_args = normalize_diagnostics_arguments(
            {
                "file_paths": file_paths,
                "diff_filter": {
                    "mode": "changed_lines",
                    "changed_lines": {path: changed_lines[path] for path in file_paths},
                },
            },
            target_files=file_paths,
        )

    # Exempt generated/unsupported files remain direct guard inputs even when
    # they have live new-side lines; the guard removes them from required LSP
    # coverage after validating the exact exemption registry entry.
    guard_changes: list[dict[str, Any]] = []
    for change in changed_output:
        guard_changes.append(dict(change))
    guard_paths = {item["path"] for item in guard_changes}
    exemption_output = tuple(
        dict(item)
        for item in sorted(explicit_exemptions, key=lambda item: item["path"])
        if item["path"] in guard_paths
    )
    return CoveragePlan(
        mode=run_mode,
        changed_files=tuple(guard_changes),
        exemptions=exemption_output,
        diagnostics_args=diagnostics_args,
        all_changed_files=tuple(dict(item) for item in effective),
        deleted_files=tuple(sorted(deleted_files)),
        zero_line_files=tuple(sorted(zero_line_files)),
    )


def build_diagnostics_coverage(*args: Any, **kwargs: Any) -> CoveragePlan:
    """Descriptive alias for :func:`build_coverage`."""

    return build_coverage(*args, **kwargs)


def generate_diagnostics_coverage(*args: Any, **kwargs: Any) -> CoveragePlan:
    """Runner-facing alias for :func:`build_coverage`."""

    return build_coverage(*args, **kwargs)


parse_git_diff = parse_unified_diff
parse_diff = parse_unified_diff
build_plan = build_coverage
generate_coverage = build_coverage


def build_diagnostics_arguments(*args: Any, **kwargs: Any) -> dict[str, Any] | None:
    """Return only the strict MCP arguments, or ``None`` for No-LSP."""

    return build_coverage(*args, **kwargs).diagnostics_args


def _codex_patch_fragment(path: str, kind: str, fragment: str) -> str:
    path = _normalise_path(path, "Codex file_change path")
    kind = kind.strip().lower()
    if kind in {"add", "create", "added"}:
        old, new = _NULL, "b/" + path
    elif kind in {"delete", "remove", "deleted"}:
        old, new = "a/" + path, _NULL
    else:
        old, new = "a/" + path, "b/" + path
    return "\n".join(
        [f"diff --git {old if old == _NULL else 'a/' + path} {new if new == _NULL else 'b/' + path}", f"--- {old}", f"+++ {new}", fragment.rstrip("\n"), ""]
    )


def _normalise_codex_patch(patch: str, file_change: Any) -> str:
    """Accept a Codex ``*** Begin Patch`` envelope or a hunk fragment."""

    if patch.startswith("diff --git ") or not patch.lstrip().startswith("*** Begin Patch"):
        if patch.startswith("@@"):
            if not isinstance(file_change, Mapping):
                raise CoverageError("a hunk-only Codex patch needs file_change metadata")
            path = _first(file_change, "path", "file", "file_path", "new_path", "old_path")
            kind = str(_first(file_change, "kind", "operation", "change_type") or "update")
            return _codex_patch_fragment(_text(path, "Codex file_change path"), kind, patch)
        return patch
    lines = patch.splitlines()
    if not lines or lines[0].strip() != "*** Begin Patch":
        raise DiffParseError("malformed Codex patch envelope")
    output: list[str] = []
    cursor = 1
    while cursor < len(lines):
        line = lines[cursor]
        if line.strip() == "*** End Patch":
            cursor += 1
            break
        match = re.match(r"^\*\*\* (Update|Add|Delete) File: (.+)$", line)
        if match is None:
            raise DiffParseError(f"unknown Codex patch directive: {line!r}")
        operation, raw_path = match.groups()
        path = _normalise_path(raw_path, "Codex patch path")
        cursor += 1
        body: list[str] = []
        while cursor < len(lines) and not lines[cursor].startswith("*** "):
            body.append(lines[cursor])
            cursor += 1
        kind = operation.lower()
        output.append(_codex_patch_fragment(path, kind, "\n".join(body)))
    if cursor > len(lines) or not any(line.strip() == "*** End Patch" for line in lines):
        raise DiffParseError("Codex patch envelope lacks *** End Patch")
    return "".join(output)


def _extract_codex_changes(value: Any) -> list[Any]:
    if isinstance(value, Mapping):
        nested = value.get("changes")
        if isinstance(nested, Sequence) and not isinstance(nested, (str, bytes, bytearray)):
            return list(nested)
        return [value]
    if isinstance(value, Sequence) and not isinstance(value, (str, bytes, bytearray)):
        return list(value)
    return [value]


def from_codex_file_change(
    file_change: Any,
    patch_text: str | bytes | None = None,
    language_support: Any = None,
    exemptions: Iterable[Any] | Mapping[str, Any] | None = None,
    mode: str = "with_lsp",
) -> CoveragePlan:
    """Build coverage from a real Codex ``file_change`` item plus patch text.

    The item may contain ``path``/``kind`` metadata, a ``changes`` list, or a
    patch/diff field.  Passing patch text explicitly is preferred because it
    preserves the exact hunk bytes used by the runner.
    """

    entries = _extract_codex_changes(file_change)
    metadata: list[dict[str, Any]] = []
    patches: list[str] = []
    for entry in entries:
        if not isinstance(entry, Mapping):
            raise CoverageError(f"Codex file_change entry must be an object: {entry!r}")
        raw_path = _first(entry, "path", "file", "file_path", "new_path", "old_path")
        if raw_path is None:
            raise CoverageError("Codex file_change entry has no path")
        item = dict(entry)
        item["path"] = raw_path
        metadata.append(item)
        embedded = _first(entry, "patch", "patch_text", "diff", "unified_diff")
        if embedded is not None:
            if not isinstance(embedded, (str, bytes)):
                raise CoverageError("Codex file_change patch must be text")
            patches.append(embedded.decode("utf-8") if isinstance(embedded, bytes) else embedded)
    if patch_text is not None:
        if isinstance(patch_text, bytes):
            try:
                patch_text = patch_text.decode("utf-8")
            except UnicodeDecodeError as exc:
                raise DiffParseError("binary or non-UTF-8 Codex patch") from exc
        if not isinstance(patch_text, str):
            raise CoverageError("patch_text must be text or UTF-8 bytes")
        patches = [patch_text]
    if not patches:
        raise CoverageError("Codex file_change requires patch text")

    combined = "\n".join(_normalise_codex_patch(text, metadata[0] if len(metadata) == 1 else metadata) for text in patches)
    changed_files = []
    for item in metadata:
        changed_files.append(_coerce_changed_file(item))
    return build_coverage(
        combined,
        changed_files=changed_files,
        language_support=language_support,
        exemptions=exemptions,
        mode=mode,
    )


coverage_from_codex_file_change = from_codex_file_change
build_from_codex_file_change = from_codex_file_change
from_file_change = from_codex_file_change


def _load_json_file(path: str | None, label: str) -> Any:
    if path is None:
        return None
    try:
        return json.loads(pathlib.Path(path).read_text(encoding="utf-8"))
    except OSError as exc:
        raise CoverageError(f"cannot read {label}: {path}") from exc
    except json.JSONDecodeError as exc:
        raise CoverageError(f"invalid {label} JSON: {path}") from exc


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--diff-file", help="UTF-8 git diff; stdin when omitted")
    parser.add_argument("--changed-files", help="JSON list/object of changed files")
    parser.add_argument("--language-support", help="JSON language support registry")
    parser.add_argument("--exemptions", help="JSON exemption registry")
    parser.add_argument("--mode", default="with_lsp", choices=("with_lsp", "no_lsp"))
    args = parser.parse_args(argv)
    try:
        patch = (
            pathlib.Path(args.diff_file).read_bytes()
            if args.diff_file
            else sys.stdin.buffer.read()
        )
        plan = build_coverage(
            patch,
            changed_files=_load_json_file(args.changed_files, "changed files"),
            language_support=_load_json_file(args.language_support, "language support registry"),
            exemptions=_load_json_file(args.exemptions, "exemption registry"),
            mode=args.mode,
        )
        json.dump(plan.to_dict(), sys.stdout, ensure_ascii=False, sort_keys=True)
        sys.stdout.write("\n")
        return 0
    except CoverageError as exc:
        print(f"cross-file diagnostics coverage: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
