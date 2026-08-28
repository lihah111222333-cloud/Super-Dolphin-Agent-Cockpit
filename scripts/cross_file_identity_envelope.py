#!/usr/bin/env python3
"""Fail-closed identity binding for cross-file benchmark evidence.

This module is deliberately independent from the benchmark runner, scorer,
grader-feedback parser, and remote CI.  It records the identities needed to
decide whether two pieces of benchmark evidence belong to the same
experiment.  The envelope is evidence metadata only; it is not a remote-CI
request, lease, receipt, or authority record.

The canonical JSON shape is::

    {
      "schema": "super-dolphin/benchmark-identity-envelope/v1",
      "authority": "BENCHMARK_EVIDENCE_ONLY",
      "bindings": {
        "manifest": {"id": "...", "sha256": "sha256:<64 hex>"},
        "dataset": {"id": "...", "sha256": "sha256:<64 hex>"},
        "task": {"id": "...", "sha256": "sha256:<64 hex>"},
        "base": {
          "commit_sha": "<40 or 64 hex>",
          "tree_sha": "<40 or 64 hex>",
          "sha256": "sha256:<64 hex>"
        },
        "source_image": {"id": "...", "sha256": "sha256:<64 hex>"},
        "toolchain": {"id": "...", "sha256": "sha256:<64 hex>"},
        "grader": {"id": "...", "sha256": "sha256:<64 hex>"},
        "runner": {"id": "...", "sha256": "sha256:<64 hex>"},
        "model": {"id": "...", "sha256": "sha256:<64 hex>"},
        "prompt": {"id": "...", "sha256": "sha256:<64 hex>"},
        "pricing": {"id": "...", "sha256": "sha256:<64 hex>"},
        "scoring": {"id": "...", "sha256": "sha256:<64 hex>"},
        "session": {"id": "...", "sha256": "sha256:<64 hex>"},
        "thread": {"id": "...", "sha256": "sha256:<64 hex>"},
        "agent": {"id": "...", "sha256": "sha256:<64 hex>"},
        "patch": {"id": "...", "sha256": "sha256:<64 hex>"},
        "final_tree": {
          "tree_sha": "<40 or 64 hex>",
          "sha256": "sha256:<64 hex>"
        }
      },
      "binding_sha256": "sha256:<64 hex>"
    }

Every component is required.  Unknown fields, missing fields, malformed
hashes, authority drift, and a stale ``binding_sha256`` are hard failures.
"""

from __future__ import annotations

import dataclasses
import base64
import hashlib
import json
import pathlib
import re
from collections.abc import Mapping
from typing import Any


SCHEMA = "super-dolphin/benchmark-identity-envelope/v1"
BENCHMARK_EVIDENCE_AUTHORITY = "BENCHMARK_EVIDENCE_ONLY"
MAX_ENVELOPE_BYTES = 1 * 1024 * 1024

_SHA256_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
_GIT_OBJECT_RE = re.compile(r"^[0-9a-f]{40}(?:[0-9a-f]{24})?$")
_REMOTE_CI_FIELDS = frozenset(
    {
        "authority_generation",
        "executor_binding_digest",
        "executor_kind",
        "lease_id",
        "node_id",
        "node_set_digest",
        "operation_id",
        "receipt_id",
        "remote_ci_authority",
        "reservation_id",
    }
)
_SIGNING_KEY_FIELDS = frozenset({"algorithm", "key_id", "public_key"})
_ED25519_PUBLIC_KEY_BYTES = 32


class IdentityEnvelopeError(ValueError):
    """Base class for malformed or untrustworthy benchmark identity data."""


class UnknownFieldError(IdentityEnvelopeError):
    """An envelope object contains a field outside its strict schema."""


class MissingFieldError(IdentityEnvelopeError):
    """A required envelope field is missing or empty."""


class IdentityDriftError(IdentityEnvelopeError):
    """A digest or cross-envelope identity comparison does not match."""


class AuthorityBoundaryError(IdentityEnvelopeError):
    """The caller attempted to use benchmark evidence as remote authority."""


@dataclasses.dataclass(frozen=True)
class Binding:
    """A named benchmark identity and its content/definition digest."""

    id: str
    sha256: str


@dataclasses.dataclass(frozen=True)
class BaseBinding:
    """The exact base commit/tree used to construct a benchmark task."""

    commit_sha: str
    tree_sha: str
    sha256: str


