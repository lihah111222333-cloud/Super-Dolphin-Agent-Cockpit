# T04 - Redacted Model-Facing Projection

Depends on: T01

## Objective

Convert raw trace events into a bounded, redacted diagnosis suitable for model exposure.

## Source Anchors

- `internal/platform/observability/sanitizer.go:13-17`
- `internal/platform/observability/sanitizer.go:122-148`
- `internal/module/observability/rpc.go:66-73`
- `internal/module/observability/rpc.go:234-244`

## Scope

Implement projection logic used by `DiagnoseTrace`:

- timeline summaries
- slow span summaries
- error and panic summaries
- code anchors
- bounded stack summaries
- related thread, turn, call, and tool ids

## Requirements

- Do not expose raw `Metadata` except explicitly allowlisted scalar keys.
- Omit prompt text, file snippets, and arbitrary user content.
- Redact PII-like and secret-like values before model exposure.
- Normalize file and stack paths to repo-relative paths using `WorkspaceRoot` or `CWD` only after canonicalizing and cleaning both root and candidate path.
- Path matching must be boundary-aware. A candidate is inside a root only when `filepath.Rel(root, candidate)` succeeds and the relative path is not absolute, does not equal `..`, and does not start with `../`.
- Do not use plain string prefix checks for path containment; sibling prefixes such as `/home/u/repo-secret` must not match `/home/u/repo`.
- Replace any absolute path outside `WorkspaceRoot` or `CWD` with `[REDACTED_PATH]` in every model-facing string, including `tail_error`, `tail_warnings`, error summaries, stack summaries, code anchors, and omitted-key diagnostics.
- Apply the same path and PII scrubbing to model-facing strings produced from filesystem errors, decode errors, panic messages, and warning text.
- Bound timeline length, string size, error summaries, stack frame count, and serialized payload size according to T01.

Allowed metadata keys:

- `component`
- `operation`
- `provider`
- `model`
- `tool_name`
- `method`
- `status`
- `error_kind`
- `duration_ms`
- `latency_ms`
- `attempt`
- `retry_count`

All other metadata keys must be omitted by default. If omitted keys are reported for debugging, expose only sanitized key names that match `^[A-Za-z0-9_.:-]{1,64}$` and are not sensitive by name; replace all other key names with `[REDACTED_KEY]`.

PII-like values include at least:

- email addresses;
- phone-number-like strings;
- home-directory paths and usernames;
- absolute filesystem paths outside `WorkspaceRoot` or `CWD`;
- IP addresses and hostnames unless explicitly allowlisted later;
- values whose key name contains `user`, `email`, `phone`, `name`, `address`, `token`, `secret`, `key`, `password`, `credential`, `cookie`, or `auth`.

Sensitive metadata key names include any key containing `user`, `email`, `phone`, `name`, `address`, `token`, `secret`, `key`, `password`, `credential`, `cookie`, or `auth`, even if the key name is regex-safe.

Secret-like and PII-like values must be replaced with `[REDACTED]`; path values that cannot be normalized to repo-relative form must be replaced with `[REDACTED_PATH]`.

## Acceptance Criteria

- Diagnosis output is not a raw `TraceEvent` passthrough.
- Tests can assert raw metadata and raw event bodies are absent.
- Output remains useful for locating performance and bug causes through event type, timing, ids, anchors, and summarized errors.
- Large traces remain under the T01 payload bound after projection.
- Tests cover absolute paths, sibling-prefix paths outside the workspace, `..` relative results, email-like values, phone-like values, username/home-directory values, IP or hostname values, secret-like values, sensitive metadata key names, and unsafe omitted-key names.
