# Super Dolphin Agent Chat UI Redesign Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the B3 Stitch-faithful Super Dolphin Agent redesign for the app shell and Chat workflow.

**Architecture:** Keep the current React/Vite page structure and store/API bridge intact while translating `DESIGN.md` into the existing CSS token system. Apply the high-fidelity Super Dolphin Agent composition through scoped shell and Chat CSS first, with React component edits only when required to expose stable class hooks or preserve accessibility.

**Tech Stack:** React 19, Vite, Vitest, Testing Library, PostCSS-based CSS assertions, existing CSS modules/files, Wails bridge through current frontend contracts.

**Verification Surface:** `frontend-app` lint/test/build; focused `styles.test.js`, `App.test.jsx`, `AppShell` tests if added, Chat page/component tests, Composer tests, Runtime panel tests; required LSP evidence before source edits.

---

## File Structure

Planned files and responsibilities:

- Modify: `frontend-app/src/styles.css`
  - Owns Super Dolphin Agent theme tokens and light/dark token aliases used by app shell and Chat.
- Modify: `frontend-app/src/styles.test.js`
  - Owns CSS cascade, token, layout, and shrink-safety assertions.
- Modify: `frontend-app/src/AppShell.css`
  - Owns app shell, navigation rail, top command, shared page button surfaces.
- Modify: `frontend-app/src/AppChrome.css`
  - Owns outer window/background chrome and shell framing.
- Modify: `frontend-app/src/App.test.jsx`
  - Owns shell smoke tests if existing rendered structure needs a Super Dolphin Agent class or data hook assertion.
- Modify: `frontend-app/src/pages/chat/ChatPage.css`
  - Owns Chat workbench grid, conversation canvas, intro state, timeline container, splitters, scroll control.
- Modify: `frontend-app/src/pages/chat/ChatPageWorkbench.css`
  - Owns later cascade refinements for Chat header/workbench layout already loaded after base Chat styles.
- Modify: `frontend-app/src/pages/chat/ChatMessages.css`
  - Owns message cards, markdown/code/diff surfaces, approvals, directives, and media.
- Modify: `frontend-app/src/pages/chat/ChatTimeline.css`
  - Owns timeline item spacing and chronological material styling.
- Modify: `frontend-app/src/pages/chat/ChatReasoning.css`
  - Owns reasoning/tool trace surfaces and collapse affordances.
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.css`
  - Owns floating/docked composer visual treatment, toolbar row, send/interrupt/disabled/drop states.
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`
  - Owns behavior and accessibility tests for composer controls; add class/state assertions only when markup hooks change.
- Modify: `frontend-app/src/pages/chat/runtime/RuntimePanel.css`
  - Owns runtime inspector, toolbar, diff, activity, warning log, and resize-safe surfaces.
- Modify: `frontend-app/src/pages/chat/components/RuntimePanelPolish.css`
  - Owns late cascade runtime polish if required by existing import order.
- Modify: `frontend-app/src/pages/chat/components/RuntimePanelComponents.test.jsx`
  - Owns runtime component behavior tests; add accessibility/state tests if component hooks change.
- Do not modify: `frontend-app/src/entities/client/model/useClientStore.js`, `frontend-app/src/shared/api/backendApi.js`, `frontend-app/src/shared/api/wailsBridge.js`
  - These remain unchanged unless LSP evidence proves a presentation-only hook cannot be added elsewhere. If that happens, stop and revise this plan.
- Do not commit: `.superpowers/brainstorm/**`
  - Visual companion output is temporary brainstorming material.

## Task 1: Required LSP Evidence Gate

