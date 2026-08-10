# Agentic E2E Business Flow Discovery Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a discovery-oriented Agentic E2E harness that identifies visible frontend business flows, explores only safe read-oriented actions, and writes JSON/Markdown discovery reports.

**Architecture:** Keep the React frontend unchanged and evolve only `frontend-app/scripts`. Split the current fixed planner into small harness units: DOM facts, business discovery, safety classification, readiness selection, reporting, and runner integration. Preserve the existing fixed observability probe as the first executable discovery path while adding aggregate flow inventory output.

**Tech Stack:** Node ESM scripts, Playwright, Vitest, existing `frontend-app` npm scripts.

**Verification Surface:** `frontend-app/scripts/*.mjs`, `frontend-app/scripts/agentic-e2e.test.mjs`, `frontend-app/package.json`, local `npm run agentic:e2e`, and standard frontend `npm run lint && npm test && npm run build`.

---

## File Structure

- Create `frontend-app/scripts/agentic-e2e-discovery.mjs`: pure business-flow discovery helpers. It extracts entries/actions from DOM summaries, normalizes flow records, and applies safety policy.
- Create `frontend-app/scripts/agentic-e2e-reporter.mjs`: pure report helpers. It writes `business-flow-discovery.json` data shape and renders the Markdown report body.
- Modify `frontend-app/scripts/agentic-e2e.mjs`: import discovery/report helpers, collect richer DOM summaries, track discovered/blocked/executed flows, write aggregate reports, and add readiness waits after actions.
- Modify `frontend-app/scripts/agentic-e2e-planner.mjs`: keep the existing fixed probe behavior, but allow discovery metadata to be attached to actions without changing product frontend code.
- Modify `frontend-app/scripts/agentic-e2e.test.mjs`: add focused unit coverage for discovery, safety, readiness, and report shape.

Do not modify `frontend-app/src/**` in this plan unless a later verification run proves a missing stable test id blocks discovery. If that happens, stop and ask for a scoped follow-up.

## Task 1: Add Discovery Model And Safety Policy

**Files:**
- Create: `frontend-app/scripts/agentic-e2e-discovery.mjs`
- Test: `frontend-app/scripts/agentic-e2e.test.mjs`

- [ ] **Step 1: Write failing discovery and safety tests**

Add imports near the top of `frontend-app/scripts/agentic-e2e.test.mjs`:

```js
import {
  BLOCKED_ACTION_KEYWORDS,
  discoverBusinessFlows,
  safetyForAction,
} from './agentic-e2e-discovery.mjs';
```

Add this test block after the existing planner tests:

```js
describe('agentic e2e business discovery', () => {
  it('discovers sidebar entries and safe query actions from DOM summary', () => {
    const flows = discoverBusinessFlows({
      url: 'http://127.0.0.1:5176/',
      title: 'Super Dolphin Agent',
      domSummary: [
        { tag: 'button', role: '', testId: '', ariaLabel: '链路追踪', text: '', disabled: false, sourceTestId: 'sidebar-secondary-nav' },
        { tag: 'button', role: '', testId: '', ariaLabel: 'Settings', text: '', disabled: false, sourceTestId: 'app-sidebar' },
        { tag: 'button', role: '', testId: '', ariaLabel: '', text: '查询最新日志', disabled: false },
      ],
    });

    expect(flows.map((flow) => flow.entry.label)).toContain('链路追踪');
    expect(flows.map((flow) => flow.entry.label)).toContain('Settings');
    expect(flows.flatMap((flow) => flow.actions).some((action) => action.label === '查询最新日志' && action.safety === 'allowed')).toBe(true);
  });

  it('blocks mutating and provider-turn actions', () => {
    expect(BLOCKED_ACTION_KEYWORDS).toContain('删除');
    expect(safetyForAction({ label: '发送', type: 'click' })).toEqual(expect.objectContaining({
      safety: 'blocked',
      reason: expect.stringContaining('mutating or provider action'),
    }));
    expect(safetyForAction({ label: '查询最新日志', type: 'click' })).toEqual(expect.objectContaining({
      safety: 'allowed',
    }));
  });
});
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: FAIL because `agentic-e2e-discovery.mjs` does not exist.

- [ ] **Step 3: Implement minimal discovery helpers**

Create `frontend-app/scripts/agentic-e2e-discovery.mjs`:

```js
export const BLOCKED_ACTION_KEYWORDS = Object.freeze([
  '发送', '中断', '保存', '应用', '删除', '重置', '移除', '上传', '导入', '导出', '安装',
  'send', 'interrupt', 'save', 'apply', 'delete', 'reset', 'remove', 'upload', 'import', 'export', 'install',
]);

