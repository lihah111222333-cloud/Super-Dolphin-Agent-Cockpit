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
  importDatasourceLocalFile,
  installAppUpdate,
  installLatestAppUpdate,
  listDatasourceDocuments,
  listMCPServers,
  getVideoApiKey,
  RPC_METHODS,
  rollbackWorkflowTemplate,
  saveWorkflowTemplate,
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

  it('exposes the dedicated frontend observability ingest RPC method name', () => {
    expect(RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST).toBe('observability/frontend/ingest');
    expect(typeof emitFrontendTraceEvent).toBe('function');
  });

  it('maps observability query helpers to dedicated RPC methods', async () => {
    const response = { source: 'memory', events: [{ traceId: 'trace-1' }] };
    const callAPI = vi.fn().mockResolvedValue(response);
    const api = createBackendApi({ callAPI });

    await expect(api.getObservabilityTrace({ trace_id: 'trace-1', limit: 5 })).resolves.toEqual(response);
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
  });

  it('wraps datasource_v2 CRUD RPC methods with strict payloads', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.createDatasourceDocument({ source_path: ' C:\\data\\alpha.txt ' });
    await api.listDatasourceDocuments({ keyword: 'alpha', limit: '25' });
    await api.getDatasourceDocument({ document_id: '101' });
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
    expect(callAPI).toHaveBeenNthCalledWith(4, RPC_METHODS.DATASOURCE_V2_UPDATE, {
      documentId: 101,
      sourcePath: 'C:\\data\\alpha-renamed.txt',
      fileName: 'alpha-renamed.txt',
      extension: '.txt',
      sizeBytes: 42,
    });
    expect(callAPI).toHaveBeenNthCalledWith(5, RPC_METHODS.DATASOURCE_V2_DELETE, {
      documentId: 101,
    });
    expectInvalidInputDoesNotCall(callAPI, () => api.createDatasourceDocument({ sourcePath: '' }), 'sourcePath is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.listDatasourceDocuments({}), 'limit must be a positive integer');
    expectInvalidInputDoesNotCall(callAPI, () => api.getDatasourceDocument({ documentId: 0 }), 'documentId is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.updateDatasourceDocument({ documentId: 101, sourcePath: 'C:\\data\\a.txt', sizeBytes: 1 }), 'fileName is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.deleteDatasourceDocument({ documentId: '' }), 'documentId is required');
    expect(typeof createDatasourceDocument).toBe('function');
    expect(typeof listDatasourceDocuments).toBe('function');
    expect(typeof getDatasourceDocument).toBe('function');
    expect(typeof updateDatasourceDocument).toBe('function');
    expect(typeof deleteDatasourceDocument).toBe('function');
  });

  it('maps user-selected datasource imports to the local file RPC', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.importDatasourceLocalFile({ source_path: ' D:\\new\\fj.txt ' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DATASOURCE_V2_IMPORT_LOCAL_FILE, {
      sourcePath: 'D:\\new\\fj.txt',
    });
    expectInvalidInputDoesNotCall(callAPI, () => api.importDatasourceLocalFile({ sourcePath: '' }), 'sourcePath is required');
    expect(typeof importDatasourceLocalFile).toBe('function');
  });

  it('wraps app update RPC methods', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
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

  it('wraps MCP server list and default controls with strict empty payloads', async () => {
    const listResponse = { mcpServers: { sqlite: { enabled: false } } };
    const startResponse = { serverName: 'sqlite', enabled: true };
    const stopResponse = { serverName: 'sqlite', enabled: false };
    const playwrightStartResponse = { serverName: 'playwright', enabled: true };
    const playwrightStopResponse = { serverName: 'playwright', enabled: false };
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

  it('wraps workflow template RPC methods with canonical payloads', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
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
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
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
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.archiveThread({ cwd: '/repo/app', threadId: 'thread-1' });
    await api.unarchiveThread({ cwd: '/repo/app', thread_id: 'thread-2' });
    await api.deleteThread({ cwd: '/repo/app', threadId: 'thread-3' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_ARCHIVE, { threadId: 'thread-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_UNARCHIVE, { threadId: 'thread-2' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_DELETE, { threadId: 'thread-3' });
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
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.renameThread({ cwd: '/repo/app', threadId: 'thread-1', name: 'Renamed' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_NAME_SET, {
      threadId: 'thread-1',
      name: 'Renamed',
    });
  });

  it('maps thread config get and set to legacy thread config RPCs', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
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

  it('strips cwd from strict thread-scoped runtime RPC payloads', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.interruptTurn({ cwd: '/repo/app', threadId: 'thread-1', turnId: 'turn-1', source: 'ui_stop' });
    await api.forceCompleteTurn({ cwd: '/repo/app', threadId: 'thread-1' });
    await api.compactThread({ cwd: '/repo/app', threadId: 'thread-1' });
    await api.recoverThread({ cwd: '/repo/app', threadId: 'thread-1' });

    expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.TURN_INTERRUPT, {
      thread_id: 'thread-1',
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

  it('wraps approval/respond with strict request id and decision payloads', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.respondApproval({ requestId: 11, approved: false });

    expect(RPC_METHODS.APPROVAL_RESPOND).toBe('approval/respond');
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.APPROVAL_RESPOND, {
      requestId: 11,
      approved: false,
    });
    expect(() => api.respondApproval({ requestId: 0, approved: true }))
      .toThrow('approval/respond: requestId is required');
    expect(() => api.respondApproval({ requestId: 11 }))
      .toThrow('approval/respond: approved is required');
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

    await expect(api.interruptTurn({ cwd: '/repo/app', threadId: 'thread-1', turnId: 'turn-1', source: 'ui_stop' }))
      .resolves.toEqual(response);

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.TURN_INTERRUPT, {
      thread_id: 'thread-1',
      source: 'ui_stop',
    });
  });

  it('fails fast before cwd-scoped RPCs when cwd is missing', () => {
    const callAPI = vi.fn();
    const api = createBackendApi({ callAPI });

    expectInvalidInputDoesNotCall(callAPI, () => api.getProjects({ cwd: '' }), 'cwd is required');
    expectInvalidInputDoesNotCall(callAPI, () => api.startThread({ cwd: '/repo/app', name: 'Hello' }), 'provider is required');
  });

  it('deletes skills with cwd, scope, and personal type', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.deleteSkill({
      cwd: '/repo/app',
      name: 'DocsSkill',
      scope: 'personal',
      personalType: 'user',
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_DELETE, {
      cwd: '/repo/app',
      name: 'DocsSkill',
      scope: 'personal',
      personal_type: 'user',
    });
    expect(() => api.deleteSkill({ cwd: '/repo/app', name: 'DocsSkill', scope: 'system' }))
      .toThrow('scope must be project or personal');
  });

  it('creates project skills through the dedicated internal skills/create RPC', async () => {
    const callAPI = vi.fn().mockResolvedValue({ path: '/repo/app/.agent/skills/DocsSkill/SKILL.md' });
    const api = createBackendApi({ callAPI });

    await api.createSkill({
      cwd: '/repo/app',
      name: 'DocsSkill',
      content: '---\nname: "DocsSkill"\n---\n\n## Docs',
    });

    expect(RPC_METHODS.SKILLS_CREATE).toBe('skills/create');
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_CREATE, {
      cwd: '/repo/app',
      name: 'DocsSkill',
      content: '---\nname: "DocsSkill"\n---\n\n## Docs',
    });
    expect(() => api.createSkill({ cwd: '/repo/app', name: '', content: 'body' }))
      .toThrow('skills/create: name is required');
    expect(() => api.createSkill({ cwd: '/repo/app', name: 'DocsSkill' }))
      .toThrow('skills/create: content is required');
  });

  it('wraps skill editor and import RPCs with legacy payload shapes', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(
      method === RPC_METHODS.SKILLS_SUMMARY_SUGGEST
        ? { description: '当你需要编写文档时使用。' }
        : { ok: true },
    ));
    const selectProjectDirs = vi.fn().mockResolvedValue(['/imports/a']);
    const api = createBackendApi({ callAPI, selectProjectDirs });

    await callSkillEditorApis(api);
    await api.selectProjectDirs();

    expectSkillEditorCalls(callAPI);
    expect(selectProjectDirs).toHaveBeenCalledTimes(1);
  });

async function callSkillEditorApis(api) {
  await api.readSkill({ cwd: '/repo/app', path: '/repo/app/.agent/skills/docs/SKILL.md' });
  await api.listSkillFiles({ cwd: '/repo/app', dir: '/repo/app/.agent/skills/docs' });
  await api.writeSkill({ cwd: '/repo/app', path: 'DocsSkill', content: '---', scope: 'personal', personalType: 'user' });
  await api.importSkillDirectories({ cwd: '/repo/app', paths: ['/imports/a'], scope: 'personal', personal_type: 'imported' });
  await api.suggestSkillSummary({ cwd: '/repo/app', name: 'DocsSkill', description: '', content: 'body', scenario_words: ['docs'], scope: 'project' });
}

function expectSkillEditorCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_READ, {
    cwd: '/repo/app',
    path: '/repo/app/.agent/skills/docs/SKILL.md',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_LIST_FILES, {
    cwd: '/repo/app',
    dir: '/repo/app/.agent/skills/docs',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_WRITE, {
    cwd: '/repo/app',
    path: 'DocsSkill',
    content: '---',
    scope: 'personal',
    personal_type: 'user',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_IMPORT_DIR, {
    cwd: '/repo/app',
    paths: ['/imports/a'],
    scope: 'personal',
    personal_type: 'imported',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_SUMMARY_SUGGEST, {
    cwd: '/repo/app',
    name: 'DocsSkill',
    description: '',
    content: 'body',
    scenario_words: ['docs'],
    scope: 'project',
  });
}

  it('normalizes skill summary suggestions to description text', async () => {
    const callAPI = vi.fn().mockResolvedValue({ description: ' 当你需要部署服务时使用。 ' });
    const api = createBackendApi({ callAPI });

    await expect(api.suggestSkillSummary({
      cwd: '/repo/app',
      name: 'DeploySkill',
      description: '',
      content: 'body',
      scenario_words: ['deploy'],
      scope: 'project',
      provider: 'codex',
      model: 'gpt-5.5',
      codexModelProvider: 'openrouter',
    })).resolves.toBe('当你需要部署服务时使用。');

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_SUMMARY_SUGGEST, {
      cwd: '/repo/app',
      name: 'DeploySkill',
      description: '',
      content: 'body',
      scenario_words: ['deploy'],
      scope: 'project',
      provider: 'codex',
      model: 'gpt-5.5',
      model_provider: 'openrouter',
    });
  });

  it('does not duplicate skill summary retry at the frontend facade', async () => {
    const callAPI = vi.fn().mockRejectedValueOnce(new Error('parse skill summary suggestion: invalid character'));
    const api = createBackendApi({ callAPI });

    await expect(api.suggestSkillSummary({
      cwd: '/repo/app',
      name: '部署技能',
      description: '',
      content: 'body',
      scenario_words: ['deploy'],
      scope: 'project',
    })).rejects.toThrow('parse skill summary suggestion');

    expect(callAPI).toHaveBeenCalledTimes(1);
  });

  it('wraps skill resolution preview and apply payloads', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.listSkillResolutions({ cwd: '/repo/app' });
    await api.previewSkillResolution({
      cwd: '/repo/app',
      conflictId: 'c1',
      action: 'view_diff',
      sourceProvider: 'codex',
      sourcePathId: 'codex:docs',
    });
    await api.applySkillResolution({
      cwd: '/repo/app',
      conflict_id: 'c1',
      action: 'canonical_overwrite_mirror',
      previewId: 'p1',
      previewHash: 'h1',
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_RESOLUTION_LIST, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_RESOLUTION_PREVIEW, {
      cwd: '/repo/app',
      conflict_id: 'c1',
      action: 'view_diff',
      source_provider: 'codex',
      source_path_id: 'codex:docs',
    });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_RESOLUTION_APPLY, {
      cwd: '/repo/app',
      conflict_id: 'c1',
      action: 'canonical_overwrite_mirror',
      preview_id: 'p1',
      preview_hash: 'h1',
    });
  });

  it('wraps DAG dashboard RPCs with the legacy payload shapes', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
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

  it('wraps cronjob RPCs with the legacy payload shapes', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.listCronJobs();
    await api.getCronJob({ id: 'job-1' });
    await api.createCronJob({ name: 'nightly', prompt: 'run tests' });
    await api.updateCronJob({ id: 'job-1', name: 'nightly v2', enabled: false });
    await api.deleteCronJob({ id: 'job-1' });
    await api.runCronJobOnce({ id: 'job-1' });
    await api.setCronJobEnabled({ id: 'job-1', enabled: true });
    await api.listCronJobRuns({ jobId: 'job-1', limit: 50 });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_LIST, {});
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_GET, { id: 'job-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_CREATE, { name: 'nightly', prompt: 'run tests' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_UPDATE, { id: 'job-1', name: 'nightly v2', enabled: false });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_DELETE, { id: 'job-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_RUN_ONCE, { id: 'job-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_SET_ENABLED, { id: 'job-1', enabled: true });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CRONJOB_LIST_RUNS, { job_id: 'job-1', limit: 50 });
    expect(() => api.getCronJob({ id: '' })).toThrow('id is required');
    expect(() => api.setCronJobEnabled({ id: 'job-1', enabled: 'true' })).toThrow('enabled must be boolean');
    expect(() => api.listCronJobRuns({ jobId: '' })).toThrow('job_id is required');
  });

  it('wraps settings config RPCs with the internal uistate method names', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.readLspPromptHint({ cwd: '/repo/app' });
    await api.writeLspPromptHint({ cwd: '/repo/app', hint: 'custom prompt' });
    await api.readBuiltinTools({ cwd: '/repo/app' });
    await api.writeBuiltinTool({ cwd: '/repo/app', id: 'Shell', enabled: false });
    await api.listDashboardLogs({ limit: 14 });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_READ, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_LSP_PROMPT_HINT_WRITE, { cwd: '/repo/app', hint: 'custom prompt' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_BUILTIN_TOOLS_READ, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.CONFIG_BUILTIN_TOOLS_WRITE, { cwd: '/repo/app', id: 'Shell', enabled: false });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_LOGS, { limit: 14 });
    expect(() => api.readLspPromptHint({ cwd: '' })).toThrow('cwd is required');
    expect(() => api.writeLspPromptHint({ cwd: '/repo/app' })).toThrow('hint is required');
    expect(() => api.writeBuiltinTool({ cwd: '/repo/app', id: '', enabled: true })).toThrow('id is required');
    expect(() => api.writeBuiltinTool({ cwd: '/repo/app', id: 'Shell', enabled: 'false' })).toThrow('enabled must be boolean');
    expect(() => api.listDashboardLogs({ limit: 0 })).toThrow('limit must be a positive integer');
  });

  it('wraps config, project, preference, and dashboard page RPCs with stable payloads', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
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

  it('wraps prompt-section and thread read RPCs with stable payloads', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.listPromptSections({ cwd: '/repo/app', prompt_id: 'prompt-1' });
    await api.writePromptSection({ cwd: '/repo/app', prompt_id: 'prompt-1', section: 'body' });
    await api.deletePromptSection({ cwd: '/repo/app', prompt_id: 'prompt-1', section: 'body' });
    await api.getThreadMessages({ threadId: 'thread-1' });
    await api.resolveThreadIdentity({ thread_id: 'thread-2' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_SECTIONS_LIST, { cwd: '/repo/app', prompt_id: 'prompt-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_SECTIONS_WRITE, { cwd: '/repo/app', prompt_id: 'prompt-1', section: 'body' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_SECTIONS_DELETE, { cwd: '/repo/app', prompt_id: 'prompt-1', section: 'body' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_MESSAGES, { threadId: 'thread-1' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_RESOLVE, { thread_id: 'thread-2', threadId: 'thread-2' });

    expect(() => api.listPromptSections({ cwd: '', prompt_id: 'prompt-1' })).toThrow('cwd is required');
    expect(() => api.writePromptSection({ cwd: '/repo/app', prompt_id: '' })).toThrow('prompt_id is required');
    expect(() => api.getThreadMessages({ threadId: '' })).toThrow('threadId is required');
    expect(() => api.resolveThreadIdentity({})).toThrow('threadId is required');
  });

  it('wraps video API key RPCs with named facade methods', async () => {
    const getResponse = { configured: true, masked: 'sk***ed' };
    const setResponse = { ok: true, savedAt: '2026-06-08T12:00:00Z' };
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


async function callPromptFacadeMethods(api) {
  await api.listPromptAssets({ cwd: '/repo/app' });
  await api.getDashboardPrompts({ cwd: '/repo/app' });
  await api.getPrompt({ cwd: '/repo/app', id: 'main/reviewer' });
  await writePromptFacadePrompt(api);
  await api.deletePrompt({ cwd: '/repo/app', id: 'main/reviewer', scope: 'global' });
  await callPromptIntentFacadeMethods(api);
}

async function writePromptFacadePrompt(api) {
  await api.writePrompt({
    cwd: '/repo/app',
    id: 'main/reviewer',
    name: '代码审查专家',
    description: '审查代码质量',
    agentType: 'coder',
    when_to_use: 'Use for code review.',
    content: '先检查阻塞问题',
    tags: ['review'],
    enabled: true,
    scope: 'global',
    priority: 5,
  });
}

async function callPromptIntentFacadeMethods(api) {
  await api.getPersonalizationProfile({ cwd: '/repo/app' });
  await api.savePersonalizationProfile({
    cwd: '/repo/app',
    profile: {
      displayName: ' 小海 ',
      role: '后端工程师',
      background: '熟悉 Go',
      customInstructions: '回答要直接',
    },
  });
  await api.draftPromptIntent({
    cwd: '/repo/app',
    kind: 'expert',
    rawInput: '当用户要求代码审查时使用。',
    sourceType: 'user_input',
    scope: 'project',
    provider: 'codex',
    model: 'gpt-5.5',
    codexModelProvider: 'openrouter',
  });
  await api.commitPromptIntent({ cwd: '/repo/app', draftKey: 'intent/expert/review' });
  await api.draftPromptIntent({
    cwd: '/repo/app',
    kind: 'expert',
    rawInput: '全局使用这条提示词。',
    scope: 'global',
  });
  await api.commitPromptIntent({ cwd: '/repo/app', draftKey: 'intent/expert/global', scope: 'global' });
  await api.discardPromptIntent({ cwd: '/repo/app', draft_key: 'intent/expert/review' });
  await api.dryRunPromptIntent({
    cwd: '/repo/app',
    draftKey: 'intent/expert/review',
    kind: 'expert',
    card: { title: '代码审查专家' },
    question: '帮我审查这段代码',
  });
}

function expectPromptFacadeCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_ASSETS_LIST, { cwd: '/repo/app' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_PROMPTS, { cwd: '/repo/app' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPTS_GET, { cwd: '/repo/app', id: 'main/reviewer' });
  expectPromptWriteCall(callAPI);
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPTS_DELETE, {
    cwd: '/repo/app',
    id: 'main/reviewer',
    scope: 'global',
  });
  expectPromptIntentFacadeCalls(callAPI);
}

function expectPromptWriteCall(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPTS_WRITE, {
    cwd: '/repo/app',
    id: 'main/reviewer',
    name: '代码审查专家',
    description: '审查代码质量',
    agentType: 'coder',
    when_to_use: 'Use for code review.',
    content: '先检查阻塞问题',
    tags: ['review'],
    enabled: true,
    scope: 'global',
    priority: 5,
  });
}

function expectPromptIntentFacadeCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PERSONALIZATION_PROFILE_GET, {
    cwd: '/repo/app',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PERSONALIZATION_PROFILE_SAVE, {
    cwd: '/repo/app',
    profile: {
      displayName: ' 小海 ',
      role: '后端工程师',
      background: '熟悉 Go',
      customInstructions: '回答要直接',
    },
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_DRAFT, {
    cwd: '/repo/app',
    kind: 'expert',
    raw_input: '当用户要求代码审查时使用。',
    source_type: 'user_input',
    provider: 'codex',
    model: 'gpt-5.5',
    model_provider: 'openrouter',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_COMMIT, {
    cwd: '/repo/app',
    draft_key: 'intent/expert/review',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_DRAFT, {
    cwd: '/repo/app',
    kind: 'expert',
    raw_input: '全局使用这条提示词。',
    source_type: 'user_input',
    enable_global: true,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_COMMIT, {
    cwd: '/repo/app',
    draft_key: 'intent/expert/global',
    enable_global: true,
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_DISCARD, {
    cwd: '/repo/app',
    draft_key: 'intent/expert/review',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_INTENTS_DRY_RUN, {
    cwd: '/repo/app',
    draft_key: 'intent/expert/review',
    kind: 'expert',
    card: { title: '代码审查专家' },
    question: '帮我审查这段代码',
  });
}

function expectPromptFacadeValidation(api) {
  expect(() => api.listPromptAssets({ cwd: '' })).toThrow('cwd is required');
  expect(() => api.getPrompt({ cwd: '/repo/app', id: '' })).toThrow('id is required');
  expect(() => api.writePrompt({ cwd: '/repo/app', name: '' })).toThrow('name is required');
  expect(() => api.commitPromptIntent({ cwd: '/repo/app', draftKey: '' })).toThrow('draft_key is required');
  expect(() => api.dryRunPromptIntent({ cwd: '/repo/app', draftKey: 'd1', question: '' })).toThrow('question is required');
  expect(() => api.getPersonalizationProfile({ cwd: '' })).toThrow('cwd is required');
  expect(() => api.savePersonalizationProfile({ cwd: '', profile: {} })).toThrow('cwd is required');
  expect(() => api.savePersonalizationProfile({ cwd: '/repo/app', profile: null })).toThrow('profile must be an object');
}

  it('wraps prompt RPCs with legacy payload shapes', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await callPromptFacadeMethods(api);

    expectPromptFacadeCalls(callAPI);
    expectPromptFacadeValidation(api);
  });

  it('wraps memory center RPCs with the legacy payload shapes', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await callMemoryCenterApis(api);

    expectMemoryCenterCalls(callAPI);
    expectMemoryCenterValidation(api);
  });

async function callMemoryCenterApis(api) {
  await api.getMemorySnapshot({ cwd: '/repo/app' });
  await api.getMemoryEntry({ cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
  await api.upsertMemoryEntry({
    cwd: '/repo/app', target: 'private', existingPath: 'feedback/tdd.md',
    name: 'tdd-rule', description: '先写红测', type: 'feedback', content: '规则', title: '遵守 TDD',
  });
  await api.deleteMemoryEntry({ cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
  await api.setMemoryAutoDreamIntent({ enabled: true });
  await callMemorySimilarityApis(api);
}

async function callMemorySimilarityApis(api) {
  await api.mergeMemoryEntries({ cwd: '/repo/app', targetA: 'private', pathA: 'a.md', targetB: 'team', pathB: 'b.md' });
  await api.ignoreMemorySimilarity({ cwd: '/repo/app', targetA: 'private', pathA: 'a.md', targetB: 'team', pathB: 'b.md' });
  await api.consolidateMemorySimilarities({ cwd: '/repo/app' });
  await api.startConsolidateMemorySimilarities({
    cwd: '/repo/app',
    provider: 'codex',
    model: 'gpt-5.5',
    codexModelProvider: 'openai',
  });
  await api.getMemoryConsolidationStatus({ cwd: '/repo/app', jobId: 'memory-job-1' });
}

function expectMemoryCenterCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_GET, { cwd: '/repo/app' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_GET, { cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_UPSERT, {
    cwd: '/repo/app', target: 'private', existingPath: 'feedback/tdd.md',
    name: 'tdd-rule', description: '先写红测', type: 'feedback', content: '规则', title: '遵守 TDD',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_DELETE, { cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT, { enabled: true });
  expectMemorySimilarityCalls(callAPI);
}

function expectMemorySimilarityCalls(callAPI) {
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_MERGE, { cwd: '/repo/app', targetA: 'private', pathA: 'a.md', targetB: 'team', pathB: 'b.md' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_SIMILARITY_IGNORE, { cwd: '/repo/app', targetA: 'private', pathA: 'a.md', targetB: 'team', pathB: 'b.md' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL, { cwd: '/repo/app' });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START, {
    cwd: '/repo/app',
    provider: 'codex',
    model: 'gpt-5.5',
    model_provider: 'openai',
  });
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS, { cwd: '/repo/app', jobId: 'memory-job-1' });
}

function expectMemoryCenterValidation(api) {
  expect(() => api.getMemoryEntry({ cwd: '/repo/app', path: '' })).toThrow('path is required');
  expect(() => api.upsertMemoryEntry({ cwd: '/repo/app', name: 'x', description: 'd', type: 'feedback', content: '' })).toThrow('content is required');
  expect(() => api.setMemoryAutoDreamIntent({})).toThrow('enabled is required');
  expect(() => api.mergeMemoryEntries({ cwd: '/repo/app', targetA: 'private', pathA: 'a.md', targetB: 'team' })).toThrow('pathB is required');
}

  it('wraps the independent new-window RPC with cwd validation', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.openNewWindow({ cwd: '/repo/window' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_OPEN_NEW_WINDOW, { cwd: '/repo/window' });
    expect(() => api.openNewWindow({ cwd: '' })).toThrow('cwd is required');
  });

  it('wraps shared file list, read, delete and native open helpers with the expected payload shapes', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const openSharedFile = vi.fn().mockResolvedValue({ opened: true });
    const api = createBackendApi({ callAPI, openSharedFile });

    await api.listSharedFiles();
    await api.readSharedFile({ path: 'reports/final.md' });
    await api.deleteSharedFile({ path: 'scratch/work.json' });
    await api.openSharedFile({ path: 'dag/video/final.mp4' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_SHARED_FILES, {});
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_SHARED_FILE_GET, {
      path: 'reports/final.md',
    });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_SHARED_FILE_DELETE, {
      path: 'scratch/work.json',
    });
    expect(openSharedFile).toHaveBeenCalledWith({ path: 'dag/video/final.mp4' });
    expect(() => api.listSharedFiles([])).toThrow('params must be an object');
    expect(() => api.readSharedFile({ path: '' })).toThrow('path is required');
    expect(() => api.deleteSharedFile({ path: '' })).toThrow('path is required');
  });

  it('wraps runtime code locate, open and save RPCs with scoped payloads', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.locateCodeFile({ filePath: 'src/App.jsx', project: '/repo/app', projects: ['/repo/app'] });
    await api.openCodeFile({ filePath: 'src/App.jsx', project: '/repo/app', projects: ['/repo/app'], line: 10, column: 2 });
    await api.openPath({ filePath: 'src', project: '/repo/app', projects: ['/repo/app'] });
    await api.saveCodeFile({ filePath: 'src/App.jsx', content: 'export default App;', project: '/repo/app', projects: ['/repo/app'] });

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
      projects: ['/repo/app'],
    });
    expect(() => api.locateCodeFile({ filePath: '' })).toThrow('filePath is required');
    expect(() => api.openCodeFile({ filePath: '' })).toThrow('filePath is required');
    expect(() => api.openPath({ filePath: '' })).toThrow('filePath is required');
    expect(() => api.saveCodeFile({ filePath: 'src/App.jsx' })).toThrow('content is required');
  });
