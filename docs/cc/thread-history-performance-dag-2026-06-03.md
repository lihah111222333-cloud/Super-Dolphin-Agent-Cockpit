# Thread History Performance Fix DAG

Base branch: `feat/thread-history-performance-integration`

Goal: make thread-card switching show recent conversation history quickly without server-side full-history scans or front-end full-history mounting.

## DAG

```text
T1 backend-page-api
  -> integration

T2 frontend-initial-page
  -> integration

T3 frontend-render-window
  -> integration

Integration gate:
  T1 + T2 + T3 must pass two-reviewer scoring before merge.
```

## Task T1: backend-page-api

Branch: `task/thread-history-backend-pagination`

Owned paths:
- `internal/dto/provider/message.go`
- `internal/module/thread/**`
- `internal/provider/codexapp/*history*`
- `internal/provider/claudecli/*history*`
- `internal/util/historyjsonl/**`

Required outcome:
- `thread/messages` returns page metadata: `hasMore` and `nextBefore`.
- Chat first-page reads must not call `ReadHistory(..., 0)` or scan full provider history when only recent messages are requested.
- Keep old full-history `ReadHistory(limit=0)` behavior for compact, fork, token, and other non-chat paths.
- Codex, Claude, and persisted fallback history paths expose consistent page semantics.
- Cursor ownership moves to backend; frontend no longer needs to infer numeric cursors for new responses.

Verification:
- `./scripts/test_with_guard.sh ./internal/module/thread ./internal/provider/codexapp ./internal/provider/claudecli ./internal/util/historyjsonl -count=1`

## Task T2: frontend-initial-page

Branch: `task/thread-history-frontend-loading`

Owned paths:
- `frontend-app/src/entities/client/model/useClientStore.js`
- `frontend-app/src/entities/client/model/useClientStore.test.js`
- frontend API type/adapter files only if needed for the new response fields

Required outcome:
- Initial conversation display applies the first `thread/messages` page immediately.
- Pagination uses backend `hasMore` / `nextBefore` when present.
- `total` no longer forces initial full-history loading.
- Existing trusted cache remains visible during refresh.
- Message page apply and loading cleanup use generation/request guards for stale-request safety.
- If background backfill is introduced, it must be per-thread deduped and cancellable/ignorable by generation.

Verification:
- `cd frontend-app && npm test -- useClientStore.test.js`

## Task T3: frontend-render-window

Branch: `task/thread-history-frontend-rendering`

Owned paths:
- `frontend-app/src/pages/chat/ChatPage.jsx`
- `frontend-app/src/pages/chat/ChatPage.test.jsx`
- `frontend-app/src/App.test.jsx` only for timeline/mermaid behavior tests

Required outcome:
- Conversation timeline must not mount every loaded historical message at once.
- Older pages are materialized only when visible or explicitly requested by scroll/load-more behavior.
- Markdown and Mermaid heavy rendering should not run for offscreen/unmaterialized older history.
- Preserve existing cached timeline and current-message rendering behavior.

Verification:
- `cd frontend-app && npm test -- ChatPage.test.jsx App.test.jsx`

## Review Gate

For each task worktree:
- Two reviewer agents score the completed work from production feasibility, performance priority, risk, accuracy, and maintainability.
- Passing threshold: both reviewers mark every dimension acceptable and no blocking findings remain.
- Passing task: commit owned changes in that task branch, merge into `feat/thread-history-performance-integration`.
- Failing task: send reviewer findings back to the task worker for revision; repeat review until passing.
