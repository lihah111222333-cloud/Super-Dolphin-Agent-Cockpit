import { describe, expect, it } from 'vitest';
import { RPC_METHODS } from './backendApi.js';
import {
  RPC_CONTRACT_LEVELS,
  RPC_CONTRACT_MATRIX,
  RPC_CONTRACT_REGISTRY,
} from './backendApi.contractMatrix.js';

const EXPECTED_EXISTING_RESPONSE_VALIDATORS = Object.freeze([
  ['APP_UPDATE_INSTALL', 'appUpdateInstallResponse'],
  ['APP_UPDATE_INSTALL_LATEST', 'appUpdateInstallResponse'],
  ['CONFIG_LSP_PROMPT_HINT_READ', 'lspPromptHintResponse'],
  ['CONFIG_LSP_PROMPT_HINT_WRITE', 'lspPromptHintResponse'],
  ['DASHBOARD_DAG_CREATE_AND_START', 'dashboardDagCreateAndStartResponse'],
  ['DASHBOARD_DAG_START', 'dashboardDagStartResponse'],
  ['DASHBOARD_SHARED_FILES', 'sharedFilesDashboardResponse'],
  ['MCP_SERVER_LIST', 'mcpServerListResponse'],
  ['MCP_SERVER_PLAYWRIGHT_START', 'mcpServerControlResponse'],
  ['MCP_SERVER_PLAYWRIGHT_STOP', 'mcpServerControlResponse'],
  ['MCP_SERVER_SQLITE_START', 'mcpServerControlResponse'],
  ['MCP_SERVER_SQLITE_STOP', 'mcpServerControlResponse'],
  ['MODEL_PROVIDERS_APPLY', 'modelProviderRegistryResponse'],
  ['MODEL_PROVIDERS_LIST', 'modelProviderRegistryResponse'],
  ['OBSERVABILITY_ERROR_LIST', 'observabilityResultResponse'],
  ['OBSERVABILITY_RECENT_LIST', 'observabilityResultResponse'],
  ['OBSERVABILITY_SLOW_LIST', 'observabilityResultResponse'],
  ['OBSERVABILITY_THREAD_RECENT', 'observabilityResultResponse'],
  ['OBSERVABILITY_TRACE_GET', 'observabilityResultResponse'],
  ['SKILLS_LOCAL_READ', 'skillReadResponse'],
  ['THREAD_FORK', 'threadForkResponse'],
  ['THREAD_MESSAGES', 'threadMessagesResponse'],
  ['THREAD_RESOLVE', 'threadResolveResponse'],
  ['THREAD_START', 'threadStartResponse'],
  ['TURN_FORCE_COMPLETE', 'turnForceCompleteResponse'],
  ['TURN_START', 'turnStartResponse'],
  ['UI_MEMORY_GET', 'memorySnapshotResponse'],
  ['UI_SHARED_FILE_GET', 'sharedFileDetailResponse'],
  ['UI_STATE_GET', 'uiStateResponse'],
]);

