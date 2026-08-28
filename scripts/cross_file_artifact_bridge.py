#!/usr/bin/env python3
"""Fail-closed conversion of real Codex JSONL into canonical driver artifacts."""

from __future__ import annotations

import json
import dataclasses
import hashlib
import re
import secrets
from datetime import datetime, timezone
import pathlib
import fcntl
from collections.abc import Iterable, Mapping
from typing import Any

try:
    from .cross_file_go_receipts import (
        SCHEMA as GO_RECEIPT_SCHEMA,
        GoReceiptError,
        validate_go_receipt_stream,
    )
    from .cross_file_signed_receipts import (
        Ed25519Signer,
        Ed25519Verifier,
        attach_signature,
        verify_signed_record,
    )
except ImportError:  # pragma: no cover - direct script execution fallback.
    from cross_file_go_receipts import (  # type: ignore[no-redef]
        SCHEMA as GO_RECEIPT_SCHEMA,
        GoReceiptError,
        validate_go_receipt_stream,
    )
    from cross_file_signed_receipts import (  # type: ignore[no-redef]
        Ed25519Signer,
        Ed25519Verifier,
        attach_signature,
        verify_signed_record,
    )


class ArtifactBridgeError(RuntimeError):
    """The container output cannot be bound to one canonical Agent turn."""


class ExecutorAttestationError(ArtifactBridgeError):
    """The final executor evidence is malformed or cannot be verified."""


EXECUTOR_FINAL_ATTESTATION_SCHEMA = "super-dolphin/executor-final-attestation/v3"
EXECUTOR_FINAL_ATTESTATION_KIND = "executor_final_attestation"
_ATTESTATION_FIELDS = frozenset(
    {
        "schema",
        "task",
        "arm",
        "session",
        "thread",
        "mode",
        "run_id",
        "attempt",
        "nonce",
        "issued_at",
        "patch_hash",
        "final_tree_hash",
        "server_generation",
        "receipt_log_digest",
        "receipt_count",
        "receipt_head_digest",
        "prompt",
        "prompt_digest",
        "isolation_policy",
        "isolation_policy_digest",
        "signature",
    }
)
_GO_RECEIPT_FIELDS = frozenset(
    {
        "schema",
        "sequence",
        "previous_digest",
        "payload",
        "signer_public_key_sha256",
        "event_digest",
        "signature",
    }
)
_GO_PAYLOAD_FIELDS = frozenset(
    {
        "receipt_id",
        "tool",
        "action",
        "status",
        "error_code",
        "error_retryable",
        "started_at",
        "completed_at",
        "duration_ms",
        "arguments_bytes",
        "arguments_sha256",
        "arguments",
        "result_bytes",
        "result_sha256",
        "result",
        "error_sha256",
        "result_type",
        "agent_id",
        "thread_id",
        "turn_id",
        "call_id",
        "cwd_sha256",
        "workspace_root_count",
    }
)
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
_TREE_HASH_RE = re.compile(r"^[0-9a-f]{40}(?:[0-9a-f]{24})?$")


@dataclasses.dataclass(frozen=True)
class ExecutorFinalAttestation:
    """Validated executor binding for one native mcp-lsp receipt log."""

    task: str
    arm: str
    session: str
    thread: str
    mode: str
    run_id: str
    attempt: int
    nonce: str
    issued_at: str
    patch_hash: str
    final_tree_hash: str
    server_generation: str
    receipt_log_digest: str
    receipt_count: int
    receipt_head_digest: str
    prompt: Any
    prompt_digest: str
    isolation_policy: Mapping[str, Any]
    isolation_policy_digest: str
    signature: Mapping[str, Any]

    def to_dict(self) -> dict[str, Any]:
        """Return the signed attestation without retaining the raw receipt log."""

        return {
            "schema": EXECUTOR_FINAL_ATTESTATION_SCHEMA,
            "task": self.task,
            "arm": self.arm,
            "session": self.session,
            "thread": self.thread,
            "mode": self.mode,
            "run_id": self.run_id,
            "attempt": self.attempt,
            "nonce": self.nonce,
            "issued_at": self.issued_at,
            "patch_hash": self.patch_hash,
            "final_tree_hash": self.final_tree_hash,
            "server_generation": self.server_generation,
            "receipt_log_digest": self.receipt_log_digest,
            "receipt_count": self.receipt_count,
            "receipt_head_digest": self.receipt_head_digest,
            "prompt": self.prompt,
            "prompt_digest": self.prompt_digest,
            "isolation_policy": dict(self.isolation_policy),
            "isolation_policy_digest": self.isolation_policy_digest,
            "signature": dict(self.signature),
        }


