import { expect, it, vi } from 'vitest';
import {
  checkAppUpdate,
  createBackendApi,
  createAndStartDag,
  createDatasourceDocument,
  deleteDatasourceDocument,
  downloadAppUpdate,
  emitFrontendTraceEvent,
  getDatasourceDocument,
  getPromptHistory,
  importDatasourceLocalFile,
  installAppUpdate,
  installLatestAppUpdate,
  listDatasourceChunks,
  listDatasourceDocuments,
  listMCPToolLifecycle,
  listMCPServers,
  listToolbridgeTools,
  exportMCPToolLifecycle,
  getVideoApiKey,
  RPC_METHODS,
  rollbackWorkflowTemplate,
  saveWorkflowTemplate,
  setMCPToolLifecycle,
  setVideoApiKey,
  startPlaywrightMCPServer,
  startSQLiteMCPServer,
  stopPlaywrightMCPServer,
  stopSQLiteMCPServer,
  updateDatasourceDocument,
  writeWorkflowMaterial,
} from './backendApi.js';

function expectInvalidInputDoesNotCall(callAPI, action, message) {
  const callCount = callAPI.mock.calls.length;
  expect(action).toThrow(message);
  expect(callAPI).toHaveBeenCalledTimes(callCount);
}

function runtimeConfigResponse(overrides = {}) {
  return {
    model: 'gpt-5.5',
    modelProvider: null,
    cwd: '/repo/app',
    approvalPolicy: 'on-failure',
    sandbox: 'workspace-write',
    config: null,
    baseInstructions: null,
    developerInstructions: null,
    personality: null,
    toolRouting: {
      mode: 'legacy',
      routerModel: '',
      routerProvider: 'openai_compatible',
      routerBaseURL: '',
      routerHasAPIKey: false,
      confidenceThreshold: 0.65,
      timeoutSec: 8,
    },
    ...overrides,
  };
}

function builtinToolsResponse() {
  return { tools: [{ id: 'Shell', label: 'Shell', enabled: true }] };
}

function windowBootstrapResponse() {
  return { snapshot: {} };
}

function sidebarStateResponse(overrides = {}) {
  return {
    threads: [{
      id: 'thread-1',
      name: 'Main',
      agent_id: 'agent-1',
      createdAt: '2026-07-13T00:00:00Z',
      updatedAt: '2026-07-13T00:00:01Z',
      lifecycleStatus: 'active',
      state: 'running',
      threadStatus: 'running',
      agentState: 'working',
      lastMessage: 'Working',
      overlayText: 'Running',
      overlayType: 'status',
      overlayPriority: 1,
    }],
    agents: [{
      id: 'agent-1',
      name: 'Main agent',
      thread_id: 'thread-1',
      provider_thread_id: 'provider-thread-1',
      parent_id: '', parentAgentId: '', assignment: { title: 'Main agent', description: 'Run the main task', assignedAt: '2026-07-13T00:00:00Z' }, progress: { status: 'turn_running', currentStep: null, completedSteps: null, totalSteps: null, updatedAt: '2026-07-13T00:00:01Z' }, outcome: null,
      state: 'running',
      provider: 'codex',
      model: 'gpt-5.5',
      cwd: '/repo/app',
      port: 8090,
      logPath: '/tmp/agent.log',
      createdAt: '2026-07-13T00:00:00Z',
      updatedAt: '2026-07-13T00:00:01Z',
      last_report: 'Working',
      agentState: 'working',
      threadStatus: 'running',
      lastMessage: 'Working',
    }],
    active_turn: {
      id: 'turn-1',
      agent_id: 'agent-1',
      thread_id: 'thread-1',
      status: 'running',
      success: true,
      error: '',
      reason: '',
      started_at: '2026-07-13T00:00:00Z',
      completed_at: '2026-07-13T00:00:01Z',
    },
    recent_turns: [],
    workspace: {
      runs: [{
        run_key: 'run-1',
        dag_key: 'dag-1',
        status: 'running',
        source_root: '/repo/app',
        workspace_path: '/repo/worktree',
        created_by: 'agent-1',
        updated_by: 'agent-1',
        merged_file_count: 1,
        conflicts: 0,
        errors: 0,
        message: 'Working',
        updated_at: '2026-07-13T00:00:01Z',
      }],
    },
    token_usage: {
      inputTokens: 1,
      outputTokens: 2,
      totalTokens: 3,
      usedTokens: 3,
      contextWindowTokens: 128000,
      usedPercent: 0.01,
    },
    statuses: { 'thread-1': 'running' },
    interruptibleByThread: { 'thread-1': true },
    statusHeadersByThread: { 'thread-1': 'Running' },
    statusDetailsByThread: { 'thread-1': 'Working' },
    agentRuntimeById: { 'agent-1': { pid: 42 } },
    activeThreadId: 'thread-1',
    activeCmdThreadId: 'thread-1',
    mainAgentId: 'agent-1',
    'viewPrefs.chat': { density: 'compact' },
    'viewPrefs.cmd': { wrap: true },
    'threadPins.chat': { 'thread-1': 1 },
    'threadArchives.chat': { 'thread-2': 2 },
    groups: [{ key: 'active', title: 'Active', threads: [{ id: 'thread-1' }] }],
    ...overrides,
  };
}

function frontendIngestResponse() {
  return { enabled: true, recorded: 1, dropped: 0 };
}

function openWindowResponse() {
  return { ok: true, windowId: 'window-2', cwd: '/repo/app' };
}

function codeSaveResponse() {
  return { ok: true, filePath: '/repo/app/src/App.jsx', relative: 'src/App.jsx', totalLines: 1, contentVersion: 'sha256:app-v2' };
}

function projectsStateResponse() {
  return { projects: ['/repo/app'], active: '/repo/app' };
}

function okResponse() {
  return { ok: true };
}

function modelProviderRegistryResponse() {
  return { activeVendorId: '', vendors: [] };
}

function dashboardPageResponse() {
  return {
    agents: [],
    dags: [],
    commandCards: [],
    finalOutputRefs: [],
    sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
  };
}

function videoApiKeyStatusResponse() {
  return { configured: false, masked: '' };
}

function dashboardLogsResponse() {
  return { logs: [] };
}

function threadConfigResponse(overrides = {}) {
  return {
    threadId: 'thread-1',
    provider: 'codex',
    supportsThreadOverride: true, availableModels: ['gpt-5.5', 'gpt-5.4'],
    override: { model: 'gpt-5.5', effort: 'high', approvals: 'on-request' },
    effective: { model: 'gpt-5.5', effort: 'high', approvals: 'on-request' },
    ...overrides,
  };
}

function threadCompactResponse(overrides = {}) {
  return {
    threadId: 'thread-1',
    command: '/compact',
    beforeTokens: 1200,
    afterTokens: 640,
    compacted: true,
    ...overrides,
  };
}

function threadRecoverResponse(overrides = {}) {
  return {
    thread: { id: 'thread-1', status: 'recovering' },
    recovered: true,
    mode: 'relaunch_resume',
    ...overrides,
  };
}

function dashboardDagNode(overrides = {}) {
  return {
    id: 11,
    dag_key: 'dag-1',
    node_key: 'draft',
    title: 'Draft',
    status: 'ready',
    created_at: '2026-07-13T00:00:00Z',
    updated_at: '2026-07-13T00:00:01Z',
    ...overrides,
  };
}

function dashboardDagSummary(overrides = {}) {
  return {
    id: 7,
    dag_key: 'dag-1',
    version: 3,
    title: 'Release workflow',
    status: 'active',
    schedule_enabled: false,
    created_at: '2026-07-13T00:00:00Z',
    updated_at: '2026-07-13T00:00:01Z',
    ...overrides,
  };
}

function dashboardDagRun(overrides = {}) {
  return {
    id: 31,
    run_key: 'run-1',
    dag_key: 'dag-1',
    dag_version_snapshot: 3,
    status: 'running',
    started_at: '2026-07-13T00:00:00Z',
    budget_used: 2,
    created_at: '2026-07-13T00:00:00Z',
    updated_at: '2026-07-13T00:00:01Z',
    ...overrides,
  };
}

function workflowTemplateSummary(overrides = {}) {
  return {
    id: 'government-enterprise/meeting-minutes',
    version: 2,
    title: { zh: '会议纪要', en: 'Meeting minutes' },
    description: { zh: '生成会议纪要' },
    category: 'government-enterprise',
    business_flow: 'meeting-review',
    output_types: ['docx'],
    tags: ['meeting'],
    estimated_nodes: 2,
    requires_review: true,
    supports_schedule: false,
    final_node_key: 'final',
    trust: { level: 'builtin', source: 'repository' },
    compatibility: { runtime: 'dag-v2', node_types: ['agent'], required_capabilities: [] },
    available_versions: [1, 2],
    ...overrides,
  };
}

function workflowTemplateDraft(overrides = {}) {
  return {
    template_id: 'government-enterprise/meeting-minutes',
    template_version: 2,
    dag_key: 'meeting-minutes',
    title: '会议纪要',
    description: '生成会议纪要',
    trigger: 'manual',
    final_node_key: 'final',
    review_node_key: 'review',
    nodes: [{
      node_key: 'draft',
      title: '起草',
      node_type: 'agent',
      assigned_to: 'codex',
      depends_on: [],
      config: {},
    }],
    final_output: { node_key: 'final', kind: 'file', path_template: 'reports/final.docx' },
    metadata: {},
    ...overrides,
  };
}

function workflowTemplateDetail(overrides = {}) {
  return {
    id: 'government-enterprise/meeting-minutes',
    version: 2,
    title: { zh: '会议纪要', en: 'Meeting minutes' },
    description: { zh: '生成会议纪要' },
    category: 'government-enterprise',
    business_flow: 'meeting-review',
    output_types: ['docx'],
    tags: ['meeting'],
    estimated_nodes: 2,
    requires_review: true,
    supports_schedule: false,
    trust: { level: 'builtin', source: 'repository' },
    compatibility: { runtime: 'dag-v2', node_types: ['agent'], required_capabilities: [] },
    ui_schema: [],
    dag_template: {
      dag_key_template: 'meeting-minutes',
      title_template: '会议纪要',
      description_template: '生成会议纪要',
      trigger: 'manual',
      final_node_key: 'final',
      nodes: [],
    },
    validation: { require_review_before_final: true, require_final_node_key: true },
    final_output: { node_key: 'final', kind: 'file', path_template: 'reports/final.docx' },
    ...overrides,
  };
}

