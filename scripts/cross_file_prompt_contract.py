#!/usr/bin/env python3
"""Fail-closed prompt inputs for the Cross-File A/B evaluation.

The benchmark has two independent prompt surfaces:

* ``task_prompt`` is the user/task text and must be byte-identical in both
  arms of one question; and
* shared developer instructions are split into a native baseline (present in
  both arms) and a conditional With-LSP addendum (present only in the
  With-LSP system layer).

``shared-developer-instructions.md`` is the only source for the latter two
surfaces. This module keeps their exact UTF-8 bytes and hashes together so
generators and execution drivers cannot accidentally turn the conditional
addendum into task text or silently use a stale prompt mirror.
"""

from __future__ import annotations

import dataclasses
import hashlib
import pathlib
import re
from collections.abc import Mapping
from typing import Any


PROMPT_CONTRACT_SCHEMA = "super-dolphin/cross-file-prompt-contract/v1"
CANONICAL_PROMPT_NAME = "shared-developer-instructions.md"
STALE_PROMPT_NAMES = frozenset({"lsp-smoke-addendum.md", "55-lsp-basics.md"})
NATIVE_BASELINE_MARKER = "## Native baseline"
LSP_ADDENDUM_MARKER = "## Conditional With-LSP addendum"
ALLOWED_ARM_DIFFERENCES = ("system.with_lsp_addendum",)
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
_PROMPT_FIELDS = frozenset(
    {
        "schema",
        "task_prompt",
        "task_prompt_sha256",
        "common_developer_instructions",
        "common_developer_instructions_sha256",
        "with_lsp_addendum",
        "with_lsp_addendum_sha256",
        "canonical_prompt_path",
        "canonical_prompt_sha256",
        "allowed_arm_differences",
    }
)
_LOCK_PROMPT_FIELDS = frozenset(
    {
        "schema",
        "canonical_prompt_path",
        "canonical_prompt_sha256",
        "common_developer_instructions_sha256",
        "with_lsp_addendum_sha256",
        "task_prompt_policy",
        "allowed_arm_differences",
    }
)


class PromptContractError(RuntimeError):
    """The runtime prompt artifact is missing, stale, or identity-drifted."""


def prompt_sha256(payload: bytes) -> str:
    """Return the raw lowercase SHA-256 digest for exact prompt bytes."""

    if not isinstance(payload, bytes):
        raise TypeError("prompt_sha256 expects bytes")
    return hashlib.sha256(payload).hexdigest()


def _digest_text(value: str, label: str) -> str:
    if not isinstance(value, str) or _SHA256_RE.fullmatch(value) is None:
        raise PromptContractError(f"{label} must be 64 lowercase hex characters")
    return value


def _prompt_bytes(value: str | bytes, label: str) -> bytes:
    if isinstance(value, bytes):
        payload = value
    elif isinstance(value, str):
        try:
            payload = value.encode("utf-8")
        except UnicodeEncodeError as exc:  # pragma: no cover - Python str invariant.
            raise PromptContractError(f"{label} must be UTF-8") from exc
    else:
        raise PromptContractError(f"{label} must be text or UTF-8 bytes")
    if not payload.strip():
        raise PromptContractError(f"{label} must not be empty")
    return payload


def _decode_prompt(payload: bytes, label: str) -> str:
    try:
        return payload.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise PromptContractError(f"{label} must be UTF-8") from exc


def _canonical_path(path: str | pathlib.Path) -> pathlib.Path:
    if not isinstance(path, (str, pathlib.Path)):
        raise PromptContractError("runtime prompt path must be text")
    candidate = pathlib.Path(path)
    if not candidate.is_absolute():
        raise PromptContractError("runtime prompt path must be absolute")
    if candidate.name in STALE_PROMPT_NAMES or candidate.name != CANONICAL_PROMPT_NAME:
        raise PromptContractError("runtime prompt must use the canonical latest artifact")
    return candidate