const EXPECTED_NEW_RESPONSE_VALIDATORS = Object.freeze([
  ['APPROVAL_RESPOND', 'nullResponse'],
  ['CONFIG_BUILTIN_TOOLS_READ', 'builtinToolsResponse'],
  ['CONFIG_BUILTIN_TOOLS_WRITE', 'builtinToolsResponse'],
  ['CONFIG_READ', 'runtimeConfigResponse'],
  ['CRONJOB_LIST', 'cronListResponse'],
  ['DASHBOARD_DAG_DETAIL', 'dashboardDagDetailResponse'],
  ['DASHBOARD_DAG_RUN', 'dashboardDagRunResponse'],
  ['DASHBOARD_DAG_RUNS', 'dashboardDagRunsResponse'],
  ['DASHBOARD_LOGS', 'dashboardLogsResponse'],
  ['DASHBOARD_PROMPTS', 'dashboardPromptsResponse'],
  ['DASHBOARD_WORKFLOW_MATERIAL_WRITE', 'workflowMaterialWriteResponse'],
  ['DATASOURCE_V2_GET', 'datasourceDetailResponse'],
  ['DATASOURCE_V2_LIST', 'datasourceDocumentsResponse'],
  ['DATASOURCE_V2_LIST_CHUNKS', 'datasourceChunksResponse'],
  ['DATASOURCE_V2_UPDATE', 'datasourceDocumentResponse'],
  ['MODEL_PROVIDERS_SAVE', 'modelProviderRegistryResponse'],
  ['OBSERVABILITY_FRONTEND_INGEST', 'frontendIngestResponse'],
  ['PERSONALIZATION_PROFILE_GET', 'personalizationProfileResponse'],
  ['PERSONALIZATION_PROFILE_SAVE', 'personalizationProfileResponse'],
  ['PROMPTS_DELETE', 'okResponse'],
  ['PROMPTS_GET', 'promptDetailResponse'],
  ['PROMPTS_WRITE', 'promptDetailResponse'],
  ['PROMPT_ASSETS_LIST', 'promptAssetsResponse'],
  ['PROMPT_INTENTS_COMMIT', 'promptIntentCommitResponse'],
  ['PROMPT_INTENTS_DISCARD', 'promptIntentDiscardResponse'],
  ['PROMPT_INTENTS_DRAFT', 'promptIntentDraftResponse'],
  ['PROMPT_INTENTS_DRY_RUN', 'promptIntentDryRunResponse'],
  ['SKILLS_LOCAL_IMPORT_DIR', 'skillImportResponse'],
  ['SKILLS_LOCAL_LIST_FILES', 'skillFilesResponse'],
  ['SKILLS_RESOLUTION_APPLY', 'skillResolutionApplyResponse'],
  ['SKILLS_RESOLUTION_LIST', 'skillResolutionListResponse'],
  ['SKILLS_RESOLUTION_PREVIEW', 'skillResolutionPreviewResponse'],
  ['SKILLS_SUMMARY_SUGGEST', 'skillSummarySuggestionResponse'],
  ['SKILL_TOOLS_LIST', 'skillToolsListResponse'],
  ['THREAD_ARCHIVE', 'nullResponse'],
  ['THREAD_COMPACT_START', 'threadCompactResponse'],
  ['THREAD_CONFIG_GET', 'threadConfigResponse'],
  ['THREAD_CONFIG_SET', 'threadConfigResponse'],
  ['THREAD_DELETE', 'nullResponse'],
  ['THREAD_NAME_SET', 'nullResponse'],
  ['THREAD_PROMPT_HISTORY', 'threadPromptHistoryResponse'],
  ['THREAD_RECOVER', 'threadRecoverResponse'],
  ['TOOLBRIDGE_TOOLS_LIST', 'toolbridgeToolsListResponse'],
  ['THREAD_UNARCHIVE', 'nullResponse'],
  ['UI_CODE_SAVE', 'codeSaveResponse'],
  ['UI_DASHBOARD_GET', 'dashboardPageResponse'],
  ['UI_MEMORY_AUTO_DREAM_SET_INTENT', 'memoryAutoDreamIntentResponse'],
  ['UI_MEMORY_ENTRY_DELETE', 'memoryEntryDeleteResponse'],
  ['UI_MEMORY_ENTRY_GET', 'memoryEntryDetailResponse'],
  ['UI_MEMORY_ENTRY_MERGE', 'memoryEntryDetailResponse'],
  ['UI_MEMORY_ENTRY_UPSERT', 'memoryEntryDetailResponse'],
  ['UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START', 'memoryConsolidationJobResponse'],
  ['UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS', 'memoryConsolidationJobResponse'],
  ['UI_MEMORY_SIMILARITY_IGNORE', 'memorySimilarityIgnoreResponse'],
  ['UI_OPEN_NEW_WINDOW', 'openWindowResponse'],
  ['UI_PREFERENCES_SET', 'okResponse'],
  ['UI_PROJECTS_ADD', 'projectsStateResponse'],
  ['UI_PROJECTS_GET', 'projectsStateResponse'],
  ['UI_PROJECTS_REMOVE', 'projectsStateResponse'],
  ['UI_PROJECTS_SET_ACTIVE', 'projectsStateResponse'],
  ['UI_SHARED_FILE_DELETE', 'sharedFileDeleteResponse'],
  ['UI_SIDEBAR_GET', 'sidebarStateResponse'],
  ['UI_VIDEO_GET_API_KEY', 'videoApiKeyStatusResponse'],
  ['UI_VIDEO_SET_API_KEY', 'okResponse'],
  ['UI_WINDOW_BOOTSTRAP_GET', 'windowBootstrapResponse'],
  ['WORKFLOW_TEMPLATES_GET', 'workflowTemplateResponse'],
  ['WORKFLOW_TEMPLATES_LIST', 'workflowTemplatesListResponse'],
  ['WORKFLOW_TEMPLATES_RENDER_DAG', 'workflowTemplateDraftResponse'],
  ['WORKFLOW_TEMPLATES_SAVE', 'workflowTemplateSaveResponse'],
]);

