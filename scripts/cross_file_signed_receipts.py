#!/usr/bin/env python3
"""Ed25519 signatures for remote benchmark receipts.

The remote Ubuntu driver signs the complete receipt payload.  The signature
block is deliberately kept outside that payload so a signature cannot become
a self-hash of JSON.  Verification is pinned to the public key carried by the
identity envelope; a key supplied by the receipt itself is never trusted.
"""

from __future__ import annotations

import base64
import binascii
import dataclasses
import hashlib
import json
import pathlib
import re
from collections.abc import Mapping
from typing import Any

try:
    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.asymmetric.ed25519 import (
        Ed25519PrivateKey,
        Ed25519PublicKey,
    )
except ImportError as exc:  # pragma: no cover - exercised on unsupported hosts.
    serialization = None  # type: ignore[assignment]
    Ed25519PrivateKey = None  # type: ignore[assignment,misc]
    Ed25519PublicKey = None  # type: ignore[assignment,misc]
    _CRYPTOGRAPHY_IMPORT_ERROR: ImportError | None = exc
else:
    _CRYPTOGRAPHY_IMPORT_ERROR = None


SCHEMA = "super-dolphin/remote-signed-receipt/v1"
ALGORITHM = "Ed25519"
MCP_RECEIPT_KIND = "mcp_server_receipt"
NO_LSP_ISOLATION_KIND = "no_lsp_isolation"
GRADER_RECEIPT_KIND = "grader_receipt"
SIGNATURE_FIELD = "signature"
SIGNATURE_FIELDS = frozenset(
    {"schema", "algorithm", "kind", "issuer", "public_key_id", "signature"}
)
PUBLIC_KEY_FIELDS = frozenset({"algorithm", "key_id", "public_key"})
ARTIFACT_FIELDS = frozenset({"id", "sha256"})
GRADER_RECEIPT_SCHEMA = "super-dolphin/grader-receipt/v1"
GRADER_RECEIPT_FIELDS = frozenset(
    {
        "schema",
        "task",
        "arm",
        "session",
        "thread",
        "seq",
        "request_id",
        "patch_hash",
        "grader_artifact",
        "final_tree_hash",
        "result",
        "signature",
    }
)
_GRADER_RECEIPT_REQUIRED_FIELDS = GRADER_RECEIPT_FIELDS - {"final_tree_hash"}
_SHA256_RE = re.compile(r"^(?:sha256:)?[0-9a-f]{64}$")
_TREE_HASH_RE = re.compile(r"^[0-9a-f]{40}(?:[0-9a-f]{24})?$")
_BASE64_RE = re.compile(r"^[A-Za-z0-9+/]+={0,2}$")


class SignedReceiptError(ValueError):
    """Base class for malformed, untrusted, or unverifiable receipts."""


class SigningKeyError(SignedReceiptError):
    """A signing key or explicit key path is invalid."""


class SignatureSchemaError(SignedReceiptError):
    """A signature block or public-key binding violates its schema."""


class SignatureVerificationError(SignedReceiptError):
    """An Ed25519 signature does not verify against the pinned key."""


class ReceiptReplayError(SignedReceiptError):
    """A receipt request or signature was observed more than once."""


def _required_text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise SignedReceiptError(f"{label} must be a non-empty string")
    return value.strip()


