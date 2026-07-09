# Frontend A-Candidate Mature Replacement Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the two approved A-level frontend hand-written wheels with already-installed mature parsing APIs while keeping behavior fail-fast and independently verifiable.

**Architecture:** Implement two independent atomic tasks. First, move the critical skipped-test guard from regex/string scanning to TypeScript AST traversal. Second, move workflow cron expression validation from manual range parsing to `cron-parser` while preserving the product boundary that only simplified schedule presets are editable in the UI.

**Tech Stack:** React 19, Vite, Vitest, TypeScript compiler API, cron-parser, existing `frontend-app` guard scripts and workflow tests.

**Verification Surface:** `frontend-app/scripts/no-critical-skip.test.mjs`, `frontend-app/scripts/no-critical-skip.mjs`, `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`, `frontend-app/src/pages/workflows/WorkflowPage.jsx`, LSP diagnostics, `npm run lint`, `npm test`, `npm run build`.

---

## Execution Boundary

Base worktree:

```text
/home/l4place/Super-Dolphin/.worktrees/frontend-mature-next-wave-20260708
```

Base commit:

```text
origin/main @ 3e26037169cb6fe89766e5cf01f22c964b5f5add
```

Do not use the dirty root worktree for implementation. Keep the two approved tasks as separate commits. Do not include the B/P3 candidates from the final section in the same implementation train.

## File Map

- `frontend-app/scripts/no-critical-skip.mjs`: owns the critical `.skip` guard used by `npm test`.
- `frontend-app/scripts/no-critical-skip.test.mjs`: owns focused guard fixtures.
- `frontend-app/src/pages/workflows/WorkflowPage.jsx`: owns workflow schedule parsing, schedule labels, and schedule modal initialization.
- `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`: owns workflow schedule behavior coverage.
- `frontend-app/package.json`: already contains `typescript` and `cron-parser`; do not add dependencies for this plan.

## Non-Goals

- Do not change `frontend-app/src/shared/api/backendApi.js` cronjob payload validation in this plan. Backend cron grammar parity must be confirmed before frontend bridge rejection expands beyond empty `schedule_expr`.
- Do not replace `cronExprFromSchedule` with `cron-parser.fieldsToExpression`; the UI builder must keep emitting `CRON_TZ=Asia/Shanghai` plus the current five-field wire shape.
- Do not change workflow query coalescing, workflow action mutations, prompt normalization, skills normalization, runtime popovers, image lightbox, or code-size guard behavior in this plan.
- Do not update guard baselines or lower thresholds.

---

## Task 1: Move Critical Skip Guard To TypeScript AST

**Files:**
- Modify: `frontend-app/scripts/no-critical-skip.mjs`
- Modify: `frontend-app/scripts/no-critical-skip.test.mjs`

- [ ] **Step 1: Write RED tests for AST-only skip cases**

Add tests to `frontend-app/scripts/no-critical-skip.test.mjs` that fail under the current regex/trivia scanner but pass with AST traversal:

```js
  it('detects multiline and computed skipped tests without matching comments or strings', () => {
    const source = [
      'const skippedText = "it.skip(\\'provider flow is hidden\\', () => {})";',
      '// test.skip(\\'workflow hidden in comment\\', () => {})',
      'describe',
      '  .skip(\\'thread workflow contract\\', () => {});',
      'test[\\'skip\\'](\\'rpc contract bracket access\\', () => {});',
    ].join('\n');

    expect(criticalSkipViolationsFromSources(new Map([
      ['src/shared/api/thread.test.js', source],
    ]))).toEqual([
      expect.objectContaining({
        file: 'src/shared/api/thread.test.js',
        name: 'thread workflow contract',
        parseError: false,
      }),
      expect.objectContaining({
        file: 'src/shared/api/thread.test.js',
        name: 'rpc contract bracket access',
        parseError: false,
      }),
    ]);
  });

  it('fails fast when a test source cannot be parsed as JavaScript', () => {
    expect(() => skippedTestsInSource('src/shared/api/broken.test.js', 'it.skip('))
      .toThrow(/critical skip source parse failed: src\/shared\/api\/broken\.test\.js/);
  });
```

