# Preserve Paused DAG Schedule Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Editing the cron expression for an already paused scheduled DAG must keep that DAG paused.

**Architecture:** The frontend schedule-save action builds a single `update_dag` patch. Preserve the existing paused state by adding `schedule_enabled:false` only when the selected DAG is already scheduled and `activeDetailDag.scheduleEnabled === false`.

**Tech Stack:** React/Vite frontend in `frontend-app`, Vitest + Testing Library, Wails backend facade mocks.

**Verification Surface:** `frontend-app/src/pages/workflows/WorkflowPage.jsx`, `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`, then full `cd frontend-app && npm run lint && npm test && npm run build`.

---

### Task 1: Lock The Paused Schedule Regression

**Files:**
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`

- [ ] **Step 1: Write the failing test**

Add a test near `saves schedule cron expressions with the backend timezone prefix`:

```jsx
it('preserves paused scheduled DAGs when editing the schedule cron expression', async () => {
  const dag = {
    dag_key: 'paused-flow',
    title: 'Paused Flow',
    status: 'ready',
    trigger: 'scheduled',
    cron_expr: 'CRON_TZ=Asia/Shanghai 0 9 * * *',
    schedule_enabled: false,
    version: 7,
  };
  backend.getDashboardPage.mockResolvedValue({ dags: [dag] });
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [] });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
  backend.applyDagOps.mockResolvedValue({ newVersion: 8 });

  renderWorkflowPage();

  fireEvent.click(await screen.findByRole('button', { name: '修改计划' }));
  fireEvent.change(screen.getByLabelText('运行时间'), { target: { value: '06:30' } });
  fireEvent.click(screen.getAllByRole('button', { name: '修改计划' }).at(-1));

  await waitFor(() => expect(backend.applyDagOps).toHaveBeenCalled());
  expect(backend.applyDagOps.mock.calls[0][0].ops[0].patch).toMatchObject({
    trigger: 'scheduled',
    cron_expr: 'CRON_TZ=Asia/Shanghai 30 6 * * *',
    schedule_enabled: false,
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
cd frontend-app
npm test -- src/pages/workflows/WorkflowPage.test.jsx -t "preserves paused scheduled DAGs"
```

Expected: FAIL because the patch does not include `schedule_enabled:false`.

### Task 2: Preserve The Existing Paused Flag

**Files:**
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.jsx`

- [ ] **Step 1: Build the schedule patch explicitly**

Inside `useSaveScheduleAction`, replace the inline patch object with:

```jsx
const activeDetailDag = derived.activeDetailDag;
const schedulePatch = { trigger: 'scheduled', cron_expr: cronExpr };
const preservingExistingSchedule = isScheduledTrigger(activeDetailDag?.trigger) || Boolean(activeDetailDag?.cronExpr);
if (preservingExistingSchedule && activeDetailDag?.scheduleEnabled === false) {
  schedulePatch.schedule_enabled = false;
}
```

Then pass `schedulePatch` into `applyDagOps`.

- [ ] **Step 2: Run focused workflow tests**

Run:

```bash
cd frontend-app
npm test -- src/pages/workflows/WorkflowPage.test.jsx -t "preserves paused scheduled DAGs|saves schedule cron expressions"
```

Expected: both focused schedule tests pass.

### Task 3: Verify And Commit

**Files:**
- Verify: all modified files

- [ ] **Step 1: Run full frontend gate**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all commands exit 0.

- [ ] **Step 2: Commit and push to remote main**

```bash
git add docs/plans/2026-07-05-preserve-paused-dag-schedule.md frontend-app/src/pages/workflows/WorkflowPage.jsx frontend-app/src/pages/workflows/WorkflowPage.test.jsx
git commit -m "fix: 保留暂停定时自动化状态"
git push origin HEAD:main
```