**Files:**
- Read: `docs/internal-notes/LSP系统提示词.md`
- Inspect: `frontend-app/src/App.jsx`
- Inspect: `frontend-app/src/AppRoutes.jsx`
- Inspect: `frontend-app/src/pages/chat/ChatPage.jsx`
- Inspect: `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- Inspect: `frontend-app/src/pages/chat/runtime/RuntimePanel.jsx`
- Inspect: `frontend-app/src/styles.css`
- Inspect: `frontend-app/src/styles.test.js`

- [ ] **Step 1: Confirm LSP server/tool availability**

Use the repository-mandated LSP tool chain, not shell substitutes:

```text
tool_search query: "mcp-lsp grep file structure inspect xref diagnostics edit completion"
```

Expected: callable LSP tools for `grep`, `structure`, `inspect`, `xref`, `file`, and diagnostics. If the tools are not exposed, record a blocker in the implementation notes with:

```text
tool/action: tool_search for mcp-lsp tools
work_dir: /Users/l4place/Documents/Super-Dolphin
target: frontend-app React/CSS UI implementation
error: no mcp-lsp grep/file/structure/inspect/xref/diagnostics tools exposed
retry narrowing: queried "mcp-lsp grep file structure inspect xref diagnostics edit completion"
```

Do not edit production source until the LSP toolset is available or the user explicitly changes the repository LSP requirement.

- [ ] **Step 2: Locate source symbols with LSP**

Run LSP symbol/document searches:

```text
structure(document_symbol, file_path="frontend-app/src/App.jsx", work_dir="/Users/l4place/Documents/Super-Dolphin")
structure(document_symbol, file_path="frontend-app/src/AppRoutes.jsx", work_dir="/Users/l4place/Documents/Super-Dolphin")
structure(document_symbol, file_path="frontend-app/src/pages/chat/ChatPage.jsx", work_dir="/Users/l4place/Documents/Super-Dolphin")
structure(document_symbol, file_path="frontend-app/src/pages/chat/composer/ComposerDock.jsx", work_dir="/Users/l4place/Documents/Super-Dolphin")
structure(document_symbol, file_path="frontend-app/src/pages/chat/runtime/RuntimePanel.jsx", work_dir="/Users/l4place/Documents/Super-Dolphin")
```

Expected: symbols include `App`, `ActivePageContent`, `ChatPage`, `ComposerDock`, and `RuntimePanel`.

- [ ] **Step 3: Inspect definitions and references**

Use LSP definition/reference tools at the exported component symbols:

```text
inspect(definition, pos="frontend-app/src/pages/chat/ChatPage.jsx:161:10", work_dir="/Users/l4place/Documents/Super-Dolphin")
xref(references, pos="frontend-app/src/pages/chat/ChatPage.jsx:161:10", work_dir="/Users/l4place/Documents/Super-Dolphin")
inspect(definition, pos="frontend-app/src/pages/chat/composer/ComposerDock.jsx:51:10", work_dir="/Users/l4place/Documents/Super-Dolphin")
xref(references, pos="frontend-app/src/pages/chat/composer/ComposerDock.jsx:51:10", work_dir="/Users/l4place/Documents/Super-Dolphin")
inspect(definition, pos="frontend-app/src/pages/chat/runtime/RuntimePanel.jsx:15:10", work_dir="/Users/l4place/Documents/Super-Dolphin")
xref(references, pos="frontend-app/src/pages/chat/runtime/RuntimePanel.jsx:15:10", work_dir="/Users/l4place/Documents/Super-Dolphin")
```

Expected: references confirm Chat is routed through `AppRoutes.jsx`, composer through `ChatPage.jsx`, and runtime through Chat runtime slots. If a symbol line differs, use the exact line from Step 2.

- [ ] **Step 4: Read relevant source through LSP file tool**

Read exact source windows with LSP:

```text
file(read_file, pos="frontend-app/src/styles.css:1", scope="lines", limit=180, work_dir="/Users/l4place/Documents/Super-Dolphin")
file(read_file, pos="frontend-app/src/styles.test.js:1", scope="lines", limit=520, work_dir="/Users/l4place/Documents/Super-Dolphin")
file(read_file, pos="frontend-app/src/AppShell.css:1", scope="lines", limit=260, work_dir="/Users/l4place/Documents/Super-Dolphin")
file(read_file, pos="frontend-app/src/pages/chat/ChatPage.css:1", scope="lines", limit=260, work_dir="/Users/l4place/Documents/Super-Dolphin")
file(read_file, pos="frontend-app/src/pages/chat/composer/ComposerDock.css:1", scope="lines", limit=260, work_dir="/Users/l4place/Documents/Super-Dolphin")
file(read_file, pos="frontend-app/src/pages/chat/runtime/RuntimePanel.css:1", scope="lines", limit=260, work_dir="/Users/l4place/Documents/Super-Dolphin")
```

Expected: CSS import order, theme token definitions, Chat canvas, composer, and runtime panel styles are visible.

- [ ] **Step 5: Capture baseline diagnostics**

Run LSP diagnostics on React/CSS sources:

```text
file(diagnostics, file_path="frontend-app/src/App.jsx", work_dir="/Users/l4place/Documents/Super-Dolphin")
file(diagnostics, file_path="frontend-app/src/pages/chat/ChatPage.jsx", work_dir="/Users/l4place/Documents/Super-Dolphin")
file(diagnostics, file_path="frontend-app/src/pages/chat/composer/ComposerDock.jsx", work_dir="/Users/l4place/Documents/Super-Dolphin")
file(diagnostics, file_path="frontend-app/src/pages/chat/runtime/RuntimePanel.jsx", work_dir="/Users/l4place/Documents/Super-Dolphin")
```

Expected: no unhandled Error, Warning, Information, or Hint diagnostics. If any diagnostic appears, fix it in the task that touches that file or stop and ask for scope approval if it is unrelated.

- [ ] **Step 6: Commit the LSP evidence note if any implementation branch requires it**

If the implementation run has a durable notes file, append the LSP evidence summary there. If no notes file exists, include the evidence in the task completion message and commit no files for this gate.

Run:

```bash
git status --short
```

Expected: no production source changes from this task.

## Task 2: Super Dolphin Agent Token And CSS Guardrail Tests

**Files:**
- Modify: `frontend-app/src/styles.test.js`
- Modify: `frontend-app/src/styles.css`
- Test: `frontend-app/src/styles.test.js`

- [ ] **Step 1: Write failing Super Dolphin Agent token assertions**

Add this test block near the existing `theme-aware component styles` tests in `frontend-app/src/styles.test.js`:

```js
describe('super-dolphin-agent design tokens', () => {
  it('maps the light theme to exported DESIGN.md tokens', () => {
    const light = declarationsFor('.sa-window[data-theme="light"]');

    expect(light['--bg']).toBe('#fbf9f2');
    expect(light['--bg-elevated']).toBe('#fbf9f3');
    expect(light['--surface']).toBe('#ffffff');
    expect(light['--surface-2']).toBe('#f5f4ed');
    expect(light['--surface-3']).toBe('#f0eee7');
    expect(light['--primary']).toBe('#a03b00');
    expect(light['--primary-2']).toBe('#792b00');
    expect(light['--text-pri']).toBe('#1b1c18');
    expect(light['--text-sec']).toBe('#584238');
    expect(light['--text-muted']).toBe('#8b7268');
  });

  it('keeps the Super Dolphin Agent workbench aliases available for shell and chat surfaces', () => {
    const root = declarationsFor(':root');

    expect(root['--super-dolphin-agent-sidebar-width']).toBe('280px');
    expect(root['--super-dolphin-agent-content-max-width']).toBe('1100px');
    expect(root['--super-dolphin-agent-gutter']).toBe('24px');
    expect(root['--super-dolphin-agent-card-shadow']).toBe('0 20px 40px -10px rgba(0, 0, 0, 0.05)');
    expect(root['--super-dolphin-agent-input-shadow']).toBe('0 8px 30px rgba(0, 0, 0, 0.04)');
  });
});
```

- [ ] **Step 2: Run the focused test and confirm failure**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL. The failure should mention mismatched light theme tokens or missing `--super-dolphin-agent-*` aliases.

- [ ] **Step 3: Implement Super Dolphin Agent tokens**

Modify `frontend-app/src/styles.css` so `:root` defines shared Super Dolphin Agent aliases and the light theme uses the exported `DESIGN.md` values:

```css
:root {
  color-scheme: dark;
  --super-dolphin-agent-background: #fbf9f2;
  --super-dolphin-agent-surface-bright: #fbf9f3;
  --super-dolphin-agent-surface-lowest: #ffffff;
  --super-dolphin-agent-surface-low: #f5f4ed;
  --super-dolphin-agent-surface: #f0eee7;
  --super-dolphin-agent-surface-high: #eae8e1;
  --super-dolphin-agent-surface-highest: #e4e3dc;
  --super-dolphin-agent-on-surface: #1b1c18;
  --super-dolphin-agent-on-surface-variant: #584238;
  --super-dolphin-agent-outline: #8b7268;
  --super-dolphin-agent-outline-variant: #e0c0b3;
  --super-dolphin-agent-primary: #a03b00;
  --super-dolphin-agent-primary-deep: #792b00;
  --super-dolphin-agent-primary-fixed: #ffdbcd;
  --super-dolphin-agent-primary-fixed-dim: #ffb597;
  --super-dolphin-agent-sidebar-width: 280px;
  --super-dolphin-agent-content-max-width: 1100px;
  --super-dolphin-agent-gutter: 24px;
  --super-dolphin-agent-card-shadow: 0 20px 40px -10px rgba(0, 0, 0, 0.05);
  --super-dolphin-agent-input-shadow: 0 8px 30px rgba(0, 0, 0, 0.04);
  /* keep the existing dark theme tokens below this block */
}

