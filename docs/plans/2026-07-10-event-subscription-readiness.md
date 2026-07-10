# Event Subscription Readiness Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** bridge 与 reconnect 两个事件订阅都 ready 前禁止 bootstrap 进入 ready，并提供原子清理、真实重连通知和显式用户重试。

**Architecture:** `initializeEvents()` 变为 generation-protected single-flight Promise，一次 attempt 同时准备两个订阅，全部 ready 后才提交 unsubscribe。`bootstrap()` 在 config/snapshot RPC 前等待该 Promise；开发 Wails shim 在失败或断线后的首次成功 open 发送一次本地 `wails:loaded`；现有 `bootstrapStatus/error` 保持唯一用户状态。

**Tech Stack:** React 19、Zustand、Vitest、Testing Library、Vite、JavaScript、Wails dev runtime shim。

**Verification Surface:** runtime slice/helpers、Wails bridge/shim、chat recovery UI、前端 lint/test/build、LSP JavaScript diagnostics、前端代码地图。

---

## Files and coordination

- Modify: `frontend-app/src/entities/client/model/helpers/runtimeSliceHelpers.js`
- Modify: `frontend-app/src/entities/client/model/runtimeSlice.js`
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreRuntimeCore.js`
- Modify: `frontend-app/src/entities/client/model/runtimeSlice.test.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.test.js`
- Modify: `frontend-app/public/wails/runtime.js`
- Modify: `frontend-app/public/wails/runtime.test.js`
- Modify: `frontend-app/src/pages/chat/model/chatHeaderModel.js`
- Modify: `frontend-app/src/pages/chat/components/ChatPageHeader.jsx`
- Modify: `frontend-app/src/pages/chat/ChatPage.jsx`
- Modify: matching chat tests and `ChatPageWorkbench.css`
- Modify: `docs/doc/codemap/01-terminal-ui-react.md`

Plan 1 must be `new_task_runtime_accepted=true`. Serialize edits to `useClientStore.test.js`, `App.test.jsx`, and the codemap with Plan 2. Do not add automatic infinite retry or a second user-visible connection state.

### Task 0: Enforce the LSP and dependency gates

- [ ] **Step 1: Verify the new-task LSP runtime**

```bash
go run ./cmd/codex-worktree-setup verify
codex mcp get lsp
```

Expected: current worktree binary/config, both language servers and seven short tools.

- [ ] **Step 2: Run LSP navigation and diagnostics**

Use `grep` for `initializeEvents`, then `structure`, `inspect`, `xref`, `file(read_file)` and `file(diagnostics)` on `runtimeSlice.js`, `runtimeSliceHelpers.js`, and `public/wails/runtime.js`.

Expected: real semantic results and no unresolved diagnostic. Tool failure blocks Task 1.

- [ ] **Step 3: Install locked frontend dependencies when absent**

```bash
cd frontend-app
test -x node_modules/.bin/vitest || npm ci
```

Expected: exit 0 and local Vitest exists.

### Task 1: Make event initialization atomic and single-flight

**Files:** runtime helper/core/slice and `runtimeSlice.test.js`.

- [ ] **Step 1: Write RED single-flight and cleanup tests**

```js
it('shares one promise and commits two subscriptions atomically', async () => {
  const bridge = deferred();
  const reconnect = deferred();
  const bridgeUnsubscribe = vi.fn();
  const reconnectUnsubscribe = vi.fn();
  const runtime = createRuntime();
  const actions = createRuntimeSlice(runtime, createDeps({
    onBridgeEvent: vi.fn(() => ({ ready: bridge.promise, unsubscribe: bridgeUnsubscribe })),
    onRuntimeReconnect: vi.fn(() => ({ ready: reconnect.promise, unsubscribe: reconnectUnsubscribe })),
  }));
  const first = actions.initializeEvents();
  const second = actions.initializeEvents();
  expect(first).toBe(second);
  bridge.resolve(true);
  await bridge.promise;
  expect(runtime.bridgeUnsubscribe).toBeNull();
  reconnect.resolve(true);
  await first;
  expect(runtime.bridgeUnsubscribe).toBe(bridgeUnsubscribe);
  expect(runtime.reconnectUnsubscribe).toBe(reconnectUnsubscribe);
});

