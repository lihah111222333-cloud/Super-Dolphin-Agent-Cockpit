import { readFile } from "node:fs/promises";
import { join } from "node:path";
import {
  REPO_ROOT,
  createShadowRepo,
  MATRIX_PATH,
  shadowMatrix,
} from "./rpc-audit-test-support-core.mjs";

export const REAL_INJECTION_PATH =
  "frontend-app/src/entities/client/model/helpers/a1/clientStoreActiveThreadActions.js";
export const REAL_CONSUMER_PATH = "frontend-app/src/entities/client/model/threadLifecycleRuntime.js";
export const REAL_REGRESSION_PATH =
  "frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js";

export function realResultHandledPolicy() {
  return `{
    kind: 'result-handled',
    consumer: { path: '${REAL_CONSUMER_PATH}', symbol: 'attachActiveThreadRpcRuntime' },
    handler: { path: '${REAL_CONSUMER_PATH}', symbol: 'notifyThreadActionFailure' },
    regressionTest: { path: '${REAL_REGRESSION_PATH}', symbol: 'reports interrupt ok:false as warning without showing success' },
  }`;
}

export function realResultHandledSources({
  facade = "interruptTurn",
  action = "thread.interrupt",
  resultProperty = "result",
  injectionBody = "",
  handlerCondition = "action === 'thread.interrupt' && result?.ok === false",
  handlerDefinitions = `
    const INTERRUPT_UNCONFIRMED_MESSAGE = '停止未确认，任务可能仍在运行'
    function interruptFailureMessage(result) {
      if (result?.mode !== 'interrupt_timeout') throw new Error('thread.interrupt ok:false response must be interrupt_timeout')
      return 'stop confirmation timed out; see Health diagnostic ID'
    }
  `,
  warningBody = [
    "notifyAction('中断当前执行失败，请重试。', 'warning', { threadId });",
    "addWarning('warn', `${action}.failed`, { threadId, error: 'action failure; see Health diagnostic ID' });",
    "return true",
  ].join(" "),
  regressionDefinitions = "",
  regressionBeforeAwait = "",
  regressionBetween = "",
} = {}) {
  return {
    consumer: `
      import { ${facade} } from '../../../../../shared/api/backendApi.js'
      export function createActiveThreadActions(runtime) {
        ${injectionBody || `return { interruptActiveThread: () => runtime.activeThreadRPC('${action}', ${facade}) }`}
      }
    `,
    handler: `
      ${handlerDefinitions}
      function notifyThreadActionFailure(params) {
        const { action, addWarning, notifyAction, result } = params
        const threadId = params.threadId
        if (${handlerCondition}) {
          if (result.mode === 'interrupt_timeout') {
            notifyAction(INTERRUPT_UNCONFIRMED_MESSAGE, 'warning', { threadId })
            addWarning('warn', \`\${action}.unconfirmed\`, { threadId, error: 'stop confirmation timed out; see Health diagnostic ID' })
            return true
          }
          ${warningBody}
        }
        return false
      }
      export function attachActiveThreadRpcRuntime(runtime) {
        const { addWarning, notifyAction } = runtime
        const runActiveThreadRPC = async (action, rpc, options = {}) => {
          const currentState = get()
          const requiresActiveTurn = threadActionRequiresActiveTurn(action)
          const activeTurnTarget = requiresActiveTurn ? activeThreadInterruptTarget(currentState) : null
          const threadId = options.threadId || activeTurnTarget?.threadId || backendThreadIdForState(currentState, currentState.activeThreadId)
          if (!threadId) {
            notifyAction('当前没有可操作的后端线程', 'warning')
            return { ok: false, threadId: '', result: null }
          }
          try {
            const cwd = requireCwd(action)
            const payload = threadActionPayload({ action, activeThreadInterruptTarget, activeTurnTarget, cleanObject, createRequestId, currentState, cwd, notifyAction, threadId })
            if (!payload) return { ok: false, threadId, result: null }
            const result = await rpc({})
            if (notifyThreadActionFailure({ action, addWarning, notifyAction, ${resultProperty}: result, threadId })) return { ok: false, threadId, result }
            return { ok: true, threadId, result }
          } catch (error) {
            return { ok: false, threadId, result: null }
          }
        }
        const activeThreadRPC = async (action, rpc) => {
          const outcome = await runActiveThreadRPC(action, rpc)
          if (!outcome.ok) return false
          notifyAction('success', 'success')
          return true
        }
        Object.assign(runtime, { activeThreadRPC })
      }
    `,
    regression: `
      import { vi } from 'vitest'
      import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js'
      function createRuntime() { return { notifyAction: vi.fn(), addWarning: vi.fn() } }
      function createDeps() { return {} }
      ${regressionDefinitions}
      it('reports interrupt ok:false as warning without showing success', async () => {
        const runtime = createRuntime()
        const deps = createDeps()
        const rpc = vi.fn().mockResolvedValue({
          ok: false, accepted: true, requestId: 'stop-request-1', expectedTurnId: 'turn-1',
          turnId: 'turn-1', status: 'running', confirmed: true, mode: 'interrupt_timeout',
          interruptSent: true, stateBefore: 'running', stateAfter: 'running', waitedMs: 1, activeObserved: true,
        })
        attachActiveThreadRpcRuntime(runtime, deps)
        ${regressionBeforeAwait}
        await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false)
        ${regressionBetween}
        expect(rpc).toHaveBeenCalledTimes(1)
        expect(rpc).toHaveBeenCalledWith({ cwd: '/repo/app', threadId: 'thread-1', expectedTurnId: 'turn-1', requestId: 'stop-request-1', source: 'ui_stop' })
        expect(runtime.notifyAction).toHaveBeenCalledWith('停止未确认，任务可能仍在运行', 'warning', { threadId: 'thread-1' })
        expect(runtime.notifyAction).not.toHaveBeenCalledWith('正在请求停止，尚未确认，任务可能仍在运行', 'info', { threadId: 'thread-1' })
        expect(runtime.notifyAction).not.toHaveBeenCalledWith('已发送中断请求', 'success', { threadId: 'thread-1' })
        expect(runtime.addWarning).toHaveBeenCalledWith(
          'warn',
          'thread.interrupt.unconfirmed',
          { threadId: 'thread-1', error: 'stop confirmation timed out; see Health diagnostic ID' },
        )
      })
    `,
  };
}

