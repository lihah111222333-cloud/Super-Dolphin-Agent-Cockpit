# Retryable Sync Error Failure Reporting Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make shared sync retry failures visible instead of leaving rejected retry promises unreported.

**Architecture:** Keep the behavior in `RetryableSyncError` so Files, Skills, and Workflows all share the same retry failure handling. Reuse the existing shared `errorMessage` helper for consistent message formatting.

**Tech Stack:** React/Vite frontend, Testing Library, Vitest.

**Verification Surface:** `frontend-app/src/pages/shared/pageComponents.test.jsx`, `frontend-app/src/pages/shared/pageComponents.jsx`, frontend lint/test/build, LSP diagnostics for modified files.

---

### Task 1: Report Retry Failures

**Files:**
- Modify: `frontend-app/src/pages/shared/pageComponents.jsx`
- Modify: `frontend-app/src/pages/shared/pageComponents.test.jsx`

- [x] **Step 1: Write the failing test**

Add a test that clicks the retry button when `onRetry` rejects and expects a visible failure message:

```jsx
it('shows retry failures instead of dropping rejected retry promises', async () => {
  const onRetry = vi.fn().mockRejectedValue(new Error('backend offline'));
  render(<RetryableSyncError message="Sync failed" onRetry={onRetry} />);

  fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

  expect(onRetry).toHaveBeenCalledTimes(1);
  expect(await screen.findByText('重试同步失败：backend offline')).toBeInTheDocument();
});
```

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
cd frontend-app
npx vitest run src/pages/shared/pageComponents.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because the current component calls `void onRetry()` and never renders the rejection.

Actual: FAIL. The new test timed out waiting for `重试同步失败：backend offline`.

- [x] **Step 3: Write minimal implementation**

Use React state and the shared `errorMessage` helper:

```jsx
function RetryableSyncError({ className = 'danger-text', message, onRetry }) {
  const [retryError, setRetryError] = useState('');
  useEffect(() => {
    setRetryError('');
  }, [message]);
  if (!message) return null;
  const handleRetry = () => {
    setRetryError('');
    Promise.resolve()
      .then(() => onRetry())
      .catch((error) => setRetryError(`重试同步失败：${errorMessage(error)}`));
  };
  return (
    <div className={className} role="alert">
      <span>{message}</span>
      <button type="button" className="ghost" onClick={handleRetry}>重试同步</button>
      {retryError ? <span>{retryError}</span> : null}
    </div>
  );
}
```

- [x] **Step 4: Run focused tests**

Run:

```bash
cd frontend-app
npx vitest run src/pages/shared/pageComponents.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

Actual: PASS. `1 passed (1)`, `2 passed (2)`.

- [x] **Step 5: Run full verification**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all commands exit 0.

Actual:
- `npm run lint`: PASS.
- `npm test`: PASS, `82 passed (82)`, `1038 passed (1038)`.
- `npm run build`: PASS.
- `git diff --check`: PASS.
- LSP diagnostics: timed out for `pageComponents.jsx` and `pageComponents.test.jsx` after repeated retries; LSP references/call hierarchy were used to confirm Files, Skills, and Workflows call sites before editing.