it('cleans both subscriptions when one readiness is false', async () => {
  const bridgeUnsubscribe = vi.fn();
  const reconnectUnsubscribe = vi.fn();
  const actions = createRuntimeSlice(createRuntime(), createDeps({
    onBridgeEvent: vi.fn(() => ({ ready: Promise.resolve(true), unsubscribe: bridgeUnsubscribe })),
    onRuntimeReconnect: vi.fn(() => ({ ready: Promise.resolve(false), unsubscribe: reconnectUnsubscribe })),
  }));
  await expect(actions.initializeEvents()).rejects.toThrow('runtime.reconnect.subscribe unavailable');
  expect(bridgeUnsubscribe).toHaveBeenCalledTimes(1);
  expect(reconnectUnsubscribe).toHaveBeenCalledTimes(1);
});
```

Add rejected-ready and `destroy()`-before-settle tests; the latter expects `runtime event initialization superseded` and no late handle writeback.

- [ ] **Step 2: Run RED**

```bash
cd frontend-app
npx vitest run src/entities/client/model/runtimeSlice.test.js -t 'shares one promise|cleans both subscriptions|superseded' --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because initialization returns no shared Promise and commits/cleans handles independently.

- [ ] **Step 3: Implement tracked subscription and runtime fields**

Add runtime-owned fields:

```js
eventInitializationPromise: null,
eventInitializationGeneration: 0,
eventInitializationState: 'idle',
pendingRuntimeSubscriptions: new Set(),
bridgeUnsubscribe: null,
reconnectUnsubscribe: null,
```

Use one-shot unsubscribe wrappers and a tracked readiness contract:

```js
export function trackRuntimeSubscription(runtime, subscription, label, generation) {
  const unsubscribe = onceUnsubscribe(requiredUnsubscribe(subscription, label));
  const pending = { generation, unsubscribe, active: true };
  runtime.pendingRuntimeSubscriptions.add(pending);
  const ready = Promise.resolve(subscription.ready ?? true)
    .then((value) => {
      if (!pending.active || generation !== runtime.eventInitializationGeneration) {
        throw new Error('runtime event initialization superseded');
      }
      if (value !== true) throw new Error(`${label} unavailable`);
      return unsubscribe;
    })
    .catch((error) => {
      unsubscribe();
      throw error;
    })
    .finally(() => {
      pending.active = false;
      runtime.pendingRuntimeSubscriptions.delete(pending);
    });
  return { ready, unsubscribe };
}
```

`initializeEvents()` returns the existing Promise when initializing, commits both handles only after `Promise.all`, cleans both on false/reject, clears the Promise in `finally`, and allows complete retry after half-success.

`destroy()` unsubscribes pending/committed handles and increments generation; it does not claim to cancel native Promises.

- [ ] **Step 4: Run GREEN and diagnostics**

```bash
cd frontend-app
npx vitest run src/entities/client/model/runtimeSlice.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS; LSP diagnostics on three production files are empty.

- [ ] **Step 5: Commit atomic lifecycle**

```bash
git add frontend-app/src/entities/client/model/runtimeSlice.js frontend-app/src/entities/client/model/helpers/runtimeSliceHelpers.js frontend-app/src/entities/client/model/helpers/a1/clientStoreRuntimeCore.js frontend-app/src/entities/client/model/runtimeSlice.test.js
git commit -m "fix(frontend): make event subscriptions atomic"
```

### Task 2: Gate bootstrap and preserve events that beat the snapshot

**Files:** `runtimeSlice.js` and `useClientStore.test.js`.

- [ ] **Step 1: Write RED bootstrap tests**

```js
it('waits for both subscriptions before bootstrap RPCs', async () => {
  const bridge = deferred();
  const reconnect = deferred();
  backend.onBridgeEvent.mockReturnValue({ ready: bridge.promise, unsubscribe: vi.fn() });
  backend.onRuntimeReconnect.mockReturnValue({ ready: reconnect.promise, unsubscribe: vi.fn() });
  const promise = useClientStore.getState().bootstrap();
  await flushPromises();
  expect(backend.readConfig).not.toHaveBeenCalled();
  bridge.resolve(true);
  await flushPromises();
  expect(backend.readConfig).not.toHaveBeenCalled();
  reconnect.resolve(true);
  await promise;
  expect(backend.readConfig).toHaveBeenCalledTimes(1);
  expect(useClientStore.getState().bootstrapStatus).toBe('ready');
});
```

Add a false-ready test expecting `bootstrapStatus: 'failed'`, visible error and no RPC. Add an event-before-snapshot test where a live `running` patch arrives while sidebar snapshot is pending; after an `idle` snapshot resolves, state must remain running.

- [ ] **Step 2: Run RED**

```bash
cd frontend-app
npx vitest run src/entities/client/model/useClientStore.test.js -t 'waits for both subscriptions|subscription is unavailable|event before snapshot' --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because bootstrap currently starts RPCs without awaiting subscriptions and stale snapshot may overwrite live state.

