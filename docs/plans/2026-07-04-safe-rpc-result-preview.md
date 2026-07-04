# Safe RPC Result Preview

## Problem

`api.rpc.done` bridge diagnostics currently stringify successful RPC results into `result_preview`.
When frontend trace debug is enabled, or when a call is slow, this can expose prompts,
profile data, local paths, tool output bodies, and token-like fields in the UI log store.
Runtime result helpers then display that preview as visible activity detail.

## Scope

- Replace raw successful RPC previews with a structure-preserving safe preview.
- Preserve useful numeric and boolean diagnostics such as counts and status flags.
- Drop known sensitive keys and redact free-text string leaves.
- Defensively sanitize legacy `result_preview` strings before runtime result display.

## Verification

- Add focused regression coverage for bridge diagnostics and runtime result display.
- Run targeted tests first, then the full frontend lint/test/build gate.