@dataclasses.dataclass(frozen=True)
class NativeReceiptProjection:
    """Audit-only projection of a validated native Go receipt.

    It deliberately carries no Python-generated MCP signature.  The native
    Go record remains the authority and was cryptographically verified before
    this projection is created.
    """

    task: str
    arm: str
    session: str
    thread: str
    seq: int
    request_id: str
    tool: str
    arguments: Mapping[str, Any]
    result: Any
    server_generation: str
    patch_hash: str
    final_tree_hash: str
    source: str = "mcp-lsp-native-go"

    def to_dict(self) -> dict[str, Any]:
        return {
            "schema": GO_RECEIPT_SCHEMA,
            "source": self.source,
            "task": self.task,
            "arm": self.arm,
            "session": self.session,
            "thread": self.thread,
            "seq": self.seq,
            "request_id": self.request_id,
            "args_hash": hashlib.sha256(
                json.dumps(self.arguments, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
            ).hexdigest(),
            "result_hash": hashlib.sha256(
                json.dumps(self.result, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")
            ).hexdigest(),
            "status": "success",
            "server_generation": self.server_generation,
            "patch_hash": self.patch_hash,
            "final_tree_hash": self.final_tree_hash,
            "tool": self.tool,
            "arguments": dict(self.arguments),
            "result": self.result,
        }


@dataclasses.dataclass(frozen=True)
class CanonicalArmEvidenceProjection:
    """Canonical arm shape derived only from an executor attestation."""

    mode: str
    task: str
    arm: str
    session: str
    thread: str
    receipts: tuple[NativeReceiptProjection, ...]
    isolation: Mapping[str, Any] | None

    def to_dict(self) -> dict[str, Any]:
        return {
            "schema": "super-dolphin/cross-file-arm-evidence/v2",
            "mode": self.mode,
            "task": self.task,
            "arm": self.arm,
            "session": self.session,
            "thread": self.thread,
            "receipts": [item.to_dict() for item in self.receipts],
            "isolation": None if self.isolation is None else dict(self.isolation),
        }


def _attestation_text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ExecutorAttestationError(f"{label} must be a non-empty string")
    return value.strip()


def _attestation_digest(value: Any, label: str) -> str:
    text = _attestation_text(value, label)
    if _SHA256_RE.fullmatch(text) is None:
        raise ExecutorAttestationError(f"{label} must be a lowercase SHA-256 digest")
    return text


def _attestation_tree(value: Any, label: str) -> str:
    text = _attestation_text(value, label)
    if _TREE_HASH_RE.fullmatch(text) is None:
        raise ExecutorAttestationError(f"{label} must be a lowercase Git tree hash")
    return text


def _attestation_attempt(value: Any, label: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 1:
        raise ExecutorAttestationError(f"{label} must be a positive integer")
    return value


def _attestation_nonce(value: Any, label: str) -> str:
    text = _attestation_text(value, label)
    if len(text) < 16 or len(text) > 256 or re.fullmatch(r"[A-Za-z0-9._:-]+", text) is None:
        raise ExecutorAttestationError(f"{label} has an invalid nonce")
    return text


def _canonical_json(value: Any, label: str) -> tuple[Any, str]:
    """Normalize JSON policy material and return it with its SHA-256 digest."""

    try:
        encoded = json.dumps(
            value,
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
            allow_nan=False,
        )
        normalized = json.loads(encoded)
    except (TypeError, ValueError, json.JSONDecodeError) as exc:
        raise ExecutorAttestationError(f"{label} is not canonical JSON material") from exc
    if normalized in (None, "", [], {}):
        raise ExecutorAttestationError(f"{label} must not be empty")
    return normalized, hashlib.sha256(encoded.encode("utf-8")).hexdigest()


def _canonical_prompt(value: Any) -> tuple[Any, str]:
    if isinstance(value, bytes):
        try:
            value = value.decode("utf-8", errors="strict")
        except UnicodeDecodeError as exc:
            raise ExecutorAttestationError("prompt must be UTF-8") from exc
    if isinstance(value, str) and not value.strip():
        raise ExecutorAttestationError("prompt must be non-empty text")
    if not isinstance(value, (str, Mapping)):
        raise ExecutorAttestationError("prompt must be text or an object")
    return _canonical_json(value, "prompt")


def _canonical_isolation_policy(value: Any) -> tuple[Any, str]:
    if not isinstance(value, Mapping):
        raise ExecutorAttestationError("isolation_policy must be an object")
    return _canonical_json(value, "isolation_policy")


def _receipt_log_bytes(value: str | bytes) -> bytes:
    if isinstance(value, bytes):
        raw = value
    elif isinstance(value, str):
        raw = value.encode("utf-8")
    else:
        raise ExecutorAttestationError("receipt_log must be the exact UTF-8 text or bytes")
    if not raw:
        raise ExecutorAttestationError("receipt_log is empty")
    try:
        raw.decode("utf-8", errors="strict")
    except UnicodeDecodeError as exc:
        raise ExecutorAttestationError("receipt_log must be UTF-8") from exc
    return raw


def _validate_native_go_log(
    value: str | bytes, *, verifier: Ed25519Verifier
) -> tuple[bytes, tuple[Mapping[str, Any], ...]]:
    """Validate native v2 records and return their exact bytes and parsed rows."""

    raw = _receipt_log_bytes(value)
    text = raw.decode("utf-8", errors="strict")
    lines = text.splitlines()
    if any(not line.strip() for line in lines):
        raise ExecutorAttestationError("receipt_log contains an empty line")
    try:
        records = validate_go_receipt_stream(raw, verifier=verifier)
    except (GoReceiptError, ValueError, TypeError) as exc:
        raise ExecutorAttestationError(f"native mcp-lsp v2 receipt chain rejected: {exc}") from exc
    if not records:
        raise ExecutorAttestationError("native mcp-lsp v2 receipt chain is empty")
    for index, record in enumerate(records, 1):
        unknown = sorted(set(record) - _GO_RECEIPT_FIELDS)
        if unknown:
            raise ExecutorAttestationError(
                f"receipt record {index} has unknown field(s): {', '.join(unknown)}"
            )
        payload = record.get("payload")
        if not isinstance(payload, Mapping):
            raise ExecutorAttestationError(f"receipt record {index} payload is not an object")
        unknown_payload = sorted(set(payload) - _GO_PAYLOAD_FIELDS)
        if unknown_payload:
            raise ExecutorAttestationError(
                f"receipt record {index} payload has unknown field(s): {', '.join(unknown_payload)}"
            )
        if record.get("schema") != GO_RECEIPT_SCHEMA:
            raise ExecutorAttestationError(f"receipt record {index} is not native v2")
    return raw, records


def _log_digest(raw: bytes) -> str:
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def _no_lsp_event_markers(events: Iterable[Mapping[str, Any]]) -> tuple[str, ...]:
    """Return MCP/LSP markers from structured tool events only.

    Agent prose, shell output, file paths, and diagnostics printed by tests are
    untrusted text rather than evidence that Codex invoked an MCP tool.  Only
    event fields that describe an actual tool invocation may contaminate the
    No-LSP arm.
    """

    markers: set[str] = set()
    semantic_tools = {"structure", "xref", "diagnostics"}
    tool_event_types = {"mcp_tool_call", "tool_call", "function_call"}

    def visit(value: Any) -> None:
        if isinstance(value, Mapping):
            event_type = str(value.get("type", "")).strip().lower()
            names = tuple(
                str(value.get(key, "")).strip()
                for key in ("tool", "name", "server", "server_name")
                if isinstance(value.get(key), str)
            )
            lowered = tuple(name.lower() for name in names if name)
            is_mcp_type = event_type == "mcp_tool_call"
            is_mcp_name = any(
                name.startswith(("mcp__", "mcp_")) or "mcp-lsp" in name or "mcp_lsp" in name
                for name in lowered
            )
            is_lsp_tool = event_type in tool_event_types and any(
                name in semantic_tools for name in lowered
            )
            if is_mcp_type or is_mcp_name or is_lsp_tool:
                markers.add(":".join((event_type or "tool_event", *lowered)))
            for child in value.values():
                if isinstance(child, (Mapping, list, tuple)):
                    visit(child)
        elif isinstance(value, (list, tuple)):
            for child in value:
                visit(child)

    for event in events:
        visit(event)
    return tuple(sorted(markers))


def _validate_no_lsp_observation(value: str | bytes, label: str) -> tuple[Mapping[str, Any], ...]:
    if value in (b"", ""):
        return ()
    try:
        events = parse_codex_jsonl(value)
    except (ArtifactBridgeError, UnicodeDecodeError) as exc:
        raise ExecutorAttestationError(f"No-LSP {label} is not valid JSONL: {exc}") from exc
    markers = _no_lsp_event_markers(events)
    if markers:
        raise ExecutorAttestationError(
            f"No-LSP {label} contains MCP/LSP event(s): " + ", ".join(markers)
        )
    return events


class PersistentAttestationReplayStore:
    """Atomic JSON replay store for executor attestation consumption."""

    def __init__(self, path: str | pathlib.Path):
        candidate = pathlib.Path(path)
        if not candidate.is_absolute():
            raise ExecutorAttestationError("replay store path must be absolute")
        if candidate.exists() and (candidate.is_symlink() or not candidate.is_file()):
            raise ExecutorAttestationError("replay store must be a regular non-symlink file")
        self.path = candidate

    def _load(self) -> dict[str, Any]:
        if not self.path.exists():
            return {"schema": "super-dolphin/executor-attestation-replay/v1", "consumed": {}}
        try:
            raw = json.loads(self.path.read_text(encoding="utf-8"))
        except (OSError, UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise ExecutorAttestationError("replay store cannot be read") from exc
        if not isinstance(raw, Mapping) or raw.get("schema") != "super-dolphin/executor-attestation-replay/v1":
            raise ExecutorAttestationError("replay store schema is invalid")
        consumed = raw.get("consumed")
        if not isinstance(consumed, Mapping):
            raise ExecutorAttestationError("replay store consumed index is invalid")
        return {"schema": raw["schema"], "consumed": dict(consumed)}

    def consume(self, attestation: Mapping[str, Any]) -> None:
        """Record one attestation; a second consume of its signature fails."""

        if not isinstance(attestation, Mapping):
            raise ExecutorAttestationError("attestation replay input must be an object")
        signature = attestation.get("signature")
        if not isinstance(signature, Mapping) or not isinstance(signature.get("signature"), str):
            raise ExecutorAttestationError("attestation replay input lacks signature")
        key = signature["signature"]
        self.path.parent.mkdir(parents=True, exist_ok=True)
        lock_path = self.path.with_name(f".{self.path.name}.lock")
        temporary = self.path.with_name(f".{self.path.name}.{secrets.token_hex(8)}.tmp")
        try:
            if lock_path.exists() and lock_path.is_symlink():
                raise ExecutorAttestationError("replay store lock must be non-symlink")
            with lock_path.open("a+", encoding="utf-8") as lock:
                fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
                state = self._load()
                consumed = state["consumed"]
                if key in consumed:
                    raise ExecutorAttestationError("executor attestation replay detected")
                consumed[key] = {
                    "run_id": _attestation_text(attestation.get("run_id"), "attestation.run_id"),
                    "nonce": _attestation_nonce(attestation.get("nonce"), "attestation.nonce"),
                }
                temporary.write_text(
                    json.dumps(state, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n",
                    encoding="utf-8",
                )
                temporary.replace(self.path)
                fcntl.flock(lock.fileno(), fcntl.LOCK_UN)
        except ExecutorAttestationError:
            raise
        except OSError as exc:
            raise ExecutorAttestationError("replay store cannot be persisted") from exc
        finally:
            temporary.unlink(missing_ok=True)


def build_executor_final_attestation(
    *,
    receipt_log: str | bytes,
    task: str,
    arm: str,
    session: str,
    thread: str,
    patch_hash: str,
    final_tree_hash: str,
    server_generation: str,
    mode: str,
    run_id: str,
    attempt: int,
    nonce: str,
    issued_at: str,
    prompt: Any,
    isolation_policy: Mapping[str, Any],
    signer: Ed25519Signer,
    verifier: Ed25519Verifier,
) -> dict[str, Any]:
    """Validate native Go receipts, then sign their immutable final binding.

    ``receipt_log`` is hashed as the exact UTF-8 bytes supplied by the
    executor.  The raw log is intentionally not copied into the attestation;
    a verifier must receive the log separately and revalidate the native
    chain before accepting its digest.
    """

    if not isinstance(signer, Ed25519Signer):
        raise ExecutorAttestationError("signer must be an Ed25519Signer")
    if not isinstance(verifier, Ed25519Verifier):
        raise ExecutorAttestationError("verifier must be an Ed25519Verifier")
    trusted = verifier
    raw = _receipt_log_bytes(receipt_log) if receipt_log else b""
    if raw.strip():
        try:
            raw, records = _validate_native_go_log(raw, verifier=trusted)
        except (ExecutorAttestationError, ValueError, TypeError) as exc:
            if isinstance(exc, ExecutorAttestationError):
                raise
            raise ExecutorAttestationError(f"receipt chain verification failed: {exc}") from exc
    else:
        records = ()
    prompt_value, prompt_digest = _canonical_prompt(prompt)
    policy_value, policy_digest = _canonical_isolation_policy(isolation_policy)
    normalized_mode = _attestation_text(mode, "attestation.mode")
    if normalized_mode != "with_lsp":
        raise ExecutorAttestationError(
            "native receipt-log attestation requires mode=with_lsp"
        )
    payload = {
        "schema": EXECUTOR_FINAL_ATTESTATION_SCHEMA,
        "task": _attestation_text(task, "attestation.task"),
        "arm": _attestation_text(arm, "attestation.arm"),
        "session": _attestation_text(session, "attestation.session"),
        "thread": _attestation_text(thread, "attestation.thread"),
        "mode": normalized_mode,
        "run_id": _attestation_text(run_id, "attestation.run_id"),
        "attempt": _attestation_attempt(attempt, "attestation.attempt"),
        "nonce": _attestation_nonce(nonce, "attestation.nonce"),
        "issued_at": _attestation_text(issued_at, "attestation.issued_at"),
        "patch_hash": _attestation_digest(patch_hash, "attestation.patch_hash"),
        "final_tree_hash": _attestation_tree(final_tree_hash, "attestation.final_tree_hash"),
        "server_generation": _attestation_text(
            server_generation, "attestation.server_generation"
        ),
        "receipt_log_digest": _log_digest(raw),
        "receipt_count": len(records),
        "receipt_head_digest": (
            _attestation_text(
                records[-1].get("event_digest"), "attestation.receipt_head_digest"
            )
            if records
            else ""
        ),
        "prompt": prompt_value,
        "prompt_digest": prompt_digest,
        "isolation_policy": policy_value,
        "isolation_policy_digest": policy_digest,
    }
    return attach_signature(
        payload,
        signer=signer,
        kind=EXECUTOR_FINAL_ATTESTATION_KIND,
        issuer="executor",
    )


def build_no_lsp_executor_attestation(
    *,
    stdout: str | bytes,
    sidecar_observation: str | bytes = b"",
    task: str,
    arm: str,
    session: str,
    thread: str,
    patch_hash: str,
    final_tree_hash: str,
    server_generation: str,
    run_id: str,
    attempt: int,
    nonce: str,
    issued_at: str,
    prompt: Any,
    isolation_policy: Mapping[str, Any],
    signer: Ed25519Signer,
) -> dict[str, Any]:
    """Create a signed No-LSP attestation from real transport observations."""

    if not isinstance(signer, Ed25519Signer):
        raise ExecutorAttestationError("signer must be an Ed25519Signer")
    stdout_events = _validate_no_lsp_observation(stdout, "Codex stdout")
    sidecar_events = _validate_no_lsp_observation(sidecar_observation, "sidecar observation")
    prompt_value, prompt_digest = _canonical_prompt(prompt)
    policy_value, policy_digest = _canonical_isolation_policy(isolation_policy)
    if policy_value.get("mode") not in (None, "no_lsp"):
        raise ExecutorAttestationError("No-LSP isolation policy mode drifted")
    model_egress = policy_value.get("model_egress")
    mcp_sidecar = policy_value.get("mcp_sidecar")
    mcp_ingress = policy_value.get("mcp_ingress")
    if not isinstance(model_egress, Mapping) or model_egress.get("allowed") is not True:
        raise ExecutorAttestationError(
            "No-LSP policy must explicitly allow model egress"
        )
    proxy = model_egress.get("proxy")
    if proxy is not None and (not isinstance(proxy, str) or not proxy.strip()):
        raise ExecutorAttestationError(
            "No-LSP model_egress.proxy must be null or non-empty text"
        )
    if not isinstance(mcp_sidecar, Mapping) or mcp_sidecar.get("disabled") is not True:
        raise ExecutorAttestationError(
            "No-LSP policy must explicitly disable the MCP sidecar"
        )
    if (
        not isinstance(mcp_ingress, Mapping)
        or isinstance(mcp_ingress.get("count"), bool)
        or mcp_ingress.get("count") != 0
    ):
        raise ExecutorAttestationError(
            "No-LSP policy must explicitly declare zero MCP ingress"
        )
    policy_value = {
        **policy_value,
        "mode": "no_lsp",
        "stdout_digest": _log_digest(
            stdout.encode("utf-8") if isinstance(stdout, str) else stdout
        ),
        "sidecar_digest": _log_digest(
            sidecar_observation.encode("utf-8")
            if isinstance(sidecar_observation, str)
            else sidecar_observation
        ),
        "ingress_count": 0,
    }
    # Recompute after adding the executor-observed zero-ingress facts.
    _, policy_digest = _canonical_isolation_policy(policy_value)
    payload = {
        "schema": EXECUTOR_FINAL_ATTESTATION_SCHEMA,
        "task": _attestation_text(task, "attestation.task"),
        "arm": _attestation_text(arm, "attestation.arm"),
        "session": _attestation_text(session, "attestation.session"),
        "thread": _attestation_text(thread, "attestation.thread"),
        "mode": "no_lsp",
        "run_id": _attestation_text(run_id, "attestation.run_id"),
        "attempt": _attestation_attempt(attempt, "attestation.attempt"),
        "nonce": _attestation_nonce(nonce, "attestation.nonce"),
        "issued_at": _attestation_text(issued_at, "attestation.issued_at"),
        "patch_hash": _attestation_digest(patch_hash, "attestation.patch_hash"),
        "final_tree_hash": _attestation_tree(final_tree_hash, "attestation.final_tree_hash"),
        "server_generation": _attestation_text(
            server_generation, "attestation.server_generation"
        ),
        "receipt_log_digest": _log_digest(b""),
        "receipt_count": 0,
        "receipt_head_digest": "",
        "prompt": prompt_value,
        "prompt_digest": prompt_digest,
        "isolation_policy": policy_value,
        "isolation_policy_digest": policy_digest,
    }
    if stdout_events is None or sidecar_events is None:  # pragma: no cover - type narrowing.
        raise ExecutorAttestationError("No-LSP observation is unavailable")
    return attach_signature(
        payload,
        signer=signer,
        kind=EXECUTOR_FINAL_ATTESTATION_KIND,
        issuer="executor",
    )


def validate_executor_final_attestation(
    value: Mapping[str, Any],
    *,
    receipt_log: str | bytes,
    verifier: Ed25519Verifier,
    task: str,
    arm: str,
    session: str,
    thread: str,
    patch_hash: str,
    final_tree_hash: str,
    server_generation: str,
    mode: str,
    run_id: str,
    attempt: int,
    nonce: str,
    issued_at: str,
    prompt: Any | None = None,
    isolation_policy: Mapping[str, Any] | None = None,
    receipt_verifier: Ed25519Verifier | None = None,
) -> ExecutorFinalAttestation:
    """Revalidate a signed executor attestation against its native receipt log."""

    if not isinstance(value, Mapping):
        raise ExecutorAttestationError("executor final attestation must be an object")
    unknown = sorted(set(value) - _ATTESTATION_FIELDS)
    if unknown:
        raise ExecutorAttestationError(
            "executor final attestation has unknown field(s): " + ", ".join(unknown)
        )
    required = _ATTESTATION_FIELDS - {"signature"}
    missing = sorted(required - set(value))
    if missing or "signature" not in value:
        missing = missing + ([] if "signature" in value else ["signature"])
        raise ExecutorAttestationError(
            "executor final attestation missing field(s): " + ", ".join(missing)
        )
    if value.get("schema") != EXECUTOR_FINAL_ATTESTATION_SCHEMA:
        raise ExecutorAttestationError("unsupported executor final attestation schema")
    if not isinstance(verifier, Ed25519Verifier):
        raise ExecutorAttestationError("verifier must be an Ed25519Verifier")
    trusted_receipts = receipt_verifier or verifier
    if not isinstance(trusted_receipts, Ed25519Verifier):
        raise ExecutorAttestationError("receipt_verifier must be an Ed25519Verifier")
    try:
        payload = verify_signed_record(
            value,
            verifier=verifier,
            kind=EXECUTOR_FINAL_ATTESTATION_KIND,
            issuer="executor",
        )
    except Exception as exc:
        raise ExecutorAttestationError(
            f"executor final attestation signature rejected: {exc}"
        ) from exc
    try:
        if payload.get("mode") == "no_lsp":
            raw = _receipt_log_bytes(receipt_log) if receipt_log else b""
            if raw:
                raise ExecutorAttestationError("No-LSP attestation must bind an empty receipt log")
            records = ()
        else:
            raw = _receipt_log_bytes(receipt_log) if receipt_log else b""
            records = (
                _validate_native_go_log(raw, verifier=trusted_receipts)[1]
                if raw.strip()
                else ()
            )
        expected_identity = {
            "task": task,
            "arm": arm,
            "session": session,
            "thread": thread,
            "server_generation": server_generation,
        }
        for field, expected in expected_identity.items():
            actual = _attestation_text(payload.get(field), f"attestation.{field}")
            if actual != _attestation_text(expected, f"expected {field}"):
                raise ExecutorAttestationError(f"attestation.{field} drifted")
        expected_mode = _attestation_text(mode, "expected mode")
        if expected_mode not in ("with_lsp", "no_lsp"):
            raise ExecutorAttestationError("expected mode is unsupported")
        if payload.get("mode") != expected_mode:
            raise ExecutorAttestationError("attestation.mode drifted")
        if payload.get("run_id") != _attestation_text(run_id, "expected run_id"):
            raise ExecutorAttestationError("attestation.run_id drifted")
        if payload.get("attempt") != _attestation_attempt(attempt, "expected attempt"):
            raise ExecutorAttestationError("attestation.attempt drifted")
        if payload.get("nonce") != _attestation_nonce(nonce, "expected nonce"):
            raise ExecutorAttestationError("attestation.nonce drifted")
        if payload.get("issued_at") != _attestation_text(issued_at, "expected issued_at"):
            raise ExecutorAttestationError("attestation.issued_at drifted")
        if payload.get("patch_hash") != _attestation_digest(patch_hash, "expected patch_hash"):
            raise ExecutorAttestationError("attestation.patch_hash drifted")
        if payload.get("final_tree_hash") != _attestation_tree(
            final_tree_hash, "expected final_tree_hash"
        ):
            raise ExecutorAttestationError("attestation.final_tree_hash drifted")
        if payload.get("receipt_log_digest") != _log_digest(raw):
            raise ExecutorAttestationError("attestation.receipt_log_digest drifted")
        count = payload.get("receipt_count")
        if isinstance(count, bool) or not isinstance(count, int) or count != len(records):
            raise ExecutorAttestationError("attestation.receipt_count drifted")
        expected_head = records[-1].get("event_digest") if records else ""
        if payload.get("receipt_head_digest") != expected_head:
            raise ExecutorAttestationError("attestation.receipt_head_digest drifted")
        embedded_prompt, embedded_prompt_digest = _canonical_prompt(payload.get("prompt"))
        if payload.get("prompt_digest") != embedded_prompt_digest:
            raise ExecutorAttestationError("attestation.prompt_digest drifted")
        embedded_policy, embedded_policy_digest = _canonical_isolation_policy(
            payload.get("isolation_policy")
        )
        if payload.get("isolation_policy_digest") != embedded_policy_digest:
            raise ExecutorAttestationError("attestation.isolation_policy_digest drifted")
        if prompt is not None:
            _, expected_prompt_digest = _canonical_prompt(prompt)
            if expected_prompt_digest != embedded_prompt_digest:
                raise ExecutorAttestationError("attestation.prompt drifted")
        if isolation_policy is not None:
            _, expected_policy_digest = _canonical_isolation_policy(isolation_policy)
            if expected_policy_digest != embedded_policy_digest:
                raise ExecutorAttestationError("attestation.isolation_policy drifted")
    except ExecutorAttestationError:
        raise
    except (ValueError, TypeError) as exc:
        raise ExecutorAttestationError(f"executor final attestation is invalid: {exc}") from exc
    return ExecutorFinalAttestation(
        task=payload["task"],
        arm=payload["arm"],
        session=payload["session"],
        thread=payload["thread"],
        mode=payload["mode"],
        run_id=payload["run_id"],
        attempt=payload["attempt"],
        nonce=payload["nonce"],
        issued_at=payload["issued_at"],
        patch_hash=payload["patch_hash"],
        final_tree_hash=payload["final_tree_hash"],
        server_generation=payload["server_generation"],
        receipt_log_digest=payload["receipt_log_digest"],
        receipt_count=payload["receipt_count"],
        receipt_head_digest=payload["receipt_head_digest"],
        prompt=embedded_prompt,
        prompt_digest=embedded_prompt_digest,
        isolation_policy=embedded_policy,
        isolation_policy_digest=embedded_policy_digest,
        signature=dict(value["signature"]),
    )


def consume_executor_final_attestation(
    value: Mapping[str, Any],
    *,
    replay_store: str | pathlib.Path,
    **validation: Any,
) -> ExecutorFinalAttestation:
    """Validate and atomically consume one attestation exactly once."""

    result = validate_executor_final_attestation(value, **validation)
    PersistentAttestationReplayStore(replay_store).consume(value)
    return result


def derive_canonical_arm_evidence_from_attestation(
    attestation: Mapping[str, Any],
    *,
    receipt_log: str | bytes,
    executor_verifier: Ed25519Verifier,
    receipt_verifier: Ed25519Verifier,
    task: str,
    arm: str,
    session: str,
    thread: str,
    patch_hash: str,
    final_tree_hash: str,
    server_generation: str,
    mode: str,
    run_id: str,
) -> CanonicalArmEvidenceProjection:
    """Project already verified native evidence into the runner arm shape."""

    attestation_attempt = _attestation_attempt(
        attestation.get("attempt"), "attestation.attempt"
    )
    attestation_nonce = _attestation_nonce(
        attestation.get("nonce"), "attestation.nonce"
    )
    attestation_issued_at = _attestation_text(
        attestation.get("issued_at"), "attestation.issued_at"
    )
    if mode == "no_lsp":
        validated = validate_executor_final_attestation(
            attestation,
            receipt_log=receipt_log,
            verifier=executor_verifier,
            receipt_verifier=receipt_verifier,
            task=task,
            arm=arm,
            session=session,
            thread=thread,
            patch_hash=patch_hash,
            final_tree_hash=final_tree_hash,
            server_generation=server_generation,
            mode=mode,
            run_id=run_id,
            attempt=attestation_attempt,
            nonce=attestation_nonce,
            issued_at=attestation_issued_at,
        )
        if validated.receipt_count != 0:
            raise ExecutorAttestationError("No-LSP projection contains receipt events")
        return CanonicalArmEvidenceProjection(
            mode=mode,
            task=task,
            arm=arm,
            session=session,
            thread=thread,
            receipts=(),
            isolation=attestation,
        )
    raw = _receipt_log_bytes(receipt_log) if receipt_log else b""
    records = (
        _validate_native_go_log(raw, verifier=receipt_verifier)[1]
        if raw.strip()
        else ()
    )
    # Validate the attestation against the exact chain before projecting rows.
    validate_executor_final_attestation(
        attestation,
        receipt_log=receipt_log,
        verifier=executor_verifier,
        receipt_verifier=receipt_verifier,
        task=task,
        arm=arm,
        session=session,
        thread=thread,
        patch_hash=patch_hash,
        final_tree_hash=final_tree_hash,
        server_generation=server_generation,
        mode=mode,
        run_id=run_id,
        attempt=attestation_attempt,
        nonce=attestation_nonce,
        issued_at=attestation_issued_at,
    )
    projections: list[NativeReceiptProjection] = []
    for record in records:
        payload = record["payload"]
        tool_text = payload.get("tool")
        if not isinstance(tool_text, str) or not tool_text.strip():
            raise ExecutorAttestationError("native receipt tool is missing")
        tool = tool_text.strip().lower().replace("/", ".").rsplit(".", 1)[-1].replace("-", "_")
        if tool not in {"structure", "xref", "diagnostics"}:
            raise ExecutorAttestationError(f"unsupported native receipt tool: {tool_text!r}")
        arguments = payload.get("arguments")
        result = payload.get("result")
        if not isinstance(arguments, Mapping) or "result" not in payload:
            raise ExecutorAttestationError("native receipt projection lacks canonical IO")
        projections.append(
            NativeReceiptProjection(
                task=task,
                arm=arm,
                session=session,
                thread=thread,
                seq=record["sequence"],
                request_id=_attestation_text(payload.get("receipt_id"), "native receipt_id"),
                tool=tool,
                arguments=dict(arguments),
                result=result,
                server_generation=server_generation,
                patch_hash=patch_hash,
                final_tree_hash=final_tree_hash,
            )
        )
    return CanonicalArmEvidenceProjection(
        mode=mode,
        task=task,
        arm=arm,
        session=session,
        thread=thread,
        receipts=tuple(projections),
        isolation=None,
    )


def parse_codex_jsonl(value: str | bytes) -> tuple[Mapping[str, Any], ...]:
    """Parse only JSON objects; malformed non-empty lines invalidate the turn."""

    text = value.decode("utf-8", errors="strict") if isinstance(value, bytes) else value
    if not isinstance(text, str) or not text.strip():
        raise ArtifactBridgeError("Codex JSONL is empty")
    events: list[Mapping[str, Any]] = []
    for number, line in enumerate(text.splitlines(), 1):
        if not line.strip():
            continue
        try:
            item = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ArtifactBridgeError(f"Codex JSONL line {number} is invalid") from exc
        if not isinstance(item, Mapping):
            raise ArtifactBridgeError(f"Codex JSONL line {number} is not an object")
        events.append(dict(item))
    if not events:
        raise ArtifactBridgeError("Codex JSONL contains no events")
    return tuple(events)


def dynamic_identity(events: Iterable[Mapping[str, Any]]) -> dict[str, str]:
    """Require exactly one session/thread identity from the real event stream."""

    thread_ids: set[str] = set()
    session_ids: set[str] = set()
    agent_ids: set[str] = set()
    for event in events:
        for field, target in (
            ("thread_id", thread_ids),
            ("session_id", session_ids),
            ("code_agent_id", agent_ids),
        ):
            value = event.get(field)
            if isinstance(value, str) and value.strip():
                target.add(value.strip())
        if event.get("type") == "thread.started":
            value = event.get("thread_id") or event.get("thread", {}).get("id") if isinstance(event.get("thread"), Mapping) else event.get("thread_id")
            if isinstance(value, str) and value.strip():
                thread_ids.add(value.strip())
    if not session_ids and len(thread_ids) == 1:
        session_ids = set(thread_ids)
    if len(thread_ids) != 1 or len(session_ids) != 1:
        raise ArtifactBridgeError(
            f"Codex identity is unavailable or ambiguous: sessions={sorted(session_ids)!r} threads={sorted(thread_ids)!r}"
        )
    if len(agent_ids) > 1:
        raise ArtifactBridgeError("Codex code_agent_id drifted within one turn")
    return {
        "session_id": next(iter(session_ids)),
        "thread_id": next(iter(thread_ids)),
        "code_agent_id": next(iter(agent_ids)) if agent_ids else "codex-agent",
    }


def usage_from_events(events: Iterable[Mapping[str, Any]]) -> dict[str, int]:
    """Extract the final cumulative token counters without inventing zeros."""

    candidates: list[Mapping[str, Any]] = []
    for event in events:
        for value in (event.get("usage"), event.get("token_usage")):
            if isinstance(value, Mapping):
                candidates.append(value)
    if not candidates:
        raise ArtifactBridgeError("Codex JSONL has no token usage")
    latest = candidates[-1]
    result: dict[str, int] = {}
    for field in ("input_tokens", "cached_input_tokens", "output_tokens", "reasoning_tokens"):
        value = (
            latest.get("reasoning_tokens", latest.get("reasoning_output_tokens"))
            if field == "reasoning_tokens"
            else latest.get(field)
        )
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            raise ArtifactBridgeError(f"Codex usage field {field} is missing or invalid")
        result[field] = value
    return result


def build_agent_manifest(
    *,
    events: Iterable[Mapping[str, Any]],
    patch_hash: str,
    tree_sha: str,
    changed_files: Iterable[str],
    mode: str,
) -> dict[str, Any]:
    """Build the exact manifest consumed by UbuntuExecutionDriver."""

    material = tuple(events)
    identity = dynamic_identity(material)
    files = sorted({str(path).strip() for path in changed_files if str(path).strip()})
    if len(patch_hash) != 64 or len(tree_sha) != 40:
        raise ArtifactBridgeError("patch_hash or tree_sha has an invalid length")
    return {
        "request": {
            "changed_files": files,
            "trajectory": [dict(item) for item in material],
            "patch_hash": patch_hash,
            "mode": mode,
        },
        **identity,
        "patch_hash": patch_hash,
        "tree_sha": tree_sha,
    }


__all__ = [
    "ArtifactBridgeError",
    "EXECUTOR_FINAL_ATTESTATION_KIND",
    "EXECUTOR_FINAL_ATTESTATION_SCHEMA",
    "ExecutorAttestationError",
    "ExecutorFinalAttestation",
    "build_agent_manifest",
    "build_executor_final_attestation",
    "build_no_lsp_executor_attestation",
    "consume_executor_final_attestation",
    "dynamic_identity",
    "parse_codex_jsonl",
    "PersistentAttestationReplayStore",
    "usage_from_events",
    "validate_executor_final_attestation",
]