- [ ] **Step 3: Implement the bootstrap gate**

At the start of bootstrap's existing try block:

```js
await runtime.get().initializeEvents();
const [config, rawWindowBootstrap] = await Promise.all([
  readConfig(),
  getWindowBootstrap(),
]);
```

Apply sidebar snapshot with existing live-status preservation options. Subscription failure flows through `handleBootstrapError` and is rethrown; it must never mark ready.

- [ ] **Step 4: Run GREEN and reconnect regression tests**

```bash
cd frontend-app
npx vitest run src/entities/client/model/useClientStore.test.js -t 'waits for both subscriptions|subscription is unavailable|event before snapshot|cold-start runtime reconnect' --no-file-parallelism --maxWorkers=1
```

Expected: PASS; diagnostics on runtime slice/store test are empty.

- [ ] **Step 5: Commit the bootstrap gate**

```bash
git add frontend-app/src/entities/client/model/runtimeSlice.js frontend-app/src/entities/client/model/useClientStore.test.js
git commit -m "fix(frontend): gate bootstrap on event readiness"
```

### Task 3: Emit real `wails:loaded` reconnect events

**Files:** `public/wails/runtime.js` and its test.

- [ ] **Step 1: Write RED shim tests**

```js
it('emits once after a failed connection recovers', async () => {
  vi.useFakeTimers();
  const sockets = [];
  const reconnected = vi.fn();
  vi.stubGlobal('WebSocket', createTestWebSocketClass(sockets));
  const runtime = await importFreshRuntimeShim();
  runtime.Events.On('wails:loaded', reconnected);
  sockets[0].error(new Error('ECONNREFUSED'));
  await vi.advanceTimersByTimeAsync(500);
  sockets[1].open();
  await Promise.resolve();
  expect(reconnected).toHaveBeenCalledTimes(1);
});
```

Also test: first normal open emits zero, each disconnect/reopen cycle emits once, and an unsubscribed listener is never called.

- [ ] **Step 2: Run RED**

```bash
cd frontend-app
npx vitest run public/wails/runtime.test.js -t 'failed connection recovers|first normal open|disconnect.*reopen|unsubscribed' --no-file-parallelism --maxWorkers=1
```

Expected: reconnect emission tests FAIL because no producer exists.

- [ ] **Step 3: Implement the reconnect-cycle marker**

```js
let reconnectNotificationPending = false;

function scheduleEventReconnect(error) {
  if (!hasEventListeners() || eventReconnectTimer != null) return;
  reconnectNotificationPending = true;
  eventReconnectTimer = setTimeout(() => {
    eventReconnectTimer = null;
    if (!hasEventListeners() || isSocketOpen(socket) || isSocketConnecting(socket)) return;
    ensureSocket().catch((nextError) => scheduleEventReconnect(nextError || error));
  }, EVENT_RECONNECT_DELAY_MS);
}
```

On `onopen`, capture/clear the marker and call `emitEvent('wails:loaded', {})` only when true. When no event listeners remain, clear timer and marker.

