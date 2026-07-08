# Desktop Wide UI E2E Design

## Goal

Add an experimental Desktop Wide UI smoke suite that filters high-value DesktopUI regressions at the user interface layer before deeper provider or backend paths are exercised.

The suite is not a full autonomous site tester, not a complete responsive matrix, and not a pixel-based visual regression system. It is a deterministic Playwright suite for wide desktop layouts, supported by strict Wails mocking and sandbox evidence.

## Scope

The first version covers:

- Workbench shell health on desktop-wide viewports.
- Business page first-screen health for chat, plugins and skills, automation, prompts, shared files, memory, observability, and settings.
- Low-risk interactions: navigation, latest observability logs, and settings API key save through mock Wails.
- Geometry and semantic health: key regions visible, in viewport, non-zero size, no page-level horizontal overflow, and critical buttons not covered at their center point.
- Failure evidence: Playwright trace, screenshot, video, and JSON report under `.tmp`.

The first version does not cover:

- Real provider sends.
- Real local project mutation.
- Full responsive coverage below desktop-wide widths.
- Pixel snapshot assertions as hard pass/fail criteria.
- CI integration or pre-commit integration.

## Multi-Agent Review Outcome

Twelve effective review agents returned usable decisions. All were conditional approvals. Six later replacement agents timed out and were excluded rather than counted as fake evidence.

The accepted conditions are:

- Treat this as Desktop Wide UI Smoke, not a complete visual or responsive suite.
- Use `1440x900` as the required baseline because it matches the current desktop default more closely; use `1600x1000` as a supplemental wide project.
- Keep the suite opt-in via a dedicated npm script and Playwright config. Do not let existing desktop smoke, `npm test`, hooks, or CI pick it up implicitly.
- Keep strict Wails mock fail-fast. Unknown RPCs, mock failures, and sandbox path escapes fail the test.
- Keep real Desktop/Wails smoke separate and low-risk. Its current shape is better described as desktop-host-backed Vite smoke than proof of a real Wails WebView page load.
- Keep helper reuse conservative. Do not couple deterministic Playwright smoke to the agentic goal runner planner.
- Store generated artifacts only under ignored `.tmp` paths.
- Do not make screenshots a manual approval step. Screenshots, videos, traces, and JSON reports are diagnostic evidence only.

## Architecture

Create a new Playwright config:

- `frontend-app/playwright.desktop-wide.config.js`
- testMatch limited to `desktop-wide.spec.js`
- output under `../.tmp/playwright-desktop-wide`
- web server on a dedicated Vite port
- projects:
  - `desktop-1440`: `1440x900`
  - `desktop-1600`: `1600x1000`

Create a new spec:

- `frontend-app/tests/e2e/desktop-wide.spec.js`
- import the existing agentic sandbox and strict Wails mock
- install bug capture before navigation
- prepare `.tmp/agentic-e2e/sandbox/<run-id>` fixtures
- write JSON report under `.tmp/agentic-e2e/desktop-wide-playwright`

Add one npm script:

- `test:e2e:desktop-wide`

## Test Strategy

The suite has three test groups.

### Workbench Shell Health

Assert:

- `frontend-app`, `app-sidebar`, `chat-page`, `chat-layout`, `composer-input`, and navigation groups are visible.
- Core shell elements are inside the viewport.
- The page does not have document-level horizontal overflow beyond a small tolerance.
- Buttons such as Settings and chat actions are center-clickable or can be clicked with a state change.
- Runtime panel coverage remains in the existing desktop UX smoke until the mock suite has a safe active-thread entry path. The mock wide suite must not create a provider turn just to expose the header menu.

### Business Page First Screen

Open each canonical route through user navigation where possible:

- `/skills`
- `/dags`
- `/prompts`
- `/files`
- `/memory`
- `/observability`
- `/settings`

Assert:

- URL matches the canonical route.
- A page-level test id or stable heading/key control is visible.
- Main visible surface is inside viewport.
- Document-level horizontal overflow is absent.

Do not assume every page already has a page-level `data-testid`. For pages without one, use stable headings and key roles as the first version's anchor.

### Risk-Controlled Interaction Probes

Allow:

- querying latest observability logs
- saving settings video API key through strict mock

Avoid in this suite:

- chat send, because it is already covered by the business-flow high-risk suite
- runtime panel opening from chat header while the page is in intro mode, because the header menu is intentionally absent until a safe active-thread state exists
- project/file picker actions unless and until the runtime ByID path is explicitly mocked in this suite
- provider save/apply actions that could affect real configuration

## Assertions

Use stable invariants:

- `expect(locator).toBeVisible()`
- `expect(locator).toBeInViewport()`
- role/name assertions for user-facing controls
- URL assertions for canonical routes
- bounding box checks with 1-2 px tolerance
- `document.documentElement.scrollWidth <= viewportWidth + tolerance`
- `elementFromPoint` center-point hit checks for critical controls

Do not use:

- broad button count assertions as the primary proof
- fixed pixel-perfect snapshots as hard assertions
- `networkidle`
- arbitrary `waitForTimeout`
- CSS-class-only selectors for user-facing controls where role/test id exists

## Bug Capture

Capture:

- page errors
- console errors
- request failures
- HTTP 4xx/5xx
- mock Wails unhandled RPCs
- mock Wails failures
- sandbox violations
- runtime telemetry missing only when known RPC calls were observed

Allow known development noise:

- Vite HMR websocket
- favicon/source-map noise if it appears as expected local dev noise

JSON reports must avoid large payload dumps and should record counts, methods, URLs, and sanitized error messages rather than raw prompt or secret values.

## Safety

The suite runs on Vite plus strict mock Wails. It must not call a real provider or mutate a real project directory.

The sandbox root is derived from `.tmp/agentic-e2e/sandbox/<run-id>`. Paths returned from mocks must stay inside the sandbox. Unknown RPCs fail-fast rather than silently falling back.

Real Desktop/Wails smoke remains separate and low risk. If expanded later, it must set temporary `SUPER_DOLPHIN_HOME`, sqlite path, logs, and provider/cache paths before performing any filesystem-sensitive action.

## Verification

Minimum verification after implementation:

```bash
cd frontend-app
npm run test:e2e:desktop-wide
npm run test:e2e:business
npm test -- scripts/agentic-e2e.test.mjs
```

Also run LSP diagnostics for changed JS/MJS files and `git diff --check`.