function guardedBackendResponse(method) {
  if (method === RPC_METHODS.CRONJOB_LIST) return { jobs: [], next_cursor: '', has_more: false };
  if (method === RPC_METHODS.TOOLBRIDGE_TOOLS_LIST) return { tools: [] };
  if (method === RPC_METHODS.THREAD_PROMPT_HISTORY) return { entries: [], nextCursor: '', hasMore: false, nonce: 'nonce-1' };
  if (method === RPC_METHODS.CONFIG_READ) return runtimeConfigResponse();
  if (method === RPC_METHODS.CONFIG_BUILTIN_TOOLS_READ || method === RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE) return builtinToolsResponse();
  if (method === RPC_METHODS.UI_WINDOW_BOOTSTRAP_GET) return windowBootstrapResponse();
  if (method === RPC_METHODS.UI_SIDEBAR_GET) return sidebarStateResponse();
  if (method === RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST) return frontendIngestResponse();
  if (method === RPC_METHODS.UI_OPEN_NEW_WINDOW) return openWindowResponse();
  if (method === RPC_METHODS.UI_CODE_SAVE) return codeSaveResponse();
  if ([RPC_METHODS.UI_PROJECTS_GET, RPC_METHODS.UI_PROJECTS_SET_ACTIVE, RPC_METHODS.UI_PROJECTS_ADD, RPC_METHODS.UI_PROJECTS_REMOVE].includes(method)) return projectsStateResponse();
  if (method === RPC_METHODS.UI_PREFERENCES_SET || method === RPC_METHODS.UI_VIDEO_SET_API_KEY) return okResponse();
  if (method === RPC_METHODS.UI_DASHBOARD_GET) return dashboardPageResponse();
  if (method === RPC_METHODS.UI_VIDEO_GET_API_KEY) return videoApiKeyStatusResponse();
  if (method === RPC_METHODS.DASHBOARD_LOGS) return dashboardLogsResponse();
  if (method === RPC_METHODS.DASHBOARD_SHARED_FILES) return { files: [], finalOutputRefs: [], sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 } };
  if (method === RPC_METHODS.MODEL_PROVIDERS_APPLY || method === RPC_METHODS.MODEL_PROVIDERS_LIST || method === RPC_METHODS.MODEL_PROVIDERS_SAVE) return modelProviderRegistryResponse();
  if (
    method === RPC_METHODS.OBSERVABILITY_ERROR_LIST
    || method === RPC_METHODS.OBSERVABILITY_RECENT_LIST
    || method === RPC_METHODS.OBSERVABILITY_SLOW_LIST
    || method === RPC_METHODS.OBSERVABILITY_THREAD_RECENT
    || method === RPC_METHODS.OBSERVABILITY_TRACE_GET
  ) return { source: 'buffer', events: [] };
  if (method === RPC_METHODS.UI_STATE_GET) return { threads: [], agents: [], token_usage: {} };
  if (method === RPC_METHODS.THREAD_MESSAGES) return { messages: [], total: 0, hasMore: false, nextBefore: '' };
  if (method === RPC_METHODS.THREAD_RESOLVE) return { id: 'thread-2' };
  if ([RPC_METHODS.THREAD_ARCHIVE, RPC_METHODS.THREAD_UNARCHIVE, RPC_METHODS.THREAD_DELETE, RPC_METHODS.THREAD_NAME_SET, RPC_METHODS.APPROVAL_RESPOND].includes(method)) return null;
  if (method === RPC_METHODS.THREAD_CONFIG_GET || method === RPC_METHODS.THREAD_CONFIG_SET) return threadConfigResponse();
  if (method === RPC_METHODS.THREAD_COMPACT_START) return threadCompactResponse();
  if (method === RPC_METHODS.THREAD_RECOVER) return threadRecoverResponse();
  if (method === RPC_METHODS.THREAD_START) return { threadId: 'thread-123', status: 'running' };
  if (method === RPC_METHODS.TURN_START) return { turn_id: 'turn-1' };
  if (method === RPC_METHODS.TURN_FORCE_COMPLETE) return { ok: true, forceCompleted: true };
  if (method === RPC_METHODS.DASHBOARD_DAG_START) return { runKey: 'run-1' };
  if (method === RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START) return { dagKey: 'dag-created', runKey: 'run-created' };
  if (method === RPC_METHODS.DASHBOARD_DAG_DETAIL) return { dag: dashboardDagSummary(), nodes: [dashboardDagNode()] };
  if (method === RPC_METHODS.DASHBOARD_DAG_RUNS) return { runs: [dashboardDagRun()] };
  if (method === RPC_METHODS.DASHBOARD_DAG_RUN) return { run: dashboardDagRun(), nodes: [dashboardDagNode()] };
  if (method === RPC_METHODS.DASHBOARD_WORKFLOW_MATERIAL_WRITE) return { path: 'reports/workflows/uploads/dag-1/material.md' };
  return { ok: true };
}

  it('exposes the dedicated frontend observability ingest RPC method name', () => {
    expect(RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST).toBe('observability/frontend/ingest');
    expect(typeof emitFrontendTraceEvent).toBe('function');
  });

  it('maps observability query helpers to dedicated RPC methods', async () => {
    const response = { source: 'buffer', events: [{ traceId: 'trace-1' }] };
    const callAPI = vi.fn().mockResolvedValue(response);
    const api = createBackendApi({ callAPI });

    await expect(api.getObservabilityTrace({ trace_id: 'trace-1', limit: 5 })).resolves.toMatchObject({ source: 'buffer', events: [expect.objectContaining({ traceId: 'trace-1' })] });
    await api.getObservabilityThreadRecent({ thread_id: 'thread-1', limit: 7 });
    await api.listObservabilityRecent({ limit: 20, status: 'error', component: 'frontend', keyword: 'thread/start', includeTail: false });
    await api.listObservabilitySlow({ component: 'rpc' });
    await api.listObservabilityErrors({ limit: 3 });
    await api.getObservabilityStatus();

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_TRACE_GET, { traceId: 'trace-1', limit: 5 });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_THREAD_RECENT, { threadId: 'thread-1', limit: 7 });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_RECENT_LIST, { limit: 20, status: 'error', component: 'frontend', keyword: 'thread/start', includeTail: false });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_SLOW_LIST, { component: 'rpc' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_ERROR_LIST, { limit: 3 });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_STATUS, {});
  });

  it('rejects malformed registered dashboard response boundaries', async () => {
    const callAPI = vi.fn((method) => {
      if (method === RPC_METHODS.DASHBOARD_SHARED_FILES) return Promise.resolve({ files: null });
      if (method === RPC_METHODS.MODEL_PROVIDERS_LIST) return Promise.resolve(null);
      if (method === RPC_METHODS.OBSERVABILITY_TRACE_GET) return Promise.resolve(null);
      return Promise.resolve({ ok: true });
    });
    const api = createBackendApi({ callAPI });

    await expect(api.listSharedFiles()).rejects.toThrow(/shared files dashboard response files must be an array/);
    await expect(api.listModelProviders({ cwd: '/repo/app' })).rejects.toThrow(/model provider registry/);
    await expect(api.getObservabilityTrace({ traceId: 'trace-1' })).rejects.toThrow(/observability response must be an object/);
  });

  it('fails fast without extra backend calls for representative invalid facade inputs', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.getObservabilityTrace({ trace_id: '' }), 'traceId is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.startThread({ cwd: '', modelProvider: 'codex' }), 'cwd is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.startThread({ cwd: '/repo/app' }), 'provider is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.startTurn({ cwd: '/repo/app', threadId: '', input: 'build it' }), 'threadId is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.createAndStartDag({ dagKey: 'dag-1', title: 'Dag', nodes: [] }), 'nodes must be a non-empty array');
    expectInvalidInputDoesNotCall(callAPI, () => api.dispatchDagNode({ dagKey: 'dag-1', runId: 88, nodeKey: 'draft', assignedTo: '' }), 'assignedTo is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.applyDagOps({ dagKey: 'dag-1', ops: [] }), 'baseVersion is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.setVideoApiKey({ apiKey: '' }), 'apiKey is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.listModelProviders({ cwd: '' }), 'cwd is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.saveModelProviders({ registry: { vendors: [] } }), 'cwd is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.applyModelProvider({ vendorId: 'openrouter' }), 'cwd is required');
  });

  it('wraps datasource_v2 CRUD RPC methods with strict payloads', async () => {
    const document = {
      documentId: 101, sourcePath: 'C:\\data\\alpha.txt', fileName: 'alpha.txt',
      extension: '.txt', sizeBytes: 42, contentHash: 'hash', chunkCount: 1,
      totalChars: 5, status: 'ready', errorMessage: '', qualityStatus: 'passed', qualityReason: '', extractorName: 'utf8-text', extractorVersion: 'v1', pageCount: 1, runeCount: 5, visibleRunes: 5, controlRunes: 0, nulRunes: 0, replacementRunes: 0, unmappedFonts: 0,
      createdAt: '2026-07-13T00:00:00Z', updatedAt: '2026-07-13T00:00:00Z',
    };
    const chunk = {
      id: 1, documentId: 101, chunkIndex: 0, content: 'alpha', charCount: 5,
      byteCount: 5, embeddingModel: '', embeddingDim: 0, tokenCount: 1,
      createdAt: '2026-07-13T00:00:00Z',
    };
    const callAPI = vi.fn((method) => Promise.resolve({
      [RPC_METHODS.DATASOURCE_V2_LIST]: { documents: [document] },
      [RPC_METHODS.DATASOURCE_V2_GET]: { document, chunks: [chunk], hasMore: false, nextCursor: 1 },
      [RPC_METHODS.DATASOURCE_V2_LIST_CHUNKS]: { chunks: [chunk], hasMore: false, nextCursor: 1 },
      [RPC_METHODS.DATASOURCE_V2_UPDATE]: document,
    }[method] ?? { ok: true }));
    const api = createBackendApi({ callAPI });
    await api.createDatasourceDocument({ source_path: ' C:\\data\\alpha.txt ' });
    await api.listDatasourceDocuments({ keyword: 'alpha', limit: '25' });
    await api.getDatasourceDocument({ document_id: '101' });
    await api.listDatasourceChunks({ document_id: '101', limit: '2', cursor: 0 });
    await api.updateDatasourceDocument({
      documentId: 101,
      sourcePath: ' C:\\data\\alpha-renamed.txt ',
      fileName: ' alpha-renamed.txt ',
      extension: ' .txt ',
      sizeBytes: '42',
    });
    await api.deleteDatasourceDocument({ id: 101 });
    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.DATASOURCE_V2_CREATE, {
      sourcePath: 'C:\\data\\alpha.txt',
    });
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.DATASOURCE_V2_LIST, {
      keyword: 'alpha',
      limit: 25,
    });
    expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.DATASOURCE_V2_GET, {
      documentId: 101,
    });
    expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.DATASOURCE_V2_LIST_CHUNKS, {
      documentId: 101,
      limit: 2,
      cursor: 0,
    });
    expect(callAPI).toHaveBeenNthCalledWith(5, RPC_METHODS.DATASOURCE_V2_UPDATE, {
      documentId: 101,
      sourcePath: 'C:\\data\\alpha-renamed.txt',
      fileName: 'alpha-renamed.txt',
      extension: '.txt',
      sizeBytes: 42,
    });
    expect(callAPI).toHaveBeenNthCalledWith(6, RPC_METHODS.DATASOURCE_V2_DELETE, {
      documentId: 101,
    });
    expectInvalidInputDoesNotCall(callAPI, () => api.createDatasourceDocument({ sourcePath: '' }), 'sourcePath is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.listDatasourceDocuments({}), 'limit must be a positive integer');
    expectInvalidInputDoesNotCall(callAPI, () => api.getDatasourceDocument({ documentId: 0 }), 'documentId is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.listDatasourceChunks({ documentId: 101, limit: 2 }), 'cursor is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.updateDatasourceDocument({ documentId: 101, sourcePath: 'C:\\data\\a.txt', sizeBytes: 1 }), 'fileName is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.deleteDatasourceDocument({ documentId: '' }), 'documentId is required');
    expect(typeof createDatasourceDocument).toBe('function');
    expect(typeof listDatasourceDocuments).toBe('function');
    expect(typeof getDatasourceDocument).toBe('function');
    expect(typeof listDatasourceChunks).toBe('function');
    expect(typeof updateDatasourceDocument).toBe('function');
    expect(typeof deleteDatasourceDocument).toBe('function');
  });

  it('maps user-selected datasource imports to the local file RPC', async () => {
    const callAPI = vi.fn().mockResolvedValue({ documentId: 2, sourcePath: 'D:\\new\\fj.txt', fileName: 'fj.txt', extension: '.txt', sizeBytes: 2, contentHash: 'sha256:new', chunkCount: 1, totalChars: 2, status: 'ready', qualityStatus: 'passed', qualityReason: '', extractorName: 'utf8-text', extractorVersion: 'v1', pageCount: 1, runeCount: 2, visibleRunes: 2, controlRunes: 0, nulRunes: 0, replacementRunes: 0, unmappedFonts: 0 });
    const api = createBackendApi({ callAPI });
    await api.importDatasourceLocalFile({ source_path: ' D:\\new\\fj.txt ', picker_token: ' picker-token ' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DATASOURCE_V2_IMPORT_LOCAL_FILE, {
      sourcePath: 'D:\\new\\fj.txt',
      pickerToken: 'picker-token',
    });
    expectInvalidInputDoesNotCall(callAPI, () => api.importDatasourceLocalFile({ sourcePath: '' }), 'sourcePath is required');
    expect(typeof importDatasourceLocalFile).toBe('function');
  });

  it('wraps app update RPC methods', async () => {
    const callAPI = vi.fn()
      .mockResolvedValueOnce({ ok: true })
      .mockResolvedValueOnce({ ok: true })
      .mockResolvedValueOnce({ started: true, helper: 'updater' })
      .mockResolvedValueOnce({ started: true, helper: 'updater' });
    const api = createBackendApi({ callAPI });

    await api.checkAppUpdate();
    await api.downloadAppUpdate();
    await api.installAppUpdate();
    await api.installLatestAppUpdate();

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.APP_UPDATE_CHECK, {});
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.APP_UPDATE_DOWNLOAD, {});
    expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.APP_UPDATE_INSTALL, {});
    expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.APP_UPDATE_INSTALL_LATEST, {});
    expect(typeof checkAppUpdate).toBe('function');
    expect(typeof downloadAppUpdate).toBe('function');
    expect(typeof installAppUpdate).toBe('function');
    expect(typeof installLatestAppUpdate).toBe('function');
  });

  it('rejects malformed app update recovery data with a fixed generic error', async () => {
    const secret = 'secret helper output at /Users/alice/update.dmg';
    const failure = new Error(secret);
    failure.data = {
      code: 'UPDATE_SIGNATURE_INVALID',
      retryable: false,
      action: 'preserve_state_export_diagnostics',
      transaction_id: '',
      raw_output: secret,
    };
    const api = createBackendApi({ callAPI: vi.fn().mockRejectedValue(failure) });

    await expect(api.checkAppUpdate()).rejects.toThrow('请求失败，恢复信息无效。');
  });

  it('preserves valid app update recovery data for fixed UI mapping', async () => {
    const failure = new Error('secret verifier output');
    failure.data = {
      code: 'UPDATE_SIGNATURE_INVALID',
      retryable: false,
      action: 'preserve_state_export_diagnostics',
      transaction_id: '',
    };
    const api = createBackendApi({ callAPI: vi.fn().mockRejectedValue(failure) });

    await expect(api.installLatestAppUpdate()).rejects.toBe(failure);
  });

  it('rejects malformed app update install responses', async () => {
    const invalidResponses = [
      {},
      null,
      { ok: true },
      { started: false, helper: 'updater' },
      { started: true, helper: '' },
    ];
    for (const response of invalidResponses) {
      const callAPI = vi.fn().mockResolvedValue(response);
      const api = createBackendApi({ callAPI });

      await expect(api.installAppUpdate()).rejects.toThrow('app/update/install');
      await expect(api.installLatestAppUpdate()).rejects.toThrow('app/update/installLatest');
    }
  });

  it('wraps MCP server list and default controls with strict empty payloads', async () => {
    const listResponse = { configPath: '/repo/.agent/mcp_server/config.json', mcpServers: { sqlite: { enabled: false } } };
    const startResponse = { configPath: '/repo/.agent/mcp_server/config.json', serverName: 'sqlite', enabled: true };
    const stopResponse = { configPath: '/repo/.agent/mcp_server/config.json', serverName: 'sqlite', enabled: false };
    const playwrightStartResponse = { configPath: '/repo/.agent/mcp_server/config.json', serverName: 'playwright', enabled: true };
    const playwrightStopResponse = { configPath: '/repo/.agent/mcp_server/config.json', serverName: 'playwright', enabled: false };
    const callAPI = vi.fn()
      .mockResolvedValueOnce(listResponse)
      .mockResolvedValueOnce(startResponse)
      .mockResolvedValueOnce(stopResponse)
      .mockResolvedValueOnce(playwrightStartResponse)
      .mockResolvedValueOnce(playwrightStopResponse);
    const api = createBackendApi({ callAPI });

    await expect(api.listMCPServers()).resolves.toEqual(listResponse);
    await expect(api.startSQLiteMCPServer()).resolves.toEqual(startResponse);
    await expect(api.stopSQLiteMCPServer()).resolves.toEqual(stopResponse);
    await expect(api.startPlaywrightMCPServer()).resolves.toEqual(playwrightStartResponse);
    await expect(api.stopPlaywrightMCPServer()).resolves.toEqual(playwrightStopResponse);

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.MCP_SERVER_LIST, {});
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.MCP_SERVER_SQLITE_START, {});
    expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.MCP_SERVER_SQLITE_STOP, {});
    expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.MCP_SERVER_PLAYWRIGHT_START, {});
    expect(callAPI).toHaveBeenNthCalledWith(5, RPC_METHODS.MCP_SERVER_PLAYWRIGHT_STOP, {});
    expect(typeof listMCPServers).toBe('function');
    expect(typeof startSQLiteMCPServer).toBe('function');
    expect(typeof stopSQLiteMCPServer).toBe('function');
    expect(typeof startPlaywrightMCPServer).toBe('function');
    expect(typeof stopPlaywrightMCPServer).toBe('function');
  });

  it('rejects MCP server public responses that include config details', async () => {
    const leakedListAPI = createBackendApi({
      callAPI: vi.fn().mockResolvedValue({
        configPath: '/repo/.agent/mcp_server/config.json',
        mcpServers: {
          sqlite: {
            enabled: true,
            headers: { Authorization: 'Bearer YOUR_API_KEY' },
          },
        },
      }),
    });

    await expect(leakedListAPI.listMCPServers()).rejects.toThrow('must not include headers');

    const leakedStartAPI = createBackendApi({
      callAPI: vi.fn().mockResolvedValue({
        configPath: '/repo/.agent/mcp_server/config.json',
        serverName: 'sqlite',
        enabled: true,
        config: { command: 'npx', args: ['@bytebase/dbhub@0.23.0'] },
      }),
    });

    await expect(leakedStartAPI.startSQLiteMCPServer()).rejects.toThrow('must not include config');
  });

  it('rejects malformed MCP server default control responses', async () => {
    for (const action of [
      'startSQLiteMCPServer',
      'stopSQLiteMCPServer',
      'startPlaywrightMCPServer',
      'stopPlaywrightMCPServer',
    ]) {
      const api = createBackendApi({
        callAPI: vi.fn().mockResolvedValue({ configPath: '/repo/.agent/mcp_server/config.json' }),
      });

      await expect(api[action]()).rejects.toThrow('serverName');
    }
  });

  it('wraps MCP tool lifecycle RPC methods with guarded canonical payloads', async () => {
    const setResponse = { serverName: 'my-search', toolName: 'remote_search', state: 'disabled' };
    const listResponse = [{ serverName: 'my-search', toolName: 'remote_search', state: 'disabled' }];
    const exportResponse = [
      { serverName: 'my-search', toolName: 'remote_search', state: 'disabled' },
      { serverName: 'my-worker', toolName: 'remote_worker', state: 'suspended' },
    ];
    const callAPI = vi.fn()
      .mockResolvedValueOnce(setResponse)
      .mockResolvedValueOnce(listResponse)
      .mockResolvedValueOnce(exportResponse);
    const api = createBackendApi({ callAPI });

    await expect(api.setMCPToolLifecycle({
      workspace_root: ' /repo ',
      server_name: ' my-search ',
      manifest_name: ' search_v1 ',
      tool_name: ' remote_search ',
      state: 'disabled',
      reason: ' manual review ',
      replacement_tool: ' remote_search_v2 ',
    })).resolves.toEqual(setResponse);
    await expect(api.listMCPToolLifecycle({
      workspaceRoot: ' /repo ',
      serverName: ' my-search ',
    })).resolves.toEqual(listResponse);
    await expect(api.exportMCPToolLifecycle({
      workspace_root: ' /repo ',
    })).resolves.toEqual(exportResponse);

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.MCP_TOOL_LIFECYCLE_SET, {
      workspaceRoot: '/repo',
      serverName: 'my-search',
      manifestName: 'search_v1',
      toolName: 'remote_search',
      state: 'disabled',
      reason: 'manual review',
      replacementTool: 'remote_search_v2',
    });
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.MCP_TOOL_LIFECYCLE_LIST, {
      workspaceRoot: '/repo',
      serverName: 'my-search',
    });
    expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.MCP_TOOL_LIFECYCLE_EXPORT, {
      workspaceRoot: '/repo',
    });
    expect(typeof setMCPToolLifecycle).toBe('function');
    expect(typeof listMCPToolLifecycle).toBe('function');
    expect(typeof exportMCPToolLifecycle).toBe('function');
  });

  it('fails fast for invalid MCP tool lifecycle facade inputs', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.setMCPToolLifecycle([]), 'params must be an object');
    expectInvalidInputDoesNotCall(callAPI, () => api.setMCPToolLifecycle({
      toolName: 'remote_search',
      state: 'disabled',
    }), 'serverName is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.setMCPToolLifecycle({
      serverName: 'my-search',
      state: 'disabled',
    }), 'toolName is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.setMCPToolLifecycle({
      serverName: 'my-search',
      toolName: 'remote_search',
    }), 'state is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.setMCPToolLifecycle({
      serverName: 'my-search',
      toolName: 'remote_search',
      state: 'unknown',
    }), 'state must be enabled, disabled, suspended, or removed');
    expectInvalidInputDoesNotCall(callAPI, () => api.setMCPToolLifecycle({
      serverName: 'my-search',
      toolName: { name: 'remote_search' },
      state: 'disabled',
    }), 'toolName must be a string');
    expectInvalidInputDoesNotCall(callAPI, () => api.listMCPToolLifecycle({
      serverName: 'my-search',
      extra: true,
    }), 'unsupported payload field extra');
    expectInvalidInputDoesNotCall(callAPI, () => api.exportMCPToolLifecycle({
      serverName: 'my-search',
    }), 'unsupported payload field serverName');
  });

  it('wraps workflow template RPC methods with canonical payloads', async () => {
    const callAPI = vi.fn((method) => {
      if (method === RPC_METHODS.WORKFLOW_TEMPLATES_LIST) return Promise.resolve({ templates: [workflowTemplateSummary()] });
      if (method === RPC_METHODS.WORKFLOW_TEMPLATES_GET) return Promise.resolve({ template: workflowTemplateDetail() });
      if (method === RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG) return Promise.resolve({ draft: workflowTemplateDraft() });
      if (method === RPC_METHODS.WORKFLOW_TEMPLATES_SAVE) return Promise.resolve({ template: workflowTemplateSummary() });
      return Promise.resolve({ ok: true });
    });
    const api = createBackendApi({ callAPI });

    await api.listWorkflowTemplates({
      category: 'government-enterprise',
      business_flow: 'meeting-review',
      output_type: 'docx',
      supports_schedule: true,
      locale: 'zh-CN',
    });
    await api.getWorkflowTemplate({
      templateId: 'government-enterprise/meeting-minutes',
      version: 1,
    });
    await api.renderWorkflowTemplateDraft({
      templateId: 'government-enterprise/meeting-minutes',
      version: 1,
      values: { title: 'June meeting' },
      user_inputs: { reviewer: 'office' },
      runtime_context: { locale: 'zh-CN' },
      locale: 'zh-CN',
    });
    await api.saveWorkflowTemplate({
      templateId: 'government-enterprise/meeting-minutes',
      version: 2,
      category: 'government-enterprise',
      trust: { level: 'user', source: 'save_as_template' },
      compatibility: { runtime: 'dag-v2', node_types: ['agent'] },
      draft: { dag_key: 'meeting_minutes_run' },
    });
    await api.rollbackWorkflowTemplate({
      templateId: 'government-enterprise/meeting-minutes',
      version: 1,
    });

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.WORKFLOW_TEMPLATES_LIST, {
      category: 'government-enterprise',
      business_flow: 'meeting-review',
      output_type: 'docx',
      supports_schedule: true,
      locale: 'zh-CN',
    });
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.WORKFLOW_TEMPLATES_GET, {
      templateId: 'government-enterprise/meeting-minutes',
      version: 1,
    });
    expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.WORKFLOW_TEMPLATES_RENDER_DAG, {
      templateId: 'government-enterprise/meeting-minutes',
      version: 1,
      values: { title: 'June meeting' },
      user_inputs: { reviewer: 'office' },
      runtime_context: { locale: 'zh-CN' },
      locale: 'zh-CN',
    });
    expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.WORKFLOW_TEMPLATES_SAVE, {
      templateId: 'government-enterprise/meeting-minutes',
      version: 2,
      category: 'government-enterprise',
      trust: { level: 'user', source: 'save_as_template' },
      compatibility: { runtime: 'dag-v2', node_types: ['agent'] },
      draft: { dag_key: 'meeting_minutes_run' },
    });
    expect(callAPI).toHaveBeenNthCalledWith(5, RPC_METHODS.WORKFLOW_TEMPLATES_ROLLBACK, {
      templateId: 'government-enterprise/meeting-minutes',
      version: 1,
    });
    expect(typeof saveWorkflowTemplate).toBe('function');
    expect(typeof rollbackWorkflowTemplate).toBe('function');
  });

  it('fails fast for invalid workflow template facade inputs', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.getWorkflowTemplate({ templateId: '' }), 'templateId is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.renderWorkflowTemplateDraft({
      templateId: '',
    }), 'templateId is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.renderWorkflowTemplateDraft({
      templateId: 'government-enterprise/meeting-minutes',
      values: [],
    }), 'values must be an object');
    expectInvalidInputDoesNotCall(callAPI, () => api.renderWorkflowTemplateDraft({
      templateId: 'government-enterprise/meeting-minutes',
      user_inputs: [],
    }), 'user_inputs must be an object');
    expectInvalidInputDoesNotCall(callAPI, () => api.renderWorkflowTemplateDraft({
      templateId: 'government-enterprise/meeting-minutes',
      runtime_context: [],
    }), 'runtime_context must be an object');
    expectInvalidInputDoesNotCall(callAPI, () => api.saveWorkflowTemplate({
      templateId: 'government-enterprise/meeting-minutes',
      version: 0,
      category: 'government-enterprise',
      trust: {},
      compatibility: {},
      draft: {},
    }), 'version must be a positive integer');
    expectInvalidInputDoesNotCall(callAPI, () => api.saveWorkflowTemplate({
      templateId: 'government-enterprise/meeting-minutes',
      version: 2,
      category: 'government-enterprise',
      trust: [],
      compatibility: {},
      draft: {},
    }), 'trust must be an object');
    expectInvalidInputDoesNotCall(callAPI, () => api.rollbackWorkflowTemplate({
      templateId: 'government-enterprise/meeting-minutes',
      version: 0,
    }), 'version must be a positive integer');
  });

  it('rejects malformed DAG and workflow template responses', async () => {
    const missingRunKey = dashboardDagRun();
    delete missingRunKey.run_key;
    const missingTemplateId = workflowTemplateSummary();
    delete missingTemplateId.id;
    const missingDraft = { template_id: 'government-enterprise/meeting-minutes' };
    const missingPersistedVersion = workflowTemplateSummary();
    delete missingPersistedVersion.version;
    const cases = [
      {
        call: (api) => api.getDagDetail({ dagKey: 'dag-1' }),
        response: { dag: null, nodes: [dashboardDagNode()] },
      },
      {
        call: (api) => api.getDagRuns({ dagKey: 'dag-1', limit: 5 }),
        response: { runs: null },
      },
      {
        call: (api) => api.getDagRuns({ dagKey: 'dag-1', limit: 5 }),
        response: { runs: [missingRunKey] },
      },
      {
        call: (api) => api.getDagRun({ runKey: 'run-1' }),
        response: { run: null, nodes: [dashboardDagNode()] },
      },
      {
        call: (api) => api.listWorkflowTemplates({ category: 'government-enterprise' }),
        response: { templates: null },
      },
      {
        call: (api) => api.listWorkflowTemplates({ category: 'government-enterprise' }),
        response: { templates: [missingTemplateId] },
      },
      {
        call: (api) => api.getWorkflowTemplate({ templateId: 'government-enterprise/meeting-minutes' }),
        response: { template: null },
      },
      {
        call: (api) => api.renderWorkflowTemplateDraft({
          templateId: 'government-enterprise/meeting-minutes',
          version: 2,
          values: {},
        }),
        response: { draft: missingDraft },
      },
      {
        call: (api) => api.saveWorkflowTemplate({
          templateId: 'government-enterprise/meeting-minutes',
          version: 2,
          category: 'government-enterprise',
          trust: {},
          compatibility: {},
          draft: {},
        }),
        response: { template: missingPersistedVersion },
      },
    ];

    for (const item of cases) {
      const callAPI = vi.fn().mockResolvedValue(item.response);
      const api = createBackendApi({ callAPI });
      await expect(item.call(api)).rejects.toThrow();
      expect(callAPI).toHaveBeenCalledTimes(1);
    }
  });

  it('rejects malformed datasource responses', async () => {
    const document = {
      documentId: 7, sourcePath: '/data/a.txt', fileName: 'a.txt', extension: '.txt',
      sizeBytes: 12, contentHash: 'hash', chunkCount: 1, totalChars: 4,
      status: 'ready', errorMessage: '', createdAt: '2026-07-13T00:00:00Z',
      updatedAt: '2026-07-13T00:00:00Z',
    };
    const chunk = {
      id: 9, documentId: 7, chunkIndex: 0, content: 'body', charCount: 4,
      byteCount: 4, embeddingModel: '', embeddingDim: 0, tokenCount: 1,
      createdAt: '2026-07-13T00:00:00Z',
    };
    const cases = [
      { call: (api) => api.listDatasourceDocuments({ keyword: 'a', limit: 20 }), response: { documents: [{ ...document, status: false }] } },
      { call: (api) => api.getDatasourceDocument({ documentId: 7 }), response: { document, chunks: [{ ...chunk, documentId: 8 }], hasMore: false, nextCursor: 0 } },
      { call: (api) => api.listDatasourceChunks({ documentId: 7, limit: 20, cursor: 0 }), response: { chunks: [chunk], hasMore: false, nextCursor: '0' } },
      { call: (api) => api.updateDatasourceDocument({ documentId: 7, sourcePath: '/data/a.txt', fileName: 'a.txt', extension: '.txt', sizeBytes: 12 }), response: { ...document, updatedAt: 5 } },
    ];
    for (const item of cases) {
      const callAPI = vi.fn().mockResolvedValue(item.response);
      await expect(item.call(createBackendApi({ callAPI }))).rejects.toThrow();
      expect(callAPI).toHaveBeenCalledTimes(1);
    }
  });

  it('rejects malformed workflow material responses', async () => {
    const cases = [
      { call: (api) => api.writeWorkflowMaterial({ path: 'workflow/material.md', content: 'body' }), response: { path: 9 } },
    ];

    for (const item of cases) {
      const callAPI = vi.fn().mockResolvedValue(item.response);
      await expect(item.call(createBackendApi({ callAPI }))).rejects.toThrow();
      expect(callAPI).toHaveBeenCalledTimes(1);
    }
  });

  it('accepts null workflow template tags from list get and save responses', async () => {
    const callAPI = vi.fn((method) => {
      if (method === RPC_METHODS.WORKFLOW_TEMPLATES_LIST) {
        return Promise.resolve({ templates: [workflowTemplateSummary({ tags: null })] });
      }
      if (method === RPC_METHODS.WORKFLOW_TEMPLATES_GET) {
        return Promise.resolve({ template: workflowTemplateDetail({ tags: null }) });
      }
      if (method === RPC_METHODS.WORKFLOW_TEMPLATES_SAVE) {
        return Promise.resolve({ template: workflowTemplateSummary({ tags: null }) });
      }
      throw new Error(`unexpected method ${method}`);
    });
    const api = createBackendApi({ callAPI });

    await expect(api.listWorkflowTemplates({ category: 'government-enterprise' })).resolves.toMatchObject({
      templates: [{ tags: null }],
    });
    await expect(api.getWorkflowTemplate({ templateId: 'government-enterprise/meeting-minutes' })).resolves.toMatchObject({
      template: { tags: null },
    });
    await expect(api.saveWorkflowTemplate({
      templateId: 'government-enterprise/meeting-minutes',
      version: 2,
      category: 'government-enterprise',
      trust: {},
      compatibility: {},
      draft: {},
    })).resolves.toMatchObject({ template: { tags: null } });
    expect(callAPI).toHaveBeenCalledTimes(3);

    const invalidAPI = createBackendApi({
      callAPI: vi.fn().mockResolvedValue({ templates: [workflowTemplateSummary({ tags: {} })] }),
    });
    await expect(invalidAPI.listWorkflowTemplates({ category: 'government-enterprise' })).rejects.toThrow('tags must be an array of strings');
  });

  it('calls canonical thread/fork with only the source thread id', async () => {
    const callAPI = vi.fn().mockResolvedValue({
      thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
      kickoff_state: 'created_only',
      kickoffState: 'created_only',
    });
    const api = createBackendApi({ callAPI });

    await expect(api.forkThread({ threadId: 'thread-parent' })).resolves.toEqual(expect.objectContaining({
      thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
      kickoffState: 'created_only',
    }));
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_FORK, { threadId: 'thread-parent' });
  });

  it('rejects non-canonical thread/fork request fields before calling the backend', () => {
    const callAPI = vi.fn();
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.forkThread({
      threadId: 'thread-parent',
      cwd: '/repo/app',
      provider: 'codex',
      baseInstructions: 'summary fallback',
    }), 'thread/fork: unsupported payload field');
    expectInvalidInputDoesNotCall(callAPI, () => api.forkThread({
      threadId: 'thread-parent',
      thread_id: 'different-parent',
    }), 'thread/fork: conflicting threadId values');
  });

  it('rejects thread/fork responses whose source does not match the request', async () => {
    const callAPI = vi.fn().mockResolvedValue({
      thread: { id: 'thread-fork', forkedFrom: 'different-parent' },
      kickoffState: 'created_only',
    });
    const api = createBackendApi({ callAPI });

    await expect(api.forkThread({ threadId: 'thread-parent' })).rejects.toThrow(
      'thread/fork response thread.forkedFrom must equal thread-parent',
    );
  });

  it('rejects thread/fork responses that reuse the source thread id', async () => {
    const callAPI = vi.fn().mockResolvedValue({
      thread: { id: 'thread-parent', forkedFrom: 'thread-parent' },
      kickoffState: 'created_only',
    });
    const api = createBackendApi({ callAPI });

    await expect(api.forkThread({ threadId: 'thread-parent' })).rejects.toThrow(
      'thread/fork response thread.id must differ from thread-parent',
    );
  });

  it('starts a pending backend thread with the canonical thread/start payload shape', async () => {
    const response = { threadId: 'thread-123', state: 'pending' };
    const callAPI = vi.fn().mockResolvedValue(response);
    const api = createBackendApi({ callAPI });

    await expect(api.startThread({
      cwd: '/repo/app',
      name: 'Hello',
      provider: 'codex',
      promptKey: 'main/dag_designer_zh',
      agentKey: 'assistant',
      toolSurfaceMode: 'chat',
      deferSpawn: true,
      codexModelProvider: 'openai',
      config: {
        codexHome: 'C:\\Users\\ai01\\.codex',
        codexInstanceKey: 'default',
        codexModelProvider: 'openai',
      },
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: 'Hello',
      skipInitialRuntimeSync: true,
    })).resolves.toEqual(response);

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_START, {
      cwd: '/repo/app',
      name: 'Hello',
      provider: 'codex',
      prompt_key: 'main/dag_designer_zh',
      agent_key: 'assistant',
      toolSurfaceMode: 'chat',
      defer_spawn: true,
      config: {
        codexHome: 'C:\\Users\\ai01\\.codex',
        codexInstanceKey: 'default',
        codexModelProvider: 'openai',
      },
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
    });
  });

  it('rejects invalid thread/start tool surface mode', () => {
    const callAPI = vi.fn().mockResolvedValue({ threadId: 'thread-123' });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.startThread({
      cwd: '/repo/app',
      modelProvider: 'codex',
      toolSurfaceMode: 'full',
    }), 'toolSurfaceMode must be chat, auto, or agent');
  });

  it('rejects unknown thread/start payload fields before calling the backend', () => {
    const callAPI = vi.fn().mockResolvedValue({ threadId: 'thread-123' });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.startThread({
      cwd: '/repo/app',
      modelProvider: 'codex',
      unexpectedUiField: true,
    }), 'thread/start: unsupported payload field unexpectedUiField');
  });

  it('does not opt into pending launch unless deferSpawn is explicitly requested', async () => {
    const callAPI = vi.fn().mockResolvedValue({ threadId: 'thread-123' });
    const api = createBackendApi({ callAPI });

    await api.startThread({
      cwd: '/repo/app',
      modelProvider: 'claude',
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_START, {
      cwd: '/repo/app',
      provider: 'claude',
    });
  });

  it('allows launch skill facade keys on thread/start', async () => {
    const callAPI = vi.fn().mockResolvedValue({ threadId: 'thread-123' });
    const api = createBackendApi({ callAPI });

    await api.startThread({
      cwd: '/repo/app',
      modelProvider: 'claude',
      selectedSkills: ['review'],
      selectedSkillRefs: [{ name: 'review', scope: 'project' }],
      manualSkillSelection: true,
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_START, expect.objectContaining({
      selectedSkills: ['review'],
      selectedSkillRefs: [{ name: 'review', scope: 'project' }],
      manualSkillSelection: true,
    }));
  });

  it('rejects unknown turn/start facade fields before calling the backend', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-123',
      input: 'build it',
      surprise: true,
    }), 'turn/start: unsupported payload field surprise');
  });

  it('sends turn/start input-only text and expanded arrays with explicit cwd', async () => {
    const firstResponse = { turnId: 'turn-1', status: 'queued' };
    const secondResponse = { turnId: 'turn-2', status: 'queued' };
    const callAPI = vi.fn()
      .mockResolvedValueOnce(firstResponse)
      .mockResolvedValueOnce(secondResponse);
    const api = createBackendApi({ callAPI });

    await expect(api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-123',
      input: 'build it',
      manualSkillSelection: false,
    })).resolves.toEqual(firstResponse);
    await expect(api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-456',
      input: [
        { type: 'text', text: 'inspect this' },
        { type: 'mention', name: 'a.txt', path: '/tmp/a.txt' },
      ],
    })).resolves.toEqual(secondResponse);

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.TURN_START, {
      cwd: '/repo/app',
      threadId: 'thread-123',
      prompt: 'build it',
      manualSkillSelection: false,
    });
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.TURN_START, {
      cwd: '/repo/app',
      threadId: 'thread-456',
      input: [
        { type: 'text', text: 'inspect this' },
        { type: 'mention', name: 'a.txt', path: '/tmp/a.txt' },
      ],
    });
  });

  it('sends turn/start legacy attachments when input is absent or empty', async () => {
    const callAPI = vi.fn()
      .mockResolvedValueOnce({ turn_id: 'turn-legacy-1' })
      .mockResolvedValueOnce({ turn_id: 'turn-legacy-2' });
    const api = createBackendApi({ callAPI });

    await api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-123',
      attachments: ['/tmp/a.txt'],
      manualSkillSelection: false,
    });
    await api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-456',
      input: '  ',
      attachments: [{ path: '/tmp/b.png', kind: 'image', previewUrl: 'data:image/png;base64,abc' }],
    });

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.TURN_START, {
      cwd: '/repo/app',
      threadId: 'thread-123',
      input: [
        { type: 'mention', name: 'a.txt', path: '/tmp/a.txt' },
      ],
      manualSkillSelection: false,
    });
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.TURN_START, {
      cwd: '/repo/app',
      threadId: 'thread-456',
      input: [
        { type: 'localImage', path: '/tmp/b.png', url: 'data:image/png;base64,abc' },
      ],
    });
  });

  it('rejects unknown fields across all newly guarded public response facades', async () => {
    const codeSaveCall = (api) => api.saveCodeFile({
      filePath: 'src/App.jsx',
      content: 'export default App;',
      project: '/repo/app', previewMode: 'full', contentVersion: 'sha256:app-v1',
    });
    const cases = [
      { call: (api) => api.readConfig(), response: runtimeConfigResponse({ surprise: true }) },
      { call: (api) => api.readBuiltinTools({ cwd: '/repo/app' }), response: { ...builtinToolsResponse(), surprise: true } },
      { call: (api) => api.writeBuiltinTool({ cwd: '/repo/app', id: 'Shell', enabled: false }), response: { ...builtinToolsResponse(), surprise: true } },
      { call: (api) => api.getWindowBootstrap(), response: { ...windowBootstrapResponse(), surprise: true } },
      { call: (api) => api.getSidebarState({ cwd: '/repo/app' }), response: sidebarStateResponse({ surprise: true }) },
      { call: (api) => api.callBackend(RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST, { events: [] }), response: { ...frontendIngestResponse(), surprise: true } },
      { call: (api) => api.openNewWindow({ cwd: '/repo/app' }), response: { ...openWindowResponse(), surprise: true } },
      { call: codeSaveCall, response: { ...codeSaveResponse(), surprise: true } },
      { call: (api) => api.getProjects({ cwd: '/repo/app' }), response: { ...projectsStateResponse(), surprise: true } },
      { call: (api) => api.setActiveProject({ cwd: '/repo/app', path: '/repo/next' }), response: { ...projectsStateResponse(), surprise: true } },
      { call: (api) => api.addProject({ cwd: '/repo/app', path: '/repo/new' }), response: { ...projectsStateResponse(), surprise: true } },
      { call: (api) => api.removeProject({ cwd: '/repo/app', path: '/repo/old' }), response: { ...projectsStateResponse(), surprise: true } },
      { call: (api) => api.setPreference({ key: 'settings.provider.active', value: 'codex' }), response: { ...okResponse(), surprise: true } },
      { call: (api) => api.setVideoApiKey({ apiKey: 'sk-test-key' }), response: { ...okResponse(), surprise: true } },
      { call: (api) => api.saveModelProviders({ cwd: '/repo/app', registry: { vendors: [] } }), response: { ...modelProviderRegistryResponse(), surprise: true } },
      { call: (api) => api.getDashboardPage({ cwd: '/repo/app', page: 'settings' }), response: { ...dashboardPageResponse(), surprise: true } },
      { call: (api) => api.getVideoApiKey(), response: { ...videoApiKeyStatusResponse(), surprise: true } },
      { call: (api) => api.listDashboardLogs({ limit: 10 }), response: { ...dashboardLogsResponse(), surprise: true } },
    ];

    for (const item of cases) {
      const callAPI = vi.fn().mockResolvedValue(item.response);
      const api = createBackendApi({ callAPI });
      await expect(item.call(api)).rejects.toThrow('must not include surprise');
      expect(callAPI).toHaveBeenCalledTimes(1);
    }
  });

  it('rejects unknown fields in nested guarded response DTOs', async () => {
    const vendor = {
      id: 'openrouter',
      label: 'OpenRouter',
      enabled: true,
      baseURL: 'https://openrouter.ai/api/v1',
      envKey: 'OPENROUTER_API_KEY',
      codexModelProvider: 'openrouter',
      defaultModel: 'openai/gpt-5.5',
    };
    const cases = [
      {
        call: (api) => api.writeBuiltinTool({ cwd: '/repo/app', id: 'Shell', enabled: false }),
        response: { tools: [{ ...builtinToolsResponse().tools[0], surprise: true }] },
      },
      {
        call: (api) => api.saveModelProviders({ cwd: '/repo/app', registry: { vendors: [] } }),
        response: { vendors: [{ ...vendor, surprise: true }] },
      },
      {
        call: (api) => api.saveModelProviders({ cwd: '/repo/app', registry: { vendors: [] } }),
        response: { vendors: [{ ...vendor, budget: { dailyUsd: 1, surprise: true } }] },
      },
      {
        call: (api) => api.saveModelProviders({ cwd: '/repo/app', registry: { vendors: [] } }),
        response: { vendors: [{ ...vendor, tokenPool: { priority: 1, surprise: true } }] },
      },
      {
        call: (api) => api.getDashboardPage({ cwd: '/repo/app', page: 'settings' }),
        response: {
          ...dashboardPageResponse(),
          sharedFileRetention: {
            ...dashboardPageResponse().sharedFileRetention,
            surprise: true,
          },
        },
      },
      {
        call: (api) => api.listDashboardLogs({ limit: 10 }),
        response: {
          logs: [{
            source: 'app',
            id: 1,
            timestamp: '2026-07-13T00:00:00Z',
            surprise: true,
          }],
        },
      },
    ];

    for (const item of cases) {
      const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(item.response) });
      await expect(item.call(api)).rejects.toThrow('must not include surprise');
    }
  });

  it('rejects malformed runtime config fields and nested tool routing', async () => {
    const { sandbox: _sandbox, ...missingSandbox } = runtimeConfigResponse();
    const cases = [
      { response: missingSandbox, message: 'config/read response sandbox is required' },
      { response: runtimeConfigResponse({ toolRouting: [] }), message: 'config/read response toolRouting must be an object' },
      {
        response: runtimeConfigResponse({
          toolRouting: { ...runtimeConfigResponse().toolRouting, routerHasAPIKey: 'false' },
        }),
        message: 'config/read response toolRouting.routerHasAPIKey must be a boolean',
      },
      {
        response: runtimeConfigResponse({
          toolRouting: { ...runtimeConfigResponse().toolRouting, confidenceThreshold: '0.65' },
        }),
        message: 'config/read response toolRouting.confidenceThreshold must be a finite number',
      },
      {
        response: runtimeConfigResponse({
          toolRouting: { ...runtimeConfigResponse().toolRouting, surprise: true },
        }),
        message: 'config/read response toolRouting must not include surprise',
      },
    ];

    for (const item of cases) {
      const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(item.response) });
      await expect(api.readConfig()).rejects.toThrow(item.message);
    }
  });

  it('rejects malformed sidebar required, optional, and nested DTO fields', async () => {
    const call = (api) => api.getSidebarState({ cwd: '/repo/app' });
    const base = sidebarStateResponse();
    const cases = [
      { response: sidebarStateResponse({ threads: {} }), message: 'threads must be an array' },
      { response: sidebarStateResponse({ workspace: [] }), message: 'workspace must be an object' },
      { response: sidebarStateResponse({ token_usage: { ...base.token_usage, totalTokens: '3' } }), message: 'token_usage.totalTokens must be an integer' },
      { response: sidebarStateResponse({ token_usage: { ...base.token_usage, contextWindowTokens: '128000' } }), message: 'token_usage.contextWindowTokens must be an integer' },
      { response: sidebarStateResponse({ token_usage: { ...base.token_usage, usedPercent: '0.01' } }), message: 'token_usage.usedPercent must be a finite number' },
      { response: sidebarStateResponse({ statuses: { 'thread-1': false } }), message: 'statuses.thread-1 must be a string' },
      { response: sidebarStateResponse({ interruptibleByThread: { 'thread-1': 'true' } }), message: 'interruptibleByThread.thread-1 must be a boolean' },
      { response: sidebarStateResponse({ statusHeadersByThread: [] }), message: 'statusHeadersByThread must be an object' },
      { response: sidebarStateResponse({ statusDetailsByThread: { 'thread-1': 7 } }), message: 'statusDetailsByThread.thread-1 must be a string' },
      { response: sidebarStateResponse({ agentRuntimeById: { 'agent-1': [] } }), message: 'agentRuntimeById.agent-1 must be an object' },
      { response: sidebarStateResponse({ activityStatsByThread: { 'thread-1': { lspCalls: '1', commands: 0, fileEdits: 0 } } }), message: 'activityStatsByThread.thread-1.lspCalls must be an integer' },
      { response: sidebarStateResponse({ activityStatsByThread: { 'thread-1': { lspCalls: 0, commands: 0, fileEdits: 0, toolCalls: { read: '1' } } } }), message: 'activityStatsByThread.thread-1.toolCalls.read must be an integer' },
      { response: sidebarStateResponse({ activeThreadId: 1 }), message: 'activeThreadId must be a string' },
      { response: sidebarStateResponse({ 'viewPrefs.chat': [] }), message: 'viewPrefs.chat must be an object' },
      { response: sidebarStateResponse({ 'threadPins.chat': { 'thread-1': 1.5 } }), message: 'threadPins.chat values must be integers' },
      { response: sidebarStateResponse({ groups: [{ key: 'active', title: 'Active', threads: [], surprise: true }] }), message: 'groups[0] must not include surprise' },
      { response: sidebarStateResponse({ threads: [{ id: 'thread-1', surprise: true }] }), message: 'threads[0] must not include surprise' },
      { response: sidebarStateResponse({ agents: [{ id: 1 }] }), message: 'agents[0].id must be a string' },
      { response: sidebarStateResponse({ active_turn: { ...base.active_turn, success: 'true' } }), message: 'active_turn.success must be a boolean' },
      { response: sidebarStateResponse({ recent_turns: [{ ...base.active_turn, surprise: true }] }), message: 'recent_turns[0] must not include surprise' },
      { response: sidebarStateResponse({ workspace: { runs: [{ run_key: 'run-1', merged_file_count: '1' }] } }), message: 'workspace.runs[0].merged_file_count must be an integer' },
    ];

    for (const item of cases) {
      const callAPI = vi.fn().mockResolvedValue(item.response);
      const api = createBackendApi({ callAPI });
      await expect(call(api)).rejects.toThrow(item.message);
    }
  });

  it('fails fast when the sidebar facade receives empty or malformed success bodies', async () => {
    const missingWorkspaceWithMalformedThread = sidebarStateResponse({
      threads: [{ id: 7 }],
    });
    delete missingWorkspaceWithMalformedThread.workspace;
    const missingRecentTurns = sidebarStateResponse();
    delete missingRecentTurns.recent_turns;
    const cases = [
      { response: {}, message: 'ui/sidebar/get response threads is required' },
      {
        response: { statuses: { 'thread-1': 'running' } },
        message: 'ui/sidebar/get response threads is required',
      },
      {
        response: missingWorkspaceWithMalformedThread,
        message: 'ui/sidebar/get response workspace is required',
      },
      {
        response: missingRecentTurns,
        message: 'ui/sidebar/get response recent_turns is required',
      },
    ];

    for (const item of cases) {
      const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(item.response) });
      await expect(api.getSidebarState({ cwd: '/repo/app' })).rejects.toThrow(item.message);
    }
  });

  it('rejects null sidebar list fields instead of accepting Go nil slice drift', async () => {
    // nil slice 漂移必须在 Go producer/clone 层修复（输出 []），前端契约保持严格：
    // 字段存在时必须是数组，null 一律拒绝。
    const call = (api) => api.getSidebarState({ cwd: '/repo/app' });
    const cases = [
      sidebarStateResponse({ agents: null }),
      sidebarStateResponse({ threads: null }),
      sidebarStateResponse({ recent_turns: null }),
      sidebarStateResponse({ agents: {} }),
      sidebarStateResponse({ agents: 'agents' }),
      sidebarStateResponse({ threads: 3 }),
    ];

    for (const response of cases) {
      const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(response) });
      await expect(call(api)).rejects.toThrow(/must be an array/);
    }
  });

  it('accepts empty arrays for sidebar list fields', async () => {
    const response = sidebarStateResponse({ agents: [], threads: [], recent_turns: [] });
    const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(response) });
    await expect(api.getSidebarState({ cwd: '/repo/app' })).resolves.toEqual(response);
  });

  it('rejects unsuccessful or malformed code save response fields', async () => {
    const call = (api) => api.saveCodeFile({
      filePath: 'src/App.jsx',
      content: 'export default App;',
      project: '/repo/app', previewMode: 'full', contentVersion: 'sha256:app-v1',
    });
    const cases = [
      { response: { ...codeSaveResponse(), ok: false }, message: 'ui/code/save response ok must be true' },
      { response: { ...codeSaveResponse(), ok: 'true' }, message: 'ui/code/save response ok must be a boolean' },
      { response: { ...codeSaveResponse(), filePath: '' }, message: 'ui/code/save response filePath must be a non-empty string' },
      { response: { ...codeSaveResponse(), filePath: 7 }, message: 'ui/code/save response filePath must be a non-empty string' },
      { response: { ...codeSaveResponse(), relative: '  ' }, message: 'ui/code/save response relative must be a non-empty string' },
    ];

    for (const item of cases) {
      const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(item.response) });
      await expect(call(api)).rejects.toThrow(item.message);
    }
  });

  it('accepts a null window bootstrap snapshot after the desktop host consumed it', async () => {
    // 一次性快照被桌面宿主消费后返回 { snapshot: null }，必须放行给 normalize 层回退，
    // 否则浏览器直开/刷新会让 bootstrap 永久失败、控件全部禁用。
    const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue({ snapshot: null }) });
    await expect(api.getWindowBootstrap()).resolves.toEqual({ snapshot: null });
  });

  it('fails fast on malformed guarded backend responses before consumers normalize them', async () => {
    const cases = [
      {
        call: (api) => api.readConfig(),
        response: runtimeConfigResponse({
          toolRouting: { ...runtimeConfigResponse().toolRouting, timeoutSec: '8' },
        }),
        message: 'config/read response toolRouting.timeoutSec must be an integer',
      },
      {
        call: (api) => api.readBuiltinTools({ cwd: '/repo/app' }),
        response: { tools: [{ id: 'Shell', label: 'Shell', enabled: 'true' }] },
        message: 'config/builtinTools/read response tools[0].enabled must be a boolean',
      },
      {
        call: (api) => api.getWindowBootstrap(),
        response: {},
        message: 'ui/windowBootstrap/get response snapshot must be an object',
      },
      {
        call: (api) => api.getWindowBootstrap(),
        response: { snapshot: [] },
        message: 'ui/windowBootstrap/get response snapshot must be an object',
      },
      {
        call: (api) => api.getSidebarState({ cwd: '/repo/app' }),
        response: sidebarStateResponse({
          token_usage: { inputTokens: 0, outputTokens: 0, totalTokens: 0, usedTokens: '0' },
        }),
        message: 'ui/sidebar/get response token_usage.usedTokens must be an integer',
      },
      {
        call: (api) => api.callBackend(RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST, { events: [] }),
        response: { enabled: true, recorded: '1', dropped: 0 },
        message: 'observability/frontend/ingest response recorded must be an integer',
      },
      {
        call: (api) => api.openNewWindow({ cwd: '/repo/app' }),
        response: { ok: 'true', windowId: 'window-2', cwd: '/repo/app' },
        message: 'ui/openNewWindow response ok must be a boolean',
      },
      {
        call: (api) => api.saveCodeFile({ filePath: 'src/App.jsx', content: 'export default App;', project: '/repo/app', previewMode: 'full', contentVersion: 'sha256:app-v1' }),
        response: { ok: true, filePath: '/repo/app/src/App.jsx', relative: 'src/App.jsx', totalLines: '1', contentVersion: 'sha256:app-v2' },
        message: 'ui/code/save response totalLines must be an integer',
      },
      ...[
        (api) => api.getProjects({ cwd: '/repo/app' }),
        (api) => api.setActiveProject({ cwd: '/repo/app', path: '/repo/next' }),
        (api) => api.addProject({ cwd: '/repo/app', path: '/repo/new' }),
        (api) => api.removeProject({ cwd: '/repo/app', path: '/repo/old' }),
      ].map((call) => ({
        call,
        response: { projects: [], active: 7 },
        message: 'response active must be a string',
      })),
      {
        call: (api) => api.setPreference({ key: 'settings.provider.active', value: 'codex' }),
        response: { ok: false },
        message: 'ui/preferences/set response ok must be true',
      },
      {
        call: (api) => api.setVideoApiKey({ apiKey: 'sk-test-key' }),
        response: { ok: false },
        message: 'ui/video/setApiKey response ok must be true',
      },
      {
        call: (api) => api.saveModelProviders({ cwd: '/repo/app', registry: { vendors: [] } }),
        response: { vendors: null },
        message: 'modelProviders/save response model provider registry',
      },
      {
        call: (api) => api.getDashboardPage({ cwd: '/repo/app', page: 'settings' }),
        response: { ...dashboardPageResponse(), commandCards: null },
        message: 'ui/dashboard/get response commandCards must be an array',
      },
      {
        call: (api) => api.getVideoApiKey(),
        response: { configured: 'false', masked: '' },
        message: 'ui/video/getApiKey response configured must be a boolean',
      },
      {
        call: (api) => api.listDashboardLogs({ limit: 10 }),
        response: { logs: null },
        message: 'dashboard/logs response logs must be an array',
      },
      {
        call: (api) => api.getThreadState({ cwd: '/repo/app', threadId: 'thread-1' }),
        response: {},
        message: 'ui/state/get response missing UI state snapshot fields',
      },
      {
        call: (api) => api.startThread({ cwd: '/repo/app', modelProvider: 'codex' }),
        response: { status: 'running' },
        message: 'thread/start response missing threadId or thread_id',
      },
      {
        call: (api) => api.getThreadMessages({ threadId: 'thread-1' }),
        response: { messages: null },
        message: 'thread/messages response messages must be an array',
      },
      {
        call: (api) => api.getThreadMessages({ threadId: 'thread-1' }),
        response: { messages: [], total: '1' },
        message: 'thread/messages response total must be a number',
      },
      {
        call: (api) => api.resolveThreadIdentity({ cwd: '/repo/app', threadId: 'thread-1' }),
        response: {},
        message: 'thread/resolve response missing id or threadId or thread_id',
      },
      {
        call: (api) => api.startTurn({ cwd: '/repo/app', threadId: 'thread-1', input: 'build it' }),
        response: { ok: true },
        message: 'turn/start response missing turn_id or turnId',
      },
      {
        call: (api) => api.forceCompleteTurn({ cwd: '/repo/app', threadId: 'thread-1' }),
        response: { ok: true },
        message: 'turn/forceComplete response forceCompleted must be a boolean',
      },
      {
        call: (api) => api.forceCompleteTurn({ cwd: '/repo/app', threadId: 'thread-1' }),
        response: { ok: false, forceCompleted: false },
        message: 'turn/forceComplete response failure must include errorCode, error, or message',
      },
      {
        call: (api) => api.forceCompleteTurn({ cwd: '/repo/app', threadId: 'thread-1' }),
        response: { ok: true, forceCompleted: false, errorCode: 'force_complete_target_not_found' },
        message: 'turn/forceComplete response ok true cannot have forceCompleted false',
      },
      {
        call: (api) => api.forceCompleteTurn({ cwd: '/repo/app', threadId: 'thread-1' }),
        response: { ok: false, forceCompleted: 'false', errorCode: 'force_complete_target_not_found' },
        message: 'turn/forceComplete response forceCompleted must be a boolean',
      },
      {
        call: (api) => api.startDag({ dagKey: 'dag-1', triggerSource: 'manual' }),
        response: { ok: true },
        message: 'dashboard/dagStart response missing runKey or run_key',
      },
      {
        call: (api) => api.createAndStartDag({
          dagKey: 'dag-created',
          title: 'Created DAG',
          nodes: [{ nodeKey: 'draft', title: 'Draft', nodeType: 'agent', dependsOn: [] }],
        }),
        response: { dagKey: 'dag-created' },
        message: 'dashboard/dagCreateAndStart response missing runKey or run_key',
      },
      {
        call: (api) => api.createAndStartDag({
          dagKey: 'dag-created',
          title: 'Created DAG',
          nodes: [{ nodeKey: 'draft', title: 'Draft', nodeType: 'agent', dependsOn: [] }],
        }),
        response: { runKey: 'run-created' },
        message: 'dashboard/dagCreateAndStart response missing dagKey or dag_key',
      },
    ];

    for (const item of cases) {
      const callAPI = vi.fn().mockResolvedValue(item.response);
      const api = createBackendApi({ callAPI });
      await expect(item.call(api)).rejects.toThrow(item.message);
      expect(callAPI).toHaveBeenCalledTimes(1);
    }
  });

  it('rejects turn/start legacy prompt with legacy attachments before calling the backend', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-123',
      prompt: 'build it',
      attachments: ['/tmp/a.txt'],
    }), 'turn/start: prompt and attachments cannot both contain content');
  });

  it('rejects turn/start array input with legacy attachments before calling the backend', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-123',
      input: [{ type: 'text', text: 'build it' }],
      attachments: ['/tmp/a.txt'],
    }), 'turn/start: input and attachments cannot both contain content');
  });

  it('rejects turn/start string input with legacy attachments before calling the backend', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-123',
      input: 'build it',
      attachments: ['/tmp/a.txt'],
    }), 'turn/start: input and attachments cannot both contain content');
  });

  it('maps archive, unarchive, and delete thread actions to legacy thread RPCs', async () => {
    const callAPI = vi.fn().mockResolvedValue(null);
    const api = createBackendApi({ callAPI });

    await api.archiveThread({ cwd: '/repo/app', threadId: 'thread-1' });
    await api.unarchiveThread({ cwd: '/repo/app', thread_id: 'thread-2' });
    await api.deleteThread({ cwd: '/repo/app', threadId: 'thread-3' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_ARCHIVE, { threadId: 'thread-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_UNARCHIVE, { threadId: 'thread-2' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_DELETE, { threadId: 'thread-3' });
  });

  it('rejects malformed null command responses', async () => {
    const commands = [
      { call: (api) => api.archiveThread({ threadId: 'thread-1' }) },
      { call: (api) => api.unarchiveThread({ threadId: 'thread-1' }) },
      { call: (api) => api.deleteThread({ threadId: 'thread-1' }) },
      { call: (api) => api.renameThread({ threadId: 'thread-1', name: 'Renamed' }) },
      {
        call: (api) =>
          api.respondApproval({
            sessionScope: 'session-scope-a',
            callId: 'call-a',
            requestId: 11,
            approved: true,
          }),
      },
    ];

    for (const command of commands) {
      for (const response of [{}, { ok: true }, false, undefined]) {
        const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(response) });
        await expect(command.call(api)).rejects.toThrow('response must be null');
      }

      const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(null) });
      await expect(command.call(api)).resolves.toBeNull();
    }
  });

  it('rejects unknown thread-scoped facade fields before calling the backend', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.resolveThreadIdentity({
      cwd: '/repo/app',
      threadId: 'thread-1',
      surprise: true,
    }), 'thread/resolve: unsupported payload field surprise');
    expectInvalidInputDoesNotCall(callAPI, () => api.archiveThread({
      cwd: '/repo/app',
      threadId: 'thread-1',
      surprise: true,
    }), 'thread/archive: unsupported payload field surprise');
    expectInvalidInputDoesNotCall(callAPI, () => api.renameThread({
      cwd: '/repo/app',
      threadId: 'thread-1',
      name: 'Renamed',
      surprise: true,
    }), 'thread/name/set: unsupported payload field surprise');
    expectInvalidInputDoesNotCall(callAPI, () => api.setThreadConfig({
      threadId: 'thread-1',
      model: 'gpt-5.4',
      surprise: true,
    }), 'thread/config/set: unsupported payload field surprise');
    expectInvalidInputDoesNotCall(callAPI, () => api.interruptTurn({
      cwd: '/repo/app',
      threadId: 'thread-1',
      expectedTurnId: 'turn-1',
      requestId: 'stop-request-1',
      source: 'ui_stop',
      surprise: true,
    }), 'turn/interrupt: unsupported payload field surprise');
    expectInvalidInputDoesNotCall(callAPI, () => api.compactThread({
      cwd: '/repo/app',
      threadId: 'thread-1',
      surprise: true,
    }), 'thread/compact/start: unsupported payload field surprise');
  });

  it('rejects conflicting thread id aliases before calling thread-scoped backend RPCs', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.archiveThread({
      threadId: 'thread-A',
      thread_id: 'thread-B',
    }), 'thread/archive: conflicting threadId values for threadId and thread_id');
    expectInvalidInputDoesNotCall(callAPI, () => api.resolveThreadIdentity({
      cwd: '/repo/app',
      threadId: 'thread-A',
      thread_id: 'thread-B',
    }), 'thread/resolve: conflicting threadId values for threadId and thread_id');
  });

  it('exposes text copy through the native bridge helper without adding a backend RPC payload', async () => {
    const callAPI = vi.fn();
    const beginTextClipboardWrite = vi.fn().mockReturnValue(null);
    const copyTextToClipboard = vi.fn().mockResolvedValue(true);
    const api = createBackendApi({ callAPI, beginTextClipboardWrite, copyTextToClipboard });

    expect(api.beginTextClipboardWrite()).toBeNull();
    await expect(api.copyTextToClipboard('thread info')).resolves.toBe(true);

    expect(beginTextClipboardWrite).toHaveBeenCalledTimes(1);
    expect(copyTextToClipboard).toHaveBeenCalledWith('thread info');
    expect(callAPI).not.toHaveBeenCalled();
  });

  it('maps thread rename to the legacy name RPC without cwd', async () => {
    const callAPI = vi.fn().mockResolvedValue(null);
    const api = createBackendApi({ callAPI });

    await api.renameThread({ cwd: '/repo/app', threadId: 'thread-1', name: 'Renamed' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_NAME_SET, {
      threadId: 'thread-1',
      name: 'Renamed',
    });
  });

  it('maps thread config get and set to legacy thread config RPCs', async () => {
    const callAPI = vi.fn().mockResolvedValue(threadConfigResponse());
    const api = createBackendApi({ callAPI });

    await api.getThreadConfig({ thread_id: 'thread-1' });
    await api.setThreadConfig({ threadId: 'thread-1', model: { value: 'gpt-5.4' }, effort: { id: 'medium' } });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_CONFIG_GET, { threadId: 'thread-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_CONFIG_SET, {
      threadId: 'thread-1',
      model: 'gpt-5.4',
      effort: 'medium',
    });
  });

  it('rejects malformed thread lifecycle responses', async () => {
    const configCalls = [
      (api) => api.getThreadConfig({ threadId: 'thread-1' }),
      (api) => api.setThreadConfig({ threadId: 'thread-1', model: 'gpt-5.5' }),
    ];
    const { threadId: _threadId, ...missingThreadId } = threadConfigResponse();
    const { override: _override, ...missingOverride } = threadConfigResponse();
    const { effective: _effective, ...missingEffective } = threadConfigResponse(); const { availableModels: _availableModels, ...missingAvailableModels } = threadConfigResponse();
    const configResponses = [
      missingThreadId, missingOverride,
      missingEffective,
      missingAvailableModels, threadConfigResponse({ availableModels: [] }), threadConfigResponse({ availableModels: ['gpt-5.5', ''] }),
      threadConfigResponse({ supportsThreadOverride: 'true' }),
      threadConfigResponse({ override: { model: 7 } }),
      threadConfigResponse({ effective: { surprise: true } }),
      threadConfigResponse({ surprise: true }),
    ];

    for (const call of configCalls) {
      for (const response of configResponses) {
        const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(response) });
        await expect(call(api)).rejects.toThrow();
      }
      const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(threadConfigResponse()) });
      await expect(call(api)).resolves.toEqual(threadConfigResponse());
    }

    const compactCall = (api) => api.compactThread({ cwd: '/repo/app', threadId: 'thread-1' });
    const compactResponses = [
      { ...threadCompactResponse(), threadId: undefined },
      { ...threadCompactResponse(), command: undefined },
      { ...threadCompactResponse(), beforeTokens: '1200' },
      { ...threadCompactResponse(), afterTokens: 640.5 },
      { ...threadCompactResponse(), compacted: 'true' },
      { ...threadCompactResponse(), estimated: 'false' },
      { ...threadCompactResponse(), surprise: true },
    ];
    for (const response of compactResponses) {
      const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(response) });
      await expect(compactCall(api)).rejects.toThrow();
    }
    await expect(compactCall(createBackendApi({ callAPI: vi.fn().mockResolvedValue(threadCompactResponse()) })))
      .resolves.toEqual(threadCompactResponse());

    const recoverCall = (api) => api.recoverThread({ cwd: '/repo/app', threadId: 'thread-1' });
    const recoverResponses = [
      threadRecoverResponse({ thread: { status: 'recovering' } }),
      threadRecoverResponse({ thread: { id: 'thread-1', status: false } }),
      threadRecoverResponse({ thread: { id: 'thread-1', surprise: true } }),
      threadRecoverResponse({ recovered: 'true' }),
      threadRecoverResponse({ mode: undefined }),
      threadRecoverResponse({ surprise: true }),
    ];
    for (const response of recoverResponses) {
      const api = createBackendApi({ callAPI: vi.fn().mockResolvedValue(response) });
      await expect(recoverCall(api)).rejects.toThrow();
    }
    await expect(recoverCall(createBackendApi({ callAPI: vi.fn().mockResolvedValue(threadRecoverResponse()) })))
      .resolves.toEqual(threadRecoverResponse());
  });

  it('strips cwd from strict thread-scoped runtime RPC payloads', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(guardedBackendResponse(method)));
    const api = createBackendApi({ callAPI });

    await api.interruptTurn({
      cwd: '/repo/app',
      threadId: 'thread-1',
      expectedTurnId: 'turn-1',
      requestId: 'stop-request-1',
      source: 'ui_stop',
    });
    await api.forceCompleteTurn({ cwd: '/repo/app', threadId: 'thread-1' });
    await api.compactThread({ cwd: '/repo/app', threadId: 'thread-1' });
    await api.recoverThread({ cwd: '/repo/app', threadId: 'thread-1' });

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.TURN_INTERRUPT, {
      thread_id: 'thread-1',
      expected_turn_id: 'turn-1',
      request_id: 'stop-request-1',
      source: 'ui_stop',
    });
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.TURN_FORCE_COMPLETE, {
      threadId: 'thread-1',
    });
    expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.THREAD_COMPACT_START, {
      threadId: 'thread-1',
    });
    expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.THREAD_RECOVER, {
      threadId: 'thread-1',
    });
  });

  it('passes through diagnosed turn force-complete failure envelopes from the backend facade', async () => {
    const responses = [
      { ok: false, forceCompleted: false, errorCode: 'force_complete_target_not_found' },
      { ok: false, forceCompleted: false, error: 'force complete target not found' },
      { ok: false, forceCompleted: false, message: 'force complete target not found' },
    ];

    for (const response of responses) {
      const callAPI = vi.fn().mockResolvedValue(response);
      const api = createBackendApi({ callAPI });

      await expect(api.forceCompleteTurn({ cwd: '/repo/app', threadId: 'thread-1' })).resolves.toEqual(response);
      expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.TURN_FORCE_COMPLETE, {
        threadId: 'thread-1',
      });
    }
  });

  it('rejects unknown turn/forceComplete facade fields before calling the backend', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.forceCompleteTurn({
      cwd: '/repo/app',
      threadId: 'thread-1',
      surprise: true,
    }), 'turn/forceComplete: unsupported payload field surprise');
  });

  it('wraps approval/respond with strict composite identity and decision payloads', async () => {
    const callAPI = vi.fn().mockResolvedValue(null);
    const api = createBackendApi({ callAPI });
    const identity = { sessionScope: 'session-scope-a', callId: 'call-a', requestId: 11 };

    await api.respondApproval({ ...identity, approved: false });

    expect(RPC_METHODS.APPROVAL_RESPOND).toBe('approval/respond');
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.APPROVAL_RESPOND, {
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 11,
      approved: false,
    });
    expect(() => api.respondApproval({ ...identity, requestId: 0, approved: true }))
      .toThrow('approval/respond: requestId is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.respondApproval({ ...identity, requestId: '11', approved: true }),
      'approval/respond: requestId must be a positive integer');
    expectInvalidInputDoesNotCall(callAPI, () => api.respondApproval({ ...identity, requestId: '11.9', approved: true }),
      'approval/respond: requestId must be a positive integer');
    expectInvalidInputDoesNotCall(callAPI, () => api.respondApproval({ ...identity, requestId: 11.9, approved: true }),
      'approval/respond: requestId must be a positive integer');
    expectInvalidInputDoesNotCall(callAPI, () => api.respondApproval({
      ...identity,
      requestId: Number.MAX_SAFE_INTEGER + 1,
      approved: true,
    }), 'approval/respond: requestId must be a positive integer');
    expectInvalidInputDoesNotCall(callAPI, () => api.respondApproval({ callId: 'call-a', requestId: 11, approved: true }),
      'approval/respond: sessionScope is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.respondApproval({ sessionScope: 'session-scope-a', requestId: 11, approved: true }),
      'approval/respond: callId is required');
    expect(() => api.respondApproval(identity))
      .toThrow('approval/respond: approved is required');

    await api.respondApproval({ session_scope: 'session-scope-b', call_id: 'call-b', request_id: 12, approved: true });
    expect(callAPI).toHaveBeenLastCalledWith(RPC_METHODS.APPROVAL_RESPOND, {
      sessionScope: 'session-scope-b',
      callId: 'call-b',
      requestId: 12,
      approved: true,
    });
  });

  it('rejects unknown approval/respond facade fields before calling the backend', () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.respondApproval({
      sessionScope: 'session-scope-a',
      callId: 'call-a',
      requestId: 11,
      approved: true,
      surprise: true,
    }), 'approval/respond: unsupported payload field surprise');
    expectInvalidInputDoesNotCall(callAPI, () => api.respondApproval({
      sessionScope: 'session-scope-a',
      session_scope: 'session-scope-b',
      callId: 'call-a',
      requestId: 11,
      approved: true,
    }), 'approval/respond: conflicting sessionScope values');
    expectInvalidInputDoesNotCall(callAPI, () => api.respondApproval({
      sessionScope: '',
      session_scope: 'session-scope-a',
      callId: 'call-a',
      requestId: 11,
      approved: true,
    }), 'approval/respond: conflicting sessionScope values');
  });

  it('maps turn/interrupt to the backend request and response contract', async () => {
    const response = {
      ok: true,
      turnId: 'turn-1',
      status: 'interrupted',
      confirmed: true,
      mode: 'interrupt_confirmed',
      interruptSent: true,
      stateBefore: 'running',
      stateAfter: 'interrupted',
      waitedMs: 25,
      activeObserved: false,
    };
    const callAPI = vi.fn().mockResolvedValue(response);
    const api = createBackendApi({ callAPI });

    await expect(api.interruptTurn({
      cwd: '/repo/app',
      threadId: 'thread-1',
      expectedTurnId: 'turn-1',
      requestId: 'stop-request-1',
      source: 'ui_stop',
    }))
      .resolves.toEqual(response);

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.TURN_INTERRUPT, {
      thread_id: 'thread-1',
      expected_turn_id: 'turn-1',
      request_id: 'stop-request-1',
      source: 'ui_stop',
    });
  });

  it.each([
    [{ cwd: '/repo/app', threadId: 'thread-1', requestId: 'stop-request-1', source: 'ui_stop' }, 'expectedTurnId is required'],
    [{ cwd: '/repo/app', threadId: 'thread-1', expectedTurnId: 'turn-1', source: 'ui_stop' }, 'requestId is required'],
    [{ cwd: '/repo/app', threadId: 'thread-1', expectedTurnId: 'turn-1', requestId: '', source: 'ui_stop' }, 'requestId is required'],
  ])('rejects incomplete stop identity before turn/interrupt transport', (payload, message) => {
    const callAPI = vi.fn();
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.interruptTurn(payload), `turn/interrupt: ${message}`);
  });

  it('fails fast before cwd-scoped RPCs when cwd is missing', () => {
    const callAPI = vi.fn();
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.getProjects({ cwd: '' }), 'cwd is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.startThread({ cwd: '/repo/app', name: 'Hello' }), 'provider is required');
  });

  it('wraps DAG dashboard RPCs with the legacy payload shapes', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(guardedBackendResponse(method)));
    const api = createBackendApi({ callAPI });

    await callDagDashboardApis(api);

    expectDagDashboardCalls(callAPI);
    expectInvalidInputDoesNotCall(callAPI, () => api.applyDagOps({ dagKey: 'dag-1', ops: [] }), 'baseVersion is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.getDagRun({ runKey: '' }), 'runKey is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.dispatchDagNode({ dagKey: 'dag-1', runId: 88, nodeKey: 'draft', assignedTo: '' }), 'assignedTo is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.terminateDagRun({ dagKey: 'dag-1', runKey: '' }), 'runKey is required');
    expect(typeof createAndStartDag).toBe('function');
    expect(typeof writeWorkflowMaterial).toBe('function');
  });

  it('passes through representative DAG mutation responses', async () => {
    const dispatchResponse = { ok: true, nodeKey: 'draft', assignedTo: 'codex-runner' };
    const applyResponse = { ok: true, version: 12 };
    const callAPI = vi.fn()
      .mockResolvedValueOnce(dispatchResponse)
      .mockResolvedValueOnce(applyResponse);
    const api = createBackendApi({ callAPI });

    await expect(api.dispatchDagNode({
      dagKey: 'dag-1',
      runId: 88,
      nodeKey: 'draft',
      assignedTo: 'codex-runner',
    })).resolves.toEqual(dispatchResponse);
    await expect(api.applyDagOps({
      dagKey: 'dag-1',
      baseVersion: 11,
      ops: [{ op: 'update_node', node_key: 'draft', patch: { title: 'Draft v2' } }],
    })).resolves.toEqual(applyResponse);
  });

  it('wraps cronjob RPCs with validated payload shapes', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(guardedBackendResponse(method)));
    const api = createBackendApi({ callAPI });
    const cronPayload = {
      cwd: '/repo/app',
      name: 'nightly',
      prompt: 'run tests',
      scheduleExpr: '0 9 * * *',
      timezone: 'Asia/Shanghai',
      provider: 'codex',
      model: 'gpt-5',
      config: { codexHome: '/codex', codexInstanceKey: 'default', codexModelProvider: 'openai' },
      skills: ['测试规范'],
      notifyChannel: 'desktop',
      enabled: true,
      nextRunAt: '2026-07-05T01:00:00Z',
      maxAttempts: 2,
    };

    await api.listCronJobs({ limit: 25, cursor: '' });
    await api.getCronJob({ id: 'job-1' });
    await api.createCronJob(cronPayload);
    await api.updateCronJob({ ...cronPayload, id: 'job-1', name: 'nightly v2', enabled: false });
    await api.deleteCronJob({ id: 'job-1' });
    await api.runCronJobOnce({ id: 'job-1' });
    await api.setCronJobEnabled({ id: 'job-1', enabled: true });
    await api.listCronJobRuns({ jobId: 'job-1', limit: 50 });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_LIST, { limit: 25, cursor: '' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_GET, { id: 'job-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_CREATE, {
      cwd: '/repo/app',
      name: 'nightly',
      prompt: 'run tests',
      schedule_expr: '0 9 * * *',
      timezone: 'Asia/Shanghai',
      provider: 'codex',
      model: 'gpt-5',
      config: { codexHome: '/codex', codexInstanceKey: 'default', codexModelProvider: 'openai' },
      skills: ['测试规范'],
      notify_channel: 'desktop',
      enabled: true,
      next_run_at: '2026-07-05T01:00:00Z',
      max_attempts: 2,
    });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_UPDATE, {
      id: 'job-1',
      cwd: '/repo/app',
      name: 'nightly v2',
      prompt: 'run tests',
      schedule_expr: '0 9 * * *',
      timezone: 'Asia/Shanghai',
      provider: 'codex',
      model: 'gpt-5',
      config: { codexHome: '/codex', codexInstanceKey: 'default', codexModelProvider: 'openai' },
      skills: ['测试规范'],
      notify_channel: 'desktop',
      enabled: false,
      next_run_at: '2026-07-05T01:00:00Z',
      max_attempts: 2,
    });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_DELETE, { id: 'job-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_RUN_ONCE, { id: 'job-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_SET_ENABLED, { id: 'job-1', enabled: true });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_LIST_RUNS, { job_id: 'job-1', limit: 50 });
    expectInvalidInputDoesNotCall(callAPI, () => api.createCronJob({ ...cronPayload, cwd: '' }), 'cwd is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.createCronJob({ ...cronPayload, name: '' }), 'name is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.createCronJob({ ...cronPayload, prompt: '' }), 'prompt is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.createCronJob({ ...cronPayload, scheduleExpr: '' }), 'schedule_expr is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.updateCronJob({ ...cronPayload, id: '' }), 'id is required');
    expect(() => api.getCronJob({ id: '' })).toThrow('id is required');
    expect(() => api.setCronJobEnabled({ id: 'job-1', enabled: 'true' })).toThrow('enabled must be boolean');
    expect(() => api.listCronJobRuns({ jobId: '' })).toThrow('job_id is required');
  });

  it('wraps settings config RPCs with the internal uistate method names', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(guardedBackendResponse(method)));
    const api = createBackendApi({ callAPI });

    await api.readBuiltinTools({ cwd: '/repo/app' });
    await api.writeBuiltinTool({ cwd: '/repo/app', id: 'Shell', enabled: false });
    await api.listDashboardLogs({ limit: 14 });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_BUILTIN_TOOLS_READ, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE, { cwd: '/repo/app', id: 'Shell', enabled: false });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_LOGS, { limit: 14 });
    expect(() => api.writeBuiltinTool({ cwd: '/repo/app', id: '', enabled: true })).toThrow('id is required');
    expect(() => api.writeBuiltinTool({ cwd: '/repo/app', id: 'Shell', enabled: 'false' })).toThrow('enabled must be boolean');
    expect(() => api.listDashboardLogs({ limit: 0 })).toThrow('limit must be a positive integer');
  });

  it('wraps config, project, preference, and dashboard page RPCs with stable payloads', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(guardedBackendResponse(method)));
    const api = createBackendApi({ callAPI });

    await api.readConfig();
    await api.getWindowBootstrap();
    await api.getSidebarState({ cwd: '/repo/app' });
    await api.getThreadState({ cwd: '/repo/app', threadId: 'thread-1' });
    await api.getProjects({ cwd: '/repo/app' });
    await api.setActiveProject({ cwd: '/repo/app', path: '/repo/next' });
    await api.addProject({ cwd: '/repo/app', path: '/repo/new' });
    await api.removeProject({ cwd: '/repo/app', path: '/repo/old' });
    await api.getPreference({ key: 'settings.provider.active' });
    await api.getAllPreferences({});
    await api.setPreference({ key: 'settings.provider.active', value: 'codex' });
    await api.getDashboardPage({ cwd: '/repo/app', page: 'settings' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_READ, {});
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_WINDOW_BOOTSTRAP_GET, {});
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_SIDEBAR_GET, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_STATE_GET, { cwd: '/repo/app', threadId: 'thread-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PROJECTS_GET, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PROJECTS_SET_ACTIVE, { cwd: '/repo/app', path: '/repo/next' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PROJECTS_ADD, { cwd: '/repo/app', path: '/repo/new' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PROJECTS_REMOVE, { cwd: '/repo/app', path: '/repo/old' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PREFERENCES_GET, { key: 'settings.provider.active' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PREFERENCES_GET_ALL, {});
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PREFERENCES_SET, { key: 'settings.provider.active', value: 'codex' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_DASHBOARD_GET, { cwd: '/repo/app', page: 'settings' });

    expect(() => api.getSidebarState({ cwd: '' })).toThrow('cwd is required');
    expect(() => api.getThreadState({ cwd: '/repo/app', threadId: '' })).toThrow('threadId is required');
    expect(() => api.setActiveProject({ cwd: '/repo/app', path: '' })).toThrow('path is required');
    expect(() => api.setPreference({ key: '', value: 'codex' })).toThrow('key is required');
    expect(() => api.setPreference({ key: 'settings.provider.active' })).toThrow('value is required');
    expect(() => api.getDashboardPage({ cwd: '/repo/app', page: '' })).toThrow('page is required');
  });

  it('rejects unknown full UI state fields through the backend facade', async () => {
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue({ threads: [], agents: [], token_usage: {}, surprise: true }),
    });

    await expect(api.getThreadState({ cwd: '/repo/app', threadId: 'thread-1' }))
      .rejects.toThrow('ui/state/get response body must not include surprise');
  });

  it('exposes model provider management RPC facade methods', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(guardedBackendResponse(method)));
    const api = createBackendApi({ callAPI });
    const registry = { vendors: [{ id: 'openrouter', label: 'OpenRouter', enabled: true, baseURL: 'https://openrouter.ai/api/v1', envKey: 'OPENROUTER_API_KEY', codexModelProvider: 'openrouter', defaultModel: 'openai/gpt-4.1' }] };

    await api.listModelProviders({ cwd: '/repo/app' });
    await api.saveModelProviders({ cwd: '/repo/app', registry });
    await api.applyModelProvider({ cwd: '/repo/app', vendorId: 'openrouter' });

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.MODEL_PROVIDERS_LIST, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.MODEL_PROVIDERS_SAVE, { cwd: '/repo/app', registry });
    expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.MODEL_PROVIDERS_APPLY, { cwd: '/repo/app', vendorId: 'openrouter' });
  });

  it('wraps thread read RPCs with stable payloads', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(guardedBackendResponse(method)));
    const api = createBackendApi({ callAPI });

    await api.getThreadMessages({ threadId: 'thread-1', limit: 20, before: 'cursor-1' });
    await api.resolveThreadIdentity({ cwd: '/repo/app', thread_id: 'thread-2' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_MESSAGES, { threadId: 'thread-1', limit: 20, before: 'cursor-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_RESOLVE, { threadId: 'thread-2' });

    expect(() => api.getThreadMessages({ threadId: '' })).toThrow('threadId is required');
    expect(() => api.getThreadMessages({ threadId: 'thread-1', surprise: true })).toThrow('thread/messages: unsupported payload field surprise');
    expect(() => api.resolveThreadIdentity({})).toThrow('threadId is required');
  });

  it('wraps video API key RPCs with named facade methods', async () => {
    const getResponse = { configured: true, masked: 'sk***ed' };
    const setResponse = { ok: true };
    const callAPI = vi.fn()
      .mockResolvedValueOnce(getResponse)
      .mockResolvedValueOnce(setResponse)
      .mockRejectedValueOnce(new Error('credential store unavailable'));
    const api = createBackendApi({ callAPI });

    await expect(api.getVideoApiKey()).resolves.toEqual(getResponse);
    await expect(api.setVideoApiKey({
      apiKey: ' sk-test-key ',
      unexpectedUiOnlyField: 'must-not-leak',
    })).resolves.toEqual(setResponse);
    await expect(api.setVideoApiKey({ apiKey: 'sk-test-key-2' }))
      .rejects.toThrow('credential store unavailable');

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.UI_VIDEO_GET_API_KEY, {});
    expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.UI_VIDEO_SET_API_KEY, { apiKey: 'sk-test-key' });
    expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.UI_VIDEO_SET_API_KEY, { apiKey: 'sk-test-key-2' });
    expectInvalidInputDoesNotCall(callAPI, () => api.setVideoApiKey({ apiKey: '' }), 'apiKey is required');
    expect(typeof getVideoApiKey).toBe('function');
    expect(typeof setVideoApiKey).toBe('function');
  });