def _positive_int(value: Any, label: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise SignedReceiptError(f"{label} must be a positive integer")
    return value


def _reject_unknown(value: Mapping[str, Any], allowed: frozenset[str], label: str) -> None:
    unknown = sorted(str(key) for key in value if key not in allowed)
    if unknown:
        raise SignatureSchemaError(f"{label} has unknown field(s): {', '.join(unknown)}")


def canonical_json(value: Any) -> str:
    """Serialize signed material without permitting NaN or unstable spacing."""

    try:
        return json.dumps(
            value,
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
            allow_nan=False,
        )
    except (TypeError, ValueError) as exc:
        raise SignedReceiptError(f"signed receipt payload is not canonical JSON: {exc}") from exc


def _crypto_available() -> None:
    if _CRYPTOGRAPHY_IMPORT_ERROR is not None:
        raise SigningKeyError(
            "cryptography with Ed25519 support is required for signed receipts"
        ) from _CRYPTOGRAPHY_IMPORT_ERROR


def _encode_base64(value: bytes) -> str:
    return base64.b64encode(value).decode("ascii")


def _decode_base64(value: Any, label: str, *, expected_size: int) -> bytes:
    text = _required_text(value, label)
    if _BASE64_RE.fullmatch(text) is None:
        raise SignatureSchemaError(f"{label} must be standard base64")
    try:
        decoded = base64.b64decode(text.encode("ascii"), validate=True)
    except (ValueError, UnicodeEncodeError, binascii.Error) as exc:
        raise SignatureSchemaError(f"{label} is not valid base64") from exc
    if len(decoded) != expected_size:
        raise SignatureSchemaError(f"{label} must decode to {expected_size} bytes")
    return decoded


def _validate_digest(value: Any, label: str) -> str:
    text = _required_text(value, label)
    if _SHA256_RE.fullmatch(text) is None:
        raise SignedReceiptError(f"{label} must be a lowercase SHA-256 digest")
    return text


def _validate_tree_hash(value: Any, label: str) -> str:
    text = _required_text(value, label)
    if _TREE_HASH_RE.fullmatch(text) is None:
        raise SignedReceiptError(f"{label} must be a lowercase 40- or 64-character Git tree hash")
    return text


def canonical_artifact(value: Any, label: str = "grader_artifact") -> dict[str, str]:
    """Normalize the artifact binding that is covered by the signature."""

    if not isinstance(value, Mapping):
        raise SignedReceiptError(f"{label} must be an object")
    _reject_unknown(value, ARTIFACT_FIELDS, label)
    missing = sorted(ARTIFACT_FIELDS - set(value))
    if missing:
        raise SignedReceiptError(f"{label} missing field(s): {', '.join(missing)}")
    return {
        "id": _required_text(value["id"], f"{label}.id"),
        "sha256": _validate_digest(value["sha256"], f"{label}.sha256"),
    }


@dataclasses.dataclass(frozen=True)
class PublicKeyBinding:
    """Public key material copied into an identity envelope."""

    key_id: str
    public_key: str
    algorithm: str = ALGORITHM

    def __post_init__(self) -> None:
        key_id = _required_text(self.key_id, "public key key_id")
        algorithm = _required_text(self.algorithm, "public key algorithm")
        if algorithm != ALGORITHM:
            raise SignatureSchemaError(f"unsupported public key algorithm: {algorithm!r}")
        _decode_base64(self.public_key, "public key public_key", expected_size=32)
        object.__setattr__(self, "key_id", key_id)
        object.__setattr__(self, "algorithm", algorithm)

    def to_dict(self) -> dict[str, str]:
        return {
            "algorithm": self.algorithm,
            "key_id": self.key_id,
            "public_key": self.public_key,
        }

    @classmethod
    def from_mapping(cls, value: Mapping[str, Any]) -> "PublicKeyBinding":
        if not isinstance(value, Mapping):
            raise SignatureSchemaError("public key binding must be an object")
        _reject_unknown(value, PUBLIC_KEY_FIELDS, "public key binding")
        missing = sorted(PUBLIC_KEY_FIELDS - set(value))
        if missing:
            raise SignatureSchemaError(
                "public key binding missing field(s): " + ", ".join(missing)
            )
        return cls(
            key_id=value["key_id"],
            public_key=value["public_key"],
            algorithm=value["algorithm"],
        )


class Ed25519Verifier:
    """Verify signatures against one identity-pinned Ed25519 public key."""

    def __init__(self, binding: PublicKeyBinding | Mapping[str, Any]):
        _crypto_available()
        raw_binding = binding.to_dict() if isinstance(binding, PublicKeyBinding) else binding
        self.binding = PublicKeyBinding.from_mapping(raw_binding)
        assert Ed25519PublicKey is not None
        public_bytes = _decode_base64(
            self.binding.public_key,
            "public key public_key",
            expected_size=32,
        )
        self._public_key = Ed25519PublicKey.from_public_bytes(public_bytes)

    @classmethod
    def from_mapping(cls, value: Mapping[str, Any]) -> "Ed25519Verifier":
        return cls(PublicKeyBinding.from_mapping(value))

    @classmethod
    def from_identity_envelope(cls, value: Any) -> "Ed25519Verifier":
        if hasattr(value, "to_dict"):
            value = value.to_dict()
        if not isinstance(value, Mapping):
            raise SignatureSchemaError("identity envelope must be an object")
        binding = value.get("receipt_signing_key")
        if binding is None and isinstance(value.get("bindings"), Mapping):
            binding = value["bindings"].get("receipt_signing_key")
        if binding is None:
            raise SignatureSchemaError(
                "identity envelope is missing receipt_signing_key public key"
            )
        return cls.from_mapping(binding)

    def verify(self, material: bytes, signature: bytes) -> None:
        try:
            self._public_key.verify(signature, material)
        except Exception as exc:  # cryptography raises InvalidSignature.
            raise SignatureVerificationError("Ed25519 signature verification failed") from exc


class Ed25519Signer:
    """Sign receipts without exposing private key bytes in repr or output."""

    def __init__(self, key_id: str, private_key: Any):
        _crypto_available()
        key_id = _required_text(key_id, "signing key key_id")
        if Ed25519PrivateKey is None or not isinstance(private_key, Ed25519PrivateKey):
            raise SigningKeyError("private_key must be an Ed25519 private key")
        self._key_id = key_id
        self._private_key = private_key

    def __repr__(self) -> str:
        return f"Ed25519Signer(key_id={self._key_id!r})"

    @classmethod
    def generate(cls, key_id: str) -> "Ed25519Signer":
        _crypto_available()
        assert Ed25519PrivateKey is not None
        return cls(key_id, Ed25519PrivateKey.generate())

    @classmethod
    def from_private_bytes(cls, key_id: str, value: bytes) -> "Ed25519Signer":
        _crypto_available()
        if not isinstance(value, bytes) or len(value) != 32:
            raise SigningKeyError("Ed25519 private key bytes must be exactly 32 bytes")
        assert Ed25519PrivateKey is not None
        return cls(key_id, Ed25519PrivateKey.from_private_bytes(value))

    @classmethod
    def from_file(cls, path: str | pathlib.Path, *, key_id: str) -> "Ed25519Signer":
        """Load only an explicitly supplied absolute private-key path."""

        _crypto_available()
        if not isinstance(path, (str, pathlib.Path)) or not str(path).strip():
            raise SigningKeyError("an explicit private key path is required")
        candidate = pathlib.Path(path)
        if not candidate.is_absolute():
            raise SigningKeyError("private key path must be absolute and explicit")
        try:
            data = candidate.read_bytes()
        except OSError as exc:
            raise SigningKeyError(f"read private key failed: {candidate}") from exc
        if len(data) > 16 * 1024:
            raise SigningKeyError("private key file exceeds the size limit")
        assert Ed25519PrivateKey is not None
        private_key: Any
        if data.lstrip().startswith(b"-----BEGIN"):
            if serialization is None:
                raise SigningKeyError("PEM private keys require cryptography serialization")
            try:
                private_key = serialization.load_pem_private_key(
                    data,
                    password=None,
                )
            except Exception as exc:
                raise SigningKeyError("private key PEM is invalid or encrypted") from exc
        else:
            raw = data.strip()
            if len(raw) != 32:
                try:
                    raw = base64.b64decode(raw, validate=True)
                except (ValueError, UnicodeDecodeError, binascii.Error) as exc:
                    raise SigningKeyError("private key must be raw 32-byte or base64 seed") from exc
            if len(raw) != 32:
                raise SigningKeyError("private key seed must decode to 32 bytes")
            private_key = Ed25519PrivateKey.from_private_bytes(raw)
        return cls(key_id, private_key)

    @property
    def key_id(self) -> str:
        return self._key_id

    @property
    def public_key(self) -> PublicKeyBinding:
        assert serialization is not None
        raw = self._private_key.public_key().public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw,
        )
        return PublicKeyBinding(key_id=self._key_id, public_key=_encode_base64(raw))

    def sign(self, material: bytes) -> bytes:
        return self._private_key.sign(material)


