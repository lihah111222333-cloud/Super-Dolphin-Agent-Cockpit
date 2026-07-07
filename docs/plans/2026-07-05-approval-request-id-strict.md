# Approval Request ID Strictness

## Problem

Approval responses are permission-bearing actions, but frontend and Codex provider boundaries currently coerce approval `requestId` values loosely. Decimal strings or numeric strings can become positive request IDs before calling `approval/respond`, which can approve or reject the wrong pending request instead of failing fast.

## Boundary

- Only approval request IDs are tightened in this commit.
- Generic telemetry/event numeric helpers remain unchanged.
- Frontend UI, store action, and `approval/respond` facade must require positive safe integers.
- Codex provider approval notifications must require a positive integer `json.Number` or internal integer value; strings, decimals, floats, zero, negative, and overflow are invalid.
- Existing snake_case `request_id` compatibility remains only when the value itself is strict.

## Tasks

- [x] RED: add frontend tests proving string/decimal approval IDs do not submit.
- [x] RED: add API facade tests proving non-integer request IDs throw before RPC.
- [x] RED: add store tests proving invalid IDs do not call `approval/respond`.
- [x] RED: add Go provider test proving string request IDs do not call approval hooks or send a truncated approval response.
- [x] GREEN: add approval-specific strict request ID parsers.
- [x] Verify focused tests, Go package guard, full frontend validation, and diff checks.
- [ ] Commit and push to remote `main`.

Observed RED: focused frontend tests failed because malformed IDs still normalized to valid IDs; focused Go test failed because `"91"` returned nil and entered the approval path.

Observed GREEN: the same focused frontend and Go tests passed after adding approval-specific strict parsers.

Observed verification:

- `npm test -- --run src/pages/chat/components/ChatApprovalMessage.test.jsx src/shared/api/backendApi.test.js src/entities/client/model/useClientStore.test.js`: 244 tests passed.
- `./scripts/test_with_guard.sh internal/provider/codexapp/session_approval.go`: passed.
- `./scripts/test_with_guard.sh internal/provider/codexapp/session_approval_shard02_test.go`: passed.
- `./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1`: guard passed; `internal/archtest` and `internal/provider/codexapp` passed.
- `npm run lint`: passed.
- `npm test`: 79 files and 1015 tests passed.
- `npm run build`: passed.
- `git diff --check`: passed.

## Verification

```bash
cd frontend-app
npm test -- --run src/pages/chat/components/ChatApprovalMessage.test.jsx src/shared/api/backendApi.test.js src/entities/client/model/useClientStore.test.js -t "approval|Approval"
npm run lint
npm test
npm run build
```

```bash
./scripts/test_with_guard.sh internal/provider/codexapp/session_approval.go
./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1
git diff --check
```