.sa-window[data-theme="light"] {
  color-scheme: light;
  --bg: #fbf9f2;
  --bg-elevated: #fbf9f3;
  --app-bg: #fbf9f2;
  --surface: #ffffff;
  --surface-2: #f5f4ed;
  --surface-3: #f0eee7;
  --surface-code: #f5f4ed;
  --overlay: color-mix(in srgb, var(--text-pri) 36%, transparent);
  --border: rgba(224, 192, 179, 0.62);
  --border-strong: rgba(139, 114, 104, 0.42);
  --text-pri: #1b1c18;
  --text-sec: #584238;
  --text-muted: #8b7268;
  --text-subtle: #6f5b52;
  --primary: #a03b00;
  --primary-2: #792b00;
  --primary-ink: #ffffff;
  --primary-soft: rgba(255, 219, 205, 0.68);
  --success: #128b52;
  --warning: #a03b00;
  --error: #ba1a1a;
  --info: #5a5c5c;
  --focus-ring: rgba(160, 59, 0, 0.48);
  --primary-action-bg: var(--primary);
  --primary-action-bg-hover: var(--primary-2);
  --primary-action-border: rgba(121, 43, 0, 0.46);
  --primary-action-text: #ffffff;
  --primary-action-shadow: var(--super-dolphin-agent-input-shadow);
  --shadow: var(--super-dolphin-agent-card-shadow);
  --radius: 16px;
  --radius-sm: 8px;
  --radius-md: 12px;
  --radius-lg: 16px;
  /* keep existing alias assignments in this rule */
}
```

Preserve the existing dark theme tokens unless a later task explicitly adjusts them for contrast.

- [ ] **Step 4: Run the focused test and confirm pass**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git status --short
git add frontend-app/src/styles.css frontend-app/src/styles.test.js
git commit -m "style(frontend): add super-dolphin-agent theme tokens"
```