const EXPECTED_RESPONSE_VALIDATORS = Object.freeze([
  ...EXPECTED_EXISTING_RESPONSE_VALIDATORS,
  ...EXPECTED_NEW_RESPONSE_VALIDATORS,
].sort(([left], [right]) => left.localeCompare(right)));

const EXPECTED_NEW_IGNORED_RESPONSE_POLICIES = Object.freeze([
  'DASHBOARD_DAG_APPLY_OPS',
  'DASHBOARD_DAG_DELETE',
  'DASHBOARD_DAG_DISPATCH_NODE',
  'DASHBOARD_DAG_TERMINATE',
  'DATASOURCE_V2_DELETE',
  'DATASOURCE_V2_IMPORT_LOCAL_FILE',
  'SKILLS_CREATE',
  'SKILLS_LOCAL_DELETE',
  'SKILLS_LOCAL_WRITE',
  'WORKFLOW_TEMPLATES_ROLLBACK',
]);

const EXPECTED_LOCKED_RESPONSE_POLICIES = Object.freeze({
  DASHBOARD_DAG_APPLY_OPS: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/workflows/hooks/useWorkflowActions.js', symbol: 'saveScheduleAction' }, outcome: { kind: 'published-callback', target: ['notices', 'showTaskNotice'] }, regressionTest: { path: 'frontend-app/src/pages/workflows/hooks/useWorkflowActions.ignoredResult.test.js', symbol: 'ignores malformed apply-ops body and publishes schedule success' } },
  DASHBOARD_DAG_DELETE: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/workflows/hooks/useWorkflowActions.js', symbol: 'useDeleteDagAction', visibility: 'module-private' }, regressionTest: { path: 'frontend-app/src/pages/workflows/WorkflowPage.test.jsx', symbol: 'ignores the malformed delete response body after deleting a DAG' } },
  DASHBOARD_DAG_DISPATCH_NODE: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/workflows/hooks/useWorkflowActions.js', symbol: 'dispatchDagNodeAction' }, outcome: { kind: 'published-callback', target: ['notices', 'showTaskNotice'] }, regressionTest: { path: 'frontend-app/src/pages/workflows/hooks/useWorkflowActions.ignoredResult.test.js', symbol: 'ignores malformed dispatch body and publishes dispatch success' } },
  DASHBOARD_DAG_TERMINATE: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/workflows/hooks/useWorkflowActions.js', symbol: 'stopSelectedDagAction' }, outcome: { kind: 'published-callback', target: ['notices', 'showTaskNotice'] }, regressionTest: { path: 'frontend-app/src/pages/workflows/hooks/useWorkflowActions.ignoredResult.test.js', symbol: 'ignores malformed terminate body and publishes stop success' } },
  DATASOURCE_V2_DELETE: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/skills/SkillsPage.jsx', symbol: 'handleDelete', visibility: 'module-private' }, regressionTest: { path: 'frontend-app/src/pages/skills/SkillsPage.test.jsx', symbol: 'ignores RPC response body for datasource delete' } },
  DATASOURCE_V2_IMPORT_LOCAL_FILE: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/skills/SkillsPage.jsx', symbol: 'importDatasourceSelection' }, outcome: { kind: 'published-callback', target: ['ctx', 'setNotice'] }, regressionTest: { path: 'frontend-app/src/pages/skills/SkillsPage.ignoredResultActions.test.jsx', symbol: 'ignores malformed datasource import body and publishes import success' } },
  SKILLS_CREATE: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/skills/SkillsPage.jsx', symbol: 'saveSkillEditor' }, outcome: { kind: 'published-callback', target: ['ctx', 'setNotice'] }, regressionTest: { path: 'frontend-app/src/pages/skills/SkillsPage.ignoredResultActions.test.jsx', symbol: 'ignores malformed create-skill body and publishes save success' } },
  SKILLS_LOCAL_DELETE: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/skills/SkillsPage.jsx', symbol: 'confirmDeleteSkill' }, outcome: { kind: 'published-callback', target: ['ctx', 'setNotice'] }, regressionTest: { path: 'frontend-app/src/pages/skills/SkillsPage.ignoredResultActions.test.jsx', symbol: 'ignores malformed delete-skill body and publishes delete success' } },
  SKILLS_LOCAL_WRITE: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/skills/SkillsPage.jsx', symbol: 'saveSkillEditor' }, outcome: { kind: 'published-callback', target: ['ctx', 'setNotice'] }, regressionTest: { path: 'frontend-app/src/pages/skills/SkillsPage.ignoredResultActions.test.jsx', symbol: 'ignores malformed write-skill body and publishes save success' } },
  UI_LOG: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/shared/api/wails/wailsBridgeRpc.js', symbol: 'sendFrontendLogBatch' }, regressionTest: { path: 'frontend-app/src/shared/api/wailsBridge.test.js', symbol: 'propagates frontend log batch RPC failures' } },
  UI_PREFERENCES_GET: { kind: 'consumer-validated', consumer: { path: 'frontend-app/src/shared/api/preferenceResponseGuards.js', symbol: 'getValidatedPreference' }, shape: { path: 'frontend-app/src/shared/api/preferenceResponseGuards.js', symbol: 'assertPreferenceResponseShape' }, regressionTest: { path: 'frontend-app/src/shared/api/preferenceResponseGuards.test.js', symbol: 'rejects malformed UI_PREFERENCES_GET response before returning it' } },
  TURN_INTERRUPT: { kind: 'result-handled', consumer: { path: 'frontend-app/src/entities/client/model/threadLifecycleRuntime.js', symbol: 'attachActiveThreadRpcRuntime' }, handler: { path: 'frontend-app/src/entities/client/model/threadLifecycleRuntime.js', symbol: 'notifyThreadActionFailure' }, regressionTest: { path: 'frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js', symbol: 'reports interrupt ok:false as warning without showing success' } },
  WORKFLOW_TEMPLATES_ROLLBACK: { kind: 'ignored-result', consumer: { path: 'frontend-app/src/pages/workflows/components/WorkflowEnterpriseTemplates.jsx', symbol: 'rollbackTemplate', visibility: 'module-private' }, regressionTest: { path: 'frontend-app/src/pages/workflows/WorkflowPage.enterprise.test.jsx', symbol: 'filters templates by search and shows version trust compatibility and rollback' } },
});