def signed_material(
    payload: Any,
    *,
    kind: str,
    issuer: str,
    public_key_id: str,
) -> bytes:
    """Build the exact bytes signed by Ed25519; signature is never included."""

    envelope = {
        "schema": SCHEMA,
        "kind": _required_text(kind, "signed receipt kind"),
        "issuer": _required_text(issuer, "signed receipt issuer"),
        "public_key_id": _required_text(public_key_id, "signed receipt public_key_id"),
        "payload": payload,
    }
    return canonical_json(envelope).encode("utf-8")


def sign_payload(
    payload: Any,
    *,
    signer: Ed25519Signer,
    kind: str,
    issuer: str,
) -> dict[str, str]:
    """Return a signature block over the complete payload."""

    if not isinstance(signer, Ed25519Signer):
        raise SigningKeyError("signer must be Ed25519Signer")
    issuer = _required_text(issuer, "signed receipt issuer")
    return {
        "schema": SCHEMA,
        "algorithm": ALGORITHM,
        "kind": _required_text(kind, "signed receipt kind"),
        "issuer": issuer,
        "public_key_id": signer.key_id,
        "signature": _encode_base64(
            signer.sign(
                signed_material(
                    payload,
                    kind=kind,
                    issuer=issuer,
                    public_key_id=signer.key_id,
                )
            )
        ),
    }