def _load_canonical_prompt_bytes(
    path: str | pathlib.Path, expected_sha256: str
) -> tuple[pathlib.Path, bytes]:
    candidate = _canonical_path(path)
    if candidate.is_symlink() or not candidate.is_file():
        raise PromptContractError("runtime prompt must be a regular non-symlink file")
    expected = _digest_text(expected_sha256, "runtime prompt sha256")
    try:
        payload = candidate.read_bytes()
    except OSError as exc:
        raise PromptContractError(f"read runtime prompt failed: {candidate}") from exc
    actual = prompt_sha256(payload)
    if actual != expected:
        raise PromptContractError(
            f"runtime prompt sha256 drifted: expected {expected}, got {actual}"
        )
    _decode_prompt(payload, "runtime prompt")
    if not payload.strip():
        raise PromptContractError("runtime prompt must not be empty")
    return candidate, payload


def split_shared_prompt(payload: bytes) -> tuple[bytes, bytes]:
    """Split canonical bytes into baseline and addendum without normalization.

    The returned slices concatenate byte-for-byte to ``payload``. The
    addendum slice includes its heading, which makes its system-layer
    placement explicit and prevents a caller from accidentally appending it
    to the task text.
    """

    raw = _prompt_bytes(payload, "shared developer instructions")
    marker = LSP_ADDENDUM_MARKER.encode("utf-8")
    if raw.count(marker) != 1:
        raise PromptContractError(
            "shared developer instructions require one conditional With-LSP marker"
        )
    marker_index = raw.index(marker)
    if marker_index and raw[marker_index - 1] not in (10, 13):
        raise PromptContractError("conditional With-LSP marker must start a line")
    baseline_marker = NATIVE_BASELINE_MARKER.encode("utf-8")
    if raw.count(baseline_marker) != 1 or raw.index(baseline_marker) >= marker_index:
        raise PromptContractError("shared developer instructions require one native baseline")
    common = raw[:marker_index]
    addendum = raw[marker_index:]
    if not common.strip() or not addendum[len(marker) :].strip():
        raise PromptContractError("shared prompt baseline or With-LSP addendum is empty")
    return common, addendum


