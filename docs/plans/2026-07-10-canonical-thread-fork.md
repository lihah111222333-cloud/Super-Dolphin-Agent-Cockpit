# Canonical Thread Fork Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将“继承对话”从摘要式 `thread/start` 改为 canonical `thread/fork`，保留完整 provider 历史和 prompt snapshot，并正确处理 kickoff、共享文件、事件乱序与部分成功。

**Architecture:** 共享 API 增加严格的 `thread/fork` 请求/响应合同，`sessionApi.fork()` 成为 store 唯一入口。Store 仅在 `created_only` 后发送一次 kickoff turn；共享文件使用 `filecontent`。RPC response 与先到的 `ui/thread/patch` 合并时保留事件权威字段，后端主流程不重写。

**Tech Stack:** React 19、Zustand、Vitest、JavaScript `@ts-check`、Wails JSON-RPC、Go。

**Verification Surface:** `frontend-app/src/shared/api`、fork store helpers/actions、App/store tests、`internal/module/thread`、`internal/module/uistate`、前端 lint/test/build、LSP diagnostics。

---

## Files and coordination

- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/shared/api/backend/backendRpcMethods.js`
- Modify: `frontend-app/src/shared/api/backend/backendApiFactoryThread.js`
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- Modify: `frontend-app/src/shared/api/sessionApi.js`
- Modify: matching shared API tests
- Modify: `frontend-app/src/entities/client/model/helpers/threadFork.js`
- Create: `frontend-app/src/entities/client/model/helpers/threadFork.test.js`
- Modify: `frontend-app/src/entities/client/model/threadForkState.js`
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreSendModel.js`
- Modify: `frontend-app/src/entities/client/model/helpers/a2Slice/forkSliceActions.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.test.js`
- Modify: `frontend-app/src/App.test.jsx`
- Modify: `internal/module/thread/contract.go`
- Modify: `internal/module/thread/lifecycle_fork.go`
- Modify: `internal/module/thread/fork_isolation_test.go`
- Modify: `docs/doc/codemap/01-terminal-ui-react.md`

Plan 1 must be `new_task_runtime_accepted=true`. This plan shares `useClientStore.test.js`, `App.test.jsx`, and the frontend codemap with Plan 3; serialize those edits.

### Task 0: Enforce the LSP gate

**Files:** Read-only navigation of `forkSliceActions.js` and `lifecycle_fork.go`.

- [ ] **Step 1: Verify the server and seven tools**

```bash
go run ./cmd/codex-worktree-setup verify
codex mcp get lsp
```

Expected: current worktree binary/config, both language servers and seven short tools.

- [ ] **Step 2: Run the required LSP chain**

Use `grep` for `submitForkThread` and `func (s *Service) Fork`, feed returned positions into `inspect` and `xref`, read both implementations with `file(read_file)`, and run `file(diagnostics)` on both files.

Expected: definition/reference/call-chain evidence exists. Any LSP failure or any unresolved diagnostic blocks Task 1.

### Task 1: Add the strict shared API contract

**Files:** shared API production files and tests listed above.

- [ ] **Step 1: Write RED request/response tests**

Add these complete behavioral assertions:

```js
it('calls canonical thread/fork with only threadId', async () => {
  const callAPI = vi.fn().mockResolvedValue({
    thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
    kickoff_state: 'created_only',
  });
  const api = createBackendApi({ callAPI });
  await expect(api.forkThread({ threadId: 'thread-parent' })).resolves.toEqual(
    expect.objectContaining({
      thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
      kickoffState: 'created_only',
    }),
  );
  expect(callAPI).toHaveBeenCalledWith('thread/fork', { threadId: 'thread-parent' });
});

it('rejects conflicting fork kickoff aliases', () => {
  const validators = createBackendResponseValidators(RPC_METHODS);
  const validate = validators[RPC_METHODS.THREAD_FORK];
  expect(() => validate(RPC_METHODS.THREAD_FORK, {
    thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
    kickoff_state: 'created_only',
    kickoffState: 'started',
  })).toThrow('thread/fork response kickoff state fields conflict');
});
```

Also test missing `thread.id`, missing/mismatched `forkedFrom`, missing/unknown kickoff state, and request fields `cwd/provider/baseInstructions` being rejected before `callAPI`.

- [ ] **Step 2: Run RED**