def validate_signature_block(
    value: Mapping[str, Any],
    *,
    verifier: Ed25519Verifier,
    payload: Any,
    kind: str,
    issuer: str,
) -> dict[str, str]:
    """Validate schema, key pinning, and cryptographic signature."""

    if not isinstance(verifier, Ed25519Verifier):
        raise SigningKeyError("verifier must be Ed25519Verifier")
    if not isinstance(value, Mapping):
        raise SignatureSchemaError("signed receipt signature must be an object")
    _reject_unknown(value, SIGNATURE_FIELDS, "signed receipt signature")
    missing = sorted(SIGNATURE_FIELDS - set(value))
    if missing:
        raise SignatureSchemaError(
            "signed receipt signature missing field(s): " + ", ".join(missing)
        )
    if value["schema"] != SCHEMA or value["algorithm"] != ALGORITHM:
        raise SignatureSchemaError("unsupported signed receipt signature schema/algorithm")
    expected_kind = _required_text(kind, "expected signed receipt kind")
    expected_issuer = _required_text(issuer, "expected signed receipt issuer")
    actual_kind = _required_text(value["kind"], "signed receipt signature kind")
    actual_issuer = _required_text(value["issuer"], "signed receipt signature issuer")
    if actual_kind != expected_kind or actual_issuer != expected_issuer:
        raise SignatureVerificationError("signed receipt kind or issuer drifted")
    key_id = _required_text(value["public_key_id"], "signed receipt public_key_id")
    if key_id != verifier.binding.key_id:
        raise SignatureVerificationError("signed receipt public_key_id is not identity-pinned")
    signature = _decode_base64(value["signature"], "signed receipt signature", expected_size=64)
    verifier.verify(
        signed_material(
            payload,
            kind=actual_kind,
            issuer=actual_issuer,
            public_key_id=key_id,
        ),
        signature,
    )
    return {
        "schema": SCHEMA,
        "algorithm": ALGORITHM,
        "kind": actual_kind,
        "issuer": actual_issuer,
        "public_key_id": key_id,
        "signature": value["signature"],
    }


def attach_signature(
    payload: Mapping[str, Any],
    *,
    signer: Ed25519Signer,
    kind: str,
    issuer: str,
) -> dict[str, Any]:
    """Copy a payload and append one signature block without mutating input."""

    if not isinstance(payload, Mapping):
        raise SignedReceiptError("signed receipt payload must be an object")
    if SIGNATURE_FIELD in payload:
        raise SignatureSchemaError("signed receipt payload already contains signature")
    result = dict(payload)
    result[SIGNATURE_FIELD] = sign_payload(
        result,
        signer=signer,
        kind=kind,
        issuer=issuer,
    )
    return result


def verify_signed_record(
    value: Mapping[str, Any],
    *,
    verifier: Ed25519Verifier,
    kind: str,
    issuer: str,
) -> dict[str, Any]:
    """Verify a record's signature over every field except ``signature``."""

    if not isinstance(value, Mapping):
        raise SignedReceiptError("signed receipt record must be an object")
    if SIGNATURE_FIELD not in value:
        raise SignatureSchemaError("signed receipt record requires signature")
    payload = {key: item for key, item in value.items() if key != SIGNATURE_FIELD}
    validate_signature_block(
        value[SIGNATURE_FIELD],
        verifier=verifier,
        payload=payload,
        kind=kind,
        issuer=issuer,
    )
    return payload


