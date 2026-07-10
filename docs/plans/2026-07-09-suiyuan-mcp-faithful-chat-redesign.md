# Suiyuan MCP-Faithful Chat Redesign Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rework the current Suiyuan Chat UI so the first screen follows the Stitch MCP preview composition, not only the exported token sheet.

**Architecture:** Treat Stitch MCP screens as the visual source of truth and keep `DESIGN.md` as the token source. Preserve the existing React/store/API behavior, but reshape the shell, intro stage, suggestion cards, and composer to match the `Suiyuan Chat Interface - Vibe Style` screen. Keep dark-mode-compatible structure from `Suiyuan Chat - Dark Mode`, without introducing decorative orbs in the light default.

**Tech Stack:** React 19, Vite, existing CSS files, Vitest, Testing Library, Wails bridge.

**Verification Surface:** LSP `grep/structure/inspect/xref/file/diagnostics`, `cd frontend-app && npm run lint && npm test && npm run build`, plus local desktop launch with `./run-new-ui-desktop.sh`.

---

## Stitch MCP Source

Project: `projects/16556859161700396548` (`Codex Style Main Console`)

Primary reference:
- Screen: `projects/16556859161700396548/screens/5959839627de4f9899f8e00a44d03b14`
- Title: `Suiyuan Chat Interface - Vibe Style`
- Downloaded evidence: `.tmp/stitch-suiyuan/chat-vibe.png`, `.tmp/stitch-suiyuan/chat-vibe.html`

Secondary reference:
- Screen: `projects/16556859161700396548/screens/eaa8d7d9d8754330883e3406d844e1c4`
- Title: `Suiyuan Chat - Dark Mode`
- Downloaded evidence: `.tmp/stitch-suiyuan/chat-dark.png`, `.tmp/stitch-suiyuan/chat-dark.html`

Design system:
- Asset: `assets/65a0020aad314c30b58e354bf65be2c2`
- Display name: `Luminous Minimalist`
- Root tokens are exported in `DESIGN.md`.

## Visual Facts To Match

From `chat-vibe.html`:
- Left sidebar is a fixed `280px` Suiyuan product nav, not a project/thread/task sidebar.
- Sidebar brand block shows icon, `Suiyuan`, and `AI Canvas`.
- Primary CTA is a pill-like `New Chat` button.
- Primary nav order: `Chat`, `Plugins`, `Automation`, `Roles`, `Files`, `Memory`, `Logs`; footer: `Settings`, `Support`.
- Active nav uses a `4px` left indicator and primary-fixed wash.
- Top app bar sits above the main canvas with `Overview`, `Usage`, `Limits`, plus notification/history/upgrade/profile actions.
- Main canvas is `surface-bright`, centered, and constrained to `1100px`.
- Intro stage has a centered 64px logo tile, 40px headline, 18px subtitle, then three bento suggestion cards.
- Suggestion cards are white lifted cards with 16px radius, primary icon tile, subtle border, and hover translate.
- Floating composer sits over a 24px+ bottom feather, max width about `768px`, high radius, toolbar with attach/model pill, and circular sienna send button.
- Disclaimer text is centered below the composer.

From `chat-dark.html`:
- The same structure works in dark mode with `280px` sidebar, dashboard/workspaces top bar, three bento cards, fixed bottom composer, and optional `System Latency` pill.
- Do not copy the dark-only gradient orbs into the light default; the app design rules prohibit decorative orbs.

## Current Mismatch

Current screenshot at `http://127.0.0.1:5175/` shows:
- Sidebar is still a mixed workbench with projects/tasks/chat history and 240px width.
- Top app bar from the reference is missing.
- Intro stage only has a headline and composer; bento suggestion cards and explanatory subtitle are missing.
- Composer is close in color but not in hierarchy, width, toolbar rhythm, or bottom feather treatment.
- Runtime/side-panel CSS still influences the page more than the Stitch first screen expects.

## Parallel Work Plan

### Task A: Shell And Navigation Reference Match

**Owner:** Worker A

**Write scope:**
- Modify: `frontend-app/src/App.jsx`
- Modify: `frontend-app/src/AppShell.css`
- Modify: `frontend-app/src/AppChrome.css`
- Modify: `frontend-app/src/styles.test.js`

**Do not modify:**
- `frontend-app/src/pages/chat/**`
- `frontend-app/src/shared/**`
- `frontend-app/src/entities/**`

**Steps:**
- [ ] Use LSP `structure` and `file(read_file)` on `frontend-app/src/App.jsx`, `frontend-app/src/AppShell.css`, `frontend-app/src/AppChrome.css`, and `frontend-app/src/styles.test.js`.
- [ ] Add or update CSS tests asserting the light shell exposes a 280px nav, Suiyuan nav active state with 4px indicator, and top app bar link labels `Overview`, `Usage`, `Limits`.
- [ ] Adjust shell markup/classes only as needed to present a Stitch-faithful product nav on Chat first screen while preserving route switching, locale, theme, settings, and existing navigation actions.
- [ ] Make the sidebar visually match the primary reference: product brand block, pill CTA, nav icon/text rows, footer actions, no project/task sections on the first Chat intro view.
- [ ] Keep keyboard labels and button accessible names intact.
- [ ] Run `npx vitest run src/styles.test.js --no-file-parallelism --maxWorkers=1`.
- [ ] Run LSP diagnostics on touched files.

### Task B: Chat Intro Stage And Bento Cards

**Owner:** Worker B

**Write scope:**
- Modify: `frontend-app/src/pages/chat/ChatPage.jsx`
- Modify: `frontend-app/src/pages/chat/ChatPage.css`
- Modify: `frontend-app/src/pages/chat/ChatMessages.css`
- Modify: `frontend-app/src/pages/chat/ChatTimeline.css`

