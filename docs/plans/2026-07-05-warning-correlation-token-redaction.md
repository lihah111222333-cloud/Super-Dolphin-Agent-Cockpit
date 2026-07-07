# Warning Correlation Token Redaction Repair

**Goal:** prevent `safeWarningFields()` from rehydrating token-shaped values into allowlisted warning correlation fields after the generic diagnostic preview redacts them.

**Scope:** frontend warning runtime ingestion only.

**Chosen candidate:** Q, `safeWarningFields` allowlisted correlation fields leak token-shaped values in `reason`, `code`, `status`, `provider`, and `call_id`.

## Review Decision

Round r33 completed 20 effective read-only review slices and 5 cross-decision attempts. Two decision attempts were invalid because they performed unrelated work instead of choosing a candidate; replacement decision-only agents were used. Four valid decisions selected Q as the best bounded fix.

Reasons:

- It is a direct privacy residual after the already-pushed r31/r32 runtime redaction fixes.
- It affects warning storage, warning signatures, warning popovers, and frontend warning traces.
- It has a narrow implementation surface and a clear fail-first regression test.
- It avoids mixing broad response-validator or approval-flow work into this repair commit.

Runner-up candidates were Codex approval auto-decline, package/embed integrity, and memory mutation validators. They remain valid follow-up candidates but were not mixed into this commit.

## Evidence

`safeWarningFields(fields)` first calls `safeDiagnosticPreviewValue(fields)`, which redacts string values. It then loops over `SAFE_WARNING_FIELD_ALIASES` and writes allowlisted values from raw `fields` back into the output. Before this repair, a value such as `reason: "sk-live-secret-token"` replaced `[redacted]` and could reach runtime warning UI state.

Fail-first test added:

```bash
cd frontend-app
npm test -- --run src/entities/client/model/warningRuntime.test.js
```

Observed before implementation: one failure, `reason` was `sk-live-secret-token` instead of `[redacted]`.

## Implementation

- Added `WARNING_CORRELATION_SECRET_PATTERNS` in `frontend-app/src/entities/client/model/warningRuntime.js`.
- Rejected token-shaped warning correlation scalar values before path/format allowlist checks.
- Covered common assignment strings, bearer/basic values, `sk-/pk-/rk-` style keys, GitHub/GitLab/Slack token prefixes, GitHub fine-grained token prefix, and AWS access key IDs.
- Added `safeWarningFields` regression coverage for `reason`, `code`, `status`, `provider`, and `call_id`.
- Preserved normal safe correlation fields such as `method` and numeric `req_id`.

## Verification

Passed:

```bash
cd frontend-app
npm test -- --run src/entities/client/model/warningRuntime.test.js
npm test -- --run src/entities/client/model/warningRuntime.test.js src/entities/client/model/runtimeResults.test.js src/pages/chat/components/RuntimePanelComponents.test.jsx src/entities/client/model/useClientStore.test.js --testNamePattern "warning|redact|runtime result"
npm run lint
npx eslint src/entities/client/model/warningRuntime.js src/entities/client/model/warningRuntime.test.js
npm test
npm run build
git diff --check
```

Full frontend test result: 79 files, 1012 tests passed.

LSP diagnostics for `warningRuntime.js` timed out repeatedly with `lsp_timeout` after file open and retry. This is recorded as a diagnostics-tool blocker; ESLint, contract typecheck, focused tests, full tests, and build passed.

## Commit Plan

Stage only:

- `docs/plans/2026-07-05-warning-correlation-token-redaction.md`
- `frontend-app/src/entities/client/model/warningRuntime.js`
- `frontend-app/src/entities/client/model/warningRuntime.test.js`

Commit message:

```bash
fix: 脱敏告警关联字段
```

Push directly:

```bash
git push origin HEAD:main
```