def sign_grader_receipt(
    result: Mapping[str, Any],
    *,
    task: str,
    arm: str,
    session: str,
    thread: str,
    seq: int,
    request_id: str,
    patch_hash: str,
    grader_artifact: Mapping[str, Any],
    final_tree_hash: str | None = None,
    signer: Ed25519Signer,
    issuer: str = "grader",
) -> dict[str, Any]:
    """Create a signed grader result with immutable candidate/artifact context."""

    if not isinstance(result, Mapping):
        raise SignedReceiptError("grader receipt result must be an object")
    payload = {
        "schema": GRADER_RECEIPT_SCHEMA,
        "task": _required_text(task, "grader receipt task"),
        "arm": _required_text(arm, "grader receipt arm"),
        "session": _required_text(session, "grader receipt session"),
        "thread": _required_text(thread, "grader receipt thread"),
        "seq": _positive_int(seq, "grader receipt seq"),
        "request_id": _required_text(request_id, "grader receipt request_id"),
        "patch_hash": _validate_digest(patch_hash, "grader receipt patch_hash"),
        "grader_artifact": canonical_artifact(grader_artifact),
        "result": dict(result),
    }
    if final_tree_hash is not None:
        payload["final_tree_hash"] = _validate_tree_hash(
            final_tree_hash,
            "grader receipt final_tree_hash",
        )
    return attach_signature(
        payload,
        signer=signer,
        kind=GRADER_RECEIPT_KIND,
        issuer=issuer,
    )


def sign_mcp_receipt(payload: Mapping[str, Any], *, signer: Ed25519Signer) -> dict[str, Any]:
    """Sign one MCP server payload without changing its receipt fields."""

    return attach_signature(
        payload,
        signer=signer,
        kind=MCP_RECEIPT_KIND,
        issuer="mcp-server",
    )


def sign_no_lsp_isolation(
    payload: Mapping[str, Any], *, signer: Ed25519Signer
) -> dict[str, Any]:
    """Sign one executor No-LSP isolation payload."""

    return attach_signature(
        payload,
        signer=signer,
        kind=NO_LSP_ISOLATION_KIND,
        issuer="executor",
    )


def validate_grader_receipt(
    value: Mapping[str, Any],
    *,
    verifier: Ed25519Verifier,
    task: str,
    arm: str,
    session: str,
    thread: str,
    patch_hash: str,
    grader_artifact: Mapping[str, Any],
    final_tree_hash: str | None = None,
    issuer: str = "grader",
) -> dict[str, Any]:
    """Verify a signed grader receipt and return only its signed result."""

    if not isinstance(value, Mapping):
        raise SignedReceiptError("grader receipt must be an object")
    _reject_unknown(value, GRADER_RECEIPT_FIELDS, "grader receipt")
    missing = sorted(_GRADER_RECEIPT_REQUIRED_FIELDS - set(value))
    if missing:
        raise SignedReceiptError("grader receipt missing field(s): " + ", ".join(missing))
    if value.get("schema") != GRADER_RECEIPT_SCHEMA:
        raise SignedReceiptError("unsupported grader receipt schema")
    if not isinstance(value.get("result"), Mapping):
        raise SignedReceiptError("grader receipt result must be an object")
    try:
        payload = verify_signed_record(
            value,
            verifier=verifier,
            kind=GRADER_RECEIPT_KIND,
            issuer=issuer,
        )
        validate_receipt_context(
            payload,
            task=task,
            arm=arm,
            session=session,
            thread=thread,
            patch_hash=patch_hash,
            grader_artifact=grader_artifact,
            final_tree_hash=final_tree_hash,
        )
    except SignedReceiptError:
        raise
    return dict(payload["result"])