```bash
cd frontend-app
npx vitest run src/shared/api/backendApi.test.js src/shared/api/backendResponseValidators.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because `THREAD_FORK`, `forkThread`, or its validator is absent.

- [ ] **Step 3: Implement the minimal contract**

Add `THREAD_FORK: 'thread/fork'` to both current RPC constant registries. In `backendApiFactoryThread.js`, build a thread-ID-only payload and reject unused keys. In `backendResponseValidators.js`, normalize aliases with this contract:

```js
function forkKickoffState(method, response) {
  const snake = normalizeString(response.kickoff_state);
  const camel = normalizeString(response.kickoffState);
  if (!snake && !camel) throw new Error(`${method} response kickoff state is required`);
  if (snake && camel && snake !== camel) {
    throw new Error(`${method} response kickoff state fields conflict`);
  }
  const value = camel || snake;
  if (value !== 'created_only') {
    throw new Error(`${method} response unsupported kickoff state ${value}`);
  }
  return value;
}
```

Export `forkThread`, register it as P1 with a response validator, and add:

```js
fork(params) {
  return forkThread(params);
},
```

to `sessionApi`.

- [ ] **Step 4: Run GREEN and the contract audit**

```bash
cd frontend-app
npx vitest run src/shared/api/backendApi.test.js src/shared/api/backendResponseValidators.test.js src/shared/api/sessionApi.test.js src/shared/api/backendApi.surface.test.js --no-file-parallelism --maxWorkers=1
npm run audit:rpc-contracts
```

Expected: PASS; audit resolves the existing Go handler. LSP diagnostics on changed production files are empty.

- [ ] **Step 5: Commit the API boundary**

```bash
git add frontend-app/src/shared/api
git commit -m "feat(frontend): expose canonical thread fork"
```

### Task 2: Replace summary helpers with canonical kickoff/state merge

**Files:** `threadFork.js`, new helper test, `threadForkState.js` and its tests.

- [ ] **Step 1: Write RED helper tests**

```js
it('builds kickoff from inherited history and filecontent', () => {
  expect(buildForkKickoffInput([
    { path: 'notes/a.md', content: '# A' },
  ])).toEqual([
    { type: 'text', text: FORK_KICKOFF_PROMPT },
    { type: 'filecontent', path: 'notes/a.md', name: 'notes/a.md', content: '# A' },
  ]);
});

it('preserves an event-created fork thread', () => {
  const state = buildForkThreadState({
    state: {
      provider: 'codex',
      threads: [{
        id: 'thread-fork',
        name: 'Parent (续)',
        status: '空闲',
        provider: 'codex',
        cwd: '/repo',
        generation: '7',
      }],
      timelinesByThread: { 'thread-fork': [] },
      activityThreadAtById: {},
    },
    threadId: 'thread-fork',
    sourceThreadId: 'thread-parent',
    identity: {
      agentId: 'thread-fork',
      providerThreadId: '',
      sessionId: '',
    },
    sourceThread: { id: 'thread-parent', provider: 'codex' },
    provisionalName: '继承自会话：Parent',
    kickoffText: FORK_KICKOFF_PROMPT,
    deps: {
      actionNotice: (message, tone) => ({ message, tone }),
      emptyForkDraft: () => ({ open: false }),
      nowISO: () => '2026-07-10T00:00:00Z',
      nowMillis: () => 123,
      threadActivityTimestamp: () => 456,
      threadMatchesIdentifier: (thread, id) => thread.id === id,
    },
  });
  expect(state.threads.find((item) => item.id === 'thread-fork')).toEqual(
    expect.objectContaining({ name: 'Parent (续)', status: '空闲', generation: '7' }),
  );
});
```

- [ ] **Step 2: Run RED**

```bash
cd frontend-app
npx vitest run src/entities/client/model/helpers/threadFork.test.js src/entities/client/model/threadForkState.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because kickoff builder/merge semantics are absent.

- [ ] **Step 3: Implement the focused helpers**

```js
export const FORK_KICKOFF_PROMPT =
  '请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。';

export function buildForkKickoffInput(files = []) {
  return [
    { type: 'text', text: FORK_KICKOFF_PROMPT },
    ...files.map((file) => {
      const path = normalizeString(file?.path);
      const content = normalizeString(file?.content);
      if (!path || !content) throw new Error('fork shared file path and content are required');
      return { type: 'filecontent', path, name: path, content };
    }),
  ];
}
```

Delete summary extraction/base-instructions helpers. Merge an existing event-created thread after provisional defaults so event name/status/provider/cwd/generation win; ensure one thread ID and one optimistic kickoff item.

- [ ] **Step 4: Run GREEN and commit**

```bash
cd frontend-app
npx vitest run src/entities/client/model/helpers/threadFork.test.js src/entities/client/model/threadForkState.test.js --no-file-parallelism --maxWorkers=1
cd ..
git add frontend-app/src/entities/client/model/helpers/threadFork.js frontend-app/src/entities/client/model/helpers/threadFork.test.js frontend-app/src/entities/client/model/threadForkState.js frontend-app/src/entities/client/model/threadForkState.test.js
git commit -m "refactor(frontend): prepare canonical fork state"
```

Expected: tests PASS and diagnostics are empty.

### Task 3: Switch the store action with no fallback

**Files:** store dependency/action files and `useClientStore.test.js`.

- [ ] **Step 1: Write RED store tests**

```js
it('uses thread/fork and sends one created_only kickoff', async () => {
  backend.forkThread.mockResolvedValue({
    thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
    kickoffState: 'created_only',
  });
  backend.startTurn.mockResolvedValue({ turn_id: 'turn-1' });
  await useClientStore.getState().openForkDraft();
  await expect(useClientStore.getState().submitForkThread()).resolves.toBe('thread-fork');
  expect(backend.forkThread).toHaveBeenCalledWith({ threadId: 'thread-parent' });
  expect(backend.startThread).not.toHaveBeenCalled();
  expect(backend.startTurn).toHaveBeenCalledTimes(1);
});

it('does not fall back when canonical fork fails', async () => {
  backend.forkThread.mockRejectedValue(new Error('thread_fork unsupported'));
  await useClientStore.getState().openForkDraft();
  await expect(useClientStore.getState().submitForkThread()).rejects.toThrow('thread_fork unsupported');
  expect(backend.startThread).not.toHaveBeenCalled();
  expect(backend.startTurn).not.toHaveBeenCalled();
});
```

