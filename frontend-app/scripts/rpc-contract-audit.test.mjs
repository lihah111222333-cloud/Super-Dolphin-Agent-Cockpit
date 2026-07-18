import { describe, expect, it, onTestFinished } from 'vitest'
import { spawnSync } from 'node:child_process'
import { mkdtemp, mkdir, readFile, realpath, rm, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import {
  auditRpcContracts,
  astReferencesFacadeForTest,
  collectFrontendPayloadKeysFromSource,
  collectHardcodedPayloadGuardFindingsFromSources,
  collectPayloadRegistryDrift,
  collectSidebarRequiredFieldFindingsFromSources,
  parseContractMatrixForTest,
  parseRpcMethodsForTest,
} from './rpc-contract-audit.mjs'

const REPO_ROOT = resolve(import.meta.dirname, '../..')
const SHADOW_FILES = [
  'internal/contract/rpc_handler.go',
  'internal/module/thread/rpc_types.go',
  'internal/module/turn/rpc_types.go',
  'internal/module/uistate/state.go',
  'frontend-app/src/shared/api/backendApi.js',
  'frontend-app/src/shared/api/backend/backendRpcMethods.js',
  'frontend-app/src/shared/api/backendApi.contractMatrix.js',
  'frontend-app/src/shared/api/backendResponseValidators.js',
  'frontend-app/src/shared/api/backendResponseValidatorsRuntime.js',
  'frontend-app/src/shared/api/backend/backendApiFactoryCore.js',
  'frontend-app/src/shared/api/backend/backendApiFactoryOps.js',
  'frontend-app/src/shared/api/backend/backendApiFactoryThread.js',
  'frontend-app/src/shared/api/wails/wailsBridgeConstants.js',
  'frontend-app/src/shared/api/wails/wailsBridgeRpc.js',
  'frontend-app/src/shared/api/wails/wailsBridgeTraceEvents.js',
  'frontend-app/src/pages/files/services/filesPageService.js',
  'frontend-app/src/pages/memory/services/memoryPageService.js',
  'frontend-app/src/pages/observability/services/observabilityPageService.js',
  'frontend-app/src/pages/prompts/services/promptPageService.js',
]

async function createShadowRepo(overrides) {
  const repoRoot = await mkdtemp(join(tmpdir(), 'rpc-contract-audit-'))
  onTestFinished(() => rm(repoRoot, { recursive: true, force: true }))
  await mkdir(join(repoRoot, 'cmd'), { recursive: true })
  for (const filePath of new Set([...SHADOW_FILES, ...Object.keys(overrides)])) {
    const target = join(repoRoot, filePath)
    await mkdir(dirname(target), { recursive: true })
    let source = overrides[filePath] ?? await readFile(join(REPO_ROOT, filePath), 'utf8')
    if (filePath === 'frontend-app/src/shared/api/backendApi.contractMatrix.js') {
      source = shadowStructuredMatrix(source)
    }
    await writeFile(target, source)
  }
  return repoRoot
}

const MATRIX_PATH = 'frontend-app/src/shared/api/backendApi.contractMatrix.js'
const CONSUMER_PATH = 'frontend-app/src/pages/audit-fixture/consumer.js'
const HANDLER_PATH = 'frontend-app/src/pages/audit-fixture/resultHandler.js'
const REGRESSION_PATH = 'frontend-app/src/pages/audit-fixture/consumer.test.js'
const UNUSED_POLICY = `{
  kind: 'unused',
  productionScanRoots: ['frontend-app/src'],
  excludedGlobs: [],
}`
function shadowStructuredMatrix(source) {
  return source.replace(
    /\{\s*responsePassthroughReason:\s*'[^'\n]*'\s*\}/g,
    `{ responsePolicy: ${UNUSED_POLICY} }`,
  )
}

async function createRuntimeDriftFixture() {
  return createShadowRepo({})
}

function shadowMatrix(options = '', {
  key = 'CONFIG_READ',
  facade = 'readConfig',
  level = 'P1',
} = {}) {
  return `
    function contract() {}
    export const RPC_CONTRACT_REGISTRY = Object.freeze({
      ${key}: contract(
        '${key}',
        RPC_METHODS.${key},
        '${facade}',
        '${level}',
        'config',
        [],
        [],
        false,
        ${options ? `${options},` : ''}
      ),
    })
  `
}

async function createPolicyShadow({ policy, consumer = '', handler = '', regression = '' }) {
  return createShadowRepo({
    [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${policy} }`),
    [CONSUMER_PATH]: consumer,
    [HANDLER_PATH]: handler,
    [REGRESSION_PATH]: regression,
  })
}

function ignoredResultPolicy({
  consumerPath = CONSUMER_PATH,
  consumerSymbol = 'loadConfig',
  consumerVisibility = '',
  outcomeTarget = null,
  regressionPath = REGRESSION_PATH,
  regressionSymbol = 'keeps config result irrelevant',
} = {}) {
  return `{
    kind: 'ignored-result',
    consumer: { path: '${consumerPath}', symbol: '${consumerSymbol}'${consumerVisibility ? `, visibility: '${consumerVisibility}'` : ''} },
    ${outcomeTarget ? `outcome: { kind: 'published-callback', target: [${outcomeTarget.map((part) => `'${part}'`).join(', ')}] },` : ''}
    regressionTest: { path: '${regressionPath}', symbol: '${regressionSymbol}' },
  }`
}

function consumerValidatedPolicy({
  consumerPath = CONSUMER_PATH,
  consumerSymbol = 'loadConfig',
  shapePath = CONSUMER_PATH,
  shapeSymbol = 'assertConfigShape',
  regressionPath = REGRESSION_PATH,
  regressionSymbol = 'rejects malformed config',
} = {}) {
  return `{
    kind: 'consumer-validated',
    consumer: { path: '${consumerPath}', symbol: '${consumerSymbol}' },
    shape: { path: '${shapePath}', symbol: '${shapeSymbol}' },
    regressionTest: { path: '${regressionPath}', symbol: '${regressionSymbol}' },
  }`
}

function resultHandledPolicy({
  consumerPath = CONSUMER_PATH,
  consumerSymbol = 'interruptTurn',
  handlerPath = HANDLER_PATH,
  handlerSymbol = 'handleInterruptResult',
  regressionPath = REGRESSION_PATH,
  regressionSymbol = 'warns when interrupt is rejected',
} = {}) {
  return `{
    kind: 'result-handled',
    consumer: { path: '${consumerPath}', symbol: '${consumerSymbol}' },
    handler: { path: '${handlerPath}', symbol: '${handlerSymbol}' },
    regressionTest: { path: '${regressionPath}', symbol: '${regressionSymbol}' },
  }`
}

function resultHandledConsumer({ facade = 'readConfig', argument = 'result', ignored = false } = {}) {
  return `
    import { ${facade} } from '../../shared/api/backendApi.js'
    import { handleInterruptResult } from './resultHandler.js'
    export async function interruptTurn() {
      const result = await ${facade}()
      ${ignored ? '' : `return handleInterruptResult(${argument})`}
    }
  `
}

function resultHandler({ body = `
  if (!result.ok) {
    console.warn(result.error)
    return false
  }
  return true
` } = {}) {
  return `
    export function handleInterruptResult(result) {
      ${body}
    }
  `
}

function resultHandledRegression({
  mockFacade = 'readConfig',
  assertion = "expect(warn).toHaveBeenCalledWith('interrupt denied')",
  response = "{ ok: false, error: 'interrupt denied' }",
  consumerInvocation = 'await interruptTurn()',
  beforeAssertion = '',
  extraImports = '',
} = {}) {
  return `
    import { vi } from 'vitest'
    import { interruptTurn } from './consumer.js'
    ${extraImports}
    vi.mock('../../shared/api/backendApi.js', () => ({
      ${mockFacade}: vi.fn().mockResolvedValue(${response}),
    }))
    it('warns when interrupt is rejected', async () => {
      const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
      ${consumerInvocation}
      ${beforeAssertion}
      ${assertion}
    })
  `
}

const REAL_INJECTION_PATH = 'frontend-app/src/entities/client/model/helpers/a1/clientStoreThreadActions.js'
const REAL_CONSUMER_PATH = 'frontend-app/src/entities/client/model/threadLifecycleRuntime.js'
const REAL_REGRESSION_PATH = 'frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js'

function realResultHandledPolicy() {
  return `{
    kind: 'result-handled',
    consumer: { path: '${REAL_CONSUMER_PATH}', symbol: 'attachActiveThreadRpcRuntime' },
    handler: { path: '${REAL_CONSUMER_PATH}', symbol: 'notifyThreadActionFailure' },
    regressionTest: { path: '${REAL_REGRESSION_PATH}', symbol: 'reports interrupt ok:false as warning without showing success' },
  }`
}

function realResultHandledSources({
  facade = 'interruptTurn', action = 'thread.interrupt', resultProperty = 'result',
  injectionBody = '',
  handlerCondition = "action === 'thread.interrupt' && result?.ok === false",
  handlerDefinitions = `
    function interruptFailureMessage(result) {
      for (const value of [result?.error, result?.message, result?.reason, result?.status, result?.mode]) {
        const message = normalizeOptionalTextField(value); if (message) return message
      }
      throw new Error('thread.interrupt ok:false response message is required')
    }
  `,
  warningBody = "interruptFailureMessage(result); notifyAction('中断当前执行失败，请重试。', 'warning', { threadId: 'thread-1' }); addWarning('warn', 'thread.interrupt.failed', { threadId: 'thread-1', error: 'action failure; see Health diagnostic ID' })",
  regressionDefinitions = '',
  regressionBeforeAwait = '',
  regressionBetween = '',
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
        if (${handlerCondition}) { ${warningBody}; return true }
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
        const rpc = vi.fn().mockResolvedValue({ ok: false, error: 'turn already completed' })
        attachActiveThreadRpcRuntime(runtime, deps)
        ${regressionBeforeAwait}
        await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false)
        ${regressionBetween}
        expect(rpc).toHaveBeenCalledTimes(1)
        expect(runtime.notifyAction).toHaveBeenCalledWith('中断当前执行失败，请重试。', 'warning', { threadId: 'thread-1' })
        expect(runtime.notifyAction).not.toHaveBeenCalledWith('已发送中断请求', 'success', { threadId: 'thread-1' })
        expect(runtime.addWarning).toHaveBeenCalledWith('warn', 'thread.interrupt.failed', { threadId: 'thread-1', error: 'action failure; see Health diagnostic ID' })
        expect(JSON.stringify(runtime.notifyAction.mock.calls)).not.toContain('turn already completed')
        expect(JSON.stringify(runtime.addWarning.mock.calls)).not.toContain('turn already completed')
      })
    `,
  }
}

async function createRealResultHandledShadow(overrides) {
  const sources = overrides != null ? realResultHandledSources(overrides) : {
    consumer: await readFile(join(REPO_ROOT, REAL_INJECTION_PATH), 'utf8'),
    handler: await readFile(join(REPO_ROOT, REAL_CONSUMER_PATH), 'utf8'),
    regression: await readFile(join(REPO_ROOT, REAL_REGRESSION_PATH), 'utf8'),
  }
  return createShadowRepo({
    [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${realResultHandledPolicy()} }`, { key: 'TURN_INTERRUPT', facade: 'interruptTurn' }),
    [REAL_INJECTION_PATH]: sources.consumer,
    [REAL_CONSUMER_PATH]: sources.handler,
    [REAL_REGRESSION_PATH]: sources.regression,
  })
}

async function createMutatedSingleHelperResultHandledShadow(mutateRuntime) {
  const sources = realResultHandledSources()
  const handler = mutateRuntime(sources.handler)
  if (handler === sources.handler) throw new Error('single-helper runtime mutation must change the source')
  return createShadowRepo({
    [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${realResultHandledPolicy()} }`, { key: 'TURN_INTERRUPT', facade: 'interruptTurn' }),
    [REAL_INJECTION_PATH]: sources.consumer,
    [REAL_CONSUMER_PATH]: handler,
    [REAL_REGRESSION_PATH]: sources.regression,
  })
}

async function createMutatedRealResultHandledShadow({
  injection = (source) => source,
  runtime = (source) => source,
  regression = (source) => source,
} = {}) {
  return createShadowRepo({
    [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${realResultHandledPolicy()} }`, { key: 'TURN_INTERRUPT', facade: 'interruptTurn' }),
    [REAL_INJECTION_PATH]: injection(await readFile(join(REPO_ROOT, REAL_INJECTION_PATH), 'utf8')),
    [REAL_CONSUMER_PATH]: runtime(await readFile(join(REPO_ROOT, REAL_CONSUMER_PATH), 'utf8')),
    [REAL_REGRESSION_PATH]: regression(await readFile(join(REPO_ROOT, REAL_REGRESSION_PATH), 'utf8')),
  })
}

function ignoredResultRegression() {
  return `
    import { loadConfig } from './consumer.js'
    it('keeps config result irrelevant', async () => {
      const result = await loadConfig()
      expect(result).toBeUndefined()
    })
  `
}

function publishedCallbackConsumer(body = `
  await facade.readConfig({ cwd: '/repo' })
  notices.showTaskNotice('saved', 'config')
`) {
  return `export async function loadConfig({ facade, notices }) { ${body} }`
}

function publishedCallbackRegression({
  importStatement = "import { loadConfig } from './consumer.js'",
  mockMethod = 'mockResolvedValue',
  response = "{ malformed: 'config-sentinel' }",
  beforeCall = '',
  call = 'const result = await loadConfig(ctx)',
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
  `
}

function pageIgnoredResultRegression({
  mockFacade = 'readConfig',
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
  `
}

function directWailsIgnoredResultRegression({
  method = 'ui/log',
  mockMethod = 'mockRejectedValue',
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
  `
}

const DIRECT_WAILS_IGNORED_RESULT_CONSUMER = `
  export async function sendFrontendLogBatch(entries) {
    await runtime.Call.ByID(1, 'ui/log', { entries })
  }
`

function consumerValidatedRegression() {
  return `
    import { vi } from 'vitest'
    import { loadConfig } from './consumer.js'
    vi.mock('../../shared/api/backendApi.js', () => ({
      readConfig: vi.fn().mockResolvedValue({ malformed: true }),
    }))
    it('rejects malformed config', async () => {
      await expect(loadConfig()).rejects.toThrow('invalid config')
    })
  `
}

describe('rpc contract audit', { timeout: 30000 }, () => {
  it('skips deep AST traversal when a file has no relevant facade bindings', () => {
    const unrelatedStatement = {
      type: 'ExpressionStatement',
      get expression() {
        throw new Error('unrelated AST was traversed')
      },
    }
    const ast = {
      type: 'File',
      program: {
        type: 'Program',
        body: [unrelatedStatement],
      },
    }

    expect(astReferencesFacadeForTest(
      ast,
      'frontend-app/src/unrelated.js',
      { key: 'CONFIG_READ', facade: 'readConfig' },
      new Map([['readConfig', 'CONFIG_READ']]),
      new Map(),
    )).toBe(false)
  })

  it('accepts the production matrix after response policy migration', async () => {
    const report = await auditRpcContracts({ repoRoot: REPO_ROOT })

    expect(report).toEqual(expect.objectContaining({
      missingResponsePolicies: [],
      missingFrontendResponseValidators: [],
      invalidResponsePolicyEvidence: [],
      responseContractStrategies: expect.arrayContaining([
        expect.objectContaining({
          key: 'UI_SIDEBAR_GET',
          method: 'ui/sidebar/get',
          frontendValidator: true,
        }),
        expect.objectContaining({
          key: 'THREAD_FORK',
          method: 'thread/fork',
          frontendValidator: true,
        }),
      ]),
    }))
    expect(report.missingRegistryKeys).toEqual([])
    expect(report.registryWithoutRpcMethods).toEqual([])
    expect(report.mismatchedRegistryMethods).toEqual([])
    expect(report.p0MissingBackendHandlers).toEqual([])
    expect(report.allowedPayloadRegistryDrift).toEqual([])
    expect(report.hardcodedPayloadGuardFindings).toEqual([])
    expect(report.auditStats).toEqual(expect.objectContaining({
      productionFacadeReferenceIndexBuilds: 1,
    }))
    expect(report.sidebarRequiredFieldFindings).toEqual([])
    expect(report.responseContractStrategies).toEqual(expect.arrayContaining([
      {
        key: 'UI_SIDEBAR_GET',
        method: 'ui/sidebar/get',
        matrixPolicy: 'sidebarStateResponse',
        frontendValidator: true,
      },
      {
        key: 'THREAD_FORK',
        method: 'thread/fork',
        matrixPolicy: 'threadForkResponse',
        frontendValidator: true,
      },
    ]))
    expect(report.frontendPayloadKeysByMethod.get('thread/start')).toEqual(expect.arrayContaining([
      'manualSkillSelection',
      'manual_skill_selection',
      'provider',
    ]))
    expect(report.frontendPayloadKeysByMethod.get('turn/start')).toEqual(expect.arrayContaining([
      'isWorktree',
      'is_worktree',
      'manualSkillSelection',
      'manual_skill_selection',
    ]))
    expect(report.goPayloadKeysByMethod.get('turn/start')).toEqual(expect.arrayContaining([
      'thread_id',
      'threadId',
      'selected_skill_refs',
      'selectedSkillRefs',
    ]))
    expect(report.frontendPayloadKeysByMethod.get('turn/interrupt')).toEqual(expect.arrayContaining([
      'expectedTurnId',
      'requestId',
      'threadId',
    ]))
    expect(report.goPayloadKeysByMethod.get('turn/interrupt')).toEqual(expect.arrayContaining([
      'expected_turn_id',
      'expectedTurnId',
      'request_id',
      'requestId',
      'thread_id',
      'threadId',
    ]))
  }, 30000)

  it('audits runtime payload builders when facade shadows stay unchanged', async () => {
    const runtimePath = 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js'
    const runtimeSource = await readFile(join(REPO_ROOT, runtimePath), 'utf8')
    const mutatedSource = runtimeSource.replace(
      "takePayloadField(unused, 'provider')",
      "takePayloadField(unused, 'provider_shadow')",
    )
    expect(mutatedSource).not.toBe(runtimeSource)
    const repoRoot = await createShadowRepo({ [runtimePath]: mutatedSource })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.allowedPayloadRegistryDrift).toEqual(expect.arrayContaining([
      expect.objectContaining({
        method: 'thread/start',
        missingFrontendKeys: expect.arrayContaining(['provider']),
        extraFrontendKeys: expect.arrayContaining(['provider_shadow']),
      }),
    ]))
  })

  it('audits runtime RPC methods when facade shadows stay unchanged', async () => {
    const methodsPath = 'frontend-app/src/shared/api/backend/backendRpcMethods.js'
    const methodsSource = await readFile(join(REPO_ROOT, methodsPath), 'utf8')
    const mutatedSource = methodsSource.replace(
      "  THREAD_PROMPT_HISTORY: 'thread/promptHistory',\n",
      '',
    )
    expect(mutatedSource).not.toBe(methodsSource)
    const repoRoot = await createShadowRepo({ [methodsPath]: mutatedSource })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.registryWithoutRpcMethods).toContain('THREAD_PROMPT_HISTORY')
  })

  it('fails payload drift when the Stop mapper drops expectedTurnId', async () => {
    const mapperPath = 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js'
    const mapperSource = await readFile(join(REPO_ROOT, mapperPath), 'utf8')
    const mutatedSource = mapperSource.replace(
      "    { key: 'expectedTurnId', value: takePayloadField(unused, 'expectedTurnId') },\n",
      '',
    )
    expect(mutatedSource).not.toBe(mapperSource)
    const repoRoot = await createShadowRepo({ [mapperPath]: mutatedSource })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.allowedPayloadRegistryDrift).toEqual(expect.arrayContaining([
      expect.objectContaining({
        method: 'turn/interrupt',
        missingFrontendKeys: expect.arrayContaining(['expectedTurnId']),
      }),
    ]))
  }, 15000)

  it('derives Sidebar required fields from Go tags and detects missing or stale consumer entries', async () => {
    const goSource = await readFile(join(REPO_ROOT, 'internal/module/uistate/state.go'), 'utf8')
    const runtimePath = 'frontend-app/src/shared/api/backendResponseValidatorsRuntime.js'
    const runtimeSource = await readFile(join(REPO_ROOT, runtimePath), 'utf8')
    const missingConsumerSource = runtimeSource.replace("'workspace', ", '')
    const staleProducerSource = goSource.replace(
      'Workspace             WorkspacePanel            `json:"workspace"`',
      'Workspace             WorkspacePanel            `json:"workspace,omitempty"`',
    )

    expect(missingConsumerSource).not.toBe(runtimeSource)
    expect(staleProducerSource).not.toBe(goSource)
    const repoRoot = await createShadowRepo({ [runtimePath]: missingConsumerSource })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.sidebarRequiredFieldFindings).toEqual(['missing:workspace'])
    expect(collectSidebarRequiredFieldFindingsFromSources({
      goSource: staleProducerSource,
      runtimeSource,
    })).toEqual(['stale:workspace'])
  })

  it('exits the real CLI with the exact Sidebar required-field drift', async () => {
    const runtimePath = 'frontend-app/src/shared/api/backendResponseValidatorsRuntime.js'
    const auditScriptPath = 'frontend-app/scripts/rpc-contract-audit.mjs'
    const runtimeSource = await readFile(join(REPO_ROOT, runtimePath), 'utf8')
    const auditScriptSource = await readFile(join(REPO_ROOT, auditScriptPath), 'utf8')
    const missingConsumerSource = runtimeSource.replace("'workspace', ", '')

    expect(missingConsumerSource).not.toBe(runtimeSource)
    const repoRoot = await createShadowRepo({
      [runtimePath]: missingConsumerSource,
      [auditScriptPath]: auditScriptSource,
    })
    await symlink(
      join(REPO_ROOT, 'frontend-app/node_modules'),
      join(repoRoot, 'frontend-app/node_modules'),
    )

    const canonicalRepoRoot = await realpath(repoRoot)
    const result = spawnSync(process.execPath, [join(canonicalRepoRoot, auditScriptPath)], {
      cwd: join(canonicalRepoRoot, 'frontend-app'),
      encoding: 'utf8',
    })

    expect(result.stdout).toContain('Sidebar required field drift: 1')
    expect(result.status).toBe(1)
    expect(result.stderr).toContain('\nSidebar required field drift:\n[\n  "missing:workspace"\n]\n')
  })

  it('rejects malformed Sidebar tags and an unreferenced runtime required-field registry', async () => {
    const goSource = await readFile(join(REPO_ROOT, 'internal/module/uistate/state.go'), 'utf8')
    const runtimeSource = await readFile(
      join(REPO_ROOT, 'frontend-app/src/shared/api/backendResponseValidatorsRuntime.js'),
      'utf8',
    )
    const malformedGoSource = goSource.replace('json:"workspace"', 'yaml:"workspace"')
    const unreferencedRegistrySource = runtimeSource.replace(
      'for (const requiredField of SIDEBAR_REQUIRED_RESPONSE_KEYS)',
      'for (const requiredField of [])',
    )

    expect(() => collectSidebarRequiredFieldFindingsFromSources({
      goSource: malformedGoSource,
      runtimeSource,
    })).toThrow('Sidebar.Workspace must declare exactly one json tag')
    expect(collectSidebarRequiredFieldFindingsFromSources({
      goSource,
      runtimeSource: unreferencedRegistrySource,
    })).toEqual(['runtime:SIDEBAR_REQUIRED_RESPONSE_KEYS is not used by the required-field check'])
  })

  it.each(['continue;', 'return value;'])(
    'rejects a Sidebar required-field check made unreachable by unconditional %s',
    async (controlTransfer) => {
      const goSource = await readFile(join(REPO_ROOT, 'internal/module/uistate/state.go'), 'utf8')
      const runtimeSource = await readFile(
        join(REPO_ROOT, 'frontend-app/src/shared/api/backendResponseValidatorsRuntime.js'),
        'utf8',
      )
      const loopHeader = 'for (const requiredField of SIDEBAR_REQUIRED_RESPONSE_KEYS) {\n'

      const unreachableCheckSource = runtimeSource.replace(
        loopHeader,
        `${loopHeader}    ${controlTransfer}\n`,
      )
      expect(unreachableCheckSource).not.toBe(runtimeSource)
      expect(collectSidebarRequiredFieldFindingsFromSources({
        goSource,
        runtimeSource: unreachableCheckSource,
      })).toEqual(['runtime:SIDEBAR_REQUIRED_RESPONSE_KEYS is not used by the required-field check'])
    },
  )

  it('detects frontend and Go hardcoded payload guard sources', () => {
    const findings = collectHardcodedPayloadGuardFindingsFromSources({
      frontendSource: `
        export const RPC_ALLOWED_PAYLOAD_KEYS = new Set([
          'threadId',
        ])
        const THREAD_START_ALLOWED_KEYS = new Set([
          'threadId',
        ])
      `,
      goSources: new Map([
        ['internal/module/thread/rpc_types.go', 'var startParamWireFields = map[string]struct{}{}'],
      ]),
    })

    expect(findings).toEqual([
      'frontend-app/src/shared/api/backendApi.js:RPC_ALLOWED_PAYLOAD_KEYS',
      'frontend-app/src/shared/api/backendApi.js:THREAD_START_ALLOWED_KEYS',
      'internal/module/thread/rpc_types.go:startParamWireFields',
    ])
  })

  it.each([
    ['missing', 'export { createBackendApi } from \'./backend/backendApiFactoryThread.js\''],
    ['wrong source', "export { RPC_METHODS } from './backend/notRpcMethods.js'"],
    ['local definition', "export const RPC_METHODS = Object.freeze({ SHADOW_ONLY: 'shadow/only' })"],
  ])('rejects a %s RPC_METHODS facade re-export', async (_label, facadeSource) => {
    const repoRoot = await createShadowRepo({
      'frontend-app/src/shared/api/backendApi.js': facadeSource,
    })

    await expect(auditRpcContracts({ repoRoot })).rejects.toThrow(
      'backendApi.js must named re-export RPC_METHODS from ./backend/backendRpcMethods.js exactly once',
    )
  })

  it('ignores payload guard names outside top-level Set declarations', () => {
    const findings = collectHardcodedPayloadGuardFindingsFromSources({
      frontendSource: `
        // const RPC_ALLOWED_PAYLOAD_KEYS = new Set(['comment'])
        const example = "const THREAD_START_ALLOWED_KEYS = new Set(['string'])"
        function nested() {
          const TURN_START_ALLOWED_KEYS = new Set(['nested'])
        }
        const NOT_ALLOWED_KEYS = Object.freeze(['wrong initializer'])
      `,
    })

    expect(findings).toEqual([])
  })

  it('reports only top-level payload guard Set declarations', () => {
    const findings = collectHardcodedPayloadGuardFindingsFromSources({
      frontendSource: `
        const RPC_ALLOWED_PAYLOAD_KEYS = new Set(['rpc'])
        const THREAD_START_ALLOWED_KEYS = new Set(['thread'])
      `,
    })

    expect(findings).toEqual([
      'frontend-app/src/shared/api/backendApi.js:RPC_ALLOWED_PAYLOAD_KEYS',
      'frontend-app/src/shared/api/backendApi.js:THREAD_START_ALLOWED_KEYS',
    ])
  })

  it('scans the runtime payload builder source for hardcoded payload guards', async () => {
    const builderPath = 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js'
    const builderSource = await readFile(join(REPO_ROOT, builderPath), 'utf8')
    const repoRoot = await createShadowRepo({
      [builderPath]: `const THREAD_START_ALLOWED_KEYS = new Set(['cwd'])\n${builderSource}`,
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.hardcodedPayloadGuardFindings).toEqual([
      `${builderPath}:THREAD_START_ALLOWED_KEYS`,
    ])
  })

  it('does not duplicate missing registry keys as missing response policies', async () => {
    const repoRoot = await createRuntimeDriftFixture()
    try {
      await writeFile(
        join(repoRoot, 'frontend-app/src/shared/api/backend/backendRpcMethods.js'),
        [
          'export const RPC_METHODS = Object.freeze({',
          "  THREAD_START: 'thread/start',",
          "  RESPONSELESS_RPC: 'responseless/rpc',",
          '})',
        ].join('\n'),
        'utf8',
      )
      await writeFile(
        join(repoRoot, 'frontend-app/src/shared/api/backendApi.contractMatrix.js'),
        [
          'function contract() {}',
          'export const RPC_CONTRACT_REGISTRY = Object.freeze({',
          "  THREAD_START: contract('THREAD_START', 'startThread', 'P1', 'thread', [], [], false, { responseValidator: 'threadStartResponse' }),",
          '})',
        ].join('\n'),
        'utf8',
      )

      const report = await auditRpcContracts({ repoRoot })

      expect(report.responseContractStrategies).toContainEqual({
        key: 'RESPONSELESS_RPC',
        method: 'responseless/rpc',
        matrixPolicy: '',
        frontendValidator: false,
      })
      expect(report.missingRegistryKeys).toContain('RESPONSELESS_RPC')
      expect(report.missingResponsePolicies).toEqual([])
    } finally {
      await rm(repoRoot, { recursive: true, force: true })
    }
  })

  it('parses RPC methods and contract registry entries from AST fixtures', () => {
    const rpcMethodsSource = `
      export const RPC_METHODS = Object.freeze({
        THREAD_START: 'thread/start',
        TURN_START: 'turn/start',
      })
    `
    const contractMatrixSource = `
      const TESTS = Object.freeze({ API: 'api.test.js' })
      function contract() {}
      export const RPC_CONTRACT_REGISTRY = Object.freeze({
        THREAD_START: contract('THREAD_START', RPC_METHODS.THREAD_START, 'startThread', 'P0', 'thread', [TESTS.API], ['runtime lifecycle start'], false, { responseValidator: 'threadStartResponse' }),
        TURN_START: contract('TURN_START', RPC_METHODS.TURN_START, 'startTurn', 'P0', 'turn', [TESTS.API], ['runtime lifecycle start'], false, { responsePolicy: ${UNUSED_POLICY} }),
      })
    `

    expect(parseRpcMethodsForTest(rpcMethodsSource)).toEqual([
      { key: 'THREAD_START', method: 'thread/start' },
      { key: 'TURN_START', method: 'turn/start' },
    ])
    expect(parseContractMatrixForTest(contractMatrixSource)).toEqual([
      {
        key: 'THREAD_START',
        declaredKey: 'THREAD_START',
        method: '',
        methodReferenceKey: 'THREAD_START',
        facade: 'startThread',
        level: 'P0',
        responseValidator: 'threadStartResponse',
        responsePolicy: null,
      },
      {
        key: 'TURN_START',
        declaredKey: 'TURN_START',
        method: '',
        methodReferenceKey: 'TURN_START',
        facade: 'startTurn',
        level: 'P0',
        responseValidator: '',
        responsePolicy: {
          kind: 'unused',
          productionScanRoots: ['frontend-app/src'],
          excludedGlobs: [],
        },
      },
    ])
  })

  it('uses the production RPC method source and reports missing and mismatched matrix entries', async () => {
    const matrixPath = 'frontend-app/src/shared/api/backendApi.contractMatrix.js'
    const productionMethodsPath = 'frontend-app/src/shared/api/backend/backendRpcMethods.js'
    const matrixSource = await readFile(join(REPO_ROOT, matrixPath), 'utf8')
    const repoRoot = await createShadowRepo({
      'frontend-app/src/shared/api/backendApi.js': `
        export { RPC_METHODS } from './backend/backendRpcMethods.js'
        function threadStartPayload() { return {} }
        function turnStartPayload() { return {} }
      `,
      [productionMethodsPath]: (await readFile(join(REPO_ROOT, productionMethodsPath), 'utf8'))
        .replace("CONFIG_READ: 'config/read'", "CONFIG_READ: 'config/read-mismatch'"),
      [matrixPath]: matrixSource
        .replace(/\n\s*CONFIG_LSP_PROMPT_HINT_READ: contract\([^\n]+/, '')
        .replace('RPC_METHODS.CONFIG_READ', "'config/read'"),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.missingRegistryKeys).toContain('CONFIG_LSP_PROMPT_HINT_READ')
    expect(report.mismatchedRegistryMethods).toContainEqual({
      key: 'CONFIG_READ',
      registryMethod: 'config/read',
      rpcMethod: 'config/read-mismatch',
    })
  }, 30000)

  it.each([
    ['missing', '', 'must declare exactly one of responseValidator or responsePolicy'],
    ['blank validator', "{ responseValidator: '   ' }", 'responseValidator must be non-blank'],
    ['blank passthrough reason', "{ responsePassthroughReason: '   ' }", 'responsePassthroughReason is forbidden'],
    ['one-character passthrough reason', "{ responsePassthroughReason: 'x' }", 'responsePassthroughReason is forbidden'],
    ['blanket passthrough reason', "{ responsePassthroughReason: 'passthrough for all' }", 'responsePassthroughReason is forbidden'],
  ])('rejects P0/P1 %s response policy', (_label, options, reason) => {
    expect(() => parseContractMatrixForTest(shadowMatrix(options))).toThrow(
      `RPC_CONTRACT_REGISTRY.CONFIG_READ ${reason}`,
    )
  })

  it('reports a declared response validator that is absent from the runtime validator mapping', async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(
        "{ responseValidator: 'doesNotExist' }",
        { key: 'CONFIG_LSP_PROMPT_HINT_READ', facade: 'readLspPromptHint' },
      ),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.missingFrontendResponseValidators).toContainEqual({
      key: 'CONFIG_LSP_PROMPT_HINT_READ',
      method: 'config/lspPromptHint/read',
      responseValidator: 'doesNotExist',
      runtimeResponseValidator: 'lspPromptHintResponse',
    })
  }, 30000)

  it('reports an existing runtime validator replaced by a structured response policy', async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(
        `{ responsePolicy: ${UNUSED_POLICY} }`,
        { key: 'UI_STATE_GET', facade: 'getThreadState' },
      ),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.missingFrontendResponseValidators).toContainEqual({
      key: 'UI_STATE_GET',
      method: 'ui/state/get',
      responseValidator: '',
      runtimeResponseValidator: 'uiStateResponse',
    })
  }, 30000)

  it('reports a runtime validator key that is absent from the contract registry', async () => {
    const validatorPath = 'frontend-app/src/shared/api/backendResponseValidators.js'
    const validatorSource = await readFile(join(REPO_ROOT, validatorPath), 'utf8')
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`),
      [validatorPath]: validatorSource.replace(
        'return Object.freeze({',
        'return Object.freeze({\n    [methods.TOTALLY_UNKNOWN]: validateUIStateResponse,',
      ),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.missingFrontendResponseValidators).toContainEqual({
      key: 'TOTALLY_UNKNOWN',
      method: '',
      responseValidator: '',
      runtimeResponseValidator: 'uiStateResponse',
    })
  }, 30000)

  it('reports a passthrough facade that cannot be traced to a real backend API export', async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`, { facade: 'totallyFake' }),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidFacadeLocators).toContainEqual({
      key: 'CONFIG_READ',
      facade: 'totallyFake',
      locator: 'frontend-app/src/shared/api/backendApi.js',
    })
  }, 30000)

  it('reports a real backend facade that belongs to a different RPC key', async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`, { facade: 'readBuiltinTools' }),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidFacadeLocators).toContainEqual({
      key: 'CONFIG_READ',
      facade: 'readBuiltinTools',
      locator: 'frontend-app/src/shared/api/backendApi.js',
    })
  }, 30000)

  it('reports a service facade whose downstream destructure outlives its factory member', async () => {
    const servicePath = 'frontend-app/src/pages/prompts/services/promptPageService.js'
    const serviceSource = await readFile(join(REPO_ROOT, servicePath), 'utf8')
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`, {
        key: 'PROMPT_ASSETS_LIST',
        facade: 'promptPageService.listPromptAssets',
      }),
      [servicePath]: serviceSource.replace(/    listPromptAssets\(payload\) \{\n[\s\S]*?\n    \},\n/, ''),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidFacadeLocators).toContainEqual({
      key: 'PROMPT_ASSETS_LIST',
      facade: 'promptPageService.listPromptAssets',
      locator: servicePath,
    })
  }, 30000)

  it('reports payload registry drift when frontend builders miss Go fields', () => {
    const drift = collectPayloadRegistryDrift(
      new Map([['thread/start', ['cwd', 'provider', 'new_go_field']]]),
      new Map([['thread/start', ['cwd', 'provider']]]),
    )

    expect(drift).toEqual([{
      method: 'thread/start',
      missingFrontendKeys: ['new_go_field'],
      extraFrontendKeys: [],
    }])
  })

  it('extracts consumed payload keys from facade builders instead of static key lists', () => {
    const source = `
      function threadStartPayload(params) {
        const unused = { ...params }
        // takePayloadField(unused, 'ghost_comment')
        const probe = "takePayloadField(unused, 'ghost_string')"
        const providerRaw = takePayloadField(unused, 'provider')
        const request = cleanObject({
          cwd: takePayloadField(unused, 'cwd'),
        })
        if (probe) {
          return request
        }
        return request
      }
      function turnStartPayload(params) {
        const unused = { ...params }
        const input = takePayloadField(unused, 'input')
        return takePayloadFields(unused, [
          'threadId',
          'thread_id',
        ])
      }
      function turnInterruptPayload(params) {
        const unused = { ...params }
        return takePayloadFields(unused, [
          'expectedTurnId',
          'requestId',
          'threadId',
        ])
      }
    `

    expect(collectFrontendPayloadKeysFromSource(source).get('thread/start')).toEqual(['cwd', 'provider'])
    expect(collectFrontendPayloadKeysFromSource(source).get('turn/start')).toEqual(['input', 'thread_id', 'threadId'])
    expect(collectFrontendPayloadKeysFromSource(source).get('turn/interrupt')).toEqual(['expectedTurnId', 'requestId', 'threadId'])
  })

  it('rejects generated response passthrough prose', () => {
    expect(() => parseContractMatrixForTest(shadowMatrix(`{
      responsePassthroughReason: 'CONFIG_READ response is consumed unchanged by readConfig',
    }`))).toThrow('RPC_CONTRACT_REGISTRY.CONFIG_READ responsePassthroughReason is forbidden')
  })

  it.each([
    ['missing', ''],
    ['extra metadata', `{ responsePolicy: {
      kind: 'ignored-result',
      consumer: { path: 'frontend-app/src/pages/audit-fixture/consumer.js', symbol: 'loadConfig' },
      regressionTest: { path: 'frontend-app/src/pages/audit-fixture/consumer.test.js', symbol: 'keeps config result irrelevant' },
      note: 'prose',
    } }`],
    ['duplicate metadata', `{ responsePolicy: {
      kind: 'ignored-result',
      consumer: { path: 'frontend-app/src/pages/audit-fixture/consumer.js', symbol: 'loadConfig' },
      regressionTest: { path: 'frontend-app/src/pages/audit-fixture/consumer.test.js', symbol: 'keeps config result irrelevant' },
      kind: 'ignored-result',
    } }`],
    ['computed metadata', `{ responsePolicy: {
      kind: 'ignored-result',
      consumer: { path: 'frontend-app/src/pages/audit-fixture/consumer.js', symbol: 'loadConfig' },
      regressionTest: { path: 'frontend-app/src/pages/audit-fixture/consumer.test.js', symbol: 'keeps config result irrelevant' },
      ['note']: 'prose',
    } }`],
    ['spread metadata', `{ responsePolicy: { ...policy } }`],
  ])('rejects %s structured response policy metadata', (_label, policy) => {
    expect(() => parseContractMatrixForTest(shadowMatrix(policy))).toThrow('RPC_CONTRACT_REGISTRY.CONFIG_READ')
  })

  it('parses a strict published-callback outcome target', () => {
    const [entry] = parseContractMatrixForTest(shadowMatrix(`{
      responsePolicy: ${ignoredResultPolicy({ outcomeTarget: ['notices', 'showTaskNotice'] })}
    }`))

    expect(entry.responsePolicy.outcome).toEqual({
      kind: 'published-callback',
      target: ['notices', 'showTaskNotice'],
    })
  })

  it.each([
    ['empty target', "{ kind: 'published-callback', target: [] }"],
    ['blank target part', "{ kind: 'published-callback', target: ['notices', ' '] }"],
    ['bad kind', "{ kind: 'rendered-text', target: ['notices', 'showTaskNotice'] }"],
    ['extra field', "{ kind: 'published-callback', target: ['notices', 'showTaskNotice'], note: 'x' }"],
    ['computed field', "{ kind: 'published-callback', ['target']: ['notices'] }"],
    ['spread field', "{ ...outcome }"],
  ])('rejects published-callback outcome with %s', (_label, outcome) => {
    const policy = ignoredResultPolicy().replace(
      "regressionTest:",
      `outcome: ${outcome}, regressionTest:`,
    )
    expect(() => parseContractMatrixForTest(shadowMatrix(`{ responsePolicy: ${policy} }`)))
      .toThrow('RPC_CONTRACT_REGISTRY.CONFIG_READ responsePolicy.outcome')
  })

  it.each([
    ['blank path', ignoredResultPolicy({ consumerPath: '   ' }), 'consumer', 'path must be non-blank'],
    ['escaping path', ignoredResultPolicy({ consumerPath: '../consumer.js' }), 'consumer', 'path must be normalized and repository-confined'],
    ['nonexistent path', ignoredResultPolicy({ consumerPath: 'frontend-app/src/pages/missing.js' }), 'consumer', 'file does not exist'],
    ['nonexistent symbol', ignoredResultPolicy({ consumerSymbol: 'missingConsumer' }), 'consumer', 'symbol was not found'],
    ['wrong test file kind', ignoredResultPolicy({ regressionPath: CONSUMER_PATH }), 'regressionTest', 'path must identify a JavaScript test file'],
  ])('rejects invalid response policy locator evidence: %s', async (_label, policy, field, reason) => {
    const repoRoot = await createPolicyShadow({
      policy,
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { await readConfig() }
      `,
      regression: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual(expect.arrayContaining([
      expect.objectContaining({
        key: 'CONFIG_READ',
        kind: 'ignored-result',
        field,
        reason,
      }),
    ]))
  }, 30000)

  it.each([
    ['nested-only declaration', `
      import { readConfig } from '../../shared/api/backendApi.js'
      function wrapper() { async function loadConfig() { await readConfig() } }
    `],
    ['unexported declaration', `
      import { readConfig } from '../../shared/api/backendApi.js'
      async function loadConfig() { await readConfig() }
    `],
    ['duplicate module-level declarations', `
      import { readConfig } from '../../shared/api/backendApi.js'
      export var loadConfig = async () => { await readConfig() }
      var loadConfig = async () => { await readConfig() }
    `],
    ['object-member coincidence', `
      import { readConfig } from '../../shared/api/backendApi.js'
      export const unrelated = { async loadConfig() { await readConfig() } }
    `],
  ])('rejects a locator backed by a %s', async (_label, consumer) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer,
      regression: ignoredResultRegression(),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
      reason: 'symbol was not found',
    }))
  }, 30000)

  it('resolves one explicit module-private consumer without weakening exported locator defaults', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() { await readConfig() }
      `,
      regression: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('resolves one explicit nested module-private consumer and proves only its own body', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function wrapper() {
          async function loadConfig() { await readConfig() }
          function observedSibling() { return readConfig() }
          return { loadConfig, observedSibling }
        }
      `,
      regression: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('resolves an exact hook-bound module-private callback and proves only its callback body', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { useCallback } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        export function wrapper() {
          const loadConfig = useCallback(async () => { await readConfig() }, [])
          const observedSibling = useCallback(async () => readConfig(), [])
          return { loadConfig, observedSibling }
        }
      `,
      regression: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['missing private symbol', 'missingConsumer', `async function loadConfig() { await readConfig() }`, 'symbol was not found'],
    ['duplicate private binding', 'loadConfig', `
      async function loadConfig() { await readConfig() }
      async function outer() { async function loadConfig() { await readConfig() } }
    `, 'symbol was not found'],
    ['multiple ignored private calls', 'loadConfig', `async function loadConfig() { await readConfig(); await readConfig() }`, 'consumer calls the facade more than once'],
    ['assigned private result', 'loadConfig', `async function loadConfig() { const result = await readConfig(); return result }`, 'consumer reads the RPC result'],
    ['returned private result', 'loadConfig', `async function loadConfig() { return readConfig() }`, 'consumer reads the RPC result'],
    ['inspected private result', 'loadConfig', `async function loadConfig() { const result = await readConfig(); if (result.ok) consume() }`, 'consumer reads the RPC result'],
    ['destructured private result', 'loadConfig', `async function loadConfig() { const { ok } = await readConfig(); return ok }`, 'consumer reads the RPC result'],
    ['passed private result', 'loadConfig', `async function loadConfig() { consume(await readConfig()) }`, 'consumer reads the RPC result'],
    ['multiple private calls with one observed', 'loadConfig', `async function loadConfig() { await readConfig(); return readConfig() }`, 'consumer reads the RPC result'],
  ])('rejects an explicit module-private locator with %s', async (_label, consumerSymbol, body, reason) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerSymbol, consumerVisibility: 'module-private' }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        ${body}
      `,
      regression: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
      reason,
    }))
  }, 30000)

  it('rejects an invalid response locator visibility marker', () => {
    expect(() => parseContractMatrixForTest(shadowMatrix(`{
      responsePolicy: ${ignoredResultPolicy({ consumerVisibility: 'private-ish' })}
    }`))).toThrow('responsePolicy.consumer.visibility')
  })

  it.each([
    ['exported function', `
      import { readConfig } from '../../shared/api/backendApi.js'
      function wrapper() { async function loadConfig() { return readConfig() } }
      export async function loadConfig() { await readConfig() }
    `],
    ['exported const', `
      import { readConfig } from '../../shared/api/backendApi.js'
      const wrapper = { loadConfig: () => readConfig() }
      export const loadConfig = async () => { await readConfig() }
    `],
  ])('resolves the exact %s binding for a locator and regression import', async (_label, consumer) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer,
      regression: ignoredResultRegression(),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('rejects unused policy with a production reference', async () => {
    const repoRoot = await createPolicyShadow({
      policy: UNUSED_POLICY,
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { return readConfig() }
      `,
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual({
      key: 'CONFIG_READ',
      kind: 'unused',
      field: 'productionScanRoots',
      path: CONSUMER_PATH,
      symbol: 'readConfig',
      reason: 'production facade reference exists',
    })
  }, 30000)

  it('accepts result-handled proof for the real consumer, envelope handler, and warning regression', async () => {
    const repoRoot = await createPolicyShadow({
      policy: resultHandledPolicy(),
      consumer: resultHandledConsumer(),
      handler: resultHandler(),
      regression: resultHandledRegression(),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('accepts the real TURN_INTERRUPT injection, private handler, and runtime regression shape', async () => {
    const repoRoot = await createRealResultHandledShadow()
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('rejects TURN_INTERRUPT when strict success-envelope validation is removed', async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) => source.replace(
        "      if (action === 'thread.interrupt') validateInterruptSuccessResponse(result, request);\n",
        '',
      ),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'consumer',
    }))
  }, 15000)

  it('accepts one exact helper hop from activeThreadRPC to runActiveThreadRPC', async () => {
    const repoRoot = await createRealResultHandledShadow({})
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['a shadow runActiveThreadRPC binding', (source) => source.replace(
      '        Object.assign(runtime, { activeThreadRPC })',
      '        { const runActiveThreadRPC = async () => ({ ok: true }) }\n        Object.assign(runtime, { activeThreadRPC })',
    )],
    ['a computed helper call', (source) => source.replace(
      'const outcome = await runActiveThreadRPC(action, rpc)',
      "const outcome = await ({ runActiveThreadRPC })['runActiveThreadRPC'](action, rpc)",
    )],
    ['a nested handler-call decoy', (source) => source.replace(
      'const result = await rpc({})',
      'const decoy = () => notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })\n          const result = await rpc({})',
    )],
    ['two awaited rpc calls in the helper', (source) => source.replace(
      'const result = await rpc({})',
      'const result = await rpc({})\n          await rpc({})',
    )],
    ['two awaited helper calls in the wrapper', (source) => source.replace(
      'const outcome = await runActiveThreadRPC(action, rpc)',
      'const outcome = await runActiveThreadRPC(action, rpc)\n          await runActiveThreadRPC(action, rpc)',
    )],
    ['swapped wrapper arguments', (source) => source.replace(
      'const outcome = await runActiveThreadRPC(action, rpc)',
      'const outcome = await runActiveThreadRPC(rpc, action)',
    )],
    ['the wrong handler action argument', (source) => source.replace(
      'notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })',
      'notifyThreadActionFailure({ action: rpc, addWarning, notifyAction, result: result, threadId })',
    )],
    ['a fabricated handler result', (source) => source
      .replace(
        'const result = await rpc({})',
        'const result = await rpc({})\n          const fabricatedResult = { ok: false }',
      )
      .replace(
        'notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })',
        'notifyThreadActionFailure({ action, addWarning, notifyAction, result: fabricatedResult, threadId })',
      )],
    ['an ignored helper outcome', (source) => source.replace(
      '          if (!outcome.ok) return false\n',
      '',
    )],
  ])('rejects a single-helper TURN_INTERRUPT proof with %s', async (_label, mutateRuntime) => {
    const repoRoot = await createMutatedSingleHelperResultHandledShadow(mutateRuntime)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'consumer',
    }))
  }, 30000)

  it.each([
    ['the complete result flow inside if (false)', (source) => source.replace(
      `            const result = await rpc({})
            if (notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })) return { ok: false, threadId, result }
            return { ok: true, threadId, result }`,
      `            if (false) {
              const result = await rpc({})
              if (notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })) return { ok: false, threadId, result }
              return { ok: true, threadId, result }
            }`,
    )],
    ['the complete result flow inside an arbitrary branch', (source) => source.replace(
      `            const result = await rpc({})
            if (notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })) return { ok: false, threadId, result }
            return { ok: true, threadId, result }`,
      `            if (rpc.enabled) {
              const result = await rpc({})
              if (notifyThreadActionFailure({ action, addWarning, notifyAction, result: result, threadId })) return { ok: false, threadId, result }
              return { ok: true, threadId, result }
            }`,
    )],
    ['an unconditional return before await rpc', (source) => source.replace(
      '            const result = await rpc({})',
      '            return null\n            const result = await rpc({})',
    )],
    ['an unconditional throw before await rpc', (source) => source.replace(
      '            const result = await rpc({})',
      "            throw new Error('unreachable result flow')\n            const result = await rpc({})",
    )],
  ])('rejects an unreachable single-helper TURN_INTERRUPT proof with %s', async (_label, mutateRuntime) => {
    const repoRoot = await createMutatedSingleHelperResultHandledShadow(mutateRuntime)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'consumer',
    }))
  }, 30000)

  it.each([
    ['a labeled return before result', (source) => source.replace(
      '            const result = await rpc({})',
      '            unreachable: return null\n            const result = await rpc({})',
    )],
    ['a bare nested block with a return before result', (source) => source.replace(
      '            const result = await rpc({})',
      '            { return null }\n            const result = await rpc({})',
    )],
    ['an if (true) return before result', (source) => source.replace(
      '            const result = await rpc({})',
      '            if (true) return null\n            const result = await rpc({})',
    )],
    ['a nested try-finally return before result', (source) => source.replace(
      '            const result = await rpc({})',
      '            try {} finally { return null }\n            const result = await rpc({})',
    )],
    ['a top-level finally return overriding the result flow', (source) => source.replace(
      `          } catch (error) {
            return { ok: false, threadId, result: null }
          }`,
      `          } catch (error) {
            return { ok: false, threadId, result: null }
          } finally {
            return null
          }`,
    )],
    ['a top-level finally without a return', (source) => source.replace(
      `          } catch (error) {
            return { ok: false, threadId, result: null }
          }`,
      `          } catch (error) {
            return { ok: false, threadId, result: null }
          } finally {
            notifyAction('cleanup', 'info')
          }`,
    )],
  ])('rejects a noncanonical single-helper try proof with %s', async (_label, mutateRuntime) => {
    const repoRoot = await createMutatedSingleHelperResultHandledShadow(mutateRuntime)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'consumer',
    }))
  }, 30000)

  it.each([
    ['an unconditional return between the no-thread guard and try', (source) => source.replace(
      '          try {',
      '          return null\n          try {',
    )],
    ['an unconditional throw between the no-thread guard and try', (source) => source.replace(
      '          try {',
      "          throw new Error('unreachable try')\n          try {",
    )],
    ['an if (true) return between the no-thread guard and try', (source) => source.replace(
      '          try {',
      '          if (true) return null\n          try {',
    )],
    ['an extra statement after the result try', (source) => source.replace(
      `          } catch (error) {
            return { ok: false, threadId, result: null }
          }
        }
        const activeThreadRPC`,
      `          } catch (error) {
            return { ok: false, threadId, result: null }
          }
          notifyAction('late result', 'info')
        }
        const activeThreadRPC`,
    )],
  ])('rejects a noncanonical single-helper body with %s', async (_label, mutateRuntime) => {
    const repoRoot = await createMutatedSingleHelperResultHandledShadow(mutateRuntime)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'consumer',
    }))
  }, 30000)

  it.each([
    ['direct warning seeded before awaited runtime call', {
      regressionBeforeAwait: "runtime.notifyAction('turn already completed', 'warning')",
    }],
    ['indirect warning seeded before awaited runtime call', {
      regressionDefinitions: "function seedWarning(runtime) { runtime.addWarning('warn', 'thread.interrupt.failed', { error: 'turn already completed' }) }",
      regressionBeforeAwait: 'seedWarning(runtime)',
    }],
  ])('rejects TURN_INTERRUPT regression bypass: %s', async (_label, overrides) => {
    const repoRoot = await createRealResultHandledShadow(overrides)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({ key: 'TURN_INTERRUPT', field: 'regressionTest' }),
    )
  }, 30000)

  it('rejects a decoy runtime flow when the exact runActiveThreadRPC helper passes response instead of result', async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) => source
        .replace(
          'if (notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })) return { ok: false, threadId, result };',
          'if (notifyThreadActionFailure({ action, addWarning, notifyAction, response: result, threadId })) return { ok: false, threadId, result };',
        )
        .concat(`
          async function decoyRuntimeFlow(action, rpc, addWarning, notifyAction, threadId) {
            const result = await rpc({})
            return notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })
          }
        `),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'consumer',
    }))
  }, 30000)

  it('rejects a nested handler-call decoy inside the exact runActiveThreadRPC helper', async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) => source
        .replace(
          'if (notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId })) return { ok: false, threadId, result };',
          'const decoy = () => notifyThreadActionFailure({ action, addWarning, notifyAction, result, threadId });\n      if (notifyThreadActionFailure({ action, addWarning, notifyAction, response: result, threadId })) return { ok: false, threadId, result };',
        ),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'consumer',
    }))
  }, 30000)

  it('rejects a fabricated interrupt failure helper return after dead derived-message evidence', async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) => source
        .replace('if (message) return message;', 'if (false) return message;')
        .replace(
          "throw new Error('thread.interrupt ok:false response message is required');",
          "return 'fabricated interrupt failure';",
        ),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'handler',
    }))
  }, 30000)

  it('rejects a TURN_INTERRUPT handler predicate weakened by an always-true disjunction', async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      runtime: (source) => source.replace(
        "action === 'thread.interrupt' && result?.ok === false",
        "((action === 'thread.interrupt' && result?.ok === false) || true)",
      ),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'handler',
    }))
  }, 30000)

  it.each([
    ['a top-level throw before the return', "throw new Error('unreachable injection');"],
    ['an infinite loop before the return', 'while (true) {}'],
  ])('rejects an unreachable real TURN_INTERRUPT injection after %s', async (_label, blocker) => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      injection: (source) => source.replace(
        'function createActiveThreadActions(runtime, deps) {\n  return {',
        `function createActiveThreadActions(runtime, deps) {\n  ${blocker}\n  return {`,
      ),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'consumer',
    }))
  }, 30000)

  it.each([
    [
      'a duplicate later property',
      "    interruptActiveThread: () => runtime.activeThreadRPC('thread.force_complete', forceCompleteTurn),",
    ],
    [
      'a later spread property',
      "    ...{ interruptActiveThread: () => runtime.activeThreadRPC('thread.force_complete', forceCompleteTurn) },",
    ],
  ])('rejects a real TURN_INTERRUPT injection overridden by %s', async (_label, override) => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      injection: (source) => source.replace(
        "    interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn),",
        `    interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn),\n${override}`,
      ),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'consumer',
    }))
  }, 30000)

  it('rejects warning seeding after the first post-matcher rpc assertion', async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      regression: (source) => source
        .replace(
          "import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';",
          "import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';\nfunction seedWarning(runtime) { runtime.notifyAction('seeded', 'warning'); }",
        )
        .replaceAll(
          "      source: 'ui_stop',\n    });\n    expect(runtime.notifyAction)",
          "      source: 'ui_stop',\n    });\n    seedWarning(runtime);\n    expect(runtime.notifyAction)",
        ),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'regressionTest',
    }))
  }, 30000)

  it('rejects a warning-producing variable declaration before the awaited runtime assertion', async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      regression: (source) => source.replace(
        "it('reports interrupt ok:false as warning without showing success', async () => {\n    const runtime = createRuntime();\n    const deps = createDeps();\n    const rpc = vi.fn().mockResolvedValue({ ok: false, error: 'turn already completed' });\n    attachActiveThreadRpcRuntime(runtime, deps);\n\n    await expect(runtime.activeThreadRPC",
        "it('reports interrupt ok:false as warning without showing success', async () => {\n    const runtime = createRuntime();\n    const deps = createDeps();\n    const rpc = vi.fn().mockResolvedValue({ ok: false, error: 'turn already completed' });\n    attachActiveThreadRpcRuntime(runtime, deps);\n    const seeded = runtime.notifyAction('seeded', 'warning');\n\n    await expect(runtime.activeThreadRPC",
      ),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'regressionTest',
    }))
  }, 30000)

  it('rejects a post-await helper call that only nests expect in an argument', async () => {
    const repoRoot = await createMutatedRealResultHandledShadow({
      regression: (source) => source
        .replace(
          "import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';",
          "import { attachActiveThreadRpcRuntime } from './threadLifecycleRuntime.js';\nfunction seedWarning(runtime, proof) { runtime.notifyAction('seeded', 'warning'); return proof; }",
        )
        .replace(
          "await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false);",
          "await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false);\n    seedWarning(runtime, expect(true).toBe(true));",
        ),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'TURN_INTERRUPT',
      field: 'regressionTest',
    }))
  }, 30000)

  it.each([
    ['wrong injected facade', { facade: 'startThread' }],
    ['wrong action literal', { action: 'thread.force_complete' }],
    ['dead injection branch', { injectionBody: "if (false) return { interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn) }; return {}" }],
    ['conditional injection branch', { injectionBody: "if (runtime.enabled) return { interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn) }; return {}" }],
    ['loop-only injection', { injectionBody: "while (runtime.enabled) return { interruptActiveThread: () => runtime.activeThreadRPC('thread.interrupt', interruptTurn) }; return {}" }],
    ['nested callback injection', { injectionBody: "return { interruptActiveThread: () => () => runtime.activeThreadRPC('thread.interrupt', interruptTurn) }" }],
    ['different result property', { resultProperty: 'response' }],
    ['constant-true handler', { handlerCondition: 'true' }],
    ['constant-false handler', { handlerCondition: 'false' }],
    ['success-only handler', { handlerCondition: "action === 'thread.interrupt' && result?.ok === true" }],
    ['indirect warning helper', { warningBody: 'emitWarning(notifyAction, result.error)' }],
    ['unrelated info helper', {
      handlerDefinitions: 'function unrelatedMessage(result) { return result.info }',
      warningBody: "const message = unrelatedMessage(result); notifyAction(message, 'warning'); addWarning('warn', action, { error: message })",
    }],
    ['unproved helper between consumer and assertion', { regressionBetween: 'assertWarning(runtime)' }],
  ])('rejects unsound real TURN_INTERRUPT proof: %s', async (_label, overrides) => {
    const repoRoot = await createRealResultHandledShadow(overrides)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(
      expect.objectContaining({ key: 'TURN_INTERRUPT', kind: 'result-handled' }),
    )
  }, 30000)

  it('does not leak the runtime/private-handler exception to another result-handled key', async () => {
    const actual = {
      injection: await readFile(join(REPO_ROOT, REAL_INJECTION_PATH), 'utf8'),
      runtime: await readFile(join(REPO_ROOT, REAL_CONSUMER_PATH), 'utf8'),
      regression: await readFile(join(REPO_ROOT, REAL_REGRESSION_PATH), 'utf8'),
    }
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${realResultHandledPolicy()} }`, { key: 'CONFIG_READ', facade: 'readConfig' }),
      [REAL_INJECTION_PATH]: actual.injection,
      [REAL_CONSUMER_PATH]: actual.runtime,
      [REAL_REGRESSION_PATH]: actual.regression,
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
    }))
  }, 30000)

  it.each([
    ['wrong facade', { consumer: resultHandledConsumer({ facade: 'startThread' }) }],
    ['ignored facade result', { consumer: resultHandledConsumer({ ignored: true }) }],
    ['unrelated handler argument', { consumer: resultHandledConsumer({ argument: '{ ok: false }' }) }],
    ['shadowed handler import', { consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn(handleInterruptResult) {
        const result = await readConfig()
        return handleInterruptResult(result)
      }
    ` }],
    ['nested handler forwarding', { consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn() {
        const result = await readConfig()
        return () => handleInterruptResult(result)
      }
    ` }],
    ['block-shadowed result binding', { consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn() {
        const result = await readConfig()
        {
          const result = { ok: true }
          return handleInterruptResult(result)
        }
      }
    ` }],
    ['catch-parameter-shadowed result binding', { consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn() {
        try {
          const result = await readConfig()
          throw result
        } catch (result) {
          return handleInterruptResult(result)
        }
      }
    ` }],
    ['dead consumer path', { consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn() {
        if (false) {
          const result = await readConfig()
          return handleInterruptResult(result)
        }
      }
    ` }],
    ['one-sided handler branch', { consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn(flag) {
        const result = await readConfig()
        if (flag) return handleInterruptResult(result)
        return true
      }
    ` }],
    ['loop-only handler path', { consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      import { handleInterruptResult } from './resultHandler.js'
      export async function interruptTurn(flag) {
        const result = await readConfig()
        while (flag) return handleInterruptResult(result)
        return true
      }
    ` }],
    ['handler does not inspect outcome', { handler: resultHandler({ body: 'return true' }) }],
    ['dead handler branch', { handler: resultHandler({ body: 'if (false && !result.ok) console.warn(result.error)' }) }],
    ['nested callback handling', { handler: resultHandler({ body: 'return () => { if (!result.ok) console.warn(result.error) }' }) }],
    ['wrong mocked facade', { regression: resultHandledRegression({ mockFacade: 'startThread' }) }],
    ['generic assertion', { regression: resultHandledRegression({ assertion: 'expect(true).toBe(true)' }) }],
    ['handler test mismatch', { regression: resultHandledRegression({
      assertion: "expect(warn).toHaveBeenCalledWith('different warning')",
    }) }],
    ['transport rejection', { regression: resultHandledRegression({
      response: "Promise.reject(new Error('offline'))",
      assertion: "await expect(interruptTurn()).rejects.toThrow('offline')",
    }) }],
    ['synthetic warning', { regression: resultHandledRegression({
      beforeAssertion: "console.warn('interrupt denied')",
    }) }],
    ['direct warning handler invocation', { regression: resultHandledRegression({
      extraImports: "import { handleInterruptResult } from './resultHandler.js'",
      beforeAssertion: "handleInterruptResult({ ok: false, error: 'interrupt denied' })",
    }) }],
    ['dead consumer invocation', { regression: resultHandledRegression({
      consumerInvocation: 'if (false) await interruptTurn()',
      beforeAssertion: "console.warn('interrupt denied')",
    }) }],
    ['unawaited consumer invocation', { regression: resultHandledRegression({
      consumerInvocation: 'interruptTurn()',
      beforeAssertion: "console.warn('interrupt denied')",
    }) }],
  ])('rejects unsound result-handled proof: %s', async (_label, overrides) => {
    const repoRoot = await createPolicyShadow({
      policy: resultHandledPolicy(),
      consumer: resultHandledConsumer(),
      handler: resultHandler(),
      regression: resultHandledRegression(),
      ...overrides,
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([
      expect.objectContaining({ key: 'CONFIG_READ', kind: 'result-handled' }),
    ])
  }, 30000)

  it('rejects result-handled metadata with a mismatched handler locator', async () => {
    const repoRoot = await createPolicyShadow({
      policy: resultHandledPolicy({ handlerSymbol: 'otherHandler' }),
      consumer: resultHandledConsumer(),
      handler: resultHandler(),
      regression: resultHandledRegression(),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'result-handled',
      field: 'handler',
    }))
  }, 30000)

  it('rejects ignored-result policy when the consumer reads the result', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() {
          const result = await readConfig()
          return result.value
        }
      `,
      regression: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'ignored-result',
      field: 'consumer',
      reason: 'consumer reads the RPC result',
    }))
  }, 30000)

  it('accepts ignored-result policy with an unobserved awaited call', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { await readConfig() }
      `,
      regression: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('accepts an exact published-callback action and direct regression proof', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ outcomeTarget: ['notices', 'showTaskNotice'] }),
      consumer: publishedCallbackConsumer(),
      regression: publishedCallbackRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['publisher before facade', `notices.showTaskNotice('saved'); await facade.readConfig({ cwd: '/repo' })`],
    ['publisher in catch', `try { await facade.readConfig({ cwd: '/repo' }) } catch { notices.showTaskNotice('saved') }`],
    ['publisher in finally', `try { await facade.readConfig({ cwd: '/repo' }) } finally { notices.showTaskNotice('saved') }`],
    ['publisher in nested sibling', `await facade.readConfig({ cwd: '/repo' }); const later = () => notices.showTaskNotice('saved'); return later`],
    ['computed publisher', `await facade.readConfig({ cwd: '/repo' }); notices['showTaskNotice']('saved')`],
    ['optional publisher', `await facade.readConfig({ cwd: '/repo' }); notices?.showTaskNotice('saved')`],
    ['dynamic publisher object', `await facade.readConfig({ cwd: '/repo' }); const sink = notices; sink.showTaskNotice('saved')`],
    ['ambiguous publisher', `await facade.readConfig({ cwd: '/repo' }); notices.showTaskNotice('saved'); notices.showTaskNotice('again')`],
    ['block-shadowed publisher root', `
      await facade.readConfig({ cwd: '/repo' })
      { const notices = { showTaskNotice() {} }; notices.showTaskNotice('saved') }
    `],
    ['block-shadowed facade root', `
      { const facade = { readConfig: async () => undefined }; await facade.readConfig({ cwd: '/repo' }) }
      notices.showTaskNotice('saved')
    `],
  ])('rejects published-callback production proof with %s', async (_label, body) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ outcomeTarget: ['notices', 'showTaskNotice'] }),
      consumer: publishedCallbackConsumer(body),
      regression: publishedCallbackRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
      reason: 'consumer lacks the exact post-RPC published callback outcome',
    }))
  }, 30000)

  it.each([
    ['wrong import', { importStatement: "import { loadConfig as wrong } from './consumer.js'" }],
    ['shadowed consumer', { beforeCall: 'const loadConfig = async () => undefined' }],
    ['rejected RPC mock', { mockMethod: 'mockRejectedValue' }],
    ['non-malformed response', { response: "{ ok: true }" }],
    ['malformed sentinel on an unrelated same-named spy', {
      response: "{ ok: true }",
      beforeCall: "const unrelated = { readConfig: vi.fn().mockResolvedValue({ malformed: 'other-sentinel' }) }",
    }],
    ['ambiguous spread facade source', {
      contextSetup: `
        const first = { facade: { readConfig: vi.fn().mockResolvedValue({ malformed: 'stale-sentinel' }) }, notices: { showTaskNotice: vi.fn() } }
        const second = { facade: { readConfig: vi.fn().mockResolvedValue({ ok: true }) }, notices: { showTaskNotice: vi.fn() } }
        const ctx = { ...first, ...second }
      `,
      assertions: `
        expect(first.facade.readConfig).toHaveBeenCalledWith({ cwd: '/repo' })
        expect(result).toBeUndefined()
        expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')
      `,
    }],
    ['publisher assertion before action', {
      beforeCall: "expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')",
      assertions: `
        expect(ctx.facade.readConfig).toHaveBeenCalledWith({ cwd: '/repo' })
        expect(result).toBeUndefined()
      `,
    }],
    ['unrelated publisher spy', { assertions: `
      expect(ctx.facade.readConfig).toHaveBeenCalledWith({ cwd: '/repo' })
      expect(result).toBeUndefined()
      expect(ctx.other.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')
    ` }],
    ['no exact facade args assertion', { assertions: `
      expect(ctx.facade.readConfig).toHaveBeenCalled()
      expect(result).toBeUndefined()
      expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')
    ` }],
    ['no undefined result assertion', { assertions: `
      expect(ctx.facade.readConfig).toHaveBeenCalledWith({ cwd: '/repo' })
      expect(ctx.notices.showTaskNotice).toHaveBeenLastCalledWith('saved', 'config')
    ` }],
  ])('rejects published-callback regression proof with %s', async (_label, overrides) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ outcomeTarget: ['notices', 'showTaskNotice'] }),
      consumer: publishedCallbackConsumer(),
      regression: publishedCallbackRegression(overrides),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('rejects consumer-validated policy without executable shape proof', async () => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) { return value.value }
        export async function loadConfig() {
          const result = await readConfig()
          assertConfigShape(result)
          return result.value
        }
      `,
      regression: consumerValidatedRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'consumer-validated',
      field: 'shape',
      reason: 'shape symbol lacks executable narrowing',
    }))
  }, 30000)

  it('accepts consumer-validated policy with dominating executable shape proof', async () => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) {
          if (!value || typeof value !== 'object' || typeof value.value !== 'string') {
            throw new TypeError('invalid config')
          }
        }
        export async function loadConfig() {
          const result = await readConfig()
          assertConfigShape(result)
          return result.value
        }
      `,
      regression: consumerValidatedRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['callback-local result', `queueMicrotask(async () => { const result = await readConfig(); consume(result.value) }); const result = {}; assertConfigShape(result)`],
    ['nested function result', `async function nested() { const result = await readConfig(); consume(result.value) }; const result = {}; assertConfigShape(result)`],
    ['callback-local validator', `const result = await readConfig(); queueMicrotask(() => assertConfigShape(result)); consume(result.value)`],
  ])('rejects consumer validation crossing lexical scope: %s', async (_label, body) => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) {
          if (!value || typeof value !== 'object' || typeof value.value !== 'string') throw new TypeError('invalid config')
        }
        function consume(value) { return value }
        export async function loadConfig() { ${body} }
      `,
      regression: consumerValidatedRegression(),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'consumer-validated',
      field: 'shape',
    }))
  }, 30000)

  it.each([
    ignoredResultPolicy(),
    consumerValidatedPolicy(),
    UNUSED_POLICY,
  ])('does not allow a response policy to replace a runtime validator', async (responsePolicy) => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${responsePolicy} }`).replaceAll('CONFIG_READ', 'UI_STATE_GET')
        .replace("'readConfig'", "'getThreadState'"),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.missingFrontendResponseValidators).toContainEqual({
      key: 'UI_STATE_GET',
      method: 'ui/state/get',
      responseValidator: '',
      runtimeResponseValidator: 'uiStateResponse',
    })
  }, 30000)

  it('rejects a response validator and response policy union', () => {
    expect(() => parseContractMatrixForTest(shadowMatrix(`{
      responseValidator: 'uiStateResponse',
      responsePolicy: ${UNUSED_POLICY},
    }`))).toThrow('RPC_CONTRACT_REGISTRY.CONFIG_READ must declare exactly one of responseValidator or responsePolicy')
  })

  it('handles export specifier variants before reading local bindings', async () => {
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${UNUSED_POLICY} }`),
      'frontend-app/src/shared/api/backendApi.js': `
        export * as backendMethods from './backend/backendRpcMethods.js'
        export { default as createBackendApi } from './backend/backendApiFactoryThread.js'
        export { RPC_METHODS } from './backend/backendRpcMethods.js'
      `,
    })

    await expect(auditRpcContracts({ repoRoot })).resolves.toEqual(expect.objectContaining({
      invalidResponsePolicyEvidence: [],
    }))
  }, 30000)

  it('rejects a response-policy locator file that is a symlink escaping the repository', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      regression: ignoredResultRegression(),
    })
    const externalRoot = await mkdtemp(join(tmpdir(), 'rpc-contract-audit-external-'))
    onTestFinished(() => rm(externalRoot, { recursive: true, force: true }))
    const externalConsumer = join(externalRoot, 'consumer.js')
    await writeFile(externalConsumer, `export async function loadConfig() {}`)
    await rm(join(repoRoot, CONSUMER_PATH))
    await symlink(externalConsumer, join(repoRoot, CONSUMER_PATH))

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
      reason: 'path must not resolve through a symbolic link',
    }))
  }, 30000)

  it('fails when the production scan tree contains a symlink escaping the repository', async () => {
    const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY })
    const externalRoot = await mkdtemp(join(tmpdir(), 'rpc-contract-audit-scan-'))
    onTestFinished(() => rm(externalRoot, { recursive: true, force: true }))
    await writeFile(join(externalRoot, 'escape.js'), `readConfig()`)
    await symlink(externalRoot, join(repoRoot, 'frontend-app/src/escaped-scan'))

    await expect(auditRpcContracts({ repoRoot })).rejects.toThrow(
      'production scan tree must not contain symbolic links',
    )
  }, 30000)

  it.each([
    ['wrong-object member', `
      import { readConfig } from '../../shared/api/backendApi.js'
      const unrelated = { readConfig() {} }
      export async function loadConfig() { await unrelated.readConfig() }
    `],
    ['shadowed identifier', `
      import { readConfig } from '../../shared/api/backendApi.js'
      export async function loadConfig() {
        const readConfig = async () => undefined
        await readConfig()
      }
    `],
    ['shadowed namespace receiver', `
      import * as backendApi from '../../shared/api/backendApi.js'
      export async function loadConfig() {
        const backendApi = { readConfig: async () => undefined }
        await backendApi.readConfig()
      }
    `],
  ])('rejects %s as facade provenance', async (_label, consumer) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer,
      regression: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
      reason: 'symbol does not call the facade for this RPC key',
    }))
  }, 30000)

  it.each([
    ['named import shadowed by a parameter', `
      import { readConfig } from '../../shared/api/backendApi.js'
      export async function loadConfig(readConfig) { await readConfig() }
    `],
    ['namespace import shadowed by a parameter', `
      import * as backendApi from '../../shared/api/backendApi.js'
      export async function loadConfig(backendApi) { await backendApi.readConfig() }
    `],
    ['named import shadowed in a nested scope', `
      import { readConfig } from '../../shared/api/backendApi.js'
      export async function loadConfig() {
        async function nested(readConfig) { await readConfig() }
        await nested(async () => undefined)
      }
    `],
    ['namespace import shadowed by a catch binding', `
      import * as backendApi from '../../shared/api/backendApi.js'
      export async function loadConfig() {
        try { throw { readConfig: async () => undefined } }
        catch (backendApi) { await backendApi.readConfig() }
      }
    `],
  ])('resolves %s at the candidate call site', async (_label, consumer) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer,
      regression: ignoredResultRegression(),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
      reason: 'symbol does not call the facade for this RPC key',
    }))
  }, 30000)

  it('still finds an unshadowed facade call beside a nested shadow', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() {
          { const readConfig = async () => undefined; await readConfig() }
          await readConfig()
        }
      `,
      regression: ignoredResultRegression(),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['named wrapper export', `
      import { readConfig } from '../../shared/api/backendApi.js'
      function loadConfigFromService(payload) { return readConfig(payload) }
      export { loadConfigFromService }
    `, `
      import { loadConfigFromService } from './configService.js'
      async function loadConfig() { await loadConfigFromService({}) }
    `],
    ['destructured object wrapper export', `
      import { readConfig } from '../../shared/api/backendApi.js'
      export const configService = Object.freeze({
        load: (payload) => readConfig(payload),
      })
    `, `
      import { configService } from './configService.js'
      const { load } = configService
      async function loadConfig() { await load({}) }
    `],
  ])('traces an exact %s without weakening consumer result-use proof', async (_label, service, consumer) => {
    const servicePath = 'frontend-app/src/pages/audit-fixture/configService.js'
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${ignoredResultPolicy({ consumerVisibility: 'module-private' })} }`),
      [servicePath]: service,
      [CONSUMER_PATH]: consumer,
      [REGRESSION_PATH]: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('allows a wrapper binding only when the exact facade result flows directly to return', async () => {
    const servicePath = 'frontend-app/src/pages/audit-fixture/configService.js'
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${ignoredResultPolicy({ consumerVisibility: 'module-private' })} }`),
      [servicePath]: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfigFromService(payload) {
          const result = await readConfig(payload)
          return result
        }
      `,
      [CONSUMER_PATH]: `
        import { loadConfigFromService } from './configService.js'
        async function loadConfig() { await loadConfigFromService({}) }
      `,
      [REGRESSION_PATH]: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['observed branch', `const observed = await readConfig(payload); if (observed?.ok) consume(observed); return observed`],
    ['destructure', `const { ok } = await readConfig(payload); return ok`],
    ['pass to another function', `const result = await readConfig(payload); consume(result); return result`],
    ['conditional return branch', `const result = await readConfig(payload); if (payload.flag) return result; return result`],
    ['extra inspection', `const result = await readConfig(payload); inspect(result); return result`],
  ])('rejects an imported wrapper with internal result %s', async (_label, body) => {
    const servicePath = 'frontend-app/src/pages/audit-fixture/configService.js'
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${ignoredResultPolicy({ consumerVisibility: 'module-private' })} }`),
      [servicePath]: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfigFromService(payload) { ${body} }
      `,
      [CONSUMER_PATH]: `
        import { loadConfigFromService } from './configService.js'
        async function loadConfig() { await loadConfigFromService({}) }
      `,
      [REGRESSION_PATH]: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
      reason: 'symbol does not call the facade for this RPC key',
    }))
  }, 30000)

  it('rejects an exact service wrapper whose imported facade belongs to a different RPC key', async () => {
    const servicePath = 'frontend-app/src/pages/audit-fixture/configService.js'
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${ignoredResultPolicy({ consumerVisibility: 'module-private' })} }`),
      [servicePath]: `
        import { startThread } from '../../shared/api/backendApi.js'
        export function loadConfigFromService(payload) { return startThread(payload) }
      `,
      [CONSUMER_PATH]: `
        import { loadConfigFromService } from './configService.js'
        async function loadConfig() { await loadConfigFromService({}) }
      `,
      [REGRESSION_PATH]: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
      reason: 'symbol does not call the facade for this RPC key',
    }))
  }, 30000)

  it('rejects ignored-result consumers when any matching call result is observed', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() {
          await readConfig()
          const observed = await readConfig()
          return observed
        }
      `,
      regression: ignoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'consumer',
      reason: 'consumer reads the RPC result',
    }))
  }, 30000)

  it.each([
    ['unrelated parse', `
      const OtherShape = { parse() {} }
      export function assertConfigShape(value) {
        OtherShape.parse('unrelated')
        return value
      }
    `],
    ['arbitrary parse implementation', `
      const ConfigShape = { parse(value) { return value } }
      export function assertConfigShape(value) {
        ConfigShape.parse(value)
      }
    `],
    ['inverted safeParse branch', `
      const ConfigShape = { safeParse() { return { success: true } } }
      export function assertConfigShape(value) {
        const result = ConfigShape.safeParse(value)
        if (result.success) throw new TypeError('valid config rejected')
      }
    `],
    ['unrelated safeParse', `
      const OtherShape = { safeParse() { return { success: false } } }
      export function assertConfigShape(value) {
        const result = OtherShape.safeParse('unrelated')
        if (!result.success) throw new TypeError('invalid config')
        return value
      }
    `],
    ['ignored safeParse result', `
      const ConfigShape = { safeParse() { return { success: false } } }
      export function assertConfigShape(value) {
        ConfigShape.safeParse(value)
      }
    `],
  ])('rejects consumer shape proof with %s', async (_label, shapeSource) => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        ${shapeSource}
        export async function loadConfig() {
          const result = await readConfig()
          assertConfigShape(result)
          return result.value
        }
      `,
      regression: consumerValidatedRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'shape',
      reason: 'shape symbol lacks executable narrowing',
    }))
  }, 30000)

  it.each([
    ['inverted truthiness', `if (value) throw new TypeError('valid config rejected')`],
    ['inverted object type', `if (typeof value === 'object') throw new TypeError('valid config rejected')`],
    ['inverted field type', `if (typeof value.value === 'string') throw new TypeError('valid config rejected')`],
  ])('rejects an %s guard', async (_label, guard) => {
    const repoRoot = await createPolicyShadow({ policy: consumerValidatedPolicy(), consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      export function assertConfigShape(value) { ${guard} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `, regression: consumerValidatedRegression() })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ', field: 'shape', reason: 'shape symbol lacks executable narrowing',
    }))
  }, 30000)

  it.each([
    ['falsy value', `if (!value) throw new TypeError('invalid config')`],
    ['non-object value', `if (typeof value !== 'object') throw new TypeError('invalid config')`],
    ['non-string field', `if (typeof value.value !== 'string') throw new TypeError('invalid config')`],
  ])('accepts a supported %s guard', async (_label, guard) => {
    const repoRoot = await createPolicyShadow({ policy: consumerValidatedPolicy(), consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      export function assertConfigShape(value) { ${guard} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `, regression: consumerValidatedRegression() })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('accepts parse only from a locally proven throwing schema implementation', async () => {
    const repoRoot = await createPolicyShadow({ policy: consumerValidatedPolicy(), consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      const ConfigSchema = { parse(value) {
        if (!value || typeof value !== 'object' || typeof value.value !== 'string') throw new TypeError('invalid config')
        return value
      } }
      export function assertConfigShape(value) { ConfigSchema.parse(value) }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `, regression: consumerValidatedRegression() })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['negated success', `if (!parsed.success) throw new TypeError('invalid config')`],
    ['false success', `if (parsed.success === false) throw new TypeError('invalid config')`],
  ])('accepts proven safeParse with an explicit %s failure branch', async (_label, failureGuard) => {
    const repoRoot = await createPolicyShadow({ policy: consumerValidatedPolicy(), consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      const ConfigSchema = { safeParse(value) {
        if (!value || typeof value !== 'object' || typeof value.value !== 'string') return { success: false }
        return { success: true, data: value }
      } }
      export function assertConfigShape(value) { const parsed = ConfigSchema.safeParse(value); ${failureGuard} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `, regression: consumerValidatedRegression() })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('rejects a locally shadowed otherwise-proven schema binding', async () => {
    const repoRoot = await createPolicyShadow({ policy: consumerValidatedPolicy(), consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      const ConfigSchema = { parse(value) { if (!value) throw new TypeError('invalid config'); return value } }
      export function assertConfigShape(value, ConfigSchema = { parse(input) { return input } }) {
        ConfigSchema.parse(value)
      }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `, regression: consumerValidatedRegression() })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ', field: 'shape', reason: 'shape symbol lacks executable narrowing',
    }))
  }, 30000)

  it('rejects a same-name consumer binding that is not the resolved shape symbol', async () => {
    const shapePath = 'frontend-app/src/pages/audit-fixture/configShape.js'
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${consumerValidatedPolicy({ shapePath })} }`),
      [shapePath]: `
        export function assertConfigShape(value) {
          if (!value) throw new TypeError('invalid config')
        }
      `,
      [CONSUMER_PATH]: `
        import { readConfig } from '../../shared/api/backendApi.js'
        function assertConfigShape(_value) {}
        export async function loadConfig() {
          const result = await readConfig()
          assertConfigShape(result)
          return result.value
        }
      `,
      [REGRESSION_PATH]: consumerValidatedRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'shape',
      reason: 'shape proof does not dominate consumer use',
    }))
  }, 30000)

  it.each([
    ['ordinary declaration', `export function keepsConfigResultIrrelevant() { expect(true).toBe(true) }`],
    ['empty test callback', `it('keeps config result irrelevant', () => {})`],
    ['unrelated test callback', `
      import { loadConfig } from './consumer.js'
      it('keeps config result irrelevant', async () => {
        await loadConfig()
        expect(Math.max(1, 2)).toBe(2)
      })
    `],
  ])('rejects regression locator evidence from an %s', async (_label, regression) => {
    const policy = _label === 'ordinary declaration'
      ? ignoredResultPolicy({ regressionSymbol: 'keepsConfigResultIrrelevant' })
      : ignoredResultPolicy()
    const repoRoot = await createPolicyShadow({
      policy,
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { await readConfig() }
      `,
      regression,
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
      reason: 'test callback lacks executable assertions tied to the consumer and RPC key',
    }))
  }, 30000)

  it('rejects ignored-result regression evidence that merely observes the consumer result', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export async function loadConfig() { await readConfig() }
      `,
      regression: `
        import { loadConfig } from './consumer.js'
        it('keeps config result irrelevant', async () => {
          const result = await loadConfig()
          expect(result).toBeDefined()
        })
      `,
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('rejects page-level success text without an explicit published-callback outcome contract', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [notice, setNotice] = useState('')
          async function loadConfig() { await readConfig(); setNotice('saved') }
          return notice ? <p>{notice}</p> : null
        }
      `,
      regression: pageIgnoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('accepts a negative dialog assertion backed by a concrete post-call state dismissal', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [deletingDoc, setDeletingDoc] = useState({ id: 1 })
          async function loadConfig() {
            await readConfig()
            setDeletingDoc(null)
          }
          return deletingDoc ? <div role="dialog" aria-label="Delete data source" /> : null
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion: "expect(screen.queryByRole('dialog', { name: 'Delete data source' })).not.toBeInTheDocument()",
      }),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('rejects a negative dialog assertion when the state dismissal precedes the RPC', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [deletingDoc, setDeletingDoc] = useState({ id: 1 })
          async function loadConfig() {
            setDeletingDoc(null)
            await readConfig()
          }
          return deletingDoc ? <div role="dialog" aria-label="Delete data source" /> : null
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion: "expect(screen.queryByRole('dialog', { name: 'Delete data source' })).not.toBeInTheDocument()",
      }),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('rejects an unrelated negative dialog assertion after an unrelated post-call setter', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() {
          await readConfig()
          setTotallyUnrelated(false)
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion: "expect(screen.queryByRole('dialog', { name: 'Unrelated dialog' })).not.toBeInTheDocument()",
      }),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('rejects a dialog controlled by a different state than the post-call setter', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [completed, setCompleted] = useState(true)
          const [dialogOpen] = useState(true)
          async function loadConfig() {
            await readConfig()
            setCompleted(false)
          }
          return dialogOpen ? <div role="dialog" aria-label="Different state dialog" /> : null
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion: "expect(screen.queryByRole('dialog', { name: 'Different state dialog' })).not.toBeInTheDocument()",
      }),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('rejects an ambiguous setter with multiple useState bindings', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { useState } from 'react'
        import { readConfig } from '../../shared/api/backendApi.js'
        function Fixture() {
          const [dialogOpen, setDialogOpen] = useState(true)
          async function loadConfig() {
            await readConfig()
            setDialogOpen(false)
          }
          return dialogOpen ? <div role="dialog" aria-label="Ambiguous dialog" /> : null
        }
        function OtherFixture() {
          const [otherOpen, setDialogOpen] = useState(true)
          return otherOpen ? <div role="dialog" aria-label="Other dialog" /> : null
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion: "expect(screen.queryByRole('dialog', { name: 'Ambiguous dialog' })).not.toBeInTheDocument()",
      }),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it.each([
    ['wrong RPC facade', { mockFacade: 'startThread' }],
    ['no malformed sentinel', { response: "{ value: 'valid-looking' }" }],
    ['only facade call-count assertion', { assertion: '' }],
    ['no post-trigger assertion', { assertion: '', trigger: "fireEvent.click(screen.getByRole('button', { name: 'save' }))" }],
  ])('rejects page-level ignored-result regression proof with %s', async (_label, overrides) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() { await readConfig(); showNotice('saved') }
      `,
      regression: pageIgnoredResultRegression(overrides),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
      reason: 'test callback lacks executable assertions tied to the consumer and RPC key',
    }))
  }, 30000)

  it.each([
    ['no exact facade invocation assertion', {
      invocationAssertion: '',
      assertion: "expect(await screen.findByText('existing unrelated text')).toBeInTheDocument()",
    }],
    ['exact facade invocation with unrelated screen assertion', {
      assertion: "expect(await screen.findByText('existing unrelated text')).toBeInTheDocument()",
    }],
  ])('rejects page-level false-green evidence with %s', async (_label, overrides) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() { await readConfig(); showNotice('saved') }
      `,
      regression: pageIgnoredResultRegression(overrides),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it.each([
    ['query/refetch key after the RPC', `
      async function loadConfig() {
        await readConfig()
        await queryClient.refetchQueries({ queryKey: ['unrelated-query-key'] })
      }
    `, 'unrelated-query-key'],
    ['log text after the RPC', `
      async function loadConfig() {
        await readConfig()
        log('post-call-log')
      }
    `, 'post-call-log'],
    ['unused static literal after the RPC', `
      async function loadConfig() {
        await readConfig()
        const unused = 'unused-static-literal'
      }
    `, 'unused-static-literal'],
    ['caller sibling text', `
      async function loadConfig() { await readConfig() }
      function invokeLoad() { log(loadConfig(), 'caller-sibling-text') }
    `, 'caller-sibling-text'],
  ])('rejects page-level text evidence sourced only from %s', async (_label, consumerBody, assertedText) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        ${consumerBody}
      `,
      regression: pageIgnoredResultRegression({
        assertion: `expect(await screen.findByText('${assertedText}')).toBeInTheDocument()`,
      }),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('rejects a negative alert assertion backed only by a generic catch error setter', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy({ consumerVisibility: 'module-private' }),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        async function loadConfig() {
          try {
            await readConfig()
            showNotice('saved')
          } catch (error) {
            setLoadError({ error })
          }
        }
      `,
      regression: pageIgnoredResultRegression({
        assertion: "expect(screen.queryByRole('alert')).not.toBeInTheDocument()",
      }),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('accepts exact direct-wails rejection propagation evidence for the configured RPC method', async () => {
    const consumerPath = 'frontend-app/src/shared/api/wails/wailsBridgeRpc.js'
    const policy = ignoredResultPolicy({
      consumerPath,
      consumerSymbol: 'sendFrontendLogBatch',
      regressionSymbol: 'propagates frontend log batch RPC failures',
    })
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${policy} }`, { key: 'UI_LOG', facade: 'sendFrontendLogBatch' }),
      [consumerPath]: DIRECT_WAILS_IGNORED_RESULT_CONSUMER,
      [REGRESSION_PATH]: directWailsIgnoredResultRegression(),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['wrong RPC method', { methodAssertion: "expect(byID).toHaveBeenCalledWith(expect.any(Number), 'thread/start', expect.any(Object))" }],
    ['resolved transport', { mockMethod: 'mockResolvedValue' }],
    ['no rejection assertion', { rejectionAssertion: '' }],
    ['no exact method assertion', { methodAssertion: '' }],
  ])('rejects unsound direct-wails regression proof with %s', async (_label, overrides) => {
    const consumerPath = 'frontend-app/src/shared/api/wails/wailsBridgeRpc.js'
    const policy = ignoredResultPolicy({
      consumerPath,
      consumerSymbol: 'sendFrontendLogBatch',
      regressionSymbol: 'propagates frontend log batch RPC failures',
    })
    const repoRoot = await createShadowRepo({
      [MATRIX_PATH]: shadowMatrix(`{ responsePolicy: ${policy} }`, { key: 'UI_LOG', facade: 'sendFrontendLogBatch' }),
      [consumerPath]: DIRECT_WAILS_IGNORED_RESULT_CONSUMER,
      [REGRESSION_PATH]: directWailsIgnoredResultRegression(overrides),
    })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'UI_LOG',
      field: 'regressionTest',
    }))
  }, 30000)

  it('rejects consumer-validated regression evidence without malformed-shape rejection', async () => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) {
          if (!value) throw new TypeError('invalid config')
        }
        export async function loadConfig() {
          const result = await readConfig()
          assertConfigShape(result)
          return result
        }
      `,
      regression: `
        import { loadConfig } from './consumer.js'
        it('rejects malformed config', async () => {
          expect(await loadConfig()).toBeDefined()
        })
      `,
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it.each([
    ['parameter', `async (loadConfig) => { expect(await loadConfig()).toBeUndefined() }`],
    ['nested parameter', `async () => { async function nested(loadConfig) { expect(await loadConfig()).toBeUndefined() }; await nested(async () => undefined) }`],
    ['catch binding', `async () => { try { throw async () => undefined } catch (loadConfig) { expect(await loadConfig()).toBeUndefined() } }`],
    ['block binding', `async () => { { const loadConfig = async () => undefined; expect(await loadConfig()).toBeUndefined() } }`],
  ])('rejects ignored-result regression using a shadowed consumer alias: %s', async (_label, callback) => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `import { readConfig } from '../../shared/api/backendApi.js'; export async function loadConfig() { await readConfig() }`,
      regression: `import { loadConfig } from './consumer.js'; it('keeps config result irrelevant', ${callback})`,
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('accepts regression evidence using the unshadowed imported consumer alias', async () => {
    const repoRoot = await createPolicyShadow({
      policy: ignoredResultPolicy(),
      consumer: `import { readConfig } from '../../shared/api/backendApi.js'; export async function loadConfig() { await readConfig() }`,
      regression: ignoredResultRegression(),
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['parameter', `async (loadConfig) => { await expect(loadConfig()).rejects.toThrow('invalid config') }`],
    ['nested parameter', `async () => { async function nested(loadConfig) { await expect(loadConfig()).rejects.toThrow('invalid config') }; await nested(async () => { throw new TypeError('invalid config') }) }`],
    ['catch binding', `async () => { try { throw async () => { throw new TypeError('invalid config') } } catch (loadConfig) { await expect(loadConfig()).rejects.toThrow('invalid config') } }`],
  ])('rejects consumer-validated regression using a shadowed consumer alias: %s', async (_label, callback) => {
    const repoRoot = await createPolicyShadow({
      policy: consumerValidatedPolicy(),
      consumer: `
        import { readConfig } from '../../shared/api/backendApi.js'
        export function assertConfigShape(value) { if (!value) throw new TypeError('invalid config') }
        export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result }
      `,
      regression: `
        import { vi } from 'vitest'
        import { loadConfig } from './consumer.js'
        vi.mock('../../shared/api/backendApi.js', () => ({ readConfig: vi.fn().mockResolvedValue(null) }))
        it('rejects malformed config', ${callback})
      `,
    })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      field: 'regressionTest',
    }))
  }, 30000)

  it('tracks an unused facade imported through a barrel re-export', async () => {
    const barrelPath = 'frontend-app/src/pages/audit-fixture/backendApiBarrel.js'
    const repoRoot = await createPolicyShadow({
      policy: UNUSED_POLICY,
      consumer: `
        import { readConfig } from './backendApiBarrel.js'
        readConfig()
      `,
    })
    await writeFile(join(repoRoot, barrelPath), `
      export { readConfig } from '../../shared/api/backendApi.js'
    `)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'unused',
      path: CONSUMER_PATH,
      reason: 'production facade reference exists',
    }))
  }, 30000)

  it.each([
    ['single alias', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export { readConfig as loadConfig } from '../../shared/api/backendApi.js'`,
    }, `import { loadConfig } from './barrel-a.js'; loadConfig()`],
    ['alias then star', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export { readConfig as loadConfig } from '../../shared/api/backendApi.js'`,
      'frontend-app/src/pages/audit-fixture/barrel-b.js': `export * from './barrel-a.js'`,
    }, `import { loadConfig } from './barrel-b.js'; loadConfig()`],
    ['star then alias', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export * from '../../shared/api/backendApi.js'`,
      'frontend-app/src/pages/audit-fixture/barrel-b.js': `export { readConfig as loadConfig } from './barrel-a.js'`,
    }, `import { loadConfig } from './barrel-b.js'; loadConfig()`],
  ])('tracks an unused facade through %s re-exports', async (_label, barrels, consumer) => {
    const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer })
    for (const [filePath, source] of Object.entries(barrels)) await writeFile(join(repoRoot, filePath), source)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'unused',
      path: CONSUMER_PATH,
    }))
  }, 30000)

  it.each([
    ['default import through renamed default', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export { readConfig as default } from '../../shared/api/backendApi.js'`,
    }, `import loadConfig from './barrel-a.js'; loadConfig()`],
    ['namespace member through renamed export', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export { readConfig as loadConfig } from '../../shared/api/backendApi.js'`,
    }, `import * as api from './barrel-a.js'; api.loadConfig()`],
    ['default alias after star hop', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export * from '../../shared/api/backendApi.js'`,
      'frontend-app/src/pages/audit-fixture/barrel-b.js': `export { readConfig as default } from './barrel-a.js'`,
    }, `import loadConfig from './barrel-b.js'; loadConfig()`],
    ['namespace alias after alias and star hops', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export { readConfig as intermediate } from '../../shared/api/backendApi.js'`,
      'frontend-app/src/pages/audit-fixture/barrel-b.js': `export * from './barrel-a.js'`,
      'frontend-app/src/pages/audit-fixture/barrel-c.js': `export { intermediate as loadConfig } from './barrel-b.js'`,
    }, `import * as api from './barrel-c.js'; api.loadConfig()`],
  ])('tracks an unused facade through %s', async (_label, barrels, consumer) => {
    const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer })
    for (const [filePath, source] of Object.entries(barrels)) await writeFile(join(repoRoot, filePath), source)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'unused',
      path: CONSUMER_PATH,
    }))
  }, 30000)

  it.each([
    ['direct namespace export', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export * as api from '../../shared/api/backendApi.js'`,
    }, `import { api } from './barrel-a.js'; api.readConfig()`],
    ['multi-hop namespace export', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export * as api from '../../shared/api/backendApi.js'`,
      'frontend-app/src/pages/audit-fixture/barrel-b.js': `export { api as backend } from './barrel-a.js'`,
    }, `import { backend } from './barrel-b.js'; backend.readConfig()`],
  ])('tracks an unused facade through a %s', async (_label, barrels, consumer) => {
    const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer })
    for (const [filePath, source] of Object.entries(barrels)) await writeFile(join(repoRoot, filePath), source)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'unused',
      path: CONSUMER_PATH,
    }))
  }, 30000)

  it('does not treat an unrelated member of a namespace export as facade usage', async () => {
    const repoRoot = await createPolicyShadow({
      policy: UNUSED_POLICY,
      consumer: `import { api } from './barrel-a.js'; api.createBackendApi()`,
    })
    await writeFile(
      join(repoRoot, 'frontend-app/src/pages/audit-fixture/barrel-a.js'),
      `export * as api from '../../shared/api/backendApi.js'`,
    )
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['direct nested namespace member', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export * as api from '../../shared/api/backendApi.js'`,
    }, `import * as barrel from './barrel-a.js'; barrel.api.readConfig()`],
    ['multi-hop nested namespace member', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export * as api from '../../shared/api/backendApi.js'`,
      'frontend-app/src/pages/audit-fixture/barrel-b.js': `export { api as backend } from './barrel-a.js'`,
    }, `import * as barrel from './barrel-b.js'; const api = barrel.backend; const alias = api; alias.readConfig()`],
    ['static computed nested namespace member', {
      'frontend-app/src/pages/audit-fixture/barrel-a.js': `export * as api from '../../shared/api/backendApi.js'`,
    }, `import * as barrel from './barrel-a.js'; barrel['api']['readConfig']()`],
  ])('tracks an unused facade through a %s', async (_label, barrels, consumer) => {
    const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer })
    for (const [filePath, source] of Object.entries(barrels)) await writeFile(join(repoRoot, filePath), source)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'unused',
      path: CONSUMER_PATH,
    }))
  }, 30000)

  it.each([
    ['unrelated nested member', `barrel.api.createBackendApi()`],
    ['dynamic computed member', `barrel[key].readConfig()`],
    ['shadowed barrel binding', `function use(barrel) { barrel.api.readConfig() }`],
    ['shadowed nested api binding', `const api = barrel.api; function use(api) { api.readConfig() }`],
  ])('does not treat a %s as nested namespace facade usage', async (_label, use) => {
    const repoRoot = await createPolicyShadow({
      policy: UNUSED_POLICY,
      consumer: `import * as barrel from './barrel-a.js'; const key = 'api'; ${use}`,
    })
    await writeFile(
      join(repoRoot, 'frontend-app/src/pages/audit-fixture/barrel-a.js'),
      `export * as api from '../../shared/api/backendApi.js'`,
    )
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it('does not treat unrelated aliased or star exports as facade usage', async () => {
    const barrelPath = 'frontend-app/src/pages/audit-fixture/unrelatedBarrel.js'
    const repoRoot = await createPolicyShadow({
      policy: UNUSED_POLICY,
      consumer: `import { loadOther } from './unrelatedBarrel.js'; loadOther()`,
    })
    await writeFile(join(repoRoot, barrelPath), `
      export { createBackendApi as loadOther } from '../../shared/api/backendApi.js'
      export * from '../../shared/api/backend/backendRpcMethods.js'
    `)
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['namespace member', `
      import * as backendApi from '../../shared/api/backendApi.js'
      backendApi.readConfig()
    `],
    ['namespace destructuring alias', `
      import * as backendApi from '../../shared/api/backendApi.js'
      const { readConfig: load } = backendApi
      load()
    `],
    ['transitive local alias', `
      import { readConfig as importedRead } from '../../shared/api/backendApi.js'
      const localRead = importedRead
      const load = localRead
      load()
    `],
  ])('tracks an unused facade through a %s', async (_label, consumer) => {
    const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer })

    const report = await auditRpcContracts({ repoRoot })

    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({
      key: 'CONFIG_READ',
      kind: 'unused',
      path: CONSUMER_PATH,
      reason: 'production facade reference exists',
    }))
  }, 30000)
  it.each([
    ['dead branch', `if (false) assertConfigShape(result)`],
    ['one-sided branch', `if (flag) assertConfigShape(result)`],
    ['callback', `items.forEach(() => assertConfigShape(result))`],
    ['loop', `while (flag) { assertConfigShape(result); break }`],
    ['after use', `const value = result.value; assertConfigShape(result)`],
  ])('rejects non-dominating validation in a %s', async (_label, validation) => {
    const repoRoot = await createPolicyShadow({ policy: consumerValidatedPolicy(), consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      export function assertConfigShape(value) { if (!value) throw new TypeError('invalid config') }
      export async function loadConfig(flag = false, items = []) { const result = await readConfig(); ${validation}; return result.value }
    `, regression: consumerValidatedRegression() })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({ field: 'shape', reason: 'shape proof does not dominate consumer use' }))
  }, 30000)
  it.each([
    ['nested guard', `function nested() { if (!value) throw new TypeError('invalid config') }`, ''],
    ['callback guard', `[value].forEach(() => { if (!value) throw new TypeError('invalid config') })`, ''],
    ['dead guard', `if (false) { if (!value) throw new TypeError('invalid config') }`, ''],
    ['nested parser', `function nested() { ConfigSchema.parse(value) }`, `const ConfigSchema = { parse(input) { if (!input) throw new TypeError('invalid config') } }`],
    ['callback parser', `[value].forEach(() => ConfigSchema.parse(value))`, `const ConfigSchema = { parse(input) { if (!input) throw new TypeError('invalid config') } }`],
    ['dead parser', `if (false) ConfigSchema.parse(value)`, `const ConfigSchema = { parse(input) { if (!input) throw new TypeError('invalid config') } }`],
  ])('rejects non-executable %s evidence', async (_label, proof, schema) => {
    const repoRoot = await createPolicyShadow({ policy: consumerValidatedPolicy(), consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'; ${schema}
      export function assertConfigShape(value) { ${proof} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `, regression: consumerValidatedRegression() })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({ field: 'shape', reason: 'shape symbol lacks executable narrowing' }))
  }, 30000)

  it.each([
    ['no malformed input', `import { loadConfig } from './consumer.js'; it('rejects malformed config', async () => { await expect(loadConfig()).rejects.toThrow('invalid config') })`],
    ['transport rejection', `import { vi } from 'vitest'; import { loadConfig } from './consumer.js'; vi.mock('../../shared/api/backendApi.js', () => ({ readConfig: vi.fn().mockRejectedValue(new Error('transport failed')) })); it('rejects malformed config', async () => { await expect(loadConfig()).rejects.toThrow('transport failed') })`],
    ['unrelated throw', `import { vi } from 'vitest'; import { loadConfig } from './consumer.js'; vi.mock('../../shared/api/backendApi.js', () => ({ readConfig: vi.fn().mockResolvedValue({ malformed: true }) })); it('rejects malformed config', async () => { throw new Error('invalid config') })`],
    ['generic rejection', `import { vi } from 'vitest'; import { loadConfig } from './consumer.js'; vi.mock('../../shared/api/backendApi.js', () => ({ readConfig: vi.fn().mockResolvedValue({ malformed: true }) })); it('rejects malformed config', async () => { await expect(loadConfig()).rejects.toThrow() })`],
  ])('rejects consumer regression with %s', async (_label, regression) => {
    const repoRoot = await createPolicyShadow({ policy: consumerValidatedPolicy(), consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      export function assertConfigShape(value) { if (!value) throw new TypeError('invalid config') }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `, regression })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({ field: 'regressionTest' }))
  }, 30000)

  it.each([
    ['parameter', `function shadow(readConfig) { readConfig() }`, false],
    ['nested', `function nested() { const readConfig = () => {}; readConfig() }`, false],
    ['block', `{ const readConfig = () => {}; readConfig() }`, false],
    ['catch', `try {} catch (readConfig) { readConfig() }`, false],
    ['namespace parameter', `function shadow(backendApi) { backendApi.readConfig() }`, true],
  ])('ignores an unused facade referenced only by a shadowed %s binding', async (_label, use, namespace) => {
    const consumer = namespace ? `import * as backendApi from '../../shared/api/backendApi.js'; ${use}` : `import { readConfig } from '../../shared/api/backendApi.js'; ${use}`
    const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toEqual([])
  }, 30000)

  it.each([
    ['named', `import { readConfig } from '../../shared/api/backendApi.js'; function shadow(readConfig) { readConfig() }; readConfig()`],
    ['namespace', `import * as backendApi from '../../shared/api/backendApi.js'; function shadow(backendApi) { backendApi.readConfig() }; backendApi.readConfig()`],
  ])('finds a real %s facade reference beside a shadow', async (_label, consumer) => {
    const repoRoot = await createPolicyShadow({ policy: UNUSED_POLICY, consumer })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({ kind: 'unused' }))
  }, 30000)
  it.each([
    ['failure guard in callback', `const parsed = ConfigSchema.safeParse(value); [parsed].forEach(() => { if (!parsed.success) throw new TypeError('invalid config') })`, `if (!value) return { success: false }; return { success: true, data: value }`],
    ['schema invalid return in callback', `const parsed = ConfigSchema.safeParse(value); if (!parsed.success) throw new TypeError('invalid config')`, `[value].forEach(() => { if (!value) return { success: false } }); return { success: true, data: value }`],
    ['schema success return in dead branch', `const parsed = ConfigSchema.safeParse(value); if (!parsed.success) throw new TypeError('invalid config')`, `if (!value) return { success: false }; if (false) return { success: true, data: value }`],
  ])('rejects non-dominating safeParse proof with %s', async (_label, shapeBody, schemaBody) => {
    const repoRoot = await createPolicyShadow({ policy: consumerValidatedPolicy(), consumer: `
      import { readConfig } from '../../shared/api/backendApi.js'
      const ConfigSchema = { safeParse(value) { ${schemaBody} } }
      export function assertConfigShape(value) { ${shapeBody} }
      export async function loadConfig() { const result = await readConfig(); assertConfigShape(result); return result.value }
    `, regression: consumerValidatedRegression() })
    const report = await auditRpcContracts({ repoRoot })
    expect(report.invalidResponsePolicyEvidence).toContainEqual(expect.objectContaining({ field: 'shape', reason: 'shape symbol lacks executable narrowing' }))
  }, 30000)

  it('ignores payload calls inside nested functions and instance fields', function decoyCb(){const source=`function threadStartPayload(params) { const unused = { ...params }; const nested = () => takePayloadField(unused, 'provider'); class Decoy { read = takePayloadField(unused, 'provider') }; void nested; void Decoy; return takePayloadFields(unused, ['cwd']) }\nfunction turnStartPayload(params) { const unused = { ...params }; return takePayloadFields(unused, ['cwd', 'threadId']) }\nfunction turnInterruptPayload(params) { const unused = { ...params }; return takePayloadFields(unused, ['expectedTurnId', 'requestId', 'threadId']) }`;expect(collectFrontendPayloadKeysFromSource(source).get('thread/start')).toEqual(['cwd'])})

  it('fails fast when a required builder has no top-level declaration', () => {
    const source = [
      'function wrapper() {',
      '  function threadStartPayload(params) {',
      '    const unused = { ...params }',
      "    return takePayloadFields(unused, ['cwd', 'provider'])",
      '  }',
      '}',
      'function turnStartPayload(params) {',
      '  const unused = { ...params }',
      "  return takePayloadFields(unused, ['cwd', 'threadId'])",
      '}',
    ].join('\n')

    expect(() => collectFrontendPayloadKeysFromSource(source)).toThrow(
      'threadStartPayload must have exactly one top-level FunctionDeclaration in frontend-app/src/shared/api/backend/backendApiFactoryThread.js; found 0',
    )
  })

  it('fails fast when a required builder has multiple top-level declarations', () => {
    const source = [
      'function threadStartPayload(params) {',
      '  const unused = { ...params }',
      "  return takePayloadFields(unused, ['cwd'])",
      '}',
      'function threadStartPayload(params) {',
      '  const unused = { ...params }',
      "  return takePayloadFields(unused, ['provider'])",
      '}',
      'function turnStartPayload(params) {',
      '  const unused = { ...params }',
      "  return takePayloadFields(unused, ['cwd', 'threadId'])",
      '}',
    ].join('\n')

    expect(() => collectFrontendPayloadKeysFromSource(source)).toThrow(
      'threadStartPayload must have exactly one top-level FunctionDeclaration in frontend-app/src/shared/api/backend/backendApiFactoryThread.js; found 2',
    )
  })
})