const ALLOWED_ACTION_KEYWORDS = Object.freeze([
  '查询', '搜索', '筛选', '展开', '收起', '打开', '详情',
  'query', 'search', 'filter', 'expand', 'collapse', 'open', 'detail',
]);

export function discoverBusinessFlows(facts = {}) {
  const route = routeFromURL(facts.url);
  const entries = businessEntriesFromDOMSummary(facts.domSummary || [], route);
  const actions = businessActionsFromDOMSummary(facts.domSummary || []);
  return entries.map((entry) => ({
    id: flowID(entry),
    entry,
    page: {
      route,
      title: normalizeString(facts.title),
      heading: firstHeadingText(facts.domSummary || []),
      testIds: unique((facts.domSummary || []).map((item) => normalizeString(item.testId)).filter(Boolean)),
    },
    actions,
    result: { status: 'candidate', summary: 'Discovered from visible page structure' },
  }));
}

export function safetyForAction(action = {}) {
  const label = normalizeString(action.label || action.name || action.text);
  const lowerLabel = label.toLowerCase();
  const blocked = BLOCKED_ACTION_KEYWORDS.find((keyword) => lowerLabel.includes(keyword.toLowerCase()));
  if (blocked) {
    return { safety: 'blocked', reason: `mutating or provider action keyword: ${blocked}` };
  }
  const allowed = ALLOWED_ACTION_KEYWORDS.find((keyword) => lowerLabel.includes(keyword.toLowerCase()));
  if (allowed) return { safety: 'allowed', reason: `read-oriented action keyword: ${allowed}` };
  if (action.source === 'navigation') return { safety: 'allowed', reason: 'navigation entry' };
  return { safety: 'blocked', reason: 'action is not recognized as read-only' };
}

export function businessEntriesFromDOMSummary(summary = [], route = '/') {
  return summary
    .filter((item) => isButtonLike(item) && normalizeString(item.sourceTestId || item.parentTestId || item.testId).includes('sidebar'))
    .map((item) => ({
      route,
      label: visibleName(item),
      source: normalizeString(item.sourceTestId || item.parentTestId || item.testId || 'visible-navigation'),
    }))
    .filter((entry) => entry.label);
}

export function businessActionsFromDOMSummary(summary = []) {
  return summary
    .filter((item) => isButtonLike(item))
    .map((item) => {
      const label = visibleName(item);
      const classified = safetyForAction({ type: 'click', label });
      return {
        type: 'click',
        label,
        target: item.testId ? { type: 'testId', value: item.testId } : { type: 'role', role: 'button', name: label },
        safety: classified.safety,
        reason: classified.reason,
      };
    })
    .filter((action) => action.label);
}

function isButtonLike(item = {}) {
  return item.tag === 'button' || item.role === 'button';
}

function visibleName(item = {}) {
  return normalizeString(item.ariaLabel || item.text || item.testId);
}

function firstHeadingText(summary = []) {
  const heading = summary.find((item) => item.role === 'heading' || /^h[1-6]$/i.test(item.tag));
  return heading ? visibleName(heading) : '';
}

function flowID(entry) {
  return `visible-${slug(entry.source)}-${slug(entry.label)}`;
}

function routeFromURL(value) {
  try {
    return new URL(value).pathname || '/';
  }
  catch {
    return '/';
  }
}

function unique(values) {
  return Array.from(new Set(values));
}

function slug(value) {
  return normalizeString(value).toLowerCase().replace(/[^a-z0-9\u4e00-\u9fff]+/gi, '-').replace(/^-+|-+$/g, '') || 'unknown';
}

