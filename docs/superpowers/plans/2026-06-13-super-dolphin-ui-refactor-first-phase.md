# Super-Dolphin UI Refactor First-Phase Plan

Date: 2026-06-13

## Status

This is a first-phase analysis and task split for the uploaded UI refactor prompt. No production UI or backend code has been changed in this phase.

Update: the current prototype screenshots were recovered from Codex clipboard temp files and copied into this worktree as durable visual references. Implementation can proceed in small slices against those local references.

## Confirmed Repository State

| Item | Value |
|---|---|
| Requested remote main commit | `f9ef48e1283f5c5791a61a92ef26e0cd60a1e6d5` |
| Local `main` after sync | `f9ef48e1283f5c5791a61a92ef26e0cd60a1e6d5` |
| Preserved previous local main | `backup/main-before-sync-20260613-c42fb65` at `c42fb6528af5e32473a9b53ec1542a3cc7185368` |
| Integration branch | `codex/ui-refactor-integration-20260613` |
| Integration worktree | `D:\project\Super-Dolphin-worktrees\ui-refactor-integration-20260613` |
| Worktree hook path | `D:/project/Super-Dolphin-worktrees/ui-refactor-integration-20260613/.githooks` |
| Main checkout unrelated dirty file | `D:\project\Super-Dolphin\skills\react-doctor\SKILL.md` deleted before this task; preserved and not staged |

## Product Design Brief Playback

Super-Dolphin should refactor the current `frontend-app` React/Vite UI around the uploaded prototype design. The target pages are home/new chat, chat detail, plugins, automation, personalization, shared files/artifacts, and mobile settings.

The implementation must preserve existing chat, route, auth, API, state, and build behavior. Real backend-supported functions should use the current RPC/API facade. Unsupported functions must show disabled, pending, or empty states and may reserve explicit contracts, but must not fake success.

Interactivity target: full interaction where the existing backend supports it; honest pending states where it does not.

## Visual Target Inventory

| Source | Status | Notes |
|---|---|---|
| `C:\Users\ai01\Downloads\super_dolphin_codex_ui_refactor_prompt.md` | Valid requirements doc | Not a visual target |
| `docs/superpowers/plans/assets/2026-06-13-super-dolphin-new-chat-prototype.png` | Valid prototype | New-chat/home reference, 1568x1240 |
| `docs/superpowers/plans/assets/2026-06-13-super-dolphin-chat-detail-prototype.png` | Valid prototype | Chat-detail/reference answer state, 1600x1280 |
| `C:\Users\ai01\Downloads\mermaid-diagram.png` | Invalid for this task | It is not a Super-Dolphin UI prototype |
| `frontend-app/src/assets/super-dolphin-logo.png` | Valid asset | Logo only |
| `frontend-app/design-qa-active-chat.png` | Not accepted as current prototype | Historical/current QA screenshot unless user explicitly selects it |
| `frontend-app/design-qa-mobile-chat-loaded.png` | Not accepted as current prototype | Historical/current QA screenshot unless user explicitly selects it |
| `docs/ai01-docs/assets/2026-06-03-*.png` | Not accepted as current prototype | Older review screenshots |
| `cmd/agent-terminal/frontend/tests/e2e/**/*.png` | Out of scope | Legacy Vue E2E snapshots |

Visual conclusion: the two recovered Codex clipboard screenshots are now the selected local visual target for the first implementation slices.

## Skill And Tool Routing

| Subtask | Routed skills/tools | Result |
|---|---|---|
| Skill routing | `route-skills-by-function`, `find-skills` | Installed skills were sufficient; no new skill installation needed |
| Product Design | Product Design `index`, `get-context`, `user-context`, `image-to-code` rules | Brief can be played back against the recovered local screenshot targets |
| Worktree setup | Super-Dolphin workflow, git worktree workflow | Worktree and integration branch created from the requested commit |
| Browser/Chrome/Computer Use | Browser, Chrome, Computer Use skills loaded for later UI verification | Use Browser/Chrome for local UI visual checks after implementation; Computer Use only if desktop-host behavior cannot be verified in-browser |
| Subagents | `multi_agent_v1.spawn_agent` | Used because `task_create_dag`/`task_start_node`/`task_update_node` were not exposed in this session and MCP catalog did not expose `mcp-go-agent-orchestration` |

## Subagent Split

| Agent | Scope | Output Summary |
|---|---|---|
| Frontend explorer | `frontend-app` routes, pages, state, API facade, styles | Confirmed React/Vite JS app, manual route state in `App.jsx`, Zustand store, React Query, global CSS, and no direct page-level `fetch/axios` pattern |
| Backend/API explorer | Wails/JSON-RPC backend support and pending contracts | Confirmed UI backend is Wails JSON-RPC, not REST; identified real support vs pending areas |
| Product Design/UX explorer | Visual source inventory and Product Design blockers | Initial scan missed temp clipboard images; current run recovered and anchored two valid prototype screenshots |

## Current Frontend Structure