Expected: commit includes only `styles.css` and `styles.test.js`.

## Task 3: App Shell And Navigation High-Fidelity Pass

**Files:**
- Modify: `frontend-app/src/styles.test.js`
- Modify: `frontend-app/src/AppShell.css`
- Modify: `frontend-app/src/AppChrome.css`
- Test: `frontend-app/src/styles.test.js`

- [ ] **Step 1: Write failing shell style assertions**

Add this test in `frontend-app/src/styles.test.js` near the existing navigation assertions:

```js
describe('super-dolphin-agent shell layout', () => {
  it('uses warm Super Dolphin Agent surfaces for the app shell and primary navigation', () => {
    const navRail = declarationsFor('.nav-rail');
    const activeNav = declarationsFor('.nav-rail button.active');
    const topCommand = declarationsFor('.top-command');
    const main = declarationsFor('.sa-main');

    expect(navRail.background).toBe('var(--bg-elevated)');
    expect(activeNav.background).toBe('var(--primary-soft)');
    expect(activeNav.color).toBe('var(--text-pri)');
    expect(topCommand.background).toBe('var(--bg-elevated)');
    expect(topCommand['border-bottom']).toBe('1px solid var(--line)');
    expect(main.background).toBe('var(--bg)');
  });
});
```

- [ ] **Step 2: Run the focused test and confirm failure**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL on one or more shell declarations if current shell still uses old surfaces.

- [ ] **Step 3: Implement Super Dolphin Agent shell surfaces**

Update `frontend-app/src/AppShell.css` with the Super Dolphin Agent shell rules:

```css
.nav-rail {
  background: var(--bg-elevated);
  border-right: 1px solid var(--line);
}

.nav-rail button {
  border-radius: 0 18px 18px 0;
  color: var(--text-muted);
}

.nav-rail button.active {
  color: var(--text-pri);
  background: var(--primary-soft);
}

.nav-rail button.active::before {
  background: var(--primary);
}

.nav-rail button:hover {
  color: var(--text-pri);
  background: var(--surface-2);
}

.sa-main {
  min-width: 0;
  min-height: 0;
  overflow: hidden;
  background: var(--bg);
}

.top-command {
  border-bottom: 1px solid var(--line);
  background: var(--bg-elevated);
}
```

Update `frontend-app/src/AppChrome.css` only if the outer window still paints the old gradient over the light shell. Use this shape for the light theme override:

```css
.sa-window[data-theme="light"] {
  background: var(--bg);
}

.sa-window[data-theme="light"] .sa-frame,
.sa-window[data-theme="light"] .sa-root {
  background: var(--bg);
}
```

Keep the existing class names and do not add route state.