async function callDagDashboardApis(api) {
  await api.listDags({ status: 'running', keyword: 'build', limit: 7 });
  await api.getDagDetail({ dagKey: 'dag-1' });
  await api.getDagRuns({ dagKey: 'dag-1', status: 'running', limit: 5 });
  await api.getDagRun({ runKey: 'run-1' });
  await api.startDag({ dagKey: 'dag-1', triggerSource: 'manual', idempotencyKey: 'ui-123' });
  await api.createAndStartDag({
    dagKey: 'dag-created',
    title: 'Created DAG',
    description: 'Created from template',
    finalNodeKey: 'final',
    metadata: { source: 'ui-template' },
    idempotencyKey: 'ui-create-123',
    nodes: [{
      nodeKey: 'draft',
      title: 'Draft',
      nodeType: 'agent',
      assignedTo: 'codex-runner',
      dependsOn: [],
      config: { prompt: 'draft' },
    }],
  });
  await api.writeWorkflowMaterial({
    path: 'reports/workflows/uploads/dag-1/material.md',
    content: 'source text',
  });
  await api.dispatchDagNode({ dagKey: 'dag-1', runId: 88, nodeKey: 'draft', assignedTo: 'codex-runner' });
  await api.terminateDagRun({ dagKey: 'dag-1', runKey: 'run-1', reason: 'user_requested' });
  await api.deleteDag({ dagKey: 'dag-1' });
  await api.applyDagOps({
    dagKey: 'dag-1',
    baseVersion: 11,
    ops: [{ op: 'update_node', node_key: 'draft', patch: { title: 'Draft v2' } }],
  });
}

function expectDagDashboardCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAGS, {
    status: 'running',
    keyword: 'build',
    limit: 7,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_DETAIL, { dagKey: 'dag-1' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_RUNS, {
    dagKey: 'dag-1',
    status: 'running',
    limit: 5,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_RUN, { runKey: 'run-1' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_START, {
    dagKey: 'dag-1',
    triggerSource: 'manual',
    idempotencyKey: 'ui-123',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_CREATE_AND_START, {
    dagKey: 'dag-created',
    title: 'Created DAG',
    description: 'Created from template',
    finalNodeKey: 'final',
    metadata: { source: 'ui-template' },
    idempotencyKey: 'ui-create-123',
    nodes: [{
      nodeKey: 'draft',
      title: 'Draft',
      nodeType: 'agent',
      assignedTo: 'codex-runner',
      dependsOn: [],
      config: { prompt: 'draft' },
    }],
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_WORKFLOW_MATERIAL_WRITE, {
    path: 'reports/workflows/uploads/dag-1/material.md',
    content: 'source text',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_DISPATCH_NODE, {
    dagKey: 'dag-1',
    runId: 88,
    nodeKey: 'draft',
    assignedTo: 'codex-runner',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_TERMINATE, {
    dagKey: 'dag-1',
    runKey: 'run-1',
    reason: 'user_requested',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_DELETE, { dagKey: 'dag-1' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_DAG_APPLY_OPS, {
    dagKey: 'dag-1',
    baseVersion: 11,
    ops: [{ op: 'update_node', node_key: 'draft', patch: { title: 'Draft v2' } }],
  });
}

  it('wraps the independent new-window RPC with cwd validation', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true, windowId: 'window-2', cwd: '/repo/window' });
    const api = createBackendApi({ callAPI });

    await api.openNewWindow({ cwd: '/repo/window' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_OPEN_NEW_WINDOW, { cwd: '/repo/window' });
    expect(() => api.openNewWindow({ cwd: '' })).toThrow('cwd is required');
  });

  it('wraps shared file list, read, open and preview helpers with the expected payload shapes', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(
      method === RPC_METHODS.DASHBOARD_SHARED_FILES
        ? { files: [{ path: 'reports/final.md', content: '' }], finalOutputRefs: [], sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 } }
        : guardedBackendResponse(method),
    ));
    const openSharedFile = vi.fn().mockResolvedValue({ opened: true });
    const previewSharedFile = vi.fn().mockResolvedValue({ url: '/shared-file-preview?id=sf_1' });
    const api = createBackendApi({ callAPI, openSharedFile, previewSharedFile });

    await api.listSharedFiles();
    await api.readSharedFile({ path: 'reports/final.md' });
    await api.openSharedFile({ path: 'dag/video/final.mp4' });
    await api.previewSharedFile({ path: 'dag/video/final.mp4' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_SHARED_FILES, {});
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_SHARED_FILES, {});
    expect(openSharedFile).toHaveBeenCalledWith({ path: 'dag/video/final.mp4' });
    expect(previewSharedFile).toHaveBeenCalledWith({ path: 'dag/video/final.mp4' });
    expect(() => api.listSharedFiles([])).toThrow('params must be an object');
    await expect(api.readSharedFile({ path: '' })).rejects.toThrow('path is required');
    expect(() => api.previewSharedFile({ path: '' })).toThrow('path is required');
  });

  it('rejects malformed shared file detail responses at the RPC boundary', async () => {
    const callAPI = vi.fn().mockResolvedValue({ content: 'missing path' });
    const api = createBackendApi({ callAPI });

    await expect(api.readSharedFile({ path: 'reports/final.md' }))
      .rejects.toThrow(/shared files dashboard response files must be an array/);
  });

  it('wraps runtime code locate, open and save RPCs with scoped payloads', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(
      method === RPC_METHODS.UI_CODE_SAVE ? codeSaveResponse() : { ok: true },
    ));
    const api = createBackendApi({ callAPI });

    await api.locateCodeFile({ filePath: 'src/App.jsx', project: '/repo/app', projects: ['/repo/app'] });
    await api.openCodeFile({ filePath: 'src/App.jsx', project: '/repo/app', projects: ['/repo/app'], line: 10, column: 2 });
    await api.openPath({ filePath: 'src', project: '/repo/app', projects: ['/repo/app'] });
    await api.saveCodeFile({ filePath: 'src/App.jsx', content: 'export default App;', project: '/repo/app', projects: ['/repo/app'], previewMode: 'full', contentVersion: 'sha256:app-v1' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_CODE_LOCATE, {
      filePath: 'src/App.jsx',
      project: '/repo/app',
      projects: ['/repo/app'],
    });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_CODE_OPEN, {
      filePath: 'src/App.jsx',
      project: '/repo/app',
      projects: ['/repo/app'],
      line: 10,
      column: 2,
    });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_PATH_OPEN, {
      filePath: 'src',
      project: '/repo/app',
      projects: ['/repo/app'],
    });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_CODE_SAVE, {
      filePath: 'src/App.jsx',
      content: 'export default App;',
      project: '/repo/app',
      projects: ['/repo/app'], previewMode: 'full', contentVersion: 'sha256:app-v1',
    });
    expect(() => api.locateCodeFile({ filePath: '' })).toThrow('filePath is required');
    expect(() => api.openCodeFile({ filePath: '' })).toThrow('filePath is required');
    expect(() => api.openPath({ filePath: '' })).toThrow('filePath is required');
    expect(() => api.saveCodeFile({ filePath: 'src/App.jsx' })).toThrow('content is required');
    expect(() => api.saveCodeFile({ filePath: 'src/App.jsx', content: null })).toThrow('content must be a string');
  });

  it('lists the canonical toolbridge catalog for one cwd', async () => {
    const response = { tools: [] };
    const callAPI = vi.fn().mockResolvedValue(response);
    const api = createBackendApi({ callAPI });

    await expect(api.listToolbridgeTools({ cwd: '/repo/app' })).resolves.toEqual(response);

    expect(callAPI).toHaveBeenCalledWith(
      RPC_METHODS.TOOLBRIDGE_TOOLS_LIST,
      { cwd: '/repo/app' },
    );
    expectInvalidInputDoesNotCall(
      callAPI,
      () => api.listToolbridgeTools({ cwd: '/repo/app', serverName: 'lsp' }),
      'toolbridge/tools/list: unsupported payload field serverName',
    );
    expectInvalidInputDoesNotCall(
      callAPI,
      () => api.listToolbridgeTools({ cwd: ' ' }),
      'toolbridge/tools/list: cwd is required',
    );
    expect(typeof listToolbridgeTools).toBe('function');
  });

  it('rejects an invalid recover response before a runtime consumer can observe it', async () => {
    const callAPI = vi.fn().mockResolvedValue({
      thread: { id: 'thread-1', status: 'recovering' },
      recovered: true,
      mode: 'relaunch_resume',
      unexpected: true,
    });
    const runtimeConsumer = vi.fn();
    const api = createBackendApi({ callAPI });

    await expect(
      api.recoverThread({ cwd: '/repo/app', threadId: 'thread-1' }).then(runtimeConsumer),
    ).rejects.toThrow('thread/recover response body must not include unexpected');

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_RECOVER, { threadId: 'thread-1' });
    expect(runtimeConsumer).not.toHaveBeenCalled();
  });

  it('wraps prompt history with the exact bounded request contract', async () => {
    const response = { entries: [], nextCursor: '', hasMore: false, nonce: 'nonce-1' };
    const callAPI = vi.fn().mockResolvedValue(response);
    const api = createBackendApi({ callAPI });

    await expect(api.getPromptHistory({
      cwd: ' /repo/app ',
      activeThreadId: 'thread-1',
      cursor: ' cursor-1 ',
      nonce: ' nonce-1 ',
      limit: 50,
    })).resolves.toBe(response);
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_PROMPT_HISTORY, {
      cwd: '/repo/app',
      activeThreadId: 'thread-1',
      cursor: ' cursor-1 ',
      nonce: ' nonce-1 ',
      limit: 50,
    });

    for (const params of [
      { cwd: '', limit: 50 },
      { cwd: '/repo/app', limit: 0 },
      { cwd: '/repo/app', limit: 51 },
      { cwd: '/repo/app', limit: 1.5 },
      { cwd: '/repo/app', limit: '10' },
      { cwd: '/repo/app', cursor: 'x'.repeat(2049), limit: 10 },
      { cwd: '/repo/app', nonce: 'x'.repeat(2049), limit: 10 },
      { cwd: '/repo/app', cursor: '界'.repeat(683), limit: 10 },
      { cwd: '/repo/app', limit: 10, surprise: true },
    ]) {
      expect(() => api.getPromptHistory(params)).toThrow();
    }
    expect(callAPI).toHaveBeenCalledTimes(1);
    expect(getPromptHistory).toBeTypeOf('function');
  });
