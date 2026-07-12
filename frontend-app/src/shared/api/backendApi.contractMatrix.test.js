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
        responseValidator: expect.any(String),
        responsePassthroughReason: expect.any(String),
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
      'THREAD_PROMPT_HISTORY',
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
      'TOOLBRIDGE_TOOLS_LIST',
    ];

    for (const key of expectedP1Reads) {
      expect(RPC_CONTRACT_REGISTRY[key].level).toBe('P1');
    }
  });

  it('anchors known contract exceptions in explicit policy fields instead of implicit defaults', () => {
    expect(RPC_CONTRACT_REGISTRY.DASHBOARD_SHARED_FILES.notes).toContain('params:{}-only');
    expect(RPC_CONTRACT_REGISTRY.CONFIG_LSP_PROMPT_HINT_READ.responseValidator).toBe('lspPromptHintResponse');
    expect(RPC_CONTRACT_REGISTRY.CONFIG_LSP_PROMPT_HINT_WRITE.responseValidator).toBe('lspPromptHintResponse');
    expect(RPC_CONTRACT_REGISTRY.UI_STATE_GET.responseValidator).toBe('uiStateResponse');
    expect(RPC_CONTRACT_REGISTRY.MCP_SERVER_LIST.responseValidator).toBe('mcpServerListResponse');
    expect(RPC_CONTRACT_REGISTRY.TOOLBRIDGE_TOOLS_LIST.responseValidator).toBe('toolbridgeToolsListResponse');
    expect(RPC_CONTRACT_REGISTRY.MCP_SERVER_SQLITE_START.responseValidator).toBe('mcpServerControlResponse');
    expect(RPC_CONTRACT_REGISTRY.MCP_SERVER_SQLITE_STOP.responseValidator).toBe('mcpServerControlResponse');
    expect(RPC_CONTRACT_REGISTRY.MCP_SERVER_PLAYWRIGHT_START.responseValidator).toBe('mcpServerControlResponse');
    expect(RPC_CONTRACT_REGISTRY.MCP_SERVER_PLAYWRIGHT_STOP.responseValidator).toBe('mcpServerControlResponse');
    expect(RPC_CONTRACT_REGISTRY.THREAD_FORK.responseValidator).toBe('threadForkResponse');
    expect(RPC_CONTRACT_REGISTRY.THREAD_START.responseValidator).toBe('threadStartResponse');
    expect(RPC_CONTRACT_REGISTRY.THREAD_MESSAGES.responseValidator).toBe('threadMessagesResponse');
    expect(RPC_CONTRACT_REGISTRY.THREAD_PROMPT_HISTORY.responseValidator).toBe('threadPromptHistoryResponse');
    expect(RPC_CONTRACT_REGISTRY.THREAD_RESOLVE.responseValidator).toBe('threadResolveResponse');
    expect(RPC_CONTRACT_REGISTRY.SKILLS_LOCAL_READ.responseValidator).toBe('skillReadResponse');
    expect(RPC_CONTRACT_REGISTRY.TURN_START.responseValidator).toBe('turnStartResponse');
    expect(RPC_CONTRACT_REGISTRY.TURN_FORCE_COMPLETE.responseValidator).toBe('turnForceCompleteResponse');
    expect(RPC_CONTRACT_REGISTRY.DASHBOARD_DAG_START.responseValidator).toBe('dashboardDagStartResponse');
    expect(RPC_CONTRACT_REGISTRY.DASHBOARD_DAG_CREATE_AND_START.responseValidator).toBe('dashboardDagCreateAndStartResponse');
    expect(RPC_CONTRACT_REGISTRY.TURN_INTERRUPT.responsePassthroughReason).toContain('command result envelope');
  });

  it('anchors migrated page service DTO golden coverage in route metadata', () => {
    const expectations = {
      UI_SHARED_FILE_GET: {
        facade: 'filesPageService.readSharedFile',
        tests: ['src/pages/files/services/filesPageService.test.js'],
      },
      UI_MEMORY_ENTRY_GET: {
        facade: 'memoryPageService.getMemoryEntry',
        tests: ['src/pages/memory/services/memoryPageService.test.js'],
      },
      UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS: {
        facade: 'memoryPageService.getMemoryConsolidationStatus',
        tests: ['src/pages/memory/services/memoryPageService.test.js'],
      },
      OBSERVABILITY_TRACE_GET: {
        facade: 'observabilityPageService.getObservabilityTrace',
        tests: ['src/pages/observability/services/observabilityPageService.test.js'],
      },
      OBSERVABILITY_RECENT_LIST: {
        facade: 'observabilityPageService.listObservabilityRecent',
        tests: ['src/pages/observability/services/observabilityPageService.test.js'],
      },
      PROMPT_ASSETS_LIST: {
        facade: 'promptPageService.listPromptAssets',
        tests: ['src/pages/prompts/services/promptPageService.test.js'],
      },
      DASHBOARD_PROMPTS: {
        facade: 'promptPageService.getDashboardPrompts',
        tests: ['src/pages/prompts/services/promptPageService.test.js'],
      },
      PROMPTS_GET: {
        facade: 'promptPageService.getPrompt',
        tests: ['src/pages/prompts/services/promptPageService.test.js'],
      },
      PROMPTS_WRITE: {
        facade: 'promptPageService.writePrompt',
        tests: ['src/pages/prompts/services/promptPageService.test.js'],
      },
    };

    for (const [key, expected] of Object.entries(expectations)) {
      expect(RPC_CONTRACT_REGISTRY[key]).toEqual(expect.objectContaining({
        facade: expected.facade,
        rawLiteralRpc: false,
        responsePassthroughReason: '',
        responseValidator: '',
        tests: expect.arrayContaining(expected.tests),
      }));
    }
  });
});