@dataclasses.dataclass(frozen=True)
class PromptContract:
    """Exact prompt material and identity for one question's two arms."""

    task_prompt: bytes
    common_developer_instructions: bytes
    with_lsp_addendum: bytes
    canonical_prompt_path: str
    canonical_prompt_sha256: str
    allowed_arm_differences: tuple[str, ...] = ALLOWED_ARM_DIFFERENCES

    def __post_init__(self) -> None:
        task = _prompt_bytes(self.task_prompt, "task_prompt")
        common = _prompt_bytes(
            self.common_developer_instructions, "common_developer_instructions"
        )
        addendum = _prompt_bytes(self.with_lsp_addendum, "with_lsp_addendum")
        path = _canonical_path(self.canonical_prompt_path)
        canonical_sha = _digest_text(
            self.canonical_prompt_sha256, "canonical_prompt_sha256"
        )
        if prompt_sha256(common + addendum) != canonical_sha:
            raise PromptContractError(
                "common developer instructions and With-LSP addendum do not reconstruct canonical prompt"
            )
        if NATIVE_BASELINE_MARKER.encode("utf-8") not in common:
            raise PromptContractError("common developer instructions must contain native baseline")
        if (
            not isinstance(self.allowed_arm_differences, (list, tuple))
            or tuple(self.allowed_arm_differences) != ALLOWED_ARM_DIFFERENCES
        ):
            raise PromptContractError(
                "allowed_arm_differences must contain only system.with_lsp_addendum"
            )
        object.__setattr__(self, "task_prompt", task)
        object.__setattr__(self, "common_developer_instructions", common)
        object.__setattr__(self, "with_lsp_addendum", addendum)
        object.__setattr__(self, "canonical_prompt_path", str(path))

    @property
    def task_prompt_sha256(self) -> str:
        """Hash the exact task prompt bytes shared by both arms."""

        return prompt_sha256(self.task_prompt)

    @property
    def common_developer_instructions_sha256(self) -> str:
        """Hash the exact baseline bytes present in both arms."""

        return prompt_sha256(self.common_developer_instructions)

    @property
    def with_lsp_addendum_sha256(self) -> str:
        """Hash the exact conditional addendum bytes."""

        return prompt_sha256(self.with_lsp_addendum)

    def system_overlay(self, mode: str) -> bytes:
        """Return the mode-specific system overlay, never task text."""

        normalized = mode.lower().replace("-", "_") if isinstance(mode, str) else mode
        if normalized == "no_lsp":
            return b""
        if normalized == "with_lsp":
            return self.with_lsp_addendum
        raise PromptContractError(f"unsupported prompt arm mode: {mode!r}")

    def effective_system_prompt(self, mode: str) -> bytes:
        """Return the final system bytes after common instructions are injected."""

        return self.common_developer_instructions + self.system_overlay(mode)

    # ``system_prompt`` is retained as the explicit final-effective alias for
    # callers that do not need the overlay distinction.
    system_prompt = effective_system_prompt

    def arm_payload(self, mode: str) -> dict[str, str]:
        """Expose task/system fields separately for an execution driver."""

        system = self.effective_system_prompt(mode)
        overlay = self.system_overlay(mode)
        return {
            "mode": mode,
            "task_prompt": _decode_prompt(self.task_prompt, "task_prompt"),
            "task_prompt_sha256": self.task_prompt_sha256,
            "system_prompt": _decode_prompt(system, "system_prompt"),
            "system_prompt_sha256": prompt_sha256(system),
            "system_overlay": _decode_prompt(overlay, "system_overlay")
            if overlay
            else "",
            "system_overlay_sha256": prompt_sha256(overlay),
        }

    def to_dict(self) -> dict[str, Any]:
        """Return the strict JSON-compatible contract representation."""

        return {
            "schema": PROMPT_CONTRACT_SCHEMA,
            "task_prompt": _decode_prompt(self.task_prompt, "task_prompt"),
            "task_prompt_sha256": self.task_prompt_sha256,
            "common_developer_instructions": _decode_prompt(
                self.common_developer_instructions, "common_developer_instructions"
            ),
            "common_developer_instructions_sha256": self.common_developer_instructions_sha256,
            "with_lsp_addendum": _decode_prompt(
                self.with_lsp_addendum, "with_lsp_addendum"
            ),
            "with_lsp_addendum_sha256": self.with_lsp_addendum_sha256,
            "canonical_prompt_path": self.canonical_prompt_path,
            "canonical_prompt_sha256": self.canonical_prompt_sha256,
            "allowed_arm_differences": list(self.allowed_arm_differences),
        }

    @classmethod
    def from_dict(cls, value: Mapping[str, Any]) -> "PromptContract":
        """Parse and verify a serialized contract without implicit defaults."""

        if not isinstance(value, Mapping):
            raise PromptContractError("prompt_contract must be an object")
        unknown = sorted(str(key) for key in value if key not in _PROMPT_FIELDS)
        if unknown:
            raise PromptContractError(
                "prompt_contract has unknown field(s): " + ", ".join(unknown)
            )
        missing = sorted(_PROMPT_FIELDS - set(value))
        if missing:
            raise PromptContractError(
                "prompt_contract missing field(s): " + ", ".join(missing)
            )
        if value["schema"] != PROMPT_CONTRACT_SCHEMA:
            raise PromptContractError("unsupported prompt contract schema")
        for key in (
            "task_prompt_sha256",
            "common_developer_instructions_sha256",
            "with_lsp_addendum_sha256",
            "canonical_prompt_sha256",
        ):
            _digest_text(value[key], key)
        allowed = value["allowed_arm_differences"]
        if not isinstance(allowed, (list, tuple)) or tuple(allowed) != ALLOWED_ARM_DIFFERENCES:
            raise PromptContractError(
                "allowed_arm_differences must contain only system.with_lsp_addendum"
            )
        contract = cls(
            task_prompt=_prompt_bytes(value["task_prompt"], "task_prompt"),
            common_developer_instructions=_prompt_bytes(
                value["common_developer_instructions"], "common_developer_instructions"
            ),
            with_lsp_addendum=_prompt_bytes(
                value["with_lsp_addendum"], "with_lsp_addendum"
            ),
            canonical_prompt_path=value["canonical_prompt_path"],
            canonical_prompt_sha256=value["canonical_prompt_sha256"],
            allowed_arm_differences=tuple(allowed),
        )
        for key, payload in (
            ("task_prompt_sha256", contract.task_prompt),
            ("common_developer_instructions_sha256", contract.common_developer_instructions),
            ("with_lsp_addendum_sha256", contract.with_lsp_addendum),
        ):
            if value[key] != prompt_sha256(payload):
                raise PromptContractError(f"{key} drifted")
        return contract