- [ ] **Step 4: Run shell and global style tests**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js src/App.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git status --short
git add frontend-app/src/AppShell.css frontend-app/src/AppChrome.css frontend-app/src/styles.test.js
git commit -m "style(frontend): apply super-dolphin-agent shell navigation"
```

Expected: commit includes shell CSS and the style test.

## Task 4: Chat Canvas, Timeline, And Message Cards

**Files:**
- Modify: `frontend-app/src/styles.test.js`
- Modify: `frontend-app/src/pages/chat/ChatPage.css`
- Modify: `frontend-app/src/pages/chat/ChatPageWorkbench.css`
- Modify: `frontend-app/src/pages/chat/ChatMessages.css`
- Modify: `frontend-app/src/pages/chat/ChatTimeline.css`
- Modify: `frontend-app/src/pages/chat/ChatReasoning.css`
- Test: `frontend-app/src/styles.test.js`
- Test: `frontend-app/src/pages/chat/ChatPage.test.jsx`
- Test: `frontend-app/src/pages/chat/ChatPage.timeline.test.jsx`

- [ ] **Step 1: Write failing Chat canvas style assertions**

Add this test to `frontend-app/src/styles.test.js`:

```js
describe('super-dolphin-agent chat canvas', () => {
  it('centers the chat canvas and renders message surfaces as warm cards', () => {
    const conversation = declarationsFor('.conversation');
    const activeConversation = declarationsFor('.conversation:not(.conversation--intro)');
    const timeline = declarationsFor('.timeline');
    const assistantMessage = declarationsFor('.message.assistant');
    const userMessage = declarationsFor('.message.user');
    const markdownPre = declarationsFor('.message-markdown pre');

    expect(conversation.background).toBe('var(--bg)');
    expect(activeConversation['grid-template-rows']).toBe('auto minmax(0, 1fr) auto');
    expect(timeline['align-items']).toBe('center');
    expect(assistantMessage.background).toBe('var(--surface)');
    expect(assistantMessage['box-shadow']).toBe('var(--super-dolphin-agent-card-shadow)');
    expect(userMessage.background).toBe('var(--surface-3)');
    expect(markdownPre.background).toBe('var(--surface-code)');
  });
});
```

- [ ] **Step 2: Run focused style and Chat tests and confirm failure**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js src/pages/chat/ChatPage.test.jsx src/pages/chat/ChatPage.timeline.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL on new style assertions; existing Chat behavior tests should continue to pass or fail only because the new assertions are not implemented.

- [ ] **Step 3: Implement Chat workbench and canvas surfaces**

Update `frontend-app/src/pages/chat/ChatPage.css`:

```css
.conversation {
  --conversation-content-width: min(var(--super-dolphin-agent-content-max-width), calc(100% - clamp(32px, 7vw, 112px)));
  --conversation-content-left-nudge: 0px;
  border-right: 1px solid var(--line);
  background: var(--bg);
}

.conversation:not(.conversation--intro) {
  display: grid;
  grid-template-rows: auto minmax(0, 1fr) auto;
  overflow: hidden;
}

.timeline {
  align-items: center;
  padding: 24px 0 clamp(112px, 16vh, 172px);
}

.chat-scroll-bottom-btn {
  border: 1px solid var(--line-strong);
  background: var(--surface);
  color: var(--text-sec);
  box-shadow: var(--super-dolphin-agent-card-shadow);
}

.splitter {
  background: var(--surface-2);
}

.splitter:hover,
.splitter:focus-visible {
  background: var(--surface-3);
}
```

Update `frontend-app/src/pages/chat/ChatPageWorkbench.css` to keep late cascade aligned:

```css
.chat-page-header {
  background: var(--bg);
}

.conversation {
  --conversation-content-width: min(var(--super-dolphin-agent-content-max-width), max(0px, calc(100% - clamp(32px, 7vw, 112px))));
  background: var(--bg);
}

.timeline-shell,
.timeline {
  background: transparent;
}
```

- [ ] **Step 4: Implement message and reasoning surfaces**

Update `frontend-app/src/pages/chat/ChatMessages.css` by mapping message shells to Super Dolphin Agent cards:

```css
.message {
  border: 1px solid var(--line);
  border-radius: 18px;
  background: var(--surface);
  box-shadow: var(--super-dolphin-agent-card-shadow);
}

.message.assistant {
  background: var(--surface);
  color: var(--text-pri);
}

.message.user {
  background: var(--surface-3);
  color: var(--text-pri);
  box-shadow: none;
}

.message-markdown,
.message-output {
  color: var(--text-pri);
}

.message-markdown code {
  background: var(--surface-2);
}