Keep the existing dynamic template test:

```js
expect(skippedTestsInSource(
  'src/shared/api/thread.test.js',
  "it.skip(`thread ${caseName}`, () => {})",
)).toEqual([{
  file: 'src/shared/api/thread.test.js',
  line: 1,
  name: '<unparseable>',
  parseError: true,
}]);
```

- [ ] **Step 2: Run the RED test**

Run:

```bash
cd frontend-app
npx vitest run scripts/no-critical-skip.test.mjs --no-file-parallelism --maxWorkers=1
```

Expected: FAIL. The multiline `.skip` and computed `['skip']` fixture should not both be detected by the current regex scanner, and invalid source should not currently throw the planned parse failure.

- [ ] **Step 3: Replace regex/trivia scanning with TypeScript AST traversal**

In `frontend-app/scripts/no-critical-skip.mjs`, import TypeScript:

```js
import ts from 'typescript';
```

Remove `readStringLiteral`, `skipJSSyntaxComment`, `skipJSSyntaxTrivia`, and `isInsideJSSyntaxTrivia`. Replace them with AST helpers:

```js
const SKIP_HOSTS = new Set(['describe', 'it', 'test']);

function scriptKindForFile(relFile) {
  if (relFile.endsWith('.tsx')) return ts.ScriptKind.TSX;
  if (relFile.endsWith('.jsx')) return ts.ScriptKind.JSX;
  if (relFile.endsWith('.ts') || relFile.endsWith('.mts') || relFile.endsWith('.cts')) return ts.ScriptKind.TS;
  return ts.ScriptKind.JS;
}

function parseSourceFile(relFile, source) {
  const sourceFile = ts.createSourceFile(
    relFile,
    source,
    ts.ScriptTarget.Latest,
    true,
    scriptKindForFile(relFile),
  );
  if (sourceFile.parseDiagnostics.length > 0) {
    const diagnostic = sourceFile.parseDiagnostics[0];
    const line = sourceFile.getLineAndCharacterOfPosition(diagnostic.start || 0).line + 1;
    throw new Error(`critical skip source parse failed: ${relFile}:${line}`);
  }
  return sourceFile;
}

function lineNumberForNode(sourceFile, node) {
  return sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;
}

function rootIdentifierName(expression) {
  if (ts.isIdentifier(expression)) return expression.text;
  if (ts.isCallExpression(expression)) return rootIdentifierName(expression.expression);
  if (ts.isPropertyAccessExpression(expression)) return rootIdentifierName(expression.expression);
  if (ts.isElementAccessExpression(expression)) return rootIdentifierName(expression.expression);
  return '';
}

function staticPropertyName(expression) {
  if (ts.isPropertyAccessExpression(expression)) return expression.name.text;
  if (ts.isElementAccessExpression(expression)) {
    const argument = expression.argumentExpression;
    if (argument && ts.isStringLiteral(argument)) return argument.text;
  }
  return '';
}

function isSkippedTestCall(node) {
  if (!ts.isCallExpression(node)) return false;
  const expression = node.expression;
  if (!ts.isPropertyAccessExpression(expression) && !ts.isElementAccessExpression(expression)) return false;
  return staticPropertyName(expression) === 'skip' && SKIP_HOSTS.has(rootIdentifierName(expression.expression));
}

function staticTestName(node) {
  const [nameNode] = node.arguments;
  if (!nameNode) return null;
  if (ts.isStringLiteral(nameNode) || ts.isNoSubstitutionTemplateLiteral(nameNode)) return nameNode.text;
  return null;
}
```

Rewrite `skippedTestsInSource`:

```js
export function skippedTestsInSource(relFile, source) {
  const sourceFile = parseSourceFile(relFile, source);
  const skips = [];

  const visit = (node) => {
    if (isSkippedTestCall(node)) {
      const name = staticTestName(node);
      skips.push({
        file: relFile,
        line: lineNumberForNode(sourceFile, node),
        name: name || '<unparseable>',
        parseError: !name,
      });
    }
    ts.forEachChild(node, visit);
  };

  visit(sourceFile);
  return skips;
}
```

This must preserve fail-fast behavior: syntax parse failure throws, dynamic test names return a violation with `parseError: true`, and skipped tests in comments or string literals are ignored by AST traversal.

- [ ] **Step 4: Run GREEN focused guard tests**

Run:

```bash
cd frontend-app
npx vitest run scripts/no-critical-skip.test.mjs --no-file-parallelism --maxWorkers=1
node scripts/no-critical-skip.mjs
```

Expected: PASS. The script prints:

```text
critical .skip guard passed: no critical skips (0 found)
```

- [ ] **Step 5: LSP diagnostics for guard files**

Run LSP diagnostics for:

```text
frontend-app/scripts/no-critical-skip.mjs
frontend-app/scripts/no-critical-skip.test.mjs
```

Fix every Error, Warning, Information, and Hint. If diagnostics time out, retry with each file separately and record the tool/action/work_dir/target/error in the implementation report.

- [ ] **Step 6: Commit the guard task**

Run:

```bash
git status --short
git add frontend-app/scripts/no-critical-skip.mjs frontend-app/scripts/no-critical-skip.test.mjs
git diff --cached --check
git commit -m "test(frontend): 用 AST 收敛 critical skip 守卫"
```

Expected: one atomic commit containing only the guard script and its tests.

---

## Task 2: Move Workflow Cron Validation To cron-parser

**Files:**
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.jsx`
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`

- [ ] **Step 1: Write RED tests for cron-parser boundary behavior**

In `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`, keep the existing malformed cron test and add two focused tests near it:

```jsx
  it('shows a range warning for valid cron data outside simplified schedule presets', async () => {
    const dag = {
      dag_key: 'advanced-schedule',
      title: 'Advanced Schedule',
      status: 'ready',
      trigger: 'scheduled',
      cron_expr: 'CRON_TZ=Asia/Shanghai */15 8 * * *',
      version: 3,
    };
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });

    renderWorkflowPage();

    fireEvent.click(await screen.findByRole('button', { name: '修改计划' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('已有计划超出简化设置范围，请重新选择运行频率和时间。');
  });

  it('keeps parser rejected cron data as a format warning', async () => {
    const dag = {
      dag_key: 'bad-minute',
      title: 'Bad Minute',
      status: 'ready',
      trigger: 'scheduled',
      cron_expr: 'CRON_TZ=Asia/Shanghai 61 8 * * *',
      version: 3,
    };
    backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
    backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
    backend.getDagRuns.mockResolvedValue({ runs: [] });
    backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });

    renderWorkflowPage();

    fireEvent.click(await screen.findByRole('button', { name: '修改计划' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('已有计划格式无法识别，请重新选择运行频率和时间。');
  });
```

These tests define the split between library validation and product simplification: malformed cron is a format warning; valid cron outside daily/weekdays/weekly/monthly UI presets is a range warning.

- [ ] **Step 2: Run the RED workflow test**

Run:

```bash
cd frontend-app
npx vitest run src/pages/workflows/WorkflowPage.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL for the new advanced cron test because current manual parsing treats `*/15` as malformed instead of valid-but-out-of-range.

- [ ] **Step 3: Import cron-parser with the CommonJS-compatible default import**

In `frontend-app/src/pages/workflows/WorkflowPage.jsx`, add:

```js
import cronParser from 'cron-parser';
```

Do not use a named import. In this repository's ESM runtime, `import { parseExpression } from 'cron-parser'` fails because `cron-parser` is CommonJS.

- [ ] **Step 4: Replace manual cron field range validation**

Keep `cronSchedulePartsWithTimezone` because product wire shape uses a `CRON_TZ=` prefix that `cron-parser` does not parse directly. Replace the numeric range checks in `parseCronScheduleParts` with parser validation:

```js
function singleCronFieldValue(values) {
  return Array.isArray(values) && values.length === 1 ? Number(values[0]) : null;
}