def load_canonical_prompt(path: str | pathlib.Path, expected_sha256: str) -> str:
    """Load the canonical prompt as exact UTF-8 text for legacy consumers."""

    _, payload = _load_canonical_prompt_bytes(path, expected_sha256)
    return _decode_prompt(payload, "runtime prompt")


def load_prompt_contract(
    canonical_prompt_path: str | pathlib.Path,
    expected_sha256: str,
    task_prompt: str | bytes,
) -> PromptContract:
    """Load canonical instructions and build one question's prompt contract."""

    candidate, payload = _load_canonical_prompt_bytes(
        canonical_prompt_path, expected_sha256
    )
    common, addendum = split_shared_prompt(payload)
    return PromptContract(
        task_prompt=_prompt_bytes(task_prompt, "task_prompt"),
        common_developer_instructions=common,
        with_lsp_addendum=addendum,
        canonical_prompt_path=str(candidate),
        canonical_prompt_sha256=prompt_sha256(payload),
    )


def build_prompt_contract(
    task_prompt: str | bytes,
    canonical_prompt_path: str | pathlib.Path,
    canonical_prompt_sha256: str,
) -> PromptContract:
    """Explicit alias for callers that prefer a construction verb."""

    return load_prompt_contract(canonical_prompt_path, canonical_prompt_sha256, task_prompt)


def prompt_identity_material(contract: PromptContract | Mapping[str, Any]) -> dict[str, str]:
    """Return all prompt hashes that must be covered by arm identity."""

    parsed = contract if isinstance(contract, PromptContract) else PromptContract.from_dict(contract)
    return {
        "task_prompt_sha256": parsed.task_prompt_sha256,
        "common_developer_instructions_sha256": parsed.common_developer_instructions_sha256,
        "with_lsp_addendum_sha256": parsed.with_lsp_addendum_sha256,
        "canonical_prompt_sha256": parsed.canonical_prompt_sha256,
    }