| Concern | Current Pattern |
|---|---|
| Framework | React 19 + Vite, JS/JSX |
| Entry | `frontend-app/src/main.jsx`, `frontend-app/src/App.jsx` |
| Routing | Manual `activePage` state plus `PAGE_ROUTE_BY_ID` and `history.pushState` |
| State | Zustand `useClientStore` plus slices under `frontend-app/src/entities/client/model` |
| Data cache | React Query for page data and invalidation |
| API facade | `frontend-app/src/shared/api/backendApi.js` |
| Wails bridge | `frontend-app/src/shared/api/wailsBridge.js` |
| Styling | `frontend-app/src/styles.css`, CSS variables and media queries |
| Current UI target | `frontend-app`; legacy `cmd/agent-terminal/frontend` is out of scope unless explicitly requested |

## Page Mapping

| Prompt Page | Current Code Location | Implementation Note |
|---|---|---|
| Home / new chat | `App.jsx`, `pages/chat/ChatPage.jsx`, chat composer components, `useClientStore.sendDraft` | Keep `thread/start -> turn/start` flow |
| Chat detail | `pages/chat/ChatPage.jsx`, timeline/runtime panel components | Preserve streaming/loading/error/retry behavior |
| Plugins | Closest current page is `pages/skills/SkillsPage.jsx` | Need product decision: plugin marketplace vs local skills |
| Automation | `pages/workflows/WorkflowPage.jsx` and workflow services | Real DAG/cron support exists; templates need pending or contract |
| Personalization | Split across prompts and memory pages | Profile fields need pending contract or preference mapping |
| Shared files/artifacts | `pages/files/FilesPage.jsx`, file service/adapter | Existing list/read/open/delete support; export/continue must be verified before claiming full support |
| Mobile settings/account | `pages/settings/SettingsPage.jsx` | Account/logout UI must not fake auth support |

## Backend/API Status

| Feature | Status | Existing Surface Or Pending Contract |
|---|---|---|
| Chat/thread create/send/read | Real support | `thread/start`, `turn/start`, `thread/messages`, `thread/resolve`, archive/delete/name RPCs |
| Model/provider selection | Partial | Preferences and thread config exist; dynamic model list needs explicit UI-facing contract |
| Attachments | Partial | File picker/dropped text/clipboard image and turn input references exist; no generic upload platform |
| Plugins | Pending for marketplace | Skills RPCs exist; connect/disconnect/config for plugin marketplace not found |
| Automations/workflows | Real support | Cron and DAG dashboard RPCs exist |
| Automation templates | Pending | No clear template API found |
| Personal profile | Pending/partial | Preferences and memory exist; no first-class nickname/occupation/bio profile API found |
| Memory import | Pending | Memory entry upsert exists, but import flow is not a real product API |
| Shared files/artifacts | Partial | Dashboard shared files and shared-file get/delete/open exist; export/download/continue need explicit contract |
| Projects/tasks | Partial | Projects are preference-backed; general tasks list not found |
| Auth/logout/account settings | Pending | Settings exist; logout/account session API not found |

Backend pending behavior should use the existing JSON-RPC fail-fast convention. `contract.CodeNotImplemented = -31006` and `platformrpc.ErrNotImplemented(...)` exist for NOT_IMPLEMENTED behavior; this UI path should not invent HTTP 501 REST endpoints.

## Smallest Safe Implementation Slices

1. Shell/nav/route wording and page entry alignment.
   Likely write set: `frontend-app/src/App.jsx`, `frontend-app/src/styles.css`, focused `App` tests.

2. Chat home/detail visual refactor.
   Likely write set: `frontend-app/src/pages/chat/**`, chat tests, scoped CSS. Keep store/API behavior intact.

3. Shared files/artifacts page polish.
   Likely write set: `frontend-app/src/pages/files/FilesPage.jsx`, existing file service/adapter only if needed, tests, CSS.

4. Automation/DAG UX.
   Likely write set: `frontend-app/src/pages/workflows/**`, workflow service/tests/CSS. Template actions must be disabled or pending unless real support is added.

5. Plugins decision and implementation.
   Likely write set: existing `SkillsPage` if "plugins" means local skills; otherwise a new `pages/plugins/**` plus explicit pending backend contracts.

6. Personalization page.
   Likely write set: prompts/memory pages or new `pages/personalization/**`; profile fields require pending contract or explicit preference mapping.

7. Mobile settings/account.
   Likely write set: `pages/settings/**`, `App.jsx`, CSS, tests. Logout/account must remain pending unless an auth API is confirmed.

Do not implement all slices in one large refactor. Start with slice 1 or 2 against the recovered local visual targets.

## Verification Plan

For frontend-only slices:

```powershell
cd D:\project\Super-Dolphin-worktrees\ui-refactor-integration-20260613\frontend-app
npm run lint
npm test
npm run build
```

For Go/RPC contract slices:

```powershell
go test ./internal/platform/rpc ./internal/module/thread ./internal/module/turn ./internal/module/cron ./internal/module/skill ./internal/module/dashboard ./internal/module/uistate -count=1
```

For SQL/store changes:

```powershell
make sqlc-verify
```

For UI-visible changes, start the dev app and verify with Browser/Chrome against the selected visual target. Run Product Design design QA only after a reference image and rendered implementation are both available.

## Stop Conditions

Stop before code implementation if any of these become true:

- The recovered prototype screenshots are unavailable or invalidated.
- The planned slice would mix unrelated pages or backend contracts.
- A backend capability is only assumed, not confirmed from source/tests.
- Verification cannot be run and no concrete blocker is documented.

## Implementation Gate

Use the two local prototype screenshots above as the visual source. Confirm the Product Design brief in playback form, then implement only the first approved slice.