def validate_receipt_context(
    payload: Mapping[str, Any],
    *,
    task: str,
    arm: str,
    session: str,
    thread: str,
    patch_hash: str | None = None,
    grader_artifact: Mapping[str, Any] | None = None,
    seq: int | None = None,
    final_tree_hash: str | None = None,
) -> None:
    """Require the identity and candidate/grader bindings covered by a signature."""

    expected = {
        "task": task,
        "arm": arm,
        "session": session,
        "thread": thread,
    }
    for field, wanted in expected.items():
        actual = _required_text(payload.get(field), f"signed receipt {field}")
        if actual != _required_text(wanted, f"expected {field}"):
            raise SignatureVerificationError(f"signed receipt {field} drifted")
    actual_seq = _positive_int(payload.get("seq"), "signed receipt seq")
    if seq is not None and actual_seq != _positive_int(seq, "expected seq"):
        raise SignatureVerificationError("signed receipt seq drifted")
    actual_patch = _validate_digest(payload.get("patch_hash"), "signed receipt patch_hash")
    if patch_hash is not None and actual_patch != _validate_digest(patch_hash, "expected patch_hash"):
        raise SignatureVerificationError("signed receipt patch_hash drifted")
    if grader_artifact is not None or "grader_artifact" in payload:
        actual_artifact = canonical_artifact(payload.get("grader_artifact"))
        if grader_artifact is not None and actual_artifact != canonical_artifact(
            grader_artifact,
            "expected grader_artifact",
        ):
            raise SignatureVerificationError("signed receipt grader_artifact drifted")
    if final_tree_hash is not None or "final_tree_hash" in payload:
        actual_tree = _validate_tree_hash(
            payload.get("final_tree_hash"),
            "signed receipt final_tree_hash",
        )
        if final_tree_hash is not None and actual_tree != _validate_tree_hash(
            final_tree_hash,
            "expected final_tree_hash",
        ):
            raise SignatureVerificationError("signed receipt final_tree_hash drifted")


def _bound_final_payload(
    payload: Mapping[str, Any],
    *,
    task: str,
    arm: str,
    session: str,
    thread: str,
    seq: int,
    request_id: str,
    patch_hash: str,
    grader_artifact: Mapping[str, Any],
    final_tree_hash: str,
) -> dict[str, Any]:
    if not isinstance(payload, Mapping):
        raise SignedReceiptError("final evidence payload must be an object")
    result = dict(payload)
    expected = {
        "task": _required_text(task, "final evidence task"),
        "arm": _required_text(arm, "final evidence arm"),
        "session": _required_text(session, "final evidence session"),
        "thread": _required_text(thread, "final evidence thread"),
        "seq": _positive_int(seq, "final evidence seq"),
        "request_id": _required_text(request_id, "final evidence request_id"),
        "patch_hash": _validate_digest(patch_hash, "final evidence patch_hash"),
        "grader_artifact": canonical_artifact(grader_artifact),
        "final_tree_hash": _validate_tree_hash(final_tree_hash, "final evidence final_tree_hash"),
    }
    for field, wanted in expected.items():
        if field in result and result[field] != wanted:
            raise SignedReceiptError(f"final evidence {field} does not match requested binding")
        result[field] = wanted
    return result


def sign_final_evidence(
    payload: Mapping[str, Any],
    *,
    signer: Ed25519Signer,
    kind: str,
    issuer: str,
    task: str,
    arm: str,
    session: str,
    thread: str,
    seq: int,
    request_id: str,
    patch_hash: str,
    grader_artifact: Mapping[str, Any],
    final_tree_hash: str,
) -> dict[str, Any]:
    """Sign final evidence while binding patch, artifact, and final Git tree."""

    return attach_signature(
        _bound_final_payload(
            payload,
            task=task,
            arm=arm,
            session=session,
            thread=thread,
            seq=seq,
            request_id=request_id,
            patch_hash=patch_hash,
            grader_artifact=grader_artifact,
            final_tree_hash=final_tree_hash,
        ),
        signer=signer,
        kind=kind,
        issuer=issuer,
    )


def rebind_signed_evidence(
    value: Mapping[str, Any],
    *,
    signer: Ed25519Signer,
    kind: str,
    issuer: str,
    task: str,
    arm: str,
    session: str,
    thread: str,
    seq: int,
    request_id: str,
    patch_hash: str,
    grader_artifact: Mapping[str, Any],
    final_tree_hash: str,
) -> dict[str, Any]:
    """Re-sign changed final evidence; an old signature is never retained."""

    if not isinstance(value, Mapping):
        raise SignedReceiptError("evidence to rebind must be an object")
    payload = {key: item for key, item in value.items() if key != SIGNATURE_FIELD}
    for field in (
        "task",
        "arm",
        "session",
        "thread",
        "seq",
        "request_id",
        "patch_hash",
        "grader_artifact",
        "final_tree_hash",
    ):
        payload.pop(field, None)
    return sign_final_evidence(
        payload,
        signer=signer,
        kind=kind,
        issuer=issuer,
        task=task,
        arm=arm,
        session=session,
        thread=thread,
        seq=seq,
        request_id=request_id,
        patch_hash=patch_hash,
        grader_artifact=grader_artifact,
        final_tree_hash=final_tree_hash,
    )


