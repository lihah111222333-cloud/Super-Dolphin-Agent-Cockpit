---
name: trace-diagnosis
description: Use when the user provides trace_id, traceid, traceparent, span_id, or asks to diagnose slow requests, local observability JSONL logs, or distributed tracing.
version: "1.0.0"
---

# Trace Diagnosis

When the user gives a trace id or slow-request tracing clue:

1. Prefer `observability_trace_get` when it is available.
2. Pass the trace id as `trace_id`; use `force_refresh: true` for fresh slow-request investigations.
3. Set `include_stack: true` only when errors, panics, or stack details are relevant.
4. Read the structured diagnosis first: slow spans, errors, warnings, degraded flags, and tail cost.
5. If the tool is unavailable, say the session lacks the observability trace tool; do not guess from unrelated logs.
