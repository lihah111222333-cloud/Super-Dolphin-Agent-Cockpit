import { describe, expect, it } from 'vitest';
import { RPC_METHODS } from './backendApi.js';
import {
  RPC_CONTRACT_LEVELS,
  RPC_CONTRACT_MATRIX,
  RPC_CONTRACT_REGISTRY,
} from './backendApi.contractMatrix.js';

describe('backend API contract matrix', () => {
  it('uses an explicit registry entry for every RPC method', () => {
    const methodKeys = Object.keys(RPC_METHODS);
    const registryKeys = Object.keys(RPC_CONTRACT_REGISTRY);

    expect(registryKeys).toEqual(methodKeys);
    expect(RPC_CONTRACT_MATRIX).toHaveLength(methodKeys.length);
    expect(new Set(RPC_CONTRACT_MATRIX).size).toBe(methodKeys.length);

    for (const key of methodKeys) {
      const entry = RPC_CONTRACT_REGISTRY[key];
      expect(entry).toEqual(expect.objectContaining({
        key,
        method: RPC_METHODS[key],
        facade: expect.any(String),
        level: expect.any(String),
        backendOwner: expect.any(String),
        tests: expect.any(Array),
        rawLiteralRpc: expect.any(Boolean),
        notes: expect.any(Array),
      }));
      expect(Object.values(RPC_CONTRACT_LEVELS)).toContain(entry.level);
      expect(entry.facade).not.toBe('');
      expect(entry.backendOwner).not.toBe('');
      expect(entry.tests.length).toBeGreaterThan(0);
      expect(entry.rawLiteralRpc).toBe(false);
    }
  });

  it('marks video credential methods at the documented risk levels', () => {
    expect(RPC_CONTRACT_REGISTRY.UI_VIDEO_SET_API_KEY.level).toBe('P0');
    expect(RPC_CONTRACT_REGISTRY.UI_VIDEO_SET_API_KEY.notes).toContain('credential-affecting mutation');
    expect(RPC_CONTRACT_REGISTRY.UI_VIDEO_GET_API_KEY.level).toBe('P1');
    expect(RPC_CONTRACT_REGISTRY.UI_VIDEO_GET_API_KEY.notes).toContain('credential configuration read');
  });

  it('marks model provider preference methods at the documented risk levels', () => {
    expect(RPC_CONTRACT_REGISTRY.MODEL_PROVIDERS_LIST).toEqual(expect.objectContaining({
      facade: 'listModelProviders',
      level: 'P1',
      backendOwner: 'preferences',
      notes: expect.arrayContaining(['model provider registry read']),
    }));
    expect(RPC_CONTRACT_REGISTRY.MODEL_PROVIDERS_SAVE).toEqual(expect.objectContaining({
      facade: 'saveModelProviders',
      level: 'P0',
      backendOwner: 'preferences',
      notes: expect.arrayContaining(['model provider registry mutation']),
    }));
    expect(RPC_CONTRACT_REGISTRY.MODEL_PROVIDERS_APPLY).toEqual(expect.objectContaining({
      facade: 'applyModelProvider',
      level: 'P0',
      backendOwner: 'preferences',
      notes: expect.arrayContaining(['model provider activation mutation']),
    }));
  });

  it('keeps P1 read families explicitly represented', () => {
    const expectedP1Reads = [
      'THREAD_MESSAGES',
      'THREAD_RESOLVE',
      'THREAD_CONFIG_GET',
      'UI_STATE_GET',
      'DASHBOARD_DAGS',
      'DASHBOARD_DAG_DETAIL',
      'DASHBOARD_DAG_RUNS',
      'DASHBOARD_DAG_RUN',
      'UI_DASHBOARD_GET',
      'PROMPT_ASSETS_LIST',
      'DASHBOARD_PROMPTS',
      'PROMPTS_GET',
      'PROMPT_SECTIONS_LIST',
      'UI_MEMORY_GET',
      'UI_MEMORY_ENTRY_GET',
      'UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS',
      'DASHBOARD_SHARED_FILES',
      'UI_SHARED_FILE_GET',
      'OBSERVABILITY_TRACE_GET',
      'OBSERVABILITY_THREAD_RECENT',
      'OBSERVABILITY_RECENT_LIST',
      'OBSERVABILITY_SLOW_LIST',
      'OBSERVABILITY_ERROR_LIST',
      'OBSERVABILITY_STATUS',
      'DATASOURCE_V2_LIST',
      'DATASOURCE_V2_GET',
    ];

    for (const key of expectedP1Reads) {
      expect(RPC_CONTRACT_REGISTRY[key].level).toBe('P1');
    }
  });

  it('anchors known contract exceptions in notes instead of implicit defaults', () => {
    expect(RPC_CONTRACT_REGISTRY.DASHBOARD_SHARED_FILES.notes).toContain('params:{}-only');
    expect(RPC_CONTRACT_REGISTRY.THREAD_START.notes).toContain('custom-decoder');
    expect(RPC_CONTRACT_REGISTRY.TURN_START.notes).toContain('custom-decoder');
    expect(RPC_CONTRACT_REGISTRY.TURN_INTERRUPT.notes).toContain('custom-decoder');
    expect(RPC_CONTRACT_REGISTRY.TURN_INTERRUPT.notes).toContain('passthrough response');
  });
});
