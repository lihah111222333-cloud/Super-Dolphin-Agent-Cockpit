import { describe, expect, it } from 'vitest'
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import {
  auditRpcContracts,
  collectFrontendPayloadKeysFromSource,
  collectHardcodedPayloadGuardFindingsFromSources,
  collectPayloadRegistryDrift,
  parseContractMatrixForTest,
  parseRpcMethodsForTest,
} from './rpc-contract-audit.mjs'

async function createRuntimeDriftFixture() {
  const repoRoot = await mkdtemp(join(tmpdir(), 'rpc-contract-audit-'))
  const sources = new Map([
    ['frontend-app/src/shared/api/backendApi.js', [
      'export const RPC_METHODS = Object.freeze({',
      "  THREAD_START: 'thread/start',",
      "  TURN_START: 'turn/start',",
      '})',
      'function threadStartPayload(params) {',
      '  const unused = { ...params }',
      "  return takePayloadFields(unused, ['cwd', 'provider'])",
      '}',
      'function turnStartPayload(params) {',
      '  const unused = { ...params }',
      "  const input = takePayloadField(unused, 'input')",
      "  return input && takePayloadFields(unused, ['cwd', 'threadId'])",
      '}',
      'void threadStartPayload',
      'void turnStartPayload',
    ].join('\n')],
    ['frontend-app/src/shared/api/backend/backendRpcMethods.js', [
      'export const RPC_METHODS = Object.freeze({',
      "  THREAD_START: 'thread/start',",
      '})',
    ].join('\n')],
    ['frontend-app/src/shared/api/backend/backendApiFactoryThread.js', [
      'function threadStartPayload(params) {',
      '  const unused = { ...params }',
      "  return takePayloadFields(unused, ['cwd'])",
      '}',
      'function turnStartPayload(params) {',
      '  const unused = { ...params }',
      "  const input = takePayloadField(unused, 'input')",
      "  return input && takePayloadFields(unused, ['cwd', 'threadId'])",
      '}',
    ].join('\n')],
    ['frontend-app/src/shared/api/backendApi.contractMatrix.js', [
      'function contract() {}',
      'export const RPC_CONTRACT_REGISTRY = Object.freeze({',
      "  THREAD_START: contract('THREAD_START', 'startThread', 'P1', 'thread', [], [], false, { responseValidator: 'threadStartResponse' }),",
      "  TURN_START: contract('TURN_START', 'startTurn', 'P1', 'turn', [], [], false, { responseValidator: 'turnStartResponse' }),",
      '})',
    ].join('\n')],
    ['frontend-app/src/shared/api/backendResponseValidators.js', [
      'const BACKEND_RESPONSE_VALIDATORS = Object.freeze({',
      '  [RPC_METHODS.UI_STATE_GET]: value,',
      '  [RPC_METHODS.THREAD_START]: value,',
      '  [RPC_METHODS.THREAD_MESSAGES]: value,',
      '  [RPC_METHODS.THREAD_RESOLVE]: value,',
      '  [RPC_METHODS.TURN_START]: value,',
      '})',
    ].join('\n')],
    ['internal/contract/rpc_handler.go', 'package contract\n'],
    ['internal/module/thread/rpc_types.go', [
      'package thread',
      'type startParams struct {',
      '  Cwd string `json:"cwd"`',
      '  Provider string `json:"provider"`',
      '}',
      'type startParamCompatFields struct {',
      '}',
    ].join('\n')],
    ['internal/module/turn/rpc_types.go', [
      'package turn',
      'type turnStartParams struct {',
      '  Cwd string `json:"cwd"`',
      '  Input any `json:"input"`',
      '  ThreadID string `json:"threadId"`',
      '}',
      'type legacyTurnStartParams struct {',
      '}',
      'type turnSteerParams struct {',
      '}',
      'type legacyTurnSteerParams struct {',
      '}',
    ].join('\n')],
  ])

  await Promise.all([...sources].map(async ([filePath, source]) => {
    const target = join(repoRoot, filePath)
    await mkdir(dirname(target), { recursive: true })
    await writeFile(target, source, 'utf8')
  }))
  await mkdir(join(repoRoot, 'cmd'), { recursive: true })
  return repoRoot
}