**Do not modify:**
- `frontend-app/src/App.jsx`
- `frontend-app/src/AppShell.css`
- `frontend-app/src/AppChrome.css`
- `frontend-app/src/styles.test.js`
- `frontend-app/src/pages/chat/composer/**`
- `frontend-app/src/pages/chat/runtime/**`

**Steps:**
- [ ] Use LSP `structure`, `inspect(definition)`, `xref(references)`, and `file(read_file)` for `ChatPage`.
- [ ] Add a small presentational intro suggestion model in `ChatPage.jsx` if CSS alone cannot render the three bento cards.
- [ ] Match the primary reference intro: centered logo tile, headline, subtitle, three lifted bento cards, and 1100px max stage.
- [ ] Keep active-thread mode behavior unchanged: existing timeline, streaming, approvals, code previews, scroll anchoring, and scroll-to-bottom must still render.
- [ ] Ensure bento card clicks, if wired, only prefill or submit through existing composer/store behavior; do not invent backend data.
- [ ] If Chat-specific CSS assertions are needed in `frontend-app/src/styles.test.js`, report the exact proposed assertions to the coordinator instead of editing the file.
- [ ] Run focused Chat tests already covering intro/timeline behavior.
- [ ] Run LSP diagnostics on touched files.

### Task C: Floating Composer And Toolbar

**Owner:** Worker C

**Write scope:**
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.css`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`

**Do not modify:**
- `frontend-app/src/App.jsx`
- `frontend-app/src/AppShell.css`
- `frontend-app/src/pages/chat/ChatPage.jsx`
- `frontend-app/src/pages/chat/ChatPage.css`
- `frontend-app/src/pages/chat/runtime/**`

**Steps:**
- [ ] Use LSP `structure`, `inspect(definition)`, `xref(references)`, and `file(read_file)` for `ComposerDock`.
- [ ] Add focused tests only when markup/class hooks change, preserving enter-to-send with IME guard, paste/drop attachment handling, disabled state, and interrupt state.
- [ ] Match reference composer: fixed/floating bottom placement, max width around 768px, rounded 24-32px white container, textarea row, toolbar row, attach pill, model pill, circular sienna send button, and centered disclaimer.
- [ ] Preserve existing project/model/provider controls and attachment previews; do not hide fail-fast blocked-state text.
- [ ] Run `npx vitest run src/pages/chat/composer/ComposerDock.test.jsx --no-file-parallelism --maxWorkers=1`.
- [ ] Run LSP diagnostics on touched files.

### Task D: Runtime Inspector Compatibility And Responsive QA

**Owner:** Worker D

**Write scope:**
- Modify: `frontend-app/src/pages/chat/runtime/RuntimePanel.css`
- Modify: `frontend-app/src/pages/chat/components/RuntimePanelPolish.css`
- Modify: `frontend-app/src/pages/chat/components/RuntimePanelComponents.test.jsx` only if required by class/behavior changes.
- Read-only inspect: `frontend-app/src/pages/chat/ChatPageWorkbench.css`

**Do not modify:**
- `frontend-app/src/App.jsx`
- `frontend-app/src/pages/chat/ChatPage.jsx`
- `frontend-app/src/pages/chat/composer/**`

**Steps:**
- [ ] Use LSP `structure`, `inspect(definition)`, `xref(references)`, and `file(read_file)` for `RuntimePanel`.
- [ ] Make runtime surfaces recede visually so the first Chat screen still reads like the Stitch canvas when runtime is closed, and remains usable when open.
- [ ] Keep diff grouping, collapse, save/open/locate actions, activity stats/logs, warning popover, and resize affordance.
- [ ] Check responsive constraints so left nav, centered canvas, optional runtime panel, and composer do not overlap at desktop, tablet, and mobile widths.
- [ ] Run focused runtime component tests if touched.
- [ ] Run LSP diagnostics on touched files.

### Task E: Integration And Visual Acceptance

**Owner:** Main coordinator

**Write scope:**
- Integrate worker patches after review.
- Resolve CSS cascade conflicts only after workers return.
- Modify plan checkboxes as work completes.

**Steps:**
- [ ] Review each worker's changed files and reject changes outside assigned scope.
- [ ] Run LSP diagnostics for all touched React/CSS files.
- [ ] Run `cd frontend-app && npm run lint`.
- [ ] Run `cd frontend-app && npm test`.
- [ ] Run `cd frontend-app && npm run build`.
- [ ] Start `./run-new-ui-desktop.sh`.
- [ ] Capture a browser screenshot at `http://127.0.0.1:5175/`.
- [ ] Compare against `.tmp/stitch-suiyuan/chat-vibe.png`: 280px nav, top app bar, centered logo/title/subtitle/cards, bottom floating composer, warm Suiyuan palette, no one-note dark console feel.
- [ ] Report remaining deliberate differences caused by existing product behavior.

## Dispatch Notes

Workers must know:
- The repository has unrelated user Go changes in `internal/archtest/**` and `internal/platform/toolbridge/**`; do not touch or revert them.
- The current branch also has local startup fixes in `frontend-app/vite.config.js`, `frontend-app/vite.config.test.js`, `internal/provider/codexapp/sidecar_runtime_env.go`, `internal/provider/codexapp/sidecar_runtime_env_test.go`, and `run-new-ui-desktop.sh`; do not mix UI work into those files unless explicitly assigned.
- `.tmp/stitch-suiyuan/**` and `.superpowers/**` are evidence/temp artifacts and must not be committed.
- Fail-fast behavior remains mandatory; do not add silent UI fallbacks for missing project, cwd, provider, thread, or runtime data.