function parseCronFields(cronText, timezone) {
  try {
    return cronParser.parseExpression(cronText, { tz: timezone }).fields;
  }
  catch {
    return null;
  }
}

function parseCronScheduleParts(cronExpr) {
  const { cronText: text, timezone } = cronSchedulePartsWithTimezone(cronExpr);
  if (!text) return { empty: true };
  const parts = text.split(/\s+/);
  if (parts.length !== 5) return { error: DAG_SCHEDULE_FORMAT_WARNING };

  const fields = parseCronFields(text, timezone);
  if (!fields) return { error: DAG_SCHEDULE_FORMAT_WARNING };

  const [minuteText, hourText, dayOfMonth, month, dayOfWeek] = parts;
  const minute = singleCronFieldValue(fields.minute);
  const hour = singleCronFieldValue(fields.hour);
  if (!Number.isInteger(hour) || !Number.isInteger(minute)) {
    return { rangeOnly: true, minuteText, hourText, dayOfMonth, month, dayOfWeek, timezone };
  }

  return {
    minute,
    hour,
    dayOfMonth,
    month,
    dayOfWeek,
    time: `${twoDigits(hour)}:${twoDigits(minute)}`,
    timezone,
  };
}
```

Update `scheduleStateFromCron` so advanced but valid cron expressions remain fail-visible as range warnings:

```js
function scheduleStateFromCron(cronExpr) {
  const parsed = parseCronScheduleParts(cronExpr);
  if (parsed.empty) return dagScheduleState();
  if (parsed.error) return dagScheduleState(parsed.error);
  if (parsed.rangeOnly) return dagScheduleState(DAG_SCHEDULE_RANGE_WARNING);
  const rule = DAG_CRON_SCHEDULE_RULES.find((item) => cronScheduleRuleMatches(item, parsed));
  return rule ? scheduleStateForCronRule(rule, parsed) : dagScheduleState(DAG_SCHEDULE_RANGE_WARNING);
}
```

This intentionally keeps raw `dayOfMonth`, `month`, and `dayOfWeek` rule matching as product logic. The mature dependency owns cron grammar and field range validation; the UI still owns which valid cron expressions are simple enough for the schedule modal.

- [ ] **Step 5: Run GREEN workflow tests**

Run:

```bash
cd frontend-app
npx vitest run src/pages/workflows/WorkflowPage.test.jsx --no-file-parallelism --maxWorkers=1
npx vitest run src/App.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS. Existing tests for saving `CRON_TZ=Asia/Shanghai ...`, showing `每天 01:00`, schedule dialog initialization, and malformed cron warnings continue to pass.

- [ ] **Step 6: LSP diagnostics for workflow files**

Run LSP diagnostics for:

```text
frontend-app/src/pages/workflows/WorkflowPage.jsx
frontend-app/src/pages/workflows/WorkflowPage.test.jsx
```

Fix every Error, Warning, Information, and Hint. If `WorkflowPage.jsx` diagnostics return `result_too_large` or time out, retry with narrowed `work_dir` and then record the exact blocker in the implementation report.

- [ ] **Step 7: Commit the workflow cron task**

Run:

```bash
git status --short
git add frontend-app/src/pages/workflows/WorkflowPage.jsx frontend-app/src/pages/workflows/WorkflowPage.test.jsx
git diff --cached --check
git commit -m "refactor(frontend): 用 cron-parser 收敛工作流计划解析"
```

