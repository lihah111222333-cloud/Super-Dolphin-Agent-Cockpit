# A5 Unified Chat UI Test Report

Date: 2026-05-29
Branch: `fix/chat-page-backend-adapter-20260529`
Worktree: `/home/ai01@f666.com/桌面/project/Super-Dolphin`

## Scope

- Reviewed the production React route `AppRoot -> NewWorkbenchShell -> NewUnifiedChatPage`.
- Classified the screenshot-style `NewUnifiedChatPage` rewrite as a P1 regression because it replaced the feature-complete `UnifiedChatPage` view model with a thin local prompt/attachment/send implementation.
- Restored production chat parity by making `NewUnifiedChatPage` a compatibility adapter over `pages/UnifiedChatPage.jsx`.
- Kept the screenshot-1 dark workbench shell styling, but moved it to CSS overrides around the existing full chat component tree.
- Added a compact `warning-log-panel` surface so warn/error entries remain visible without replacing the real diff/activity panel.

## Files Reviewed / Changed

- `cmd/agent-terminal/frontend/src/new-frontend/NewUnifiedChatPage.jsx`
- `cmd/agent-terminal/frontend/src/new-frontend/NewUnifiedChatPage.test.jsx`
- `cmd/agent-terminal/frontend/src/styles/new-workbench.css`
- `cmd/agent-terminal/frontend/src/pages/UnifiedChatPage.jsx`
- `cmd/agent-terminal/frontend/src/pages/UnifiedChatPage.template.js`
- `cmd/agent-terminal/frontend/src/components/ChatTimeline.jsx`
- `cmd/agent-terminal/frontend/src/composables/useThreadActions.js`
- `cmd/agent-terminal/frontend/src/components/ComposerBar.jsx`
- `cmd/agent-terminal/frontend/src/components/unified-chat/ThreadRailSidePanel.jsx`
- `cmd/agent-terminal/frontend/src/components/unified-chat/WorkspaceChatPanel.jsx`
- `cmd/agent-terminal/frontend/src/components/DiffPanel.jsx`
- `cmd/agent-terminal/frontend/src/components/ActivityPanel.jsx`

## P0/P1/P2/P3 Findings

P0: none.

P1:

- Fixed: `NewUnifiedChatPage` directly called `threadStore.startThread()` and `threadStore.sendMessage()`, bypassing `useThreadActions`, Composer store, provider preference readiness, draft restoration, fork flow, file-ref preview, diff/activity panels, keyboard shortcuts, and lifecycle hooks.
- Fixed: the screenshot-style right panel rendered static “贝叶斯思维” content instead of the live `DiffPanel` / `ActivityPanel` data surfaces.
- Fixed: new-page tests asserted the temporary mockup behavior instead of guarding production chat parity.

P2:

- The warning log is currently a compact floating surface backed by `services/log.readLogBuffer()`. It covers warn/error visibility and `rpc.failed`, but advanced filters by method/thread/agent/trace remain future A6 work.
- Fixed: the restored component tree now exposes the plan-required anchors `chat-timeline`, `composer-dock`, `diff-panel`, and `activity-panel` in the real DOM, not only in mocked route tests.

P3:

- The restored page uses the legacy `UnifiedChatPage` component names and CSS classes under the new shell. This is intentional for production safety; a later FSD migration can wrap these pieces slice by slice after parity tests exist.

## TDD Evidence

RED:

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/new-frontend/NewUnifiedChatPage.test.jsx
```

Result: failed as expected. The old screenshot mockup did not render `legacy-unified-chat`, did not delegate props to `UnifiedChatPage`, and did not expose the legacy static contract.

GREEN:

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/new-frontend/NewUnifiedChatPage.test.jsx
```

Result: pass. 1 test file passed, 3 tests passed.

Targeted route and bridge tests:

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/new-frontend/NewUnifiedChatPage.test.jsx src/new-frontend/NewWorkbenchShell.test.jsx src/app-root-react.bridge.test.jsx
```

Result: pass. 3 test files passed, 8 tests passed.

Legacy chat behavior tests:

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/unified-chat-component.test.js src/unified-chat-thread-config.behavior.test.js src/composer-bar.behavior.test.js src/context-usage-banner.test.js
```

Result: pass. 4 test files passed, 78 tests passed.

Post-anchor targeted regression:

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/new-frontend/NewUnifiedChatPage.test.jsx src/new-frontend/NewWorkbenchShell.test.jsx src/app-root-react.bridge.test.jsx src/unified-chat-component.test.js src/unified-chat-thread-config.behavior.test.js src/composer-bar.behavior.test.js src/context-usage-banner.test.js src/unified-chat-template-contract.test.js
```

Result: pass. 8 test files passed, 92 tests passed; size guard preflight passed with no new violations.

## Contract Evidence

- Production route still renders through `AppRoot -> NewWorkbenchShell -> NewUnifiedChatPage`.
- `NewUnifiedChatPage` now delegates to `UnifiedChatPage`, so empty-thread send returns to `useThreadActions.performSend`.
- Existing backend contract remains unchanged: `thread/start`, deferred spawn, `turn/start`/`sendMessage`, explicit `cwd`, provider preference checks, draft restoration, diff/file-ref preview, fork, compact, recover, interrupt, and lifecycle sync all remain owned by existing tested modules.
- Required test anchors remain available through the restored component tree: `chat-page`, `thread-rail`, `chat-toolbar`, `chat-timeline`, `composer-dock`, `composer-input`, `diff-panel`, `activity-panel`, and `warning-log-panel`.

## Remaining Coverage Gaps

- Full frontend gate is recorded in `A9-summary.md`.
- Visual parity is CSS-level and should still be manually checked against the provided screenshot after launching the formal frontend server.