export async function createRealResultHandledShadow(overrides) {
  const sources =
    overrides != null
      ? realResultHandledSources(overrides)
      : {
          consumer: await readFile(join(REPO_ROOT, REAL_INJECTION_PATH), "utf8"),
          handler: await readFile(join(REPO_ROOT, REAL_CONSUMER_PATH), "utf8"),
          regression: await readFile(join(REPO_ROOT, REAL_REGRESSION_PATH), "utf8"),
        };
  return createShadowRepo({
    [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${realResultHandledPolicy()} }`, {
      key: "TURN_INTERRUPT",
      facade: "interruptTurn",
    }),
    [REAL_INJECTION_PATH]: sources.consumer,
    [REAL_CONSUMER_PATH]: sources.handler,
    [REAL_REGRESSION_PATH]: sources.regression,
  });
}

export async function createMutatedSingleHelperResultHandledShadow(mutateRuntime) {
  const sources = realResultHandledSources();
  const handler = mutateRuntime(sources.handler);
  if (handler === sources.handler)
    throw new Error("single-helper runtime mutation must change the source");
  return createShadowRepo({
    [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${realResultHandledPolicy()} }`, {
      key: "TURN_INTERRUPT",
      facade: "interruptTurn",
    }),
    [REAL_INJECTION_PATH]: sources.consumer,
    [REAL_CONSUMER_PATH]: handler,
    [REAL_REGRESSION_PATH]: sources.regression,
  });
}