.message-markdown pre,
.message-output pre {
  background: var(--surface-code);
  color: var(--text-pri);
  border: 1px solid var(--line);
  border-radius: 12px;
}
```

Update `frontend-app/src/pages/chat/ChatReasoning.css` with matching surfaces for reasoning/tool blocks:

```css
.reasoning-card,
.tool-call-card,
.approval-card {
  border: 1px solid var(--line);
  border-radius: 16px;
  background: var(--surface-2);
  color: var(--text-pri);
}
```

If the exact selectors differ, use LSP/file evidence and the existing selector names from `ChatReasoning.css`; do not create unused selectors.

- [ ] **Step 5: Run focused tests**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js src/pages/chat/ChatPage.test.jsx src/pages/chat/ChatPage.timeline.test.jsx src/pages/chat/ChatPage.preview.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git status --short
git add frontend-app/src/styles.test.js frontend-app/src/pages/chat/ChatPage.css frontend-app/src/pages/chat/ChatPageWorkbench.css frontend-app/src/pages/chat/ChatMessages.css frontend-app/src/pages/chat/ChatTimeline.css frontend-app/src/pages/chat/ChatReasoning.css
git commit -m "style(chat): apply super-dolphin-agent conversation canvas"
```

Expected: commit includes only Chat canvas/message CSS and the related style test.

## Task 5: Floating Composer High-Fidelity Treatment

**Files:**
- Modify: `frontend-app/src/styles.test.js`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.css`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`
- Test: `frontend-app/src/styles.test.js`
- Test: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`

- [ ] **Step 1: Write failing composer style assertions**

Add this test in `frontend-app/src/styles.test.js` inside `describe('composer layout styles', ...)`:

```js
it('renders the Super Dolphin Agent floating composer as a raised white input object', () => {
  const floatingCard = declarationsFor('.composer--floating .composer-card');
  const textarea = declarationsFor('.composer--floating textarea');
  const meta = declarationsFor('.composer--floating .composer-meta');
  const send = declarationsFor('.composer .send');
  const disabledSend = declarationsFor('.composer .send:disabled');

  expect(floatingCard.background).toBe('var(--surface)');
  expect(floatingCard['border-radius']).toBe('28px');
  expect(floatingCard['box-shadow']).toContain('var(--super-dolphin-agent-input-shadow)');
  expect(textarea.padding).toBe('22px 26px 12px');
  expect(meta['min-height']).toBe('58px');
  expect(send.background).toBe('var(--primary-action-bg)');
  expect(disabledSend.background).toBe('var(--surface-2)');
});
```

- [ ] **Step 2: Preserve existing behavior tests**

Add a class/state assertion to `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx` in the existing floating composer test:

```jsx
it('uses the floating class for the new-chat intro composer', () => {
  render(<ComposerDock {...baseProps} floating composer={createComposer()} store={createStore()} />);

  expect(screen.getByTestId('composer-dock')).toHaveClass('composer--floating');
  expect(screen.getByTestId('composer-dock')).not.toHaveClass('composer--docked');
  expect(screen.getByTestId('composer-dock').querySelector('.composer-card')).toBeInTheDocument();
});
```

- [ ] **Step 3: Run focused tests and confirm failure**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js src/pages/chat/composer/ComposerDock.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL on new Super Dolphin Agent composer style assertions if CSS is still on the old dark treatment.

- [ ] **Step 4: Implement composer CSS**

Update `frontend-app/src/pages/chat/composer/ComposerDock.css`:

```css
.composer {
  border-top: 1px solid var(--line);
  background: var(--bg);
}

.composer--floating {
  width: min(920px, calc(100% - 48px));
  padding: 0;
  border-top: 0;
  background: transparent;
}

.composer--floating .composer-card {
  width: 100%;
  margin: 0 auto;
  border: 1px solid var(--line);
  border-radius: 28px;
  background: var(--surface);
  box-shadow: var(--super-dolphin-agent-input-shadow), inset 0 1px 0 rgba(255, 255, 255, 0.82);
}

.composer--floating textarea {
  padding: 22px 26px 12px;
  font-size: 17px;
}

.composer--floating .composer-meta {
  min-height: 58px;
  padding: 0 22px 16px;
}

.composer button {
  border: 1px solid var(--line);
  border-radius: 999px;
  background: var(--surface-2);
  color: var(--text-sec);
}

.composer .send {
  border-color: var(--primary-action-border);
  background: var(--primary-action-bg);
  color: var(--primary-action-text);
  box-shadow: var(--primary-action-shadow);
}

.composer .send:hover,
.composer .send:focus-visible {
  background: var(--primary-action-bg-hover);
}

.composer .send:disabled {
  border-color: var(--line);
  background: var(--surface-2);
  color: var(--text-subtle);
  box-shadow: none;
}

