#!/usr/bin/env python3
"""Verify native Go mcp-lsp v2 append-only receipt streams."""

from __future__ import annotations

import base64
import hashlib
import json
from collections.abc import Iterable, Mapping
from typing import Any

from .cross_file_signed_receipts import Ed25519Verifier, SignatureVerificationError


SCHEMA = "super-dolphin/mcp-lsp-tool-receipt/v2"


class GoReceiptError(ValueError):
    """A native Go receipt chain is malformed, replayed, or unsigned."""


def _go_json(value: Mapping[str, Any]) -> bytes:
    # Go json.Marshal preserves struct-field order. json.loads preserves the
    # order from the signed line, so re-encoding without sort_keys reconstructs
    # the unsigned struct material.
    text = json.dumps(value, ensure_ascii=False, separators=(",", ":"))
    text = text.replace("<", r"\u003c").replace(">", r"\u003e").replace("&", r"\u0026")
    text = text.replace("\u2028", r"\u2028").replace("\u2029", r"\u2029")
    return text.encode("utf-8")


def validate_go_receipt_stream(
    lines: str | bytes | Iterable[str], *, verifier: Ed25519Verifier
) -> tuple[Mapping[str, Any], ...]:
    """Validate sequence, hash chain, public-key pin and signature for v2."""

    if isinstance(lines, bytes):
        source = lines.decode("utf-8", errors="strict").splitlines()
    elif isinstance(lines, str):
        source = lines.splitlines()
    else:
        source = list(lines)
    expected_seq = 1
    previous = ""
    result: list[Mapping[str, Any]] = []
    public_raw = base64.b64decode(verifier.binding.public_key, validate=True)
    public_hash = "sha256:" + hashlib.sha256(public_raw).hexdigest()
    for line_number, line in enumerate(source, 1):
        if not str(line).strip():
            continue
        try:
            record = json.loads(str(line))
        except json.JSONDecodeError as exc:
            raise GoReceiptError(f"Go receipt line {line_number} is invalid JSON") from exc
        if not isinstance(record, Mapping):
            raise GoReceiptError(f"Go receipt line {line_number} is not an object")
        if record.get("schema") != SCHEMA:
            raise GoReceiptError(f"Go receipt line {line_number} has unsupported schema")
        if record.get("sequence") != expected_seq:
            raise GoReceiptError(f"Go receipt line {line_number} sequence is not contiguous")
        if record.get("previous_digest", "") != previous:
            raise GoReceiptError(f"Go receipt line {line_number} previous_digest drifted")
        if record.get("signer_public_key_sha256") != public_hash:
            raise GoReceiptError(f"Go receipt line {line_number} signer key drifted")
        payload = record.get("payload")
        if not isinstance(payload, Mapping) or "arguments" not in payload or "result" not in payload:
            raise GoReceiptError(f"Go receipt line {line_number} lacks canonical IO")
        unsigned = {
            key: record[key]
            for key in ("schema", "sequence", "previous_digest", "payload", "signer_public_key_sha256")
            if key in record
        }
        material = _go_json(unsigned)
        digest = "sha256:" + hashlib.sha256(material).hexdigest()
        if record.get("event_digest") != digest:
            raise GoReceiptError(f"Go receipt line {line_number} event_digest drifted")
        try:
            signature = base64.b64decode(record.get("signature", ""), validate=True)
            verifier.verify(material, signature)
        except (ValueError, SignatureVerificationError) as exc:
            raise GoReceiptError(f"Go receipt line {line_number} signature rejected") from exc
        result.append(dict(record))
        previous = digest
        expected_seq += 1
    if not result:
        raise GoReceiptError("Go receipt stream is empty")
    return tuple(result)


__all__ = ["GoReceiptError", "SCHEMA", "validate_go_receipt_stream"]