Add shared-file `filecontent`, event-before-response, response-before-event, and kickoff-failure partial-success tests.

- [ ] **Step 2: Run RED**

```bash
cd frontend-app
npx vitest run src/entities/client/model/useClientStore.test.js -t 'canonical fork|does not fall back|filecontent|fork response|kickoff failure' --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because the action still calls `thread/start`.

- [ ] **Step 3: Implement canonical flow**

Replace fork-only `startThread/resolveLaunchPreferences/createLaunchIntentId` dependencies with `sessionApi.fork`. Load selected files before the mutating RPC, call `fork({threadId})`, merge returned identity, and only then:

```js
if (response.kickoffState !== 'created_only') {
  throw new Error(`thread/fork unsupported kickoff state ${response.kickoffState}`);
}
await sessionApi.startTurn({
  cwd,
  threadId: response.thread.id,
  input: buildForkKickoffInput(sharedFiles),
  manualSkillSelection: false,
});
```

Fork RPC failure keeps the draft open and parent active. Kickoff failure keeps the new canonical fork, marks it `需要操作`, and exposes a retryable warning.

- [ ] **Step 4: Run GREEN and App integration tests**

```bash
cd frontend-app
npx vitest run src/entities/client/model/useClientStore.test.js src/App.test.jsx -t 'fork|继承' --no-file-parallelism --maxWorkers=1
```

Expected: PASS; no assertion references `deferSpawn`, summary `baseInstructions`, or fork-path `thread/start`.

- [ ] **Step 5: Commit the store change**

```bash
git add frontend-app/src/entities/client/model/helpers/a1/clientStoreSendModel.js frontend-app/src/entities/client/model/helpers/a2Slice/forkSliceActions.js frontend-app/src/entities/client/model/useClientStore.test.js frontend-app/src/App.test.jsx
git commit -m "fix(frontend): use canonical thread fork"
```

### Task 4: Lock the Go kickoff contract and update documentation

**Files:** Go contract/fork test and frontend codemap.

- [ ] **Step 1: Write the Go RED test**

Add a test asserting `Service.Fork` returns a typed created-only state and publishes exactly one `thread.Started` after persistence:

```go
if result.KickoffState != ForkKickoffCreatedOnly {
	t.Fatalf("KickoffState = %q", result.KickoffState)
}
if got := len(events); got != 1 {
	t.Fatalf("thread.Started events = %d, want 1", got)
}
```

- [ ] **Step 2: Run RED, add the typed constant, then GREEN**

```bash
go test ./internal/module/thread -run TestServiceForkPublishesStartedExactlyOnceAfterPersistence -count=1
```

Expected RED: `ForkKickoffCreatedOnly` undefined. Add:

```go
const ForkKickoffCreatedOnly ForkKickoffState = "created_only"
```

Use the constant in `lifecycle_fork.go`, rerun the focused test and all `Test.*Fork`; expected PASS.

- [ ] **Step 3: Update the codemap**

Document `UI -> sessionApi.fork -> thread/fork -> created_only -> turn/start`, no fallback, `filecontent`, and event/response ordering.

- [ ] **Step 4: Commit Go/docs contract**

```bash
git add internal/module/thread/contract.go internal/module/thread/lifecycle_fork.go internal/module/thread/fork_isolation_test.go docs/doc/codemap/01-terminal-ui-react.md
git commit -m "test(thread): lock canonical fork contract"
```

### Task 5: Full verification

- [ ] **Step 1: Run focused and full frontend gates**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all exit 0.

- [ ] **Step 2: Run Go and architecture tests**

```bash
cd ..
go test ./internal/module/thread -run 'Test.*Fork' -count=1
go test ./internal/module/uistate -run 'Test.*ThreadStarted|Test.*Patch' -count=1
go test ./internal/archtest -run 'TestCodeSizeGuard|TestInterfaceIsolation|TestRPC' -count=1
```

Expected: all PASS; do not widen budgets.

- [ ] **Step 3: Run final LSP evidence**

Use `grep`, `inspect`, and `xref` to prove `submitForkThread -> sessionApi.fork -> thread/fork -> Service.Fork`; run diagnostics on every changed production file.

Expected: no Error, Warning, Information, or Hint.

- [ ] **Step 4: Verify forbidden fallback and diff integrity**

```bash
rg -n 'buildSeedInstructionsFromSummary|extractTimelineSummary|deferSpawn: true|baseInstructions' frontend-app/src/entities/client/model/helpers/threadFork.js frontend-app/src/entities/client/model/helpers/a2Slice/forkSliceActions.js
git diff --check
git status --short
```

Expected: first command has no matches; no generated/local LSP artifact is tracked or staged.