function normalizeString(value) {
  return String(value ?? '').trim();
}
```

- [ ] **Step 4: Run focused tests and confirm pass**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: PASS for the new discovery tests and existing tests.

- [ ] **Step 5: Commit Task 1**

```bash
git add frontend-app/scripts/agentic-e2e-discovery.mjs frontend-app/scripts/agentic-e2e.test.mjs
git commit -m "feat: 添加 agentic e2e 业务发现策略"
```

## Task 2: Enrich DOM Summary For Discovery

**Files:**
- Modify: `frontend-app/scripts/agentic-e2e.mjs`
- Test: `frontend-app/scripts/agentic-e2e.test.mjs`

- [ ] **Step 1: Write failing DOM summary normalization test**

Import the new helper from the runner:

```js
import { agenticE2EConfig, normalizeDOMSummaryItem } from './agentic-e2e.mjs';
```

Add:

```js
describe('agentic e2e DOM facts', () => {
  it('normalizes discovery fields from DOM summary items', () => {
    expect(normalizeDOMSummaryItem({
      tag: 'button',
      role: '',
      testId: '',
      parentTestId: 'sidebar-secondary-nav',
      ariaLabel: '链路追踪',
      text: '',
      disabled: false,
    })).toEqual({
      tag: 'button',
      role: '',
      testId: '',
      parentTestId: 'sidebar-secondary-nav',
      sourceTestId: 'sidebar-secondary-nav',
      ariaLabel: '链路追踪',
      text: '',
      disabled: false,
    });
  });
});
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: FAIL because `normalizeDOMSummaryItem` is not exported.

- [ ] **Step 3: Export normalization and enrich browser DOM summary**

In `frontend-app/scripts/agentic-e2e.mjs`, change the `domSummary` map to include parent test id and headings:

```js
async function domSummary(page) {
  return page.evaluate(() => {
    function closestTestId(element) {
      const parent = element.parentElement?.closest?.('[data-testid]');
      return parent ? parent.getAttribute('data-testid') || '' : '';
    }
    return Array.from(document.querySelectorAll('button, input, textarea, select, [role], [data-testid], h1, h2, h3'))
      .slice(0, 180)
      .map((element) => ({
        tag: element.tagName.toLowerCase(),
        role: element.getAttribute('role') || '',
        testId: element.getAttribute('data-testid') || '',
        parentTestId: closestTestId(element),
        ariaLabel: element.getAttribute('aria-label') || '',
        text: (element.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 100),
        disabled: Boolean(element.disabled || element.getAttribute('aria-disabled') === 'true'),
      }))
      .map((item) => normalizeDOMSummaryItem(item));
  }).catch((error) => [{ error: error.message }]);
}
```

Add this export near `collectPageFacts`:

```js
export function normalizeDOMSummaryItem(item = {}) {
  const parentTestId = normalizeString(item.parentTestId);
  const testId = normalizeString(item.testId);
  return {
    tag: normalizeString(item.tag),
    role: normalizeString(item.role),
    testId,
    parentTestId,
    sourceTestId: parentTestId || testId,
    ariaLabel: normalizeString(item.ariaLabel),
    text: normalizeString(item.text),
    disabled: Boolean(item.disabled),
  };
}
```

- [ ] **Step 4: Run focused tests and confirm pass**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

```bash
git add frontend-app/scripts/agentic-e2e.mjs frontend-app/scripts/agentic-e2e.test.mjs
git commit -m "feat: 扩展 agentic e2e DOM 摘要"
```

## Task 3: Add Discovery Report Rendering

**Files:**
- Create: `frontend-app/scripts/agentic-e2e-reporter.mjs`
- Test: `frontend-app/scripts/agentic-e2e.test.mjs`

- [ ] **Step 1: Write failing report tests**

Add imports:

```js
import { renderDiscoveryMarkdown, summarizeDiscovery } from './agentic-e2e-reporter.mjs';
```

Add:

```js
describe('agentic e2e discovery report', () => {
  it('summarizes discovered, executed, and blocked flows', () => {
    const summary = summarizeDiscovery({
      flows: [{
        id: 'visible-sidebar-secondary-nav-链路追踪',
        entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [
          { type: 'click', label: '查询最新日志', safety: 'allowed', reason: 'read-oriented action keyword: 查询' },
          { type: 'click', label: '删除日志', safety: 'blocked', reason: 'mutating or provider action keyword: 删除' },
        ],
        result: { status: 'discovered', summary: 'Recent log table became visible' },
      }],
    });

    expect(summary.totalFlows).toBe(1);
    expect(summary.allowedActions).toBe(1);
    expect(summary.blockedActions).toBe(1);
  });

  it('renders a human-readable markdown report', () => {
    const markdown = renderDiscoveryMarkdown({
      summary: { totalFlows: 1, allowedActions: 1, blockedActions: 1 },
      flows: [{
        id: 'visible-sidebar-secondary-nav-链路追踪',
        entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [{ type: 'click', label: '查询最新日志', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }],
        result: { status: 'discovered', summary: 'Recent log table became visible' },
      }],
    });

    expect(markdown).toContain('# Agentic E2E Business Flow Discovery');
    expect(markdown).toContain('链路追踪');
    expect(markdown).toContain('查询最新日志');
  });
});
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: FAIL because `agentic-e2e-reporter.mjs` does not exist.

- [ ] **Step 3: Implement report helpers**

Create `frontend-app/scripts/agentic-e2e-reporter.mjs`:

```js
export function summarizeDiscovery({ flows = [] } = {}) {
  const actions = flows.flatMap((flow) => flow.actions || []);
  return {
    totalFlows: flows.length,
    allowedActions: actions.filter((action) => action.safety === 'allowed').length,
    blockedActions: actions.filter((action) => action.safety === 'blocked').length,
  };
}