- [ ] **Step 4: Run GREEN and bridge tests**

```bash
cd frontend-app
npx vitest run public/wails/runtime.test.js src/shared/api/wailsBridge.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS; diagnostics are empty.

- [ ] **Step 5: Commit shim recovery**

```bash
git add frontend-app/public/wails/runtime.js frontend-app/public/wails/runtime.test.js
git commit -m "fix(frontend): signal recovered Wails connections"
```

### Task 4: Add explicit bootstrap retry UI

**Files:** chat header model/component/page/CSS and tests.

- [ ] **Step 1: Write RED model/component tests**

```js
it('exposes explicit bootstrap recovery', () => {
  expect(chatHeaderFeedbackForStore({
    bootstrapStatus: 'failed',
    error: 'event bridge unavailable',
  })).toEqual({
    message: '连接后端失败：event bridge unavailable',
    tone: 'error',
    bootstrapRecovery: true,
    retrying: false,
  });
});
```

Render `ChatPageHeader` with a failed store, click `重新连接后端`, and assert `bootstrap` once. Render loading+error and assert a disabled `正在重新连接后端` button; ready state removes it. Chat integration must keep composer/attachments disabled while failed/loading.

- [ ] **Step 2: Run RED**

```bash
cd frontend-app
npx vitest run src/pages/chat/model/chatHeaderModel.test.js src/pages/chat/components/ChatPageHeader.test.jsx src/pages/chat/ChatPage.core.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because recovery metadata/button do not exist.

- [ ] **Step 3: Implement finite explicit retry**

Return recovery metadata for failed and loading-with-error states. Render:

```jsx
<button
  type="button"
  className="btn secondary"
  aria-label={feedback.retrying ? '正在重新连接后端' : '重新连接后端'}
  disabled={feedback.retrying}
  onClick={() => runUIAction(() => store.bootstrap())}
>
  {feedback.retrying ? '连接中…' : '重新连接'}
</button>
```

Keep recovery header visible in intro mode; add wrapping responsive CSS. One click invokes one bootstrap, with no timer/backoff loop.

- [ ] **Step 4: Run GREEN and commit**

```bash
cd frontend-app
npx vitest run src/pages/chat/model/chatHeaderModel.test.js src/pages/chat/components/ChatPageHeader.test.jsx src/pages/chat/ChatPage.core.test.jsx --no-file-parallelism --maxWorkers=1
cd ..
git add frontend-app/src/pages/chat
git commit -m "fix(frontend): expose bootstrap reconnect action"
```

Expected: PASS and diagnostics are empty.

### Task 5: Document and fully verify readiness

- [ ] **Step 1: Update the frontend codemap**

Document: listener-first bootstrap, all-or-nothing readiness, false/reject failure, explicit retry, real dev-shim reconnect notification, destroy generation, and event-before-snapshot preservation.

- [ ] **Step 2: Run focused and full gates**

```bash
cd frontend-app
npx vitest run src/entities/client/model/runtimeSlice.test.js src/entities/client/model/useClientStore.test.js src/shared/api/wailsBridge.test.js public/wails/runtime.test.js src/pages/chat/model/chatHeaderModel.test.js src/pages/chat/components/ChatPageHeader.test.jsx src/pages/chat/ChatPage.core.test.jsx --no-file-parallelism --maxWorkers=1
npm run guard:critical-skip
npm run typecheck:contracts
npm run lint
npm test
npm run build
```

Expected: every command exits 0; no unhandled rejection or open-handle warning.

- [ ] **Step 3: Run final LSP evidence**

Use `grep/xref/call_hierarchy` to prove `bootstrap -> initializeEvents -> onBridgeEvent/onRuntimeReconnect`; diagnose every changed production file.

Expected: no Error, Warning, Information, or Hint.

- [ ] **Step 4: Verify diff and commit documentation**

```bash
cd ..
git diff --check
git status --short
git add docs/doc/codemap/01-terminal-ui-react.md
git commit -m "docs: document event subscription readiness"
```

Expected: ignored LSP artifacts and generated frontend output are not tracked/staged; shared-file changes were serialized with Plan 2.
