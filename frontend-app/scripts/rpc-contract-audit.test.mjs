import { describe, expect, it } from 'vitest'
import { auditRpcContracts } from './rpc-contract-audit.mjs'

describe('rpc contract audit', () => {
  it('keeps frontend RPC constants, registry entries, and P0 backend handlers reconciled', async () => {
    const report = await auditRpcContracts()

    expect(report.missingRegistryKeys).toEqual([])
    expect(report.registryWithoutRpcMethods).toEqual([])
    expect(report.mismatchedRegistryMethods).toEqual([])
    expect(report.p0MissingBackendHandlers).toEqual([])
    expect(report.allowedPayloadRegistryDrift).toEqual([])
    expect(report.goPayloadKeysByMethod.get('turn/start')).toEqual(expect.arrayContaining([
      'thread_id',
      'threadId',
      'selected_skill_refs',
      'selectedSkillRefs',
    ]))
  }, 15000)
})
