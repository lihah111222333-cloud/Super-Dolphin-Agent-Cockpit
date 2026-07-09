import { describe, expect, it } from 'vitest'
import {
  auditRpcContracts,
  collectFrontendPayloadKeysFromSource,
  collectHardcodedPayloadGuardFindingsFromSources,
  collectPayloadRegistryDrift,
  parseContractMatrixForTest,
  parseRpcMethodsForTest,
} from './rpc-contract-audit.mjs'

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
    `

    expect(collectFrontendPayloadKeysFromSource(source).get('thread/start')).toEqual(['cwd', 'provider'])
    expect(collectFrontendPayloadKeysFromSource(source).get('turn/start')).toEqual(['input', 'thread_id', 'threadId'])
  })
})