export async function createMutatedRealResultHandledShadow({
  injection = (source) => source,
  runtime = (source) => source,
  regression = (source) => source,
} = {}) {
  const sources = {
    injection: await readFile(join(REPO_ROOT, REAL_INJECTION_PATH), "utf8"),
    runtime: await readFile(join(REPO_ROOT, REAL_CONSUMER_PATH), "utf8"),
    regression: await readFile(join(REPO_ROOT, REAL_REGRESSION_PATH), "utf8"),
  };
  const mutated = {
    injection: injection(sources.injection),
    runtime: runtime(sources.runtime),
    regression: regression(sources.regression),
  };
  if (Object.keys(sources).every((key) => mutated[key] === sources[key])) {
    throw new Error("real result-handled mutation must change source");
  }
  return createShadowRepo({
    [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${realResultHandledPolicy()} }`, {
      key: "TURN_INTERRUPT",
      facade: "interruptTurn",
    }),
    [REAL_INJECTION_PATH]: mutated.injection,
    [REAL_CONSUMER_PATH]: mutated.runtime,
    [REAL_REGRESSION_PATH]: mutated.regression,
  });
}

export function ignoredResultRegression() {
  return `
    import { loadConfig } from './consumer.js'
    it('keeps config result irrelevant', async () => {
      const result = await loadConfig()
      expect(result).toBeUndefined()
    })
  `;
}

export function publishedCallbackConsumer(
  body = `
  await facade.readConfig({ cwd: '/repo' })
  notices.showTaskNotice('saved', 'config')
`,
) {
  return `export async function loadConfig({ facade, notices }) { ${body} }`;
}

export function publishedCallbackRegression({
  importStatement = "import { loadConfig } from './consumer.js'",
  mockMethod = "mockResolvedValue",
  response = "{ malformed: 'config-sentinel' }",
  beforeCall = "",
  call = "const result = await loadConfig(ctx)",
  contextSetup = `const ctx = {
    facade: { readConfig: vi.fn().${mockMethod}(${response}) },
    notices: { showTaskNotice: vi.fn() },
    other: { showTaskNotice: vi.fn() },
  }`,
  assertions = `
    expect(ctx.facade.readConfig).toHaveBeenCalledWith({ cwd: '/repo' })
    expect(result).toBeUndefined()
    expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')
  `,
} = {}) {
  return `
    import { expect, it, vi } from 'vitest'
    ${importStatement}
    it('keeps config result irrelevant', async () => {
      ${contextSetup}
      ${beforeCall}
      ${call}
      ${assertions}
    })
  `;
}

export function pageIgnoredResultRegression({
  mockFacade = "readConfig",
  response = "{ malformed: ['ignored-response-body'] }",
  trigger = "fireEvent.click(screen.getByRole('button', { name: 'save' }))",
  invocationAssertion = `await waitFor(() => expect(backend.${mockFacade}).toHaveBeenCalled())`,
  assertion = "expect(await screen.findByText('saved')).toBeInTheDocument()",
} = {}) {
  return `
    import { vi } from 'vitest'
    const backend = vi.hoisted(() => ({ readConfig: vi.fn(), startThread: vi.fn() }))
    vi.mock('../../shared/api/backendApi.js', () => backend)
    it('keeps config result irrelevant', async () => {
      backend.${mockFacade}.mockResolvedValue(${response})
      renderPage()
      ${trigger}
      ${invocationAssertion}
      ${assertion}
    })
  `;
}

export function directWailsIgnoredResultRegression({
  method = "ui/log",
  mockMethod = "mockRejectedValue",
  rejectionAssertion = "await expect(sendFrontendLogBatch([])).rejects.toThrow('log ingest unavailable')",
  methodAssertion = "expect(byID).toHaveBeenCalledWith(expect.any(Number), 'ui/log', expect.any(Object))",
} = {}) {
  return `
    import { vi } from 'vitest'
    it('propagates frontend log batch RPC failures', async () => {
      const byID = vi.fn().${mockMethod}(new Error('log ingest unavailable'))
      const sendFrontendLogBatch = async () => byID(1, '${method}', {})
      ${rejectionAssertion}
      ${methodAssertion}
    })
  `;
}

export const DIRECT_WAILS_IGNORED_RESULT_CONSUMER = `
  export async function sendFrontendLogBatch(entries) {
    await runtime.Call.ByID(1, 'ui/log', { entries })
  }
`;

export {
  REPO_ROOT,
  createShadowRepo,
  MATRIX_PATH,
  shadowMatrix,
} from "./rpc-audit-test-support-core.mjs";