def validate_final_evidence(
    value: Mapping[str, Any],
    *,
    verifier: Ed25519Verifier,
    kind: str,
    issuer: str,
    task: str,
    arm: str,
    session: str,
    thread: str,
    patch_hash: str,
    grader_artifact: Mapping[str, Any],
    final_tree_hash: str,
) -> dict[str, Any]:
    """Verify final evidence and all immutable candidate/artifact bindings."""

    payload = verify_signed_record(
        value,
        verifier=verifier,
        kind=kind,
        issuer=issuer,
    )
    validate_receipt_context(
        payload,
        task=task,
        arm=arm,
        session=session,
        thread=thread,
        patch_hash=patch_hash,
        grader_artifact=grader_artifact,
        final_tree_hash=final_tree_hash,
    )
    return payload


class ReceiptReplayGuard:
    """Reject duplicate request IDs and signature bytes within a receipt set."""

    def __init__(self) -> None:
        self._request_ids: set[str] = set()
        self._signatures: set[str] = set()

    def observe(self, payload: Mapping[str, Any], signature: Mapping[str, Any]) -> None:
        if not isinstance(payload, Mapping) or not isinstance(signature, Mapping):
            raise SignedReceiptError("signed receipt replay inputs must be objects")
        request_id = _required_text(
            payload.get("request_id", payload.get("receipt_id")),
            "signed receipt request_id",
        )
        signature_bytes = _decode_base64(
            signature.get("signature"),
            "signed receipt signature",
            expected_size=64,
        )
        digest = hashlib.sha256(signature_bytes).hexdigest()
        if request_id in self._request_ids:
            raise ReceiptReplayError(f"duplicate signed receipt request_id: {request_id}")
        if digest in self._signatures:
            raise ReceiptReplayError("duplicate signed receipt signature")
        self._request_ids.add(request_id)
        self._signatures.add(digest)


def verifier_from(
    *,
    verifier: Ed25519Verifier | None = None,
    identity_envelope: Any = None,
    public_key: Mapping[str, Any] | None = None,
) -> Ed25519Verifier:
    """Resolve a verifier only from an explicit key or identity envelope."""

    if verifier is not None:
        if not isinstance(verifier, Ed25519Verifier):
            raise SigningKeyError("verifier must be Ed25519Verifier")
        return verifier
    if identity_envelope is not None:
        return Ed25519Verifier.from_identity_envelope(identity_envelope)
    if public_key is not None:
        return Ed25519Verifier.from_mapping(public_key)
    raise SignatureVerificationError(
        "trusted Ed25519 verifier or identity envelope public key is required"
    )


# Descriptive aliases for Ubuntu driver integrations.
load_private_key = Ed25519Signer.from_file
verify_receipt_signature = validate_signature_block
sign_receipt_payload = sign_payload
sign_final_receipt = sign_final_evidence
rebind_receipt = rebind_signed_evidence
verify_final_evidence = validate_final_evidence


__all__ = [
    "ALGORITHM",
    "ARTIFACT_FIELDS",
    "Ed25519Signer",
    "Ed25519Verifier",
    "GRADER_RECEIPT_KIND",
    "GRADER_RECEIPT_FIELDS",
    "GRADER_RECEIPT_SCHEMA",
    "MCP_RECEIPT_KIND",
    "NO_LSP_ISOLATION_KIND",
    "PublicKeyBinding",
    "ReceiptReplayError",
    "ReceiptReplayGuard",
    "SIGNATURE_FIELD",
    "SIGNATURE_FIELDS",
    "SCHEMA",
    "SignatureSchemaError",
    "SignatureVerificationError",
    "SignedReceiptError",
    "SigningKeyError",
    "attach_signature",
    "canonical_artifact",
    "canonical_json",
    "sign_payload",
    "sign_grader_receipt",
    "sign_final_evidence",
    "sign_mcp_receipt",
    "sign_no_lsp_isolation",
    "sign_receipt_payload",
    "sign_final_receipt",
    "signed_material",
    "rebind_signed_evidence",
    "validate_final_evidence",
    "validate_receipt_context",
    "validate_grader_receipt",
    "validate_signature_block",
    "verifier_from",
    "verify_receipt_signature",
    "verify_final_evidence",
    "rebind_receipt",
    "verify_signed_record",
]