export function renderDiscoveryMarkdown({ summary = summarizeDiscovery(), flows = [] } = {}) {
  const lines = [
    '# Agentic E2E Business Flow Discovery',
    '',
    `- Total flows: ${summary.totalFlows}`,
    `- Allowed actions: ${summary.allowedActions}`,
    `- Blocked actions: ${summary.blockedActions}`,
    '',
  ];
  for (const flow of flows) {
    lines.push(`## ${flow.entry?.label || flow.id}`);
    lines.push('');
    lines.push(`- ID: \`${flow.id}\``);
    lines.push(`- Entry: ${flow.entry?.source || 'unknown'} from \`${flow.entry?.route || '/'}\``);
    lines.push(`- Page: \`${flow.page?.route || '/'}\`${flow.page?.heading ? `, heading "${flow.page.heading}"` : ''}`);
    lines.push(`- Result: ${flow.result?.status || 'candidate'} - ${flow.result?.summary || 'No summary'}`);
    lines.push('');
    lines.push('| Safety | Type | Label | Reason |');
    lines.push('|---|---|---|---|');
    for (const action of flow.actions || []) {
      lines.push(`| ${escapeCell(action.safety)} | ${escapeCell(action.type)} | ${escapeCell(action.label)} | ${escapeCell(action.reason)} |`);
    }
    lines.push('');
  }
  return `${lines.join('\n').trim()}\n`;
}

function escapeCell(value) {
  return String(value ?? '').replace(/\|/g, '\\|').replace(/\n+/g, ' ').trim();
}
```

- [ ] **Step 4: Run focused tests and confirm pass**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```bash
git add frontend-app/scripts/agentic-e2e-reporter.mjs frontend-app/scripts/agentic-e2e.test.mjs
git commit -m "feat: 生成 agentic e2e 发现报告"
```

## Task 4: Integrate Discovery Into The Runner

**Files:**
- Modify: `frontend-app/scripts/agentic-e2e.mjs`
- Test: `frontend-app/scripts/agentic-e2e.test.mjs`

- [ ] **Step 1: Write failing aggregate result test**

Update imports:

```js
import { agenticE2EConfig, mergeDiscoveredFlows, normalizeDOMSummaryItem } from './agentic-e2e.mjs';
```

Add:

```js
describe('agentic e2e discovery aggregation', () => {
  it('merges discovered flows by id without losing blocked actions', () => {
    const merged = mergeDiscoveredFlows([
      { id: 'flow-a', actions: [{ label: '查询', safety: 'allowed' }] },
    ], [
      { id: 'flow-a', actions: [{ label: '删除', safety: 'blocked' }], result: { status: 'candidate', summary: 'second sample' } },
      { id: 'flow-b', actions: [], result: { status: 'candidate', summary: 'new flow' } },
    ]);

    expect(merged).toHaveLength(2);
    expect(merged.find((flow) => flow.id === 'flow-a').actions).toHaveLength(2);
    expect(merged.find((flow) => flow.id === 'flow-b').result.summary).toBe('new flow');
  });
});
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: FAIL because `mergeDiscoveredFlows` is not exported.

- [ ] **Step 3: Wire discovery and reports into runner**

In `frontend-app/scripts/agentic-e2e.mjs`, add imports:

```js
import { discoverBusinessFlows } from './agentic-e2e-discovery.mjs';
import { renderDiscoveryMarkdown, summarizeDiscovery } from './agentic-e2e-reporter.mjs';
```

Inside `runAgenticE2E`, initialize:

```js
let discoveredFlows = [];
```

After collecting `facts` in each loop:

```js
discoveredFlows = mergeDiscoveredFlows(discoveredFlows, discoverBusinessFlows(facts));
```

Change final and failure evidence calls to pass `discoveredFlows`:

```js
await writeFinalEvidence(config.outputDir, page, steps, consoleMessages, networkRequests, discoveredFlows);
await writeFailureEvidence(config.outputDir, page, steps, consoleMessages, networkRequests, error, discoveredFlows);
```

Add exported merge helper:

```js
export function mergeDiscoveredFlows(existing = [], next = []) {
  const byID = new Map(existing.map((flow) => [flow.id, { ...flow, actions: [...(flow.actions || [])] }]));
  for (const flow of next) {
    const current = byID.get(flow.id);
    if (!current) {
      byID.set(flow.id, { ...flow, actions: [...(flow.actions || [])] });
      continue;
    }
    const actionKeys = new Set(current.actions.map(actionKey));
    for (const action of flow.actions || []) {
      const key = actionKey(action);
      if (!actionKeys.has(key)) {
        current.actions.push(action);
        actionKeys.add(key);
      }
    }
    current.page = { ...current.page, ...flow.page };
    current.result = flow.result || current.result;
  }
  return Array.from(byID.values());
}

function actionKey(action = {}) {
  return `${action.type}|${action.label}|${action.safety}|${action.reason}`;
}
```

Update evidence writers:

```js
async function writeFinalEvidence(outputDir, page, steps, consoleMessages, networkRequests, discoveredFlows = []) {
  await page.screenshot({ path: path.join(outputDir, 'final.png'), fullPage: true });
  await writeDiscoveryReports(outputDir, discoveredFlows);
  await writeJSON(path.join(outputDir, 'result.json'), { success: true, steps, consoleMessages, networkRequests, discovery: summarizeDiscovery({ flows: discoveredFlows }) });
}

async function writeFailureEvidence(outputDir, page, steps, consoleMessages, networkRequests, error, discoveredFlows = []) {
  await page.screenshot({ path: path.join(outputDir, 'failure.png'), fullPage: true }).catch(() => {});
  await writeDiscoveryReports(outputDir, discoveredFlows);
  await writeJSON(path.join(outputDir, 'result.json'), {
    success: false,
    error: error.message,
    steps,
    consoleMessages,
    networkRequests,
    discovery: summarizeDiscovery({ flows: discoveredFlows }),
  });
}

async function writeDiscoveryReports(outputDir, flows) {
  const summary = summarizeDiscovery({ flows });
  await writeJSON(path.join(outputDir, 'business-flow-discovery.json'), { summary, flows });
  await writeFile(path.join(outputDir, 'business-flow-discovery.md'), renderDiscoveryMarkdown({ summary, flows }), 'utf8');
}
```

- [ ] **Step 4: Run focused tests and confirm pass**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: PASS.

- [ ] **Step 5: Commit Task 4**

```bash
git add frontend-app/scripts/agentic-e2e.mjs frontend-app/scripts/agentic-e2e.test.mjs
git commit -m "feat: 接入 agentic e2e 发现汇总"
```

## Task 5: Replace Fixed Sleep With Readiness Selection

**Files:**
- Modify: `frontend-app/scripts/agentic-e2e.mjs`
- Test: `frontend-app/scripts/agentic-e2e.test.mjs`

- [ ] **Step 1: Write failing readiness tests**

Update imports:

```js
import { agenticE2EConfig, mergeDiscoveredFlows, normalizeDOMSummaryItem, readinessForAction } from './agentic-e2e.mjs';
```

Add:

```js
describe('agentic e2e readiness', () => {
  it('waits for observability page after clicking its sidebar entry', () => {
    expect(readinessForAction({
      type: 'click',
      target: { type: 'nestedRole', parentTestId: 'sidebar-secondary-nav', role: 'button', name: '链路追踪' },
    })).toEqual({ type: 'testId', value: 'observability-page' });
  });

  it('waits for recent logs after querying observability', () => {
    expect(readinessForAction({
      type: 'click',
      target: { type: 'role', role: 'button', name: '查询最新日志' },
    })).toEqual({ type: 'testId', value: 'observability-recent-logs' });
  });
});
```

- [ ] **Step 2: Run focused tests and confirm failure**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: FAIL because `readinessForAction` is not exported.

- [ ] **Step 3: Implement readiness helper and action wait**

In `frontend-app/scripts/agentic-e2e.mjs`, export:

```js
export function readinessForAction(action = {}) {
  const name = normalizeString(action.target?.name || action.target?.value || action.reason);
  if (name.includes('链路追踪')) return { type: 'testId', value: 'observability-page' };
  if (name.includes('查询最新日志')) return { type: 'testId', value: 'observability-recent-logs' };
  if (action.type === 'goto') return { type: 'testId', value: 'frontend-app' };
  return { type: 'stableDOM' };
}
```