@dataclasses.dataclass(frozen=True)
class FinalTreeBinding:
    """The exact final Git tree represented by a candidate patch."""

    tree_sha: str
    sha256: str


@dataclasses.dataclass(frozen=True)
class ReceiptSigningKeyBinding:
    """Public Ed25519 key pinned by the benchmark identity envelope."""

    key_id: str
    public_key: str
    algorithm: str = "Ed25519"

    def to_dict(self) -> dict[str, str]:
        return {
            "algorithm": self.algorithm,
            "key_id": self.key_id,
            "public_key": self.public_key,
        }


@dataclasses.dataclass(frozen=True)
class IdentityBindings:
    """All identities that must agree before evidence can be compared."""

    manifest: Binding
    dataset: Binding
    task: Binding
    base: BaseBinding
    source_image: Binding
    toolchain: Binding
    grader: Binding
    runner: Binding
    model: Binding
    prompt: Binding
    pricing: Binding
    scoring: Binding
    session: Binding
    thread: Binding
    agent: Binding
    patch: Binding
    final_tree: FinalTreeBinding

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "IdentityBindings":
        """Parse the component mapping through the same strict schema path."""

        return _parse_bindings(value)

    def to_dict(self) -> dict[str, Any]:
        """Return the canonical JSON-compatible component mapping."""

        return dataclasses.asdict(self)


@dataclasses.dataclass(frozen=True)
class IdentityEnvelope:
    """Parsed, validated benchmark evidence identity."""

    bindings: IdentityBindings
    binding_sha256: str
    schema: str = SCHEMA
    authority: str = BENCHMARK_EVIDENCE_AUTHORITY
    receipt_signing_key: ReceiptSigningKeyBinding | None = None
    grader_receipt_signing_key: ReceiptSigningKeyBinding | None = None

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "IdentityEnvelope":
        """Parse a JSON object and verify its binding digest."""

        return parse_identity_envelope(value)

    @property
    def identity_hash(self) -> str:
        """Compatibility name for the envelope's domain-separated digest."""

        return self.binding_sha256

    def to_dict(self) -> dict[str, Any]:
        """Return the canonical envelope object without mutating its source."""

        result = {
            "schema": self.schema,
            "authority": self.authority,
            "bindings": self.bindings.to_dict(),
            "binding_sha256": self.binding_sha256,
        }
        if self.receipt_signing_key is not None:
            result["receipt_signing_key"] = self.receipt_signing_key.to_dict()
        if self.grader_receipt_signing_key is not None:
            result["grader_receipt_signing_key"] = self.grader_receipt_signing_key.to_dict()
        return result


def _field_names(dataclass_type: type[Any]) -> frozenset[str]:
    """Derive the allowed field set from the schema owner, not a duplicate list."""

    return frozenset(field.name for field in dataclasses.fields(dataclass_type))


# Public schema inventory for consumers and field-guard tests.  It is derived
# from the dataclass that owns the production fields, so adding a component
# without updating the parser cannot silently create an unguarded field.
IDENTITY_COMPONENTS = tuple(field.name for field in dataclasses.fields(IdentityBindings))


