# Safe RPC Error Trace

## Problem

Failed Wails RPC traces send `safeTraceErrorMessage(error)` to
`observability/frontend/ingest`. The current text filter blocks prompt/content/result
terms but does not reject credential-like strings or absolute local paths embedded in
error messages, so failure telemetry can persist sensitive diagnostics remotely.

## Scope

- Treat credential key patterns and token-like values as unsafe trace error text.
- Treat absolute local filesystem paths as unsafe trace error text.
- Preserve the original thrown RPC error for callers and local bridge diagnostics.
- Keep the fix inside the Wails bridge trace sanitizer and focused tests.

## Verification

- Add a failing regression for failed RPC ingest payloads containing token/password/api_key
  values and local absolute paths.
- Run the focused Wails bridge test, then full frontend lint, test, and build.
