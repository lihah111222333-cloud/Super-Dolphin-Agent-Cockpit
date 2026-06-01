import { describe, expect, it, vi } from 'vitest';
import { createBackendApi, emitFrontendTraceEvent, RPC_METHODS } from './backendApi.js';

describe('frontend-app backend API facade', () => {
  it('exposes the dedicated frontend observability ingest RPC method name', () => {
    expect(RPC_METHODS.OBSERVABILITY_FRONTEND_INGEST).toBe('observability/frontend/ingest');
    expect(typeof emitFrontendTraceEvent).toBe('function');
  });

  it('maps observability query helpers to dedicated RPC methods', async () => {
    const callAPI = vi.fn().mockResolvedValue({ source: 'memory' });
    const api = createBackendApi({ callAPI });

    await api.getObservabilityTrace({ trace_id: 'trace-1', limit: 5 });
    await api.getObservabilityThreadRecent({ thread_id: 'thread-1', limit: 7 });
    await api.listObservabilitySlow({ component: 'rpc' });
    await api.listObservabilityErrors({ limit: 3 });
    await api.getObservabilityStatus();

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_TRACE_GET, { traceId: 'trace-1', limit: 5 });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_THREAD_RECENT, { threadId: 'thread-1', limit: 7 });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_SLOW_LIST, { component: 'rpc' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_ERROR_LIST, { limit: 3 });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.OBSERVABILITY_STATUS, {});
  });

  it('starts a pending backend thread with the legacy thread/start payload shape', async () => {
    const callAPI = vi.fn().mockResolvedValue({ threadId: 'thread-123' });
    const api = createBackendApi({ callAPI });

    await api.startThread({
      cwd: '/repo/app',
      name: 'Hello',
      provider: 'codex',
      promptKey: 'main/dag_designer_zh',
      agentKey: 'assistant',
      deferSpawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
      optimisticUserMessage: 'Hello',
      skipInitialRuntimeSync: true,
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.THREAD_START, {
      cwd: '/repo/app',
      name: 'Hello',
      modelProvider: 'codex',
      prompt_key: 'main/dag_designer_zh',
      agent_key: 'assistant',
      defer_spawn: true,
      launchIntentId: 'launch_018f00e0-39fc-72ac-a47a-2a858c75d111',
    });
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
      modelProvider: 'claude',
    });
  });

  it('sends turn/start with explicit cwd and file mentions', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.startTurn({
      cwd: '/repo/app',
      threadId: 'thread-123',
      input: 'build it',
      attachments: ['/tmp/a.txt'],
      manualSkillSelection: false,
    });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.TURN_START, {
      cwd: '/repo/app',
      threadId: 'thread-123',
      input: [
        { type: 'text', text: 'build it' },
        { type: 'mention', name: 'a.txt', path: '/tmp/a.txt' },
      ],
      manualSkillSelection: false,
    });
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

  it('fails fast before cwd-scoped RPCs when cwd is missing', () => {
    const callAPI = vi.fn();
    const api = createBackendApi({ callAPI });

    expect(() => api.getProjects({ cwd: '' })).toThrow('cwd is required');
    expect(() => api.startThread({ cwd: '/repo/app', name: 'Hello' })).toThrow('provider is required');
    expect(callAPI).not.toHaveBeenCalled();
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

  it('wraps skill editor and import RPCs with legacy payload shapes', async () => {
    const callAPI = vi.fn((method) => Promise.resolve(
      method === RPC_METHODS.SKILLS_SUMMARY_SUGGEST
        ? { description: '当你需要编写文档时使用。' }
        : { ok: true },
    ));
    const selectProjectDirs = vi.fn().mockResolvedValue(['/imports/a']);
    const api = createBackendApi({ callAPI, selectProjectDirs });

    await api.readSkill({ cwd: '/repo/app', path: '/repo/app/.agent/skills/docs/SKILL.md' });
    await api.listSkillFiles({ cwd: '/repo/app', dir: '/repo/app/.agent/skills/docs' });
    await api.writeSkill({ cwd: '/repo/app', path: 'DocsSkill', content: '---', scope: 'personal', personalType: 'user' });
    await api.importSkillDirectories({ cwd: '/repo/app', paths: ['/imports/a'], scope: 'personal', personal_type: 'imported' });
    await api.suggestSkillSummary({ cwd: '/repo/app', name: 'DocsSkill', description: '', content: 'body', scenario_words: ['docs'], scope: 'project' });
    await api.selectProjectDirs();

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
    expect(selectProjectDirs).toHaveBeenCalledTimes(1);
  });

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

    await api.listDags({ status: 'running', keyword: 'build', limit: 7 });
    await api.getDagDetail({ dagKey: 'dag-1' });
    await api.getDagRuns({ dagKey: 'dag-1', status: 'running', limit: 5 });
    await api.getDagRun({ runKey: 'run-1' });
    await api.startDag({ dagKey: 'dag-1', triggerSource: 'manual', idempotencyKey: 'ui-123' });
    await api.terminateDagRun({ dagKey: 'dag-1', runKey: 'run-1', reason: 'user_requested' });
    await api.deleteDag({ dagKey: 'dag-1' });
    await api.applyDagOps({
      dagKey: 'dag-1',
      baseVersion: 11,
      ops: [{ op: 'update_node', node_key: 'draft', patch: { title: 'Draft v2' } }],
    });

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
    expect(() => api.applyDagOps({ dagKey: 'dag-1', ops: [] })).toThrow('baseVersion is required');
    expect(() => api.getDagRun({ runKey: '' })).toThrow('runKey is required');
  });


  it('wraps prompt RPCs with legacy payload shapes', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.listPromptAssets({ cwd: '/repo/app' });
    await api.getDashboardPrompts({ cwd: '/repo/app' });
    await api.getPrompt({ cwd: '/repo/app', id: 'main/reviewer' });
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
    await api.deletePrompt({ cwd: '/repo/app', id: 'main/reviewer', scope: 'global' });
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

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPT_ASSETS_LIST, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_PROMPTS, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPTS_GET, { cwd: '/repo/app', id: 'main/reviewer' });
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
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.PROMPTS_DELETE, {
      cwd: '/repo/app',
      id: 'main/reviewer',
      scope: 'global',
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

    expect(() => api.listPromptAssets({ cwd: '' })).toThrow('cwd is required');
    expect(() => api.getPrompt({ cwd: '/repo/app', id: '' })).toThrow('id is required');
    expect(() => api.writePrompt({ cwd: '/repo/app', name: '' })).toThrow('name is required');
    expect(() => api.commitPromptIntent({ cwd: '/repo/app', draftKey: '' })).toThrow('draft_key is required');
    expect(() => api.dryRunPromptIntent({ cwd: '/repo/app', draftKey: 'd1', question: '' })).toThrow('question is required');
  });

  it('wraps memory center RPCs with the legacy payload shapes', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.getMemorySnapshot({ cwd: '/repo/app' });
    await api.getMemoryEntry({ cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
    await api.upsertMemoryEntry({
      cwd: '/repo/app', target: 'private', existingPath: 'feedback/tdd.md',
      name: 'tdd-rule', description: '先写红测', type: 'feedback', content: '规则', title: '遵守 TDD',
    });
    await api.deleteMemoryEntry({ cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
    await api.setMemoryAutoDreamIntent({ enabled: true });
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

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_GET, { cwd: '/repo/app' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_GET, { cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_UPSERT, {
      cwd: '/repo/app', target: 'private', existingPath: 'feedback/tdd.md',
      name: 'tdd-rule', description: '先写红测', type: 'feedback', content: '规则', title: '遵守 TDD',
    });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_ENTRY_DELETE, { cwd: '/repo/app', target: 'private', path: 'feedback/tdd.md' });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_MEMORY_AUTO_DREAM_SET_INTENT, { enabled: true });
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

    expect(() => api.getMemoryEntry({ cwd: '/repo/app', path: '' })).toThrow('path is required');
    expect(() => api.upsertMemoryEntry({ cwd: '/repo/app', name: 'x', description: 'd', type: 'feedback', content: '' })).toThrow('content is required');
    expect(() => api.setMemoryAutoDreamIntent({})).toThrow('enabled is required');
    expect(() => api.mergeMemoryEntries({ cwd: '/repo/app', targetA: 'private', pathA: 'a.md', targetB: 'team' })).toThrow('pathB is required');
  });

  it('wraps the independent new-window RPC with cwd validation', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.openNewWindow({ cwd: '/repo/window' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_OPEN_NEW_WINDOW, { cwd: '/repo/window' });
    expect(() => api.openNewWindow({ cwd: '' })).toThrow('cwd is required');
  });

  it('wraps shared file list, read and delete RPCs with the global payload shapes', async () => {
    const callAPI = vi.fn().mockResolvedValue({ ok: true });
    const api = createBackendApi({ callAPI });

    await api.listSharedFiles();
    await api.readSharedFile({ path: 'reports/final.md' });
    await api.deleteSharedFile({ path: 'scratch/work.json' });

    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.DASHBOARD_SHARED_FILES, {});
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_SHARED_FILE_GET, {
      path: 'reports/final.md',
    });
    expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.UI_SHARED_FILE_DELETE, {
      path: 'scratch/work.json',
    });
    expect(() => api.listSharedFiles([])).toThrow('params must be an object');
    expect(() => api.readSharedFile({ path: '' })).toThrow('path is required');
    expect(() => api.deleteSharedFile({ path: '' })).toThrow('path is required');
  });
});