describe('rpc contract audit', () => {
  it('keeps frontend RPC constants, registry entries, and P0 backend handlers reconciled', async () => {
    const report = await auditRpcContracts()

    expect(report.missingRegistryKeys).toEqual([])
    expect(report.registryWithoutRpcMethods).toEqual([])
    expect(report.mismatchedRegistryMethods).toEqual([])
    expect(report.p0MissingBackendHandlers).toEqual([])
    expect(report.allowedPayloadRegistryDrift).toEqual([])
    expect(report.hardcodedPayloadGuardFindings).toEqual([])
    expect(report.missingResponsePolicies).toEqual([])
    expect(report.missingFrontendResponseValidators).toEqual([])
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
  }, 15000)

  it('detects frontend and Go hardcoded payload guard sources', () => {
    const findings = collectHardcodedPayloadGuardFindingsFromSources({
      frontendSource: `
        export const RPC_ALLOWED_PAYLOAD_KEYS = Object.freeze({})
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
        THREAD_START: contract('THREAD_START', 'startThread', 'P0', 'thread', [TESTS.API], ['runtime lifecycle start'], false, { responseValidator: 'threadStartResponse' }),
        TURN_START: contract('TURN_START', 'startTurn', 'P0', 'turn', [TESTS.API], ['runtime lifecycle start'], false, { responsePassthroughReason: 'turn start passthrough' }),
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
        facade: 'startThread',
        level: 'P0',
        responseValidator: 'threadStartResponse',
        responsePassthroughReason: '',
      },
      {
        key: 'TURN_START',
        declaredKey: 'TURN_START',
        facade: 'startTurn',
        level: 'P0',
        responseValidator: '',
        responsePassthroughReason: 'turn start passthrough',
      },
    ])
  })

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

  it('extracts consumed payload keys from runtime builders instead of static key lists', () => {
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
    `

    expect(collectFrontendPayloadKeysFromSource(source).get('thread/start')).toEqual(['cwd', 'provider'])
    expect(collectFrontendPayloadKeysFromSource(source).get('turn/start')).toEqual(['input', 'thread_id', 'threadId'])
  })

  it('audits runtime methods and payload builders when facade shadows stay unchanged', async () => {
    const repoRoot = await createRuntimeDriftFixture()
    try {
      const report = await auditRpcContracts({ repoRoot })

      expect({
        registryWithoutRpcMethods: report.registryWithoutRpcMethods,
        allowedPayloadRegistryDrift: report.allowedPayloadRegistryDrift,
      }).toEqual({
        registryWithoutRpcMethods: ['TURN_START'],
        allowedPayloadRegistryDrift: [{
          method: 'thread/start',
          missingFrontendKeys: ['provider'],
          extraFrontendKeys: [],
        }],
      })
    } finally {
      await rm(repoRoot, { recursive: true, force: true })
    }
  })

  it.each([
    ['block comment', [
      '/*',
      'function threadStartPayload(params) {',
      '  const unused = { ...params }',
      "  return takePayloadFields(unused, ['cwd', 'provider'])",
      '}',
      '*/',
    ].join('\n')],
    ['template string', [
      'const staleBuilderExample = `',
      'function threadStartPayload(params) {',
      '  const unused = { ...params }',
      "  return takePayloadFields(unused, ['cwd', 'provider'])",
      '}',
      '`',
    ].join('\n')],
  ])('ignores a fake builder in a %s when auditing runtime payload drift', async (_, fakeBuilderSource) => {
    const repoRoot = await createRuntimeDriftFixture()
    try {
      const runtimePayloadSource = [
        fakeBuilderSource,
        'function threadStartPayload(params) {',
        '  const unused = { ...params }',
        "  return takePayloadFields(unused, ['cwd'])",
        '}',
        'function turnStartPayload(params) {',
        '  const unused = { ...params }',
        "  const input = takePayloadField(unused, 'input')",
        "  return input && takePayloadFields(unused, ['cwd', 'threadId'])",
        '}',
      ].join('\n')
      await writeFile(
        join(repoRoot, 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js'),
        runtimePayloadSource,
        'utf8',
      )

      const report = await auditRpcContracts({ repoRoot })

      expect(report.allowedPayloadRegistryDrift).toEqual([{
        method: 'thread/start',
        missingFrontendKeys: ['provider'],
        extraFrontendKeys: [],
      }])
    } finally {
      await rm(repoRoot, { recursive: true, force: true })
    }
  })

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

  it.each([
    ['nested function declaration', [
      'function providerDecoy() {',
      "  return takePayloadField(unused, 'provider')",
      '}',
    ].join('\n')],
    ['nested arrow function', "const providerDecoy = () => takePayloadField(unused, 'provider')"],
    ['nested function expression', [
      'const providerDecoy = function () {',
      "  return takePayloadField(unused, 'provider')",
      '}',
    ].join('\n')],
    ['nested class method', [
      'class ProviderDecoy {',
      '  read() {',
      "    return takePayloadField(unused, 'provider')",
      '  }',
      '}',
    ].join('\n')],
    ['public instance field', [
      'class ProviderDecoy {',
      "  read = takePayloadField(unused, 'provider');",
      '}',
    ].join('\n')],
    ['private instance field', [
      'class ProviderDecoy {',
      "  #read = takePayloadField(unused, 'provider');",
      '}',
    ].join('\n')],
  ])('ignores payload calls inside a %s', async (_, decoySource) => {
    const repoRoot = await createRuntimeDriftFixture()
    try {
      const runtimePayloadSource = [
        'function threadStartPayload(params) {',
        '  const unused = { ...params }',
        decoySource,
        "  return takePayloadFields(unused, ['cwd'])",
        '}',
        'function turnStartPayload(params) {',
        '  const unused = { ...params }',
        "  const input = takePayloadField(unused, 'input')",
        "  return input && takePayloadFields(unused, ['cwd', 'threadId'])",
        '}',
      ].join('\n')
      await writeFile(
        join(repoRoot, 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js'),
        runtimePayloadSource,
        'utf8',
      )

      const report = await auditRpcContracts({ repoRoot })

      expect(report.allowedPayloadRegistryDrift).toEqual([{
        method: 'thread/start',
        missingFrontendKeys: ['provider'],
        extraFrontendKeys: [],
      }])
    } finally {
      await rm(repoRoot, { recursive: true, force: true })
    }
  })

  it.each([
    ['static field initializer', [
      'class ProviderConsumer {',
      "  static read = takePayloadField(unused, 'provider');",
      '}',
    ].join('\n')],
    ['computed instance field key', [
      'class ProviderConsumer {',
      "  [takePayloadField(unused, 'provider')] = null;",
      '}',
    ].join('\n')],
  ])('counts payload calls in a %s', async (_, classSource) => {
    const repoRoot = await createRuntimeDriftFixture()
    try {
      const runtimePayloadSource = [
        'function threadStartPayload(params) {',
        '  const unused = { ...params }',
        classSource,
        "  return takePayloadFields(unused, ['cwd'])",
        '}',
        'function turnStartPayload(params) {',
        '  const unused = { ...params }',
        "  const input = takePayloadField(unused, 'input')",
        "  return input && takePayloadFields(unused, ['cwd', 'threadId'])",
        '}',
      ].join('\n')
      await writeFile(
        join(repoRoot, 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js'),
        runtimePayloadSource,
        'utf8',
      )

      const report = await auditRpcContracts({ repoRoot })

      expect(report.allowedPayloadRegistryDrift).toEqual([])
    } finally {
      await rm(repoRoot, { recursive: true, force: true })
    }
  })
})