Expected: one atomic commit containing only workflow cron parsing code and focused tests.

---

## Final Verification

After both task commits:

- [ ] **Step 1: Full frontend validation**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: PASS.

- [ ] **Step 2: Stop gate check for touched frontend files**

Run from the repository root:

```bash
tmp=$(mktemp)
printf '%s\n' \
  'frontend-app/scripts/no-critical-skip.mjs' \
  'frontend-app/scripts/no-critical-skip.test.mjs' \
  'frontend-app/src/pages/workflows/WorkflowPage.jsx' \
  'frontend-app/src/pages/workflows/WorkflowPage.test.jsx' > "$tmp"
CODEX_STOP_GATE_CHANGED_FILES_FILE="$tmp" \
  CODEX_STOP_GATE_LOG_DIR=$(mktemp -d) \
  bash scripts/codex_stop_gate.sh
rm -f "$tmp"
```

Expected: frontend guard checks pass. If the stop gate runs broader frontend validation, report the exact commands it executed and their exit status.

- [ ] **Step 3: Review final diff**

Run:

```bash
git status --short
git diff --stat HEAD~2..HEAD
git diff --check HEAD~2..HEAD
```

Expected: only the four owned files changed across the two commits. No generated files, package metadata, codemap, or unrelated dirty files are included.

---

## Suggested Commit Order

1. `test(frontend): 用 AST 收敛 critical skip 守卫`
2. `refactor(frontend): 用 cron-parser 收敛工作流计划解析`

---

## Completion-Gated Next Candidate Backlog

These candidates were confirmed by the parallel scan but must wait until the two A-level tasks above are implemented and verified:

1. Prompt response normalization with zod:
   - Entry: `frontend-app/src/features/prompts/PromptPageView.jsx:207`
   - Preserve readonly fallback, `fallbackMode`, risk confirmation, and global scope confirmation.

2. Skills dashboard / resolution response schemas with zod:
   - Entry: `frontend-app/src/pages/skills/SkillsPage.jsx:150`
   - Preserve canonical/mirror conflict resolution actions and labels.

3. Skills datasource and tools DTO schemas with zod:
   - Entry: `frontend-app/src/pages/skills/SkillsPage.jsx:1167`
   - Preserve `useQuery` / `useInfiniteQuery` behavior and `assertDatasourceChunkPageProgress`.

4. Settings runtime/preferences Query migration:
   - Entry: `frontend-app/src/pages/settings/SettingsPage.jsx:383`
   - Requires dirty draft protection and explicit `refetchOnWindowFocus` choices.

5. Prompt focus refresh cleanup:
   - Entry: `frontend-app/src/features/prompts/PromptPageView.jsx:557`
   - Must prove Query focus refetch does not duplicate RPC calls or break active prompt cleanup.

6. Runtime activity popovers with React Aria:
   - Entry: `frontend-app/src/pages/chat/components/RuntimeActivityPanel.jsx:53`
   - Must preserve warning redaction and runtime panel resize semantics.

7. Image lightbox with React Aria Modal:
   - Entry: `frontend-app/src/pages/chat/components/ImageLightbox.jsx:13`
   - Must preserve Mermaid sanitizer behavior and image preview close flows.

8. `rpc-contract-audit` AST parsing:
   - Entry: `frontend-app/scripts/rpc-contract-audit.mjs:168`
   - Larger guard rewrite; split JS contract AST parsing from Go source scanning.

9. `frontend-code-size-guard` AST metrics:
   - Entry: `frontend-app/scripts/frontend-code-size-guard.mjs:71`
   - Run as shadow comparison before replacing metrics; do not update or lower baseline as part of the migration.

## Execution Options

1. **子代理驱动（推荐）** - Task 1 and Task 2 have disjoint files and can be assigned to separate workers, then reviewed and verified together.

2. **当前会话内执行** - Use `执行计划` in this session and complete Task 1 before Task 2, committing after each task.