.composer .send--interrupt {
  border-color: color-mix(in srgb, var(--error) 48%, var(--line));
  background: color-mix(in srgb, var(--error) 14%, var(--surface));
  color: var(--error);
}
```

Keep the existing IME, paste, drag/drop, attachment, provider, and interrupt markup unchanged.

- [ ] **Step 5: Run focused tests**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js src/pages/chat/composer/ComposerDock.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git status --short
git add frontend-app/src/styles.test.js frontend-app/src/pages/chat/composer/ComposerDock.css frontend-app/src/pages/chat/composer/ComposerDock.test.jsx
git commit -m "style(chat): redesign composer with super-dolphin-agent input"
```

Expected: commit includes composer CSS, composer test, and style test.

## Task 6: Runtime Inspector Super Dolphin Agent Treatment

**Files:**
- Modify: `frontend-app/src/styles.test.js`
- Modify: `frontend-app/src/pages/chat/runtime/RuntimePanel.css`
- Modify: `frontend-app/src/pages/chat/components/RuntimePanelPolish.css`
- Test: `frontend-app/src/styles.test.js`
- Test: `frontend-app/src/pages/chat/components/RuntimePanelComponents.test.jsx`

- [ ] **Step 1: Write failing runtime inspector assertions**

Update the existing `keeps runtime panel details shrink-safe inside the right rail` test in `frontend-app/src/styles.test.js` to assert Super Dolphin Agent inspector surfaces:

```js
it('keeps runtime panel details shrink-safe inside the right rail', () => {
  const panel = declarationsFor('.runtime-panel');
  const toolbar = declarationsFor('.runtime-toolbar');
  const activityPanel = declarationsFor('.runtime-activity-panel');
  const diffGroup = declarationsFor('.diff-file-group');
  const icons = declarationsFor('.runtime-icons');
  const logs = declarationsFor('.log-lines');
  const tooltipRow = declarationsFor('.runtime-stat-tooltip-row');
  const tooltipName = declarationsFor('.runtime-stat-tooltip-name');
  const logLine = declarationsFor('.warning-log-line');

  expect(panel.position).toBe('relative');
  expect(Number(panel['z-index'])).toBeGreaterThan(20);
  expect(panel.overflow).toBe('hidden');
  expect(panel.background).toBe('var(--surface-2)');
  expect(toolbar.background).toBe('var(--surface-2)');
  expect(diffGroup.background).toBe('var(--surface)');
  expect(activityPanel['border-top']).toBe('1px solid var(--line)');
  expect(activityPanel.background).toBe('var(--surface-2)');
  expect(activityPanel['min-width']).toBe('0');
  expect(activityPanel['max-width']).toBe('100%');
  expect(activityPanel.overflow).toBe('hidden');
  expect(icons['min-width']).toBe('0');
  expect(icons.overflow).toBe('visible');
  expect(logs['min-width']).toBe('0');
  expect(logs['max-width']).toBe('100%');
  expect(logs['overflow-x']).toBe('hidden');
  expect(tooltipRow['min-width']).toBe('0');
  expect(tooltipName['min-width']).toBe('0');
  expect(tooltipName.overflow).toBe('visible');
  expect(tooltipName['white-space']).toBe('normal');
  expect(logLine['min-width']).toBe('0');
  expect(logLine.display).toBe('block');
  expect(logLine.width).toBe('100%');
  expect(logLine['max-width']).toBe('100%');
  expect(logLine.overflow).toBe('hidden');
  expect(logLine['text-overflow']).toBe('ellipsis');
});
```

- [ ] **Step 2: Run focused runtime tests and confirm failure**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js src/pages/chat/components/RuntimePanelComponents.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL if runtime surfaces still use old background values.

- [ ] **Step 3: Implement runtime inspector CSS**

Update `frontend-app/src/pages/chat/runtime/RuntimePanel.css`:

```css
.runtime-panel {
  border-left: 1px solid var(--line);
  background: var(--surface-2);
}

.runtime-toolbar {
  border-bottom: 1px solid var(--line);
  background: var(--surface-2);
}

.runtime-toolbar button,
.score {
  border: 1px solid var(--line);
  background: var(--surface);
  color: var(--text-sec);
}

.diff-empty {
  border: 1px dashed var(--line-strong);
  border-radius: 16px;
  background: var(--surface);
  color: var(--text-muted);
}

.diff-file-group {
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--surface);
}

.diff-file-header {
  border-bottom: 1px solid var(--line);
  background: var(--surface-3);
}

.runtime-activity-panel {
  border-top: 1px solid var(--line);
  background: var(--surface-2);
}
```

If `RuntimePanelPolish.css` overrides any of these selectors later in cascade, update it with token-compatible values rather than increasing selector specificity unnecessarily.