const EXPECTED_UNUSED_RESPONSE_POLICIES = Object.freeze([
  'APP_UPDATE_DOWNLOAD',
  'CRONJOB_CREATE',
  'CRONJOB_DELETE',
  'CRONJOB_GET',
  'CRONJOB_LIST_RUNS',
  'CRONJOB_RUN_ONCE',
  'CRONJOB_SET_ENABLED',
  'CRONJOB_UPDATE',
  'DASHBOARD_DAGS',
  'DATASOURCE_V2_CREATE',
  'MCP_TOOL_LIFECYCLE_EXPORT',
  'MCP_TOOL_LIFECYCLE_LIST',
  'MCP_TOOL_LIFECYCLE_SET',
  'OBSERVABILITY_STATUS',
  'PROMPT_SECTIONS_DELETE',
  'PROMPT_SECTIONS_LIST',
  'PROMPT_SECTIONS_WRITE',
  'SKILL_TOOLS_CREATE',
  'SKILL_TOOLS_DELETE',
  'SKILL_TOOLS_GET',
  'SKILL_TOOLS_UPDATE',
  'THREAD_LIST_PAGE',
  'THREAD_LOADED_LIST_PAGE',
  'UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL',
  'UI_PREFERENCES_GET_ALL',
]);

describe('backend API contract matrix', () => {
  it('uses structured response policies with the locked final 140-entry partition', () => {
    const entries = Object.values(RPC_CONTRACT_REGISTRY);
    const validators = entries
      .filter((entry) => entry.responseValidator !== '')
      .map((entry) => [entry.key, entry.responseValidator])
      .sort(([left], [right]) => left.localeCompare(right));
    const policies = entries.filter((entry) => entry.responsePolicy != null);
    const unusedKeys = policies
      .filter((entry) => entry.responsePolicy.kind === 'unused')
      .map((entry) => entry.key)
      .sort();
    const newIgnoredKeys = policies
      .filter((entry) => entry.responsePolicy.kind === 'ignored-result')
      .map((entry) => entry.key)
      .filter((key) => key !== 'UI_LOG')
      .sort();
    const p2WithoutMandatoryGovernance = entries.filter((entry) => (
      entry.level === 'P2'
      && entry.responseValidator === ''
      && entry.responsePolicy == null
    ));
    expect(entries).toHaveLength(140);
    expect(validators).toEqual(EXPECTED_RESPONSE_VALIDATORS);
    expect(validators).toHaveLength(98);
    expect(EXPECTED_NEW_RESPONSE_VALIDATORS).toHaveLength(69);
    expect(policies).toHaveLength(38);
    expect(unusedKeys).toEqual(EXPECTED_UNUSED_RESPONSE_POLICIES);
    expect(RPC_CONTRACT_REGISTRY.UI_LOG.responsePolicy.kind).toBe('ignored-result');
    expect(RPC_CONTRACT_REGISTRY.TURN_INTERRUPT.responsePolicy.kind).toBe('result-handled');
    expect(newIgnoredKeys).toEqual(EXPECTED_NEW_IGNORED_RESPONSE_POLICIES);
    expect(RPC_CONTRACT_REGISTRY.UI_PREFERENCES_GET.responsePolicy.kind).toBe('consumer-validated');
    for (const [key, expectedPolicy] of Object.entries(EXPECTED_LOCKED_RESPONSE_POLICIES)) {
      expect(RPC_CONTRACT_REGISTRY[key].responsePolicy).toEqual(expectedPolicy);
    }
    const publishedCallbackKeys = entries
      .filter((entry) => entry.responsePolicy?.outcome?.kind === 'published-callback')
      .map((entry) => entry.key)
      .sort();
    expect(publishedCallbackKeys).toEqual([
      'DASHBOARD_DAG_APPLY_OPS',
      'DASHBOARD_DAG_DISPATCH_NODE',
      'DASHBOARD_DAG_TERMINATE',
      'DATASOURCE_V2_IMPORT_LOCAL_FILE',
      'SKILLS_CREATE',
      'SKILLS_LOCAL_DELETE',
      'SKILLS_LOCAL_WRITE',
    ]);
    for (const key of publishedCallbackKeys) {
      expect(Object.isFrozen(RPC_CONTRACT_REGISTRY[key].responsePolicy.outcome)).toBe(true);
      expect(Object.isFrozen(RPC_CONTRACT_REGISTRY[key].responsePolicy.outcome.target)).toBe(true);
    }
    expect(p2WithoutMandatoryGovernance).toHaveLength(4);
    expect(28 + EXPECTED_NEW_RESPONSE_VALIDATORS.length + newIgnoredKeys.length + 1).toBe(108);
    expect(EXPECTED_NEW_RESPONSE_VALIDATORS.length + newIgnoredKeys.length + 1).toBe(80);
    expect(JSON.stringify(RPC_CONTRACT_REGISTRY)).not.toContain('responsePassthroughReason');
    expect(JSON.stringify(RPC_CONTRACT_REGISTRY)).not.toContain('response is consumed unchanged by');

    for (const entry of entries.filter(({ level }) => level === 'P0' || level === 'P1')) {
      expect((entry.responseValidator !== '') !== (entry.responsePolicy != null)).toBe(true);
    }
  });

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
        notes: expect.any(Array),
      }));
      expect(entry).toHaveProperty('responsePolicy');
      expect(Object.values(RPC_CONTRACT_LEVELS)).toContain(entry.level);
      expect(entry.facade).not.toBe('');
      expect(entry.backendOwner).not.toBe('');
      expect(entry.tests.length).toBeGreaterThan(0);
      expect(entry.rawLiteralRpc).toBe(false);
      if (entry.level === 'P0' || entry.level === 'P1') {
        const hasResponseValidator = entry.responseValidator.trim() !== '';
        const hasResponsePolicy = entry.responsePolicy != null;
        expect(hasResponseValidator).not.toBe(hasResponsePolicy);
      }
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
    expect(RPC_CONTRACT_REGISTRY.TURN_INTERRUPT.responsePolicy.kind).toBe('result-handled');
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
        tests: expect.arrayContaining(expected.tests),
      }));
    }
  });
});
