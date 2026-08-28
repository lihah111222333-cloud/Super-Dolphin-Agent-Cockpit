#!/usr/bin/env python3
"""Canonical, non-scoring execution metrics for Cross-File Agent turns.

The benchmark runner and auditor own score truth.  This module only records
what the executor observed on the Agent transport: whether the LSP treatment
was available, whether the Agent adopted it, the concrete tool-call counts,
prompt sections, and token accounting.  It deliberately rejects missing raw
usage instead of turning an unavailable transport counter into zero.
"""

from __future__ import annotations

import hashlib
from collections.abc import Iterable, Mapping, Sequence
from decimal import Decimal
from typing import Any


SCHEMA = "super-dolphin/cross-file-execution-metrics/v1"
LSP_TOOLS = frozenset({"structure", "xref", "diagnostics"})
CACHED_INPUT_DISCOUNT = Decimal("0.9")


class ExecutionMetricsError(ValueError):
    """Observed execution data is malformed or incomplete."""


def _text(value: Any, label: str) -> str:
    if not isinstance(value, str) or not value:
        raise ExecutionMetricsError(f"{label} must be non-empty text")
    return value


def _non_negative_int(value: Any, label: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ExecutionMetricsError(f"{label} must be a non-negative integer")
    return value


def _digest(value: str) -> str:
    return hashlib.sha256(value.encode("utf-8")).hexdigest()


def _count_token_fields(usage: Mapping[str, Any]) -> dict[str, int]:
    """Normalize raw Codex usage while keeping all counters authoritative."""

    def pick(*names: str) -> Any:
        for name in names:
            if name in usage:
                return usage[name]
        return None

    raw_input = pick("input_tokens", "prompt_tokens")
    raw_cached = pick("cached_input_tokens", "cache_read_input_tokens", "cached_tokens")
    raw_cache_write = pick("cache_write_input_tokens")
    raw_output = pick("output_tokens", "completion_tokens")
    raw_reasoning = pick("reasoning_output_tokens", "reasoning_tokens")
    if raw_input is None or raw_cached is None or raw_output is None:
        raise ExecutionMetricsError(
            "usage must contain input_tokens, cached_input_tokens, and output_tokens"
        )
    counts = {
        "input_tokens": _non_negative_int(raw_input, "usage.input_tokens"),
        "cached_input_tokens": _non_negative_int(raw_cached, "usage.cached_input_tokens"),
        "cache_write_input_tokens": (
            0
            if raw_cache_write is None
            else _non_negative_int(raw_cache_write, "usage.cache_write_input_tokens")
        ),
        "output_tokens": _non_negative_int(raw_output, "usage.output_tokens"),
        "reasoning_output_tokens": (
            0
            if raw_reasoning is None
            else _non_negative_int(raw_reasoning, "usage.reasoning_output_tokens")
        ),
    }
    if counts["cached_input_tokens"] > counts["input_tokens"]:
        raise ExecutionMetricsError("cached_input_tokens cannot exceed input_tokens")
    return counts


def _scaled_decimal(value: Decimal) -> int | float:
    """Return a JSON-native exact integer where possible, otherwise a float."""

    if value == value.to_integral_value():
        return int(value)
    return float(value)


def _normalise_tool(value: Any) -> str | None:
    if not isinstance(value, str) or not value.strip():
        return None
    return value.strip().lower().replace("/", ".").rsplit(".", 1)[-1].replace("-", "_")


def _event_type(event: Mapping[str, Any]) -> str:
    value = event.get("type", event.get("event", ""))
    return value.strip().lower() if isinstance(value, str) else ""


def _tool_from_event(event: Mapping[str, Any]) -> str | None:
    """Extract only actual MCP/tool-call events, not tool definitions."""

    typ = _event_type(event)
    nested = event.get("item")
    item = nested if isinstance(nested, Mapping) else event
    candidate = None
    for key in ("tool", "tool_name", "toolName", "name"):
        if key in item:
            candidate = item.get(key)
            break
    tool = _normalise_tool(candidate)
    if tool not in LSP_TOOLS:
        return None
    item_type = item.get("type")
    item_type_text = item_type.strip().lower() if isinstance(item_type, str) else ""
    if "output" in item_type_text or "result" in item_type_text:
        return None
    call_marker = (
        "tool" in typ
        or "mcp" in typ
        or typ in {"function_call", "function.call", "tool_call", "tool.call"}
        or "tool" in item_type_text
        or "mcp" in item_type_text
        or item_type_text in {"function_call", "function.call"}
    )
    if not call_marker:
        return None
    # A completion/result event is still an observation of the same call only
    # when it carries an explicit call id or arguments.  This prevents a
    # generic tool-result summary from double counting a call.
    if any(key in item for key in ("call_id", "callId", "arguments", "input", "result")):
        return tool
    return tool if "call" in typ or "invoke" in typ or "request" in typ else None


def _tool_calls(events: Iterable[Mapping[str, Any]]) -> tuple[int, dict[str, int]]:
    by_tool = {tool: 0 for tool in sorted(LSP_TOOLS)}
    seen_call_ids: set[str] = set()
    for event in events:
        if not isinstance(event, Mapping):
            continue
        tool = _tool_from_event(event)
        if tool is None:
            continue
        nested = event.get("item")
        item = nested if isinstance(nested, Mapping) else event
        call_id = next(
            (
                str(item[key]).strip()
                for key in ("call_id", "callId", "id")
                if isinstance(item.get(key), (str, int)) and str(item[key]).strip()
            ),
            None,
        )
        # Codex JSONL may expose both a start and a completion event.  Count
        # the call once when a stable id is present; id-less events are each
        # direct observations and cannot be safely coalesced.
        if call_id is not None:
            if call_id in seen_call_ids:
                continue
            seen_call_ids.add(call_id)
        by_tool[tool] += 1
    return sum(by_tool.values()), by_tool


def _prompt_section(value: Any, label: str) -> dict[str, Any]:
    text = _text(value, label)
    return {"bytes": len(text.encode("utf-8")), "sha256": _digest(text)}


def _normalise_attempts(attempts: Sequence[Mapping[str, Any]]) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for index, attempt in enumerate(attempts, 1):
        if not isinstance(attempt, Mapping):
            raise ExecutionMetricsError(f"attempt {index} must be an object")
        row: dict[str, Any] = {}
        for key, value in attempt.items():
            if isinstance(value, (str, int, float, bool)) or value is None:
                row[_text(key, f"attempt {index} field")] = value
            else:
                raise ExecutionMetricsError(f"attempt {index}.{key} is not scalar")
        rows.append(row)
    return rows


def build_execution_metrics(
    *,
    mode: str,
    prompt: str,
    events: Iterable[Mapping[str, Any]],
    usage: Mapping[str, Any],
    lsp_available: bool | None = None,
    prompt_sections: Mapping[str, str] | None = None,
    attempts: Sequence[Mapping[str, Any]] = (),
) -> dict[str, Any]:
    """Build one formal metrics record from real transport observations."""

    if mode not in {"no_lsp", "with_lsp"}:
        raise ExecutionMetricsError(f"unsupported mode: {mode!r}")
    prompt_text = _text(prompt, "prompt")
    if not isinstance(usage, Mapping):
        raise ExecutionMetricsError("usage must be an object")
    counts = _count_token_fields(usage)
    material = tuple(event for event in events if isinstance(event, Mapping))
    calls, by_tool = _tool_calls(material)
    available = (mode == "with_lsp") if lsp_available is None else lsp_available
    if not isinstance(available, bool):
        raise ExecutionMetricsError("lsp_available must be boolean")
    if mode == "no_lsp" and available:
        raise ExecutionMetricsError("No-LSP execution cannot report lsp_available=true")
    sections = {"task": _prompt_section(prompt_text, "prompt")}
    if prompt_sections is not None:
        for name, value in prompt_sections.items():
            section_name = _text(name, "prompt section name")
            if section_name == "task":
                if value != prompt_text:
                    raise ExecutionMetricsError("prompt_sections.task does not match prompt")
                continue
            sections[section_name] = _prompt_section(value, f"prompt_sections.{section_name}")
    uncached = counts["input_tokens"] - counts["cached_input_tokens"]
    cached_at_discount = Decimal(counts["cached_input_tokens"]) * CACHED_INPUT_DISCOUNT
    effective_input = Decimal(uncached) + cached_at_discount
    tokens = {
        **counts,
        "uncached_input_tokens": uncached,
        "cached_input_tokens_90pct": _scaled_decimal(cached_at_discount),
        "effective_input_tokens": _scaled_decimal(effective_input),
        "total_tokens": (
            counts["input_tokens"]
            + counts["output_tokens"]
            + counts["reasoning_output_tokens"]
        ),
        "cached_input_discount": float(CACHED_INPUT_DISCOUNT),
    }
    # Attempts are transport observations; preserve only scalar fields so a
    # caller cannot smuggle an arbitrary object into the formal record.
    attempt_rows = _normalise_attempts(attempts)
    result: dict[str, Any] = {
        "schema": SCHEMA,
        "mode": mode,
        "lsp_available": available,
        "adopted": bool(available and mode == "with_lsp" and calls > 0),
        "calls": calls,
        "prompt": {"bytes": len(prompt_text.encode("utf-8")), "sha256": _digest(prompt_text), "sections": sections},
        "tool": {"calls": calls, "by_name": by_tool},
        "tokens": tokens,
    }
    if attempt_rows:
        result["transport_attempts"] = attempt_rows
    return result


def build_unavailable_execution_metrics(
    *,
    mode: str,
    prompt: str,
    reason: str,
    lsp_available: bool | None = None,
    prompt_sections: Mapping[str, str] | None = None,
    attempts: Sequence[Mapping[str, Any]] = (),
) -> dict[str, Any]:
    """Record an incomplete transport without fabricating token counters."""

    if mode not in {"no_lsp", "with_lsp"}:
        raise ExecutionMetricsError(f"unsupported mode: {mode!r}")
    prompt_text = _text(prompt, "prompt")
    reason_text = _text(reason, "reason")
    available = (mode == "with_lsp") if lsp_available is None else lsp_available
    if not isinstance(available, bool):
        raise ExecutionMetricsError("lsp_available must be boolean")
    if mode == "no_lsp" and available:
        raise ExecutionMetricsError("No-LSP execution cannot report lsp_available=true")
    sections = {"task": _prompt_section(prompt_text, "prompt")}
    if prompt_sections is not None:
        for name, value in prompt_sections.items():
            if name == "task":
                if value != prompt_text:
                    raise ExecutionMetricsError("prompt_sections.task does not match prompt")
                continue
            sections[_text(name, "prompt section name")] = _prompt_section(value, f"prompt_sections.{name}")
    result: dict[str, Any] = {
        "schema": SCHEMA,
        "status": "unavailable",
        "reason": reason_text,
        "mode": mode,
        "lsp_available": available,
        "adopted": False,
        "calls": 0,
        "prompt": {"bytes": len(prompt_text.encode("utf-8")), "sha256": _digest(prompt_text), "sections": sections},
        "tool": {"calls": 0, "by_name": {tool: 0 for tool in sorted(LSP_TOOLS)}},
        "tokens": {"status": "unavailable"},
    }
    if attempts:
        result["transport_attempts"] = _normalise_attempts(attempts)
    return result


__all__ = [
    "CACHED_INPUT_DISCOUNT",
    "ExecutionMetricsError",
    "LSP_TOOLS",
    "SCHEMA",
    "build_execution_metrics",
    "build_unavailable_execution_metrics",
]