In the runner loop, replace:

```js
await page.waitForTimeout(150);
```

with:

```js
await waitForReadiness(page, readinessForAction(action));
```

Add:

```js
async function waitForReadiness(page, readiness) {
  if (readiness.type === 'testId') {
    await page.getByTestId(readiness.value).waitFor({ state: 'visible', timeout: 5000 }).catch(() => {});
    return;
  }
  if (readiness.type === 'stableDOM') {
    await page.waitForTimeout(100);
    const first = JSON.stringify(await domSummary(page));
    await page.waitForTimeout(100);
    const second = JSON.stringify(await domSummary(page));
    if (first !== second) await page.waitForTimeout(100);
  }
}
```

- [ ] **Step 4: Run focused tests and confirm pass**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: PASS.

- [ ] **Step 5: Commit Task 5**

```bash
git add frontend-app/scripts/agentic-e2e.mjs frontend-app/scripts/agentic-e2e.test.mjs
git commit -m "feat: 增加 agentic e2e 页面就绪等待"
```

## Task 6: Validate Local Discovery Flow

**Files:**
- Modify only if validation exposes a harness bug: `frontend-app/scripts/*.mjs`
- Evidence output: `.tmp/agentic-e2e/<run-id>/`

- [ ] **Step 1: Run focused unit tests**

Run:

```bash
cd frontend-app
npm test -- agentic-e2e.test.mjs
```

Expected: PASS.

- [ ] **Step 2: Run standard frontend checks**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all commands PASS.

- [ ] **Step 3: Run the agentic E2E harness locally**

Start the same local frontend/backend environment used by the current harness. Then run:

```bash
cd frontend-app
SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID=business-flow-discovery npm run agentic:e2e
```

Expected: command exits 0 and prints an output directory.

- [ ] **Step 4: Inspect discovery artifacts**

Run:

```bash
test -s .tmp/agentic-e2e/business-flow-discovery/business-flow-discovery.json
test -s .tmp/agentic-e2e/business-flow-discovery/business-flow-discovery.md
node -e "const fs=require('fs'); const p='.tmp/agentic-e2e/business-flow-discovery/business-flow-discovery.json'; const r=JSON.parse(fs.readFileSync(p,'utf8')); if (!r.summary || r.summary.totalFlows < 1) throw new Error('missing discovered flows'); console.log(r.summary)"
```

Expected: JSON and Markdown exist, and summary reports at least one discovered flow.

- [ ] **Step 5: Commit validation fixes if any**

If validation required code changes, commit only those changes:

```bash
git add frontend-app/scripts/agentic-e2e-discovery.mjs frontend-app/scripts/agentic-e2e-reporter.mjs frontend-app/scripts/agentic-e2e.mjs frontend-app/scripts/agentic-e2e.test.mjs
git commit -m "fix: 稳定 agentic e2e 发现流程"
```

If no code changes were needed, do not create an empty commit.

## Task 7: Final Review And Handoff

**Files:**
- Review: `frontend-app/scripts/agentic-e2e-discovery.mjs`
- Review: `frontend-app/scripts/agentic-e2e-reporter.mjs`
- Review: `frontend-app/scripts/agentic-e2e.mjs`
- Review: `frontend-app/scripts/agentic-e2e.test.mjs`
- Review: `frontend-app/package.json`
- Review: `frontend-app/package-lock.json`

- [ ] **Step 1: Check final diff boundaries**

Run:

```bash
git status --short
git diff --stat
git diff --check
```

Expected: no whitespace errors. Changed files should be limited to harness scripts, tests, package files from the existing Playwright upgrade, and accepted docs.

- [ ] **Step 2: Confirm reports are not staged**

Run:

```bash
git status --short .tmp
```

Expected: no tracked or staged evidence artifacts.

- [ ] **Step 3: Summarize promotion candidates**

Open `.tmp/agentic-e2e/business-flow-discovery/business-flow-discovery.md` and summarize:

- discovered visible entries,
- executed read-oriented actions,
- blocked mutating actions,
- flows that look stable enough to become deterministic goals.

- [ ] **Step 4: Prepare final implementation summary**

Report:

- commits created,
- validation commands and results,
- evidence output path,
- remaining risks, especially any actions blocked by safety policy and any flows that need manual promotion.