- [ ] **Step 4: Run focused tests**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js src/pages/chat/components/RuntimePanelComponents.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git status --short
git add frontend-app/src/styles.test.js frontend-app/src/pages/chat/runtime/RuntimePanel.css frontend-app/src/pages/chat/components/RuntimePanelPolish.css
git commit -m "style(chat): soften runtime inspector surfaces"
```

Expected: commit includes runtime CSS and style test only, unless `RuntimePanelComponents.test.jsx` needed a behavior-preserving assertion.

## Task 7: Responsive, Visual, And Full Frontend Verification

**Files:**
- Modify: `frontend-app/src/styles.test.js`
- Modify: `frontend-app/src/pages/chat/ChatPage.css`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.css`
- Test: `frontend-app/src/styles.test.js`
- Test: all frontend tests through scripts

- [ ] **Step 1: Add responsive no-overlap CSS assertions**

Add this test to `frontend-app/src/styles.test.js`:

```js
describe('super-dolphin-agent responsive chat workbench', () => {
  it('collapses side surfaces before the message canvas becomes unreadable', () => {
    const narrowConversation = mediaDeclarationFor('(max-width: 760px)', '.conversation', 'border-right');
    const narrowFloatingComposer = mediaDeclarationFor('(max-width: 760px)', '.composer--floating', 'width');
    const narrowTimeline = mediaDeclarationFor('(max-width: 760px)', '.timeline', 'padding-bottom');

    expect(narrowConversation['border-right']).toBe('0');
    expect(narrowFloatingComposer.width).toBe('calc(100% - 24px)');
    expect(narrowTimeline['padding-bottom']).toBe('clamp(104px, 18vh, 156px)');
  });
});
```

- [ ] **Step 2: Run the focused test and confirm failure**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL if responsive rules are not yet present.

- [ ] **Step 3: Implement responsive rules**

Add the responsive rules to the CSS file that owns the selector. Prefer `ChatPage.css` for `.conversation` / `.timeline` and `ComposerDock.css` for `.composer--floating`:

```css
@media (max-width: 760px) {
  .conversation {
    border-right: 0;
  }

  .timeline {
    padding-bottom: clamp(104px, 18vh, 156px);
  }

  .composer--floating {
    width: calc(100% - 24px);
  }

  .composer--floating textarea {
    padding: 18px 18px 10px;
    font-size: 16px;
  }

  .composer--floating .composer-meta {
    padding: 0 16px 14px;
    gap: 8px;
  }
}
```

- [ ] **Step 4: Run focused and page tests**

Run:

```bash
cd frontend-app
npx vitest run src/styles.test.js src/pages/chat/ChatPage.test.jsx src/pages/chat/ChatPage.scroll.test.jsx src/pages/chat/composer/ComposerDock.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

- [ ] **Step 5: Run full frontend verification**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all three commands PASS. If `npm run build` updates `cmd/agent-terminal/web-dist`, include generated embed output only if this repository convention requires it for frontend build commits; otherwise leave generated build artifacts unstaged and report them.

- [ ] **Step 6: Run manual visual smoke with local dev server**

Start the frontend dev server:

```bash
cd frontend-app
npm run dev
```

Expected: Vite serves on `http://127.0.0.1:5175`.

Inspect these states in browser automation or the in-app browser:

```text
Desktop 1440x1000:
- New Chat intro state
- Active Chat with messages
- Runtime panel open
- Runtime panel closed

Narrow 390x844:
- New Chat intro state
- Active Chat with composer visible
```

Expected: no overlapping controls, no clipped essential text, composer remains legible, runtime stays scannable, and code/diff blocks remain readable.

- [ ] **Step 7: Commit final responsive and verification updates**

Run:

```bash
git status --short
git add frontend-app/src/styles.test.js frontend-app/src/pages/chat/ChatPage.css frontend-app/src/pages/chat/composer/ComposerDock.css
git commit -m "style(chat): verify super-dolphin-agent responsive workbench"
```

Expected: final implementation commit includes responsive CSS and tests only.

## Final Integration Checklist

- [ ] Confirm `.superpowers/brainstorm/**` is not staged.
- [ ] Confirm `DESIGN.md` remains unchanged from the design commit unless the user approved design-system edits.
- [ ] Confirm no backend, store, or API files changed.
- [ ] Confirm LSP diagnostics were run after edits on touched React files.
- [ ] Confirm `cd frontend-app && npm run lint && npm test && npm run build` passed.
- [ ] Confirm visual smoke covered desktop and narrow widths.
- [ ] Provide final summary with changed files, test results, visual smoke result, and any residual risks.