def validate_manifest_prompt_contract(
    manifest: Mapping[str, Any], *, repository_root: str | pathlib.Path
) -> dict[str, Any]:
    """Validate a lock manifest's prompt identity against the canonical file.

    Lock manifests intentionally carry no single task prompt because each of
    their questions has different issue text. They do carry the shared
    instruction identities and declare that task text is bound per question.
    The canonical file is read here, so a lock cannot pass with hand-written
    or stale baseline/addendum hashes.
    """

    if not isinstance(manifest, Mapping):
        raise PromptContractError("prompt lock manifest must be an object")
    if manifest.get("schema") != "lsp-benchmark-experiment/v4":
        raise PromptContractError("prompt lock manifest must declare schema v4")
    if manifest.get("prompt_contract_schema") != PROMPT_CONTRACT_SCHEMA:
        raise PromptContractError("prompt lock manifest prompt contract schema drifted")
    raw = manifest.get("prompt_contract")
    if not isinstance(raw, Mapping):
        raise PromptContractError("prompt lock manifest prompt_contract must be an object")
    unknown = sorted(str(key) for key in raw if key not in _LOCK_PROMPT_FIELDS)
    if unknown:
        raise PromptContractError(
            "prompt lock manifest prompt_contract has unknown field(s): "
            + ", ".join(unknown)
        )
    missing = sorted(_LOCK_PROMPT_FIELDS - set(raw))
    if missing:
        raise PromptContractError(
            "prompt lock manifest prompt_contract missing field(s): "
            + ", ".join(missing)
        )
    if raw["schema"] != PROMPT_CONTRACT_SCHEMA:
        raise PromptContractError("prompt lock manifest prompt contract schema drifted")
    if raw["task_prompt_policy"] != "per-question-byte-identical":
        raise PromptContractError("prompt lock manifest task prompt policy drifted")
    allowed = raw["allowed_arm_differences"]
    if not isinstance(allowed, (list, tuple)) or tuple(allowed) != ALLOWED_ARM_DIFFERENCES:
        raise PromptContractError(
            "prompt lock manifest allowed_arm_differences drifted"
        )
    root = pathlib.Path(repository_root)
    if not root.is_absolute():
        raise PromptContractError("repository_root must be absolute")
    if not isinstance(raw["canonical_prompt_path"], str):
        raise PromptContractError(
            "prompt lock manifest canonical_prompt_path must be text"
        )
    declared_path = pathlib.Path(raw["canonical_prompt_path"])
    if declared_path.is_absolute():
        candidate = declared_path
    else:
        candidate = root / declared_path
    candidate, payload = _load_canonical_prompt_bytes(
        candidate, _digest_text(raw["canonical_prompt_sha256"], "canonical_prompt_sha256")
    )
    common, addendum = split_shared_prompt(payload)
    expected_common = _digest_text(
        raw["common_developer_instructions_sha256"],
        "common_developer_instructions_sha256",
    )
    expected_addendum = _digest_text(
        raw["with_lsp_addendum_sha256"], "with_lsp_addendum_sha256"
    )
    if prompt_sha256(common) != expected_common:
        raise PromptContractError("prompt lock manifest common instructions hash drifted")
    if prompt_sha256(addendum) != expected_addendum:
        raise PromptContractError("prompt lock manifest With-LSP addendum hash drifted")
    raw_top_level_sha = manifest.get("prompt_sha256")
    if not isinstance(raw_top_level_sha, str):
        raise PromptContractError("prompt lock manifest prompt_sha256 must be text")
    top_level_sha = _digest_text(raw_top_level_sha, "prompt_sha256")
    if top_level_sha != prompt_sha256(payload):
        raise PromptContractError("prompt lock manifest prompt_sha256 drifted")
    return {
        "schema": PROMPT_CONTRACT_SCHEMA,
        "canonical_prompt_path": str(candidate),
        "canonical_prompt_sha256": top_level_sha,
        "common_developer_instructions_sha256": expected_common,
        "with_lsp_addendum_sha256": expected_addendum,
        "task_prompt_policy": raw["task_prompt_policy"],
        "allowed_arm_differences": list(ALLOWED_ARM_DIFFERENCES),
    }


def validate_prompt_pair(
    no_lsp: PromptContract | Mapping[str, Any],
    with_lsp: PromptContract | Mapping[str, Any],
) -> None:
    """Fail closed unless two arm contracts differ only in allowed system text."""

    first = no_lsp if isinstance(no_lsp, PromptContract) else PromptContract.from_dict(no_lsp)
    second = with_lsp if isinstance(with_lsp, PromptContract) else PromptContract.from_dict(with_lsp)
    if first.task_prompt != second.task_prompt:
        raise PromptContractError("two arms must use byte-identical task_prompt")
    if first.task_prompt_sha256 != second.task_prompt_sha256:
        raise PromptContractError("two arms task_prompt_sha256 drifted")
    for label in (
        "common_developer_instructions",
        "with_lsp_addendum",
        "canonical_prompt_path",
        "canonical_prompt_sha256",
    ):
        if getattr(first, label) != getattr(second, label):
            raise PromptContractError(f"two arms {label} drifted")
    if first.allowed_arm_differences != second.allowed_arm_differences:
        raise PromptContractError("two arms allowed_arm_differences drifted")


# Descriptive aliases keep the contract discoverable to callers using either
# "prompt pair" or "arm pair" terminology.
validate_prompt_contract_pair = validate_prompt_pair
split_canonical_prompt = split_shared_prompt


__all__ = [
    "ALLOWED_ARM_DIFFERENCES",
    "CANONICAL_PROMPT_NAME",
    "LSP_ADDENDUM_MARKER",
    "NATIVE_BASELINE_MARKER",
    "PROMPT_CONTRACT_SCHEMA",
    "PromptContract",
    "PromptContractError",
    "build_prompt_contract",
    "load_canonical_prompt",
    "load_prompt_contract",
    "prompt_identity_material",
    "prompt_sha256",
    "split_canonical_prompt",
    "split_shared_prompt",
    "validate_prompt_contract_pair",
    "validate_manifest_prompt_contract",
    "validate_prompt_pair",
]