def _reject_unknown(value: Mapping[str, Any], allowed: frozenset[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise UnknownFieldError(f"{label} has unknown field(s): {', '.join(unknown)}")


def _required_text(value: Any, label: str) -> str:
    if not isinstance(value, str):
        raise MissingFieldError(f"{label} must be a non-empty string")
    if not value or value.strip() != value:
        raise MissingFieldError(f"{label} must be a canonical non-empty string")
    return value


def _required_digest(value: Any, label: str) -> str:
    text = _required_text(value, label)
    if not _SHA256_RE.fullmatch(text):
        raise IdentityEnvelopeError(f"{label} must be sha256:<64 lowercase hex>")
    return text


def _required_git_hash(value: Any, label: str) -> str:
    text = _required_text(value, label)
    if not _GIT_OBJECT_RE.fullmatch(text):
        raise IdentityEnvelopeError(f"{label} must be a 40- or 64-character lowercase Git hash")
    return text


def _as_mapping(value: Any, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise MissingFieldError(f"{label} must be an object")
    return value


def _required_keys(value: Mapping[str, Any], required: frozenset[str], label: str) -> None:
    missing = sorted(required - set(value))
    if missing:
        raise MissingFieldError(f"{label} missing field(s): {', '.join(missing)}")


def _parse_binding(value: Any, label: str) -> Binding:
    mapping = _as_mapping(value, label)
    allowed = _field_names(Binding)
    _reject_unknown(mapping, allowed, label)
    _required_keys(mapping, allowed, label)
    return Binding(
        id=_required_text(mapping["id"], f"{label}.id"),
        sha256=_required_digest(mapping["sha256"], f"{label}.sha256"),
    )


def _parse_base(value: Any) -> BaseBinding:
    label = "bindings.base"
    mapping = _as_mapping(value, label)
    allowed = _field_names(BaseBinding)
    _reject_unknown(mapping, allowed, label)
    _required_keys(mapping, allowed, label)
    return BaseBinding(
        commit_sha=_required_git_hash(mapping["commit_sha"], f"{label}.commit_sha"),
        tree_sha=_required_git_hash(mapping["tree_sha"], f"{label}.tree_sha"),
        sha256=_required_digest(mapping["sha256"], f"{label}.sha256"),
    )


def _parse_final_tree(value: Any) -> FinalTreeBinding:
    label = "bindings.final_tree"
    mapping = _as_mapping(value, label)
    allowed = _field_names(FinalTreeBinding)
    _reject_unknown(mapping, allowed, label)
    _required_keys(mapping, allowed, label)
    return FinalTreeBinding(
        tree_sha=_required_git_hash(mapping["tree_sha"], f"{label}.tree_sha"),
        sha256=_required_digest(mapping["sha256"], f"{label}.sha256"),
    )


def _parse_receipt_signing_key(value: Any) -> ReceiptSigningKeyBinding:
    """Parse the public key that signs remote evidence receipts."""

    label = "receipt_signing_key"
    mapping = _as_mapping(value, label)
    _reject_unknown(mapping, _SIGNING_KEY_FIELDS, label)
    _required_keys(mapping, _SIGNING_KEY_FIELDS, label)
    algorithm = _required_text(mapping["algorithm"], f"{label}.algorithm")
    if algorithm != "Ed25519":
        raise IdentityEnvelopeError(f"unsupported {label}.algorithm: {algorithm!r}")
    key_id = _required_text(mapping["key_id"], f"{label}.key_id")
    public_key = _required_text(mapping["public_key"], f"{label}.public_key")
    try:
        decoded = base64.b64decode(public_key.encode("ascii"), validate=True)
    except (UnicodeEncodeError, ValueError) as exc:
        raise IdentityEnvelopeError(f"{label}.public_key must be base64") from exc
    if len(decoded) != _ED25519_PUBLIC_KEY_BYTES:
        raise IdentityEnvelopeError(
            f"{label}.public_key must decode to {_ED25519_PUBLIC_KEY_BYTES} bytes"
        )
    return ReceiptSigningKeyBinding(
        key_id=key_id,
        public_key=public_key,
        algorithm=algorithm,
    )


def _parse_bindings(value: Any) -> IdentityBindings:
    mapping = _as_mapping(value, "bindings")
    allowed = _field_names(IdentityBindings)
    _reject_unknown(mapping, allowed, "bindings")
    _required_keys(mapping, allowed, "bindings")
    parsed: dict[str, Any] = {}
    for field in dataclasses.fields(IdentityBindings):
        label = f"bindings.{field.name}"
        if field.name == "base":
            parsed[field.name] = _parse_base(mapping[field.name])
        elif field.name == "final_tree":
            parsed[field.name] = _parse_final_tree(mapping[field.name])
        else:
            parsed[field.name] = _parse_binding(mapping[field.name], label)
    return IdentityBindings(**parsed)


def _canonical_json(value: Any) -> str:
    try:
        return json.dumps(
            value,
            ensure_ascii=False,
            allow_nan=False,
            sort_keys=True,
            separators=(",", ":"),
        )
    except (TypeError, ValueError) as exc:
        raise IdentityEnvelopeError(f"identity value is not canonical JSON: {exc}") from exc


def _reject_duplicate_json_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    """Reject duplicate object keys instead of letting JSON parsing overwrite them."""

    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise IdentityEnvelopeError(f"duplicate JSON field: {key}")
        result[key] = value
    return result


def canonical_json(value: Mapping[str, Any]) -> str:
    """Serialize an evidence object deterministically for hashing or storage."""

    return _canonical_json(value)


def sha256_bytes(value: bytes) -> str:
    """Return a canonical prefixed SHA-256 digest."""

    return "sha256:" + hashlib.sha256(value).hexdigest()


def sha256_text(value: str) -> str:
    """Hash UTF-8 text using the same canonical prefix as envelope fields."""

    if not isinstance(value, str):
        raise IdentityEnvelopeError("sha256_text requires text")
    return sha256_bytes(value.encode("utf-8"))


def _binding_material(
    schema: str,
    authority: str,
    bindings: IdentityBindings,
    receipt_signing_key: ReceiptSigningKeyBinding | None = None,
    grader_receipt_signing_key: ReceiptSigningKeyBinding | None = None,
) -> dict[str, Any]:
    """Build the domain-separated material covered by ``binding_sha256``."""

    material = {
        "domain": SCHEMA,
        "schema": schema,
        "authority": authority,
        "bindings": bindings.to_dict(),
    }
    if receipt_signing_key is not None:
        material["receipt_signing_key"] = receipt_signing_key.to_dict()
    if grader_receipt_signing_key is not None:
        material["grader_receipt_signing_key"] = grader_receipt_signing_key.to_dict()
    return material


def _compute_binding_sha256(
    schema: str,
    authority: str,
    bindings: IdentityBindings,
    receipt_signing_key: ReceiptSigningKeyBinding | None = None,
    grader_receipt_signing_key: ReceiptSigningKeyBinding | None = None,
) -> str:
    return sha256_text(
        _canonical_json(
            _binding_material(
                schema, authority, bindings, receipt_signing_key, grader_receipt_signing_key
            )
        )
    )


def build_identity_envelope(
    bindings: Mapping[str, Any] | IdentityBindings,
    *,
    receipt_signing_key: Mapping[str, Any] | ReceiptSigningKeyBinding | None = None,
    grader_receipt_signing_key: Mapping[str, Any] | ReceiptSigningKeyBinding | None = None,
) -> dict[str, Any]:
    """Validate components and return a fully bound benchmark evidence envelope."""

    parsed = (
        _parse_bindings(bindings.to_dict())
        if isinstance(bindings, IdentityBindings)
        else _parse_bindings(bindings)
    )
    parsed_key = (
        _parse_receipt_signing_key(receipt_signing_key.to_dict())
        if isinstance(receipt_signing_key, ReceiptSigningKeyBinding)
        else None
        if receipt_signing_key is None
        else _parse_receipt_signing_key(receipt_signing_key)
    )
    parsed_grader_key = (
        _parse_receipt_signing_key(grader_receipt_signing_key.to_dict())
        if isinstance(grader_receipt_signing_key, ReceiptSigningKeyBinding)
        else None
        if grader_receipt_signing_key is None
        else _parse_receipt_signing_key(grader_receipt_signing_key)
    )
    digest = _compute_binding_sha256(
        SCHEMA,
        BENCHMARK_EVIDENCE_AUTHORITY,
        parsed,
        parsed_key,
        parsed_grader_key,
    )
    return IdentityEnvelope(
        bindings=parsed,
        binding_sha256=digest,
        receipt_signing_key=parsed_key,
        grader_receipt_signing_key=parsed_grader_key,
    ).to_dict()


make_identity_envelope = build_identity_envelope


def parse_identity_envelope(value: Mapping[str, Any] | IdentityEnvelope | str) -> IdentityEnvelope:
    """Parse and verify one canonical envelope, failing on any drift."""

    if isinstance(value, IdentityEnvelope):
        # Do not trust a directly constructed dataclass to bypass schema or
        # digest validation; re-enter the canonical mapping parser.
        value = value.to_dict()
    if isinstance(value, str):
        try:
            value = json.loads(value, object_pairs_hook=_reject_duplicate_json_keys)
        except json.JSONDecodeError as exc:
            raise IdentityEnvelopeError(f"identity envelope is not JSON: {exc}") from exc
    mapping = _as_mapping(value, "identity envelope")
    if _REMOTE_CI_FIELDS.intersection(mapping):
        fields = ", ".join(sorted(_REMOTE_CI_FIELDS.intersection(mapping)))
        raise AuthorityBoundaryError(f"benchmark evidence cannot carry remote-CI field(s): {fields}")
    allowed = frozenset({field.name for field in dataclasses.fields(IdentityEnvelope)})
    _reject_unknown(mapping, allowed, "identity envelope")
    required = frozenset({"schema", "authority", "bindings", "binding_sha256"})
    _required_keys(mapping, required, "identity envelope")
    schema = _required_text(mapping["schema"], "schema")
    if schema != SCHEMA:
        raise IdentityEnvelopeError(f"unsupported identity envelope schema: {schema!r}")
    authority = _required_text(mapping["authority"], "authority")
    if authority != BENCHMARK_EVIDENCE_AUTHORITY:
        raise AuthorityBoundaryError(
            "identity envelope authority must be BENCHMARK_EVIDENCE_ONLY"
        )
    bindings = _parse_bindings(mapping["bindings"])
    receipt_signing_key = (
        _parse_receipt_signing_key(mapping["receipt_signing_key"])
        if "receipt_signing_key" in mapping
        else None
    )
    grader_receipt_signing_key = (
        _parse_receipt_signing_key(mapping["grader_receipt_signing_key"])
        if "grader_receipt_signing_key" in mapping
        else None
    )
    supplied_digest = _required_digest(mapping["binding_sha256"], "binding_sha256")
    expected_digest = _compute_binding_sha256(
        schema,
        authority,
        bindings,
        receipt_signing_key,
        grader_receipt_signing_key,
    )
    if supplied_digest != expected_digest:
        raise IdentityDriftError(
            f"binding_sha256 drifted: supplied {supplied_digest}, expected {expected_digest}"
        )
    return IdentityEnvelope(
        bindings=bindings,
        binding_sha256=supplied_digest,
        schema=schema,
        authority=authority,
        receipt_signing_key=receipt_signing_key,
        grader_receipt_signing_key=grader_receipt_signing_key,
    )


def load_identity_envelope(path: str | pathlib.Path) -> IdentityEnvelope:
    """Load one bounded JSON envelope from disk and verify it."""

    candidate = pathlib.Path(path)
    try:
        payload = candidate.read_bytes()
    except OSError as exc:
        raise IdentityEnvelopeError(f"read identity envelope {candidate}: {exc}") from exc
    if len(payload) > MAX_ENVELOPE_BYTES:
        raise IdentityEnvelopeError("identity envelope exceeds the size limit")
    try:
        value = json.loads(
            payload.decode("utf-8"), object_pairs_hook=_reject_duplicate_json_keys
        )
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise IdentityEnvelopeError(f"identity envelope is not UTF-8 JSON: {exc}") from exc
    return parse_identity_envelope(value)


def verify_identity_envelope(
    actual: Mapping[str, Any] | IdentityEnvelope | str,
    expected: Mapping[str, Any] | IdentityEnvelope | str,
) -> IdentityEnvelope:
    """Verify that two evidence envelopes carry exactly the same identity."""

    actual_envelope = parse_identity_envelope(actual)
    expected_envelope = parse_identity_envelope(expected)
    if actual_envelope.to_dict() != expected_envelope.to_dict():
        actual_bindings = actual_envelope.bindings.to_dict()
        expected_bindings = expected_envelope.bindings.to_dict()
        for field in dataclasses.fields(IdentityBindings):
            name = field.name
            if actual_bindings[name] != expected_bindings[name]:
                raise IdentityDriftError(f"identity drifted at bindings.{name}")
        raise IdentityDriftError("identity envelope metadata drifted")
    return actual_envelope


assert_identity_bound = verify_identity_envelope


__all__ = [
    "AuthorityBoundaryError",
    "BENCHMARK_EVIDENCE_AUTHORITY",
    "BaseBinding",
    "Binding",
    "IdentityBindings",
    "IDENTITY_COMPONENTS",
    "IdentityDriftError",
    "IdentityEnvelope",
    "IdentityEnvelopeError",
    "FinalTreeBinding",
    "MissingFieldError",
    "ReceiptSigningKeyBinding",
    "SCHEMA",
    "UnknownFieldError",
    "assert_identity_bound",
    "build_identity_envelope",
    "canonical_json",
    "load_identity_envelope",
    "make_identity_envelope",
    "parse_identity_envelope",
    "sha256_bytes",
    "sha256_text",
    "verify_identity_envelope",
]
