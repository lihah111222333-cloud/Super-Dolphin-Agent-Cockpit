// @ts-nocheck

export const CALL_API_METHOD_ID = 2963398832;
export const GET_BUILD_INFO_METHOD_ID = 2341363104;
export const SAVE_CLIPBOARD_IMAGE_METHOD_ID = 3733550318;
export const SELECT_FILES_METHOD_ID = 4126105303;
export const SELECT_PROJECT_DIR_METHOD_ID = 3694631468;

export const RUNTIME_MODULE_SOURCE = `
const listeners = globalThis.__AO_E2E_RUNTIME_LISTENERS__ || (globalThis.__AO_E2E_RUNTIME_LISTENERS__ = new Map());
function listenerSet(name) {
  const key = String(name || '');
  let bucket = listeners.get(key);
  if (!bucket) {
    bucket = new Set();
    listeners.set(key, bucket);
  }
  return bucket;
}
export const Call = {
  async ByID(methodId, ...args) {
    const backend = globalThis.__AO_E2E_BACKEND__;
    if (!backend || typeof backend.byId !== 'function') {
      throw new Error('AO E2E backend is not installed');
    }
    return backend.byId(methodId, ...args);
  },
};
export const Events = {
  On(name, callback) {
    const bucket = listenerSet(name);
    bucket.add(callback);
    return () => bucket.delete(callback);
  },
  Off(name) {
    listeners.delete(String(name || ''));
  },
};
`;

function browserInstaller({
  callApiId,
  buildInfoId,
  saveClipboardImageId,
  selectFilesId,
  selectProjectDirId,
  sourceConfig,
}) {
  const clone = (value) => {
    if (value === undefined) return undefined;
    return JSON.parse(JSON.stringify(value));
  };
  const asObject = (value) => (value && typeof value === 'object' && !Array.isArray(value) ? value : {});
  const asArray = (value) => (Array.isArray(value) ? value : []);
  const basename = (path) => {
    const normalized = (path || '').toString().trim().replace(/[\\/]+$/g, '');
    if (!normalized) return '';
    const parts = normalized.split(/[\\/]/).filter(Boolean);
    return parts[parts.length - 1] || normalized;
  };
  const dirname = (path) => {
    const normalized = (path || '').toString().trim().replace(/[\\/]+$/g, '');
    if (!normalized) return '';
    return normalized.replace(/[\\/][^\\/]+$/, '');
  };
  const normalizePath = (path) => (path || '').toString().trim().replace(/[\\/]+$/g, '');
  const isoClock = (() => {
    let tick = 0;
    return () => new Date(Date.UTC(2026, 2, 6, 9, 0, tick++)).toISOString();
  })();
  const parseFrontmatterValue = (content, field) => {
    const match = (content || '').match(new RegExp(`^${field}:\\s*(.+)$`, 'im'));
    return match ? match[1].trim().replace(/^['"]|['"]$/g, '') : '';
  };
  const parseFrontmatterList = (content, field) => {
    const match = (content || '').match(new RegExp(`^${field}:\\s*\\[(.*)\\]$`, 'im'));
    if (!match) return [];
    return match[1]
      .split(',')
      .map((item) => item.trim().replace(/^['"]|['"]$/g, ''))
      .filter(Boolean);
  };
  const parseSkillContent = (content) => ({
    name: parseFrontmatterValue(content, 'name'),
    description: parseFrontmatterValue(content, 'description'),
    summary: parseFrontmatterValue(content, 'summary'),
    triggerWords: parseFrontmatterList(content, 'trigger_words'),
    forceWords: parseFrontmatterList(content, 'force_words'),
  });
  const defaultPromptRows = () => [
    {
      id: 'expert/sqlc-drift',
      prompt_key: 'expert/sqlc-drift',
      name: 'SQLC Drift Expert',
      description: 'Checks source SQL before generated code.',
      content: 'Always inspect sql/queries and migrations before trusting generated sqlc diffs.',
      agent_key: 'coder',
      enabled: true,
      tags: ['scope.cwd:/workspace/project-alpha', 'intent:expert', 'sqlc', 'review'],
    },
    {
      id: 'recall/prompt-intent-ux',
      prompt_key: 'recall/prompt-intent-ux',
      name: 'Prompt Intent UX Notes',
      description: 'Intent wizard acceptance notes for prompt creation.',
      content: '',
      when_to_use: 'Use when reviewing prompt intent UX behavior.',
      agent_key: 'main',
      enabled: true,
      tags: ['scope.cwd:/workspace/project-alpha', 'intent:recall', 'ux', 'prompt-intent'],
    },
    {
      id: 'default-rule/no-silent-fallback',
      prompt_key: 'default-rule/no-silent-fallback',
      name: 'No Silent Fallback Rule',
      description: '',
      content: '',
      when_to_use: 'Fail fast when required prompt intent data is missing.',
      agent_key: 'default_rule',
      enabled: true,
      tags: ['scope.cwd:/workspace/project-alpha', 'intent:default_rule', 'fail-fast'],
    },
  ];
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, Math.max(0, Number(ms) || 0)));
  const promptTags = (kind, cwd, extra = [], scope = 'project') => [
    ...(scope === 'global' ? ['scope.global'] : [`scope.cwd:${normalizePath(cwd)}`]),
    `intent:${kind}`,
    ...extra,
  ];
  const intentVariantFromInput = (kind, rawInput) => {
    const text = (rawInput || '').toString().toLowerCase();
    if (text.includes('blocked') || text.includes('block') || text.includes('阻断')) return 'blocked';
    if (kind === 'default_rule' && (text.includes('conflict') || text.includes('冲突'))) return 'conflict';
    if (text.includes('review') || text.includes('risk') || text.includes('风险')) return 'review';
    return 'ready';
  };
  const intentIssues = (kind, variant) => {
    if (variant === 'blocked') {
      return [{ code: 'scope_missing', severity: 'block', message: '缺少明确适用范围，暂时不能保存' }];
    }
    if (variant === 'review' || variant === 'conflict') {
      return [{ code: `${kind}_needs_review`, severity: 'review', message: '可能影响已有提示词，请先确认风险' }];
    }
    return [];
  };
  const intentCard = (kind, variant, rawInput) => {
    const titleByKind = {
      expert: 'SQLC 迁移审查专家',
      recall: '提示词意图资料卡',
      default_rule: '默认规则：先确认作用域',
    };
    const base = {
      kind,
      title: titleByKind[kind] || '提示词草稿',
      summary: `根据输入整理的 ${kind} 草稿`,
      when_to_use: '当任务涉及提示词意图式创建验收时使用。',
      when_not_to_use: '与当前项目无关或缺少上下文时不要使用。',
      workflow: ['读取需求', '检查风险', '按项目范围保存'],
      hit_examples: ['帮我补齐提示词意图 E2E', '检查 prompt intent review 风险'],
      miss_examples: ['闲聊一下', '生成无关图片'],
      source_excerpt: (rawInput || '').toString().slice(0, 160),
    };
    if (kind === 'recall') {
      base.recall_body = 'Intent wizard stores recall material as a searchable project-scoped card.';
    }
    if (kind === 'default_rule') {
      base.default_rule_body = '保存提示词意图前必须确认项目范围，不允许静默兜底。';
      base.conflicting_rules = variant === 'conflict'
        ? [{ title: '旧的默认规则', summary: '旧规则允许缺少 scope 时继续保存。' }]
        : [];
      if (variant === 'conflict') {
        base.suggested_alternative = {
          kind: 'recall',
          reason: '这段内容更像项目资料，建议先作为资料卡保存。',
        };
      }
    }
    return base;
  };
  const promptRowFromIntentDraft = (draft) => {
    const card = asObject(draft.card);
    const kind = (draft.kind || card.kind || 'expert').toString();
    const scope = (draft.scope || '').toString() === 'global' ? 'global' : 'project';
    const idPrefix = kind === 'default_rule' ? 'default-rule' : kind;
    const id = `${idPrefix}/${(draft.draft_key || isoClock()).toString().replace(/[^a-zA-Z0-9_-]+/g, '-')}`;
    return {
      id,
      prompt_key: id,
      name: (card.title || 'Prompt Intent Draft').toString(),
      description: (card.summary || '').toString(),
      content: (card.output || card.recall_body || card.default_rule_body || '').toString(),
      when_to_use: (card.when_to_use || '').toString(),
      agent_key: kind === 'default_rule' ? 'default_rule' : 'main',
      enabled: true,
      scope,
      tags: promptTags(kind, draft.cwd, ['saved', 'e2e'], scope),
      created_at: isoClock(),
    };
  };

  const promptScopeTags = (prompt) => asArray(prompt?.tags)
    .map((tag) => (tag || '').toString().trim())
    .filter(Boolean);

  const promptScope = (prompt) => {
    const explicit = (prompt?.scope || '').toString().trim().toLowerCase();
    if (explicit === 'global') return 'global';
    return promptScopeTags(prompt).includes('scope.global') ? 'global' : 'project';
  };

  const promptLogicalKey = (prompt) => {
    const name = (prompt?.name || prompt?.title || prompt?.prompt_key || prompt?.id || '').toString().trim().toLowerCase();
    const agent = (prompt?.agent_key || prompt?.agentType || '').toString().trim().toLowerCase();
    const intent = promptScopeTags(prompt).find((tag) => tag.startsWith('intent:')) || '';
    return [agent, intent, name].join('::');
  };

  const promptVisibleForCwd = (prompt, cwd) => {
    const requested = normalizePath(cwd || '');
    if (!requested) return true;
    const tags = promptScopeTags(prompt);
    if (tags.includes('scope.global')) return true;
    const cwdTags = tags.filter((tag) => tag.startsWith('scope.cwd:'));
    if (cwdTags.length === 0) return true;
    return cwdTags.some((tag) => normalizePath(tag.slice('scope.cwd:'.length)) === requested);
  };

  const listPromptsForCwd = (cwd) => {
    const requested = normalizePath(cwd || '');
    const byKey = new Map();
    state.prompts.forEach((prompt, index) => {
      if (!promptVisibleForCwd(prompt, requested)) return;
      const scope = promptScope(prompt);
      const rank = scope === 'global' ? 1 : 0;
      const key = promptLogicalKey(prompt);
      const current = byKey.get(key);
      if (!current || rank < current.rank) {
        byKey.set(key, { prompt, index, rank });
      }
    });
    return [...byKey.values()]
      .sort((a, b) => a.index - b.index)
      .map(({ prompt }) => ({ ...prompt, scope: promptScope(prompt) }));
  };

  const source = clone(sourceConfig) || {};
  const dashboardSource = asObject(source.dashboard);
  const retentionSource = asObject(dashboardSource.sharedFileRetention);
  const sourcePrompts = asArray(source.prompts).length > 0
    ? asArray(source.prompts)
    : asArray(dashboardSource.prompts);

  const state = {
    buildInfo: {
      version: 'e2e-test',
      commit: 'local',
      runtime: 'playwright',
      buildTime: '2026-03-06 09:00:00',
      ...(asObject(source.buildInfo)),
    },
    runtimeConfig: {
      cwd: '/tmp/go-agent-v2-e2e',
      ...(asObject(source.runtimeConfig)),
    },
    projects: asArray(source.projects).map((item) => normalizePath(item)).filter(Boolean),
    activeProject: normalizePath(source.activeProject || '.') || '.',
    preferences: clone(asObject(source.preferences)) || {},
    dashboard: {
      agents: clone(asArray(dashboardSource.agents)) || [],
      dags: clone(asArray(dashboardSource.dags)) || [],
      taskAcks: clone(asArray(dashboardSource.taskAcks)) || [],
      taskTraces: clone(asArray(dashboardSource.taskTraces)) || [],
      skills: clone(asArray(dashboardSource.skills)) || [],
      commandCards: clone(asArray(dashboardSource.commandCards)) || [],
      prompts: clone(asArray(dashboardSource.prompts)) || [],
      memory: clone(asArray(dashboardSource.memory)) || [],
      finalOutputRefs: clone(asArray(dashboardSource.finalOutputRefs)) || [],
      sharedFileRetention: Array.isArray(retentionSource.items)
        ? clone(retentionSource)
        : { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    },
    dagDetails: clone(asObject(source.dagDetails)) || {},
    dagRuns: clone(asObject(source.dagRuns)) || {},
    dagRunDetails: clone(asObject(source.dagRunDetails)) || {},
    dagStart: clone(asObject(source.dagStart)) || {},
    prompts: clone(sourcePrompts.length > 0 ? sourcePrompts : defaultPromptRows()) || [],
    memoryCenter: clone(asObject(source.memoryCenter)) || {
      overview: {},
      private: { entries: [] },
      team: { entries: [] },
    },
    threads: [],
    statuses: clone(asObject(source.statuses)) || {},
    interruptibleByThread: clone(asObject(source.interruptibleByThread)) || {},
    statusHeadersByThread: clone(asObject(source.statusHeadersByThread)) || {},
    statusDetailsByThread: clone(asObject(source.statusDetailsByThread)) || {},
    timelinesByThread: clone(asObject(source.timelinesByThread)) || {},
    diffTextByThread: clone(asObject(source.diffTextByThread)) || {},
    tokenUsageByThread: clone(asObject(source.tokenUsageByThread)) || {},
    agentMetaById: clone(asObject(source.agentMetaById)) || {},
    agentRuntimeById: clone(asObject(source.agentRuntimeById)) || {},
    activityStatsByThread: clone(asObject(source.activityStatsByThread)) || {},
    alertsByThread: clone(asObject(source.alertsByThread)) || {},
    activeThreadId: (source.activeThreadId || '').toString().trim(),
    activeCmdThreadId: (source.activeCmdThreadId || '').toString().trim(),

    pinnedThreadAtById: clone(asObject(source.pinnedThreadAtById)) || {},
    archivedThreadAtById: clone(asObject(source.archivedThreadAtById)) || {},
    viewPrefsChat: {
      layout: 'split',
      splitRatio: 60,
      threadRailWidth: 232,
      ...(asObject(source.viewPrefsChat)),
    },
    viewPrefsCmd: {
      layout: 'mix',
      splitRatio: 60,
      cardCols: 3,
      ...(asObject(source.viewPrefsCmd)),
    },
    skillFilesByDir: clone(asObject(source.skillFilesByDir)) || {},
    skillFileContents: clone(asObject(source.skillFileContents)) || {},
    selectFilesResult: clone(asArray(source.selectFilesResult)) || [],
    selectProjectDirResult: (source.selectProjectDirResult || '').toString(),
    uiSelectProjectDirResult: (source.uiSelectProjectDirResult || source.selectProjectDirResult || '').toString(),
    selectProjectDirsResult: clone(asArray(source.selectProjectDirsResult)) || [],
    saveClipboardImagePath: (source.saveClipboardImagePath || '/tmp/pasted-image.png').toString(),
    confirmResult: Object.prototype.hasOwnProperty.call(source, 'confirmResult') ? Boolean(source.confirmResult) : true,
    promptResponses: clone(asObject(source.promptResponses)) || {},
    importResult: clone(source.importResult) || null,
    codeOpenByPath: clone(asObject(source.codeOpenByPath)) || {},
    nextThreadSeq: Math.max(0, Number(source.nextThreadSeq) || 0),
    nextMessageSeq: 0,
    nextPromptIntentSeq: 0,
    promptIntentDelayMs: Number(source.promptIntentDelayMs) || 0,
    promptIntentDrafts: {},
    promptIntentCommits: [],
    callLog: [],
    alerts: [],
  };

  const slugify = (value) => {
    const text = (value || '').toString().trim().toLowerCase();
    const slug = text.replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
    return slug || 'memory-entry';
  };

  function ensureMemorySection(target) {
    const key = (target || '').toString().trim().toLowerCase() === 'team' ? 'team' : 'private';
    const current = asObject(state.memoryCenter[key]);
    state.memoryCenter[key] = {
      label: current.label || '',
      rootPath: (current.rootPath || '').toString(),
      indexPath: (current.indexPath || '').toString(),
      notice: (current.notice || '').toString(),
      entries: clone(asArray(current.entries)) || [],
    };
    return state.memoryCenter[key];
  }

  function findMemoryEntry(target, path) {
    const section = ensureMemorySection(target);
    const normalizedPath = (path || '').toString().trim();
    const index = section.entries.findIndex((item) => (item?.path || '').toString().trim() === normalizedPath);
    return { section, index, entry: index >= 0 ? section.entries[index] : null };
  }

  function memoryPreview(content) {
    return (content || '').toString().split(/\r?\n/).slice(0, 5).join('\n').trim();
  }

  function upsertMemoryEntry(params) {
    const section = ensureMemorySection(params.target);
    const type = (params.type || 'project').toString().trim() || 'project';
    const name = (params.name || '').toString().trim();
    const description = (params.description || '').toString().trim();
    const content = (params.content || '').toString();
    const updatedAt = isoClock();
    const path = (params.existingPath || '').toString().trim() || `${type}/${slugify(name)}.md`;
    const next = {
      path,
      name,
      description,
      type,
      content,
      preview: memoryPreview(content),
      updatedAt,
    };
    const existingIndex = section.entries.findIndex((item) => (item?.path || '').toString().trim() === path);
    if (existingIndex >= 0) {
      section.entries[existingIndex] = { ...section.entries[existingIndex], ...next };
    } else {
      section.entries.push(next);
    }
    section.notice = section.entries.length === 0 ? section.notice : '';
    return clone(next);
  }

  function mergeMemoryEntries(params) {
    const left = findMemoryEntry(params.targetA, params.pathA);
    const right = findMemoryEntry(params.targetB, params.pathB);
    if (!left.entry) throw new Error(`memory entry not found: ${params.pathA || ''}`);
    if (!right.entry) throw new Error(`memory entry not found: ${params.pathB || ''}`);
    const mergedContent = [left.entry.content, right.entry.content]
      .map((item) => (item || '').toString().trim())
      .filter(Boolean)
      .join('\n\n');
    const next = {
      ...left.entry,
      description: (left.entry.description || right.entry.description || '').toString(),
      content: mergedContent,
      preview: memoryPreview(mergedContent),
      updatedAt: isoClock(),
    };
    left.section.entries[left.index] = next;
    right.section.entries = right.section.entries.filter((_, idx) => idx !== right.index);
    const groups = asArray(state.memoryCenter?.overview?.health?.similarGroups);
    if (state.memoryCenter?.overview?.health) {
      state.memoryCenter.overview.health.similarGroups = groups.filter((group) => !((group?.pathA === params.pathA && group?.pathB === params.pathB) || (group?.pathA === params.pathB && group?.pathB === params.pathA)));
    }
    return clone(next);
  }

  function deleteMemoryEntry(params) {
    const section = ensureMemorySection(params.target);
    const normalizedPath = (params.path || '').toString().trim();
    section.entries = section.entries.filter((item) => (item?.path || '').toString().trim() !== normalizedPath);
    if (section.entries.length === 0) {
      section.notice = section.notice || '当前目录下还没有可读的记忆条目。';
    }
    return { deleted: true };
  }

  function findSharedFile(path) {
    const normalizedPath = (path || '').toString().trim();
    return asArray(state.dashboard.memory).find((item) => (item?.path || '').toString().trim() === normalizedPath) || null;
  }

  function upsertSkillCard(card) {
    const incoming = {
      name: (card?.name || '').toString().trim(),
      dir: normalizePath(card?.dir || ''),
      description: (card?.description || '').toString(),
      summary: (card?.summary || '').toString(),
      triggerWords: asArray(card?.triggerWords).map((item) => (item || '').toString()).filter(Boolean),
      forceWords: asArray(card?.forceWords).map((item) => (item || '').toString()).filter(Boolean),
    };
    if (!incoming.name) return;
    const existingIndex = state.dashboard.skills.findIndex((item) => (item?.name || '').toString().toLowerCase() === incoming.name.toLowerCase());
    if (existingIndex >= 0) {
      state.dashboard.skills[existingIndex] = {
        ...state.dashboard.skills[existingIndex],
        ...incoming,
      };
      return;
    }
    state.dashboard.skills.push(incoming);
  }

  function removeSkillCard(name) {
    const target = (name || '').toString().trim().toLowerCase();
    if (!target) return;
    state.dashboard.skills = state.dashboard.skills.filter((item) => (item?.name || '').toString().trim().toLowerCase() !== target);
  }

  function ensureSkillFile(path, content, extras = {}) {
    const normalizedPath = normalizePath(path);
    if (!normalizedPath) return;
    const dir = dirname(normalizedPath);
    const name = basename(normalizedPath) || 'SKILL.md';
    const currentFiles = asArray(state.skillFilesByDir[dir]);
    if (!currentFiles.some((item) => normalizePath(item?.path) === normalizedPath)) {
      currentFiles.push({ name, path: normalizedPath, isMain: /(^|[\\/])SKILL\.md$/i.test(name) });
    }
    state.skillFilesByDir[dir] = currentFiles;
    state.skillFileContents[normalizedPath] = {
      content: (content || '').toString(),
      ...(asObject(extras)),
    };
  }

  function buildImportedSkill(dirPath) {
    const dir = normalizePath(dirPath);
    const name = basename(dir) || `skill-${state.nextThreadSeq + 1}`;
    const skillPath = `${dir}/SKILL.md`;
    const content = `---\nname: "${name}"\ndescription: "导入技能 ${name}"\nsummary: "导入摘要 ${name}"\ntrigger_words: ["${name}"]\n---\n\n## ${name}\n\n自动导入的技能。`;
    ensureSkillFile(skillPath, content, {
      summary: `导入摘要 ${name}`,
      summary_source: 'frontmatter',
    });
    upsertSkillCard({
      name,
      dir,
      description: `导入技能 ${name}`,
      summary: `导入摘要 ${name}`,
      triggerWords: [name],
      forceWords: [],
    });
    return {
      name,
      skill_file: skillPath,
    };
  }

  function normalizeThread(thread) {
    const normalized = asObject(thread);
    const id = (normalized.id || `thread-e2e-${state.nextThreadSeq + 1}`).toString();
    return {
      id,
      name: (normalized.name || id).toString(),
      state: (normalized.state || 'idle').toString(),
      alias: (normalized.alias || '').toString(),
      provider: (normalized.provider || '').toString(),
      cwd: normalizePath(normalized.cwd || state.activeProject || state.runtimeConfig.cwd || '.'),
      providerThreadId: (normalized.providerThreadId || '').toString(),
      port: normalized.port ?? null,
      statusHeader: (normalized.statusHeader || '').toString(),
      statusDetails: (normalized.statusDetails || '').toString(),
      interruptible: Boolean(normalized.interruptible),
      timeline: clone(asArray(normalized.timeline)) || [],
      diffText: (normalized.diffText || '').toString(),
      tokenUsage: clone(asObject(normalized.tokenUsage)) || null,
      activityStats: clone(asObject(normalized.activityStats)) || null,
      alerts: clone(asArray(normalized.alerts)) || null,
      cwdMismatch: Boolean(normalized.cwdMismatch),
      cwdMismatchReason: (normalized.cwdMismatchReason || '').toString(),
    };
  }

  function ensureThread(threadId, seed = {}) {
    const id = (threadId || '').toString().trim();
    if (!id) return null;
    let thread = state.threads.find((item) => item.id === id);
    if (!thread) {
      thread = normalizeThread({ id, ...seed });
      state.threads.push(thread);
    }
    if (!state.statuses[id]) state.statuses[id] = thread.state || 'idle';
    if (!Object.prototype.hasOwnProperty.call(state.interruptibleByThread, id)) {
      state.interruptibleByThread[id] = Boolean(thread.interruptible);
    }
    if (!Object.prototype.hasOwnProperty.call(state.statusHeadersByThread, id)) {
      state.statusHeadersByThread[id] = thread.statusHeader || '等待指示';
    }
    if (!Object.prototype.hasOwnProperty.call(state.statusDetailsByThread, id)) {
      state.statusDetailsByThread[id] = thread.statusDetails || '';
    }
    if (!Array.isArray(state.timelinesByThread[id])) {
      state.timelinesByThread[id] = clone(asArray(thread.timeline)) || [];
    }
    if (!Object.prototype.hasOwnProperty.call(state.diffTextByThread, id)) {
      state.diffTextByThread[id] = thread.diffText || '';
    }
    if (thread.tokenUsage && !state.tokenUsageByThread[id]) {
      state.tokenUsageByThread[id] = clone(thread.tokenUsage);
    }
    if (thread.activityStats && !state.activityStatsByThread[id]) {
      state.activityStatsByThread[id] = clone(thread.activityStats);
    }
    if (thread.alerts && !state.alertsByThread[id]) {
      state.alertsByThread[id] = clone(thread.alerts);
    }
    state.agentMetaById[id] = {
      ...(asObject(state.agentMetaById[id])),
      alias: thread.alias || asObject(state.agentMetaById[id]).alias || thread.name || id,
    };
    state.agentRuntimeById[id] = {
      ...(asObject(state.agentRuntimeById[id])),
      provider: thread.provider || asObject(state.agentRuntimeById[id]).provider || '',
      providerThreadId: thread.providerThreadId || asObject(state.agentRuntimeById[id]).providerThreadId || '',
      port: thread.port ?? asObject(state.agentRuntimeById[id]).port ?? null,
      cwd: thread.cwd || asObject(state.agentRuntimeById[id]).cwd || state.runtimeConfig.cwd || '.',
      cwdMismatch: Boolean(thread.cwdMismatch || asObject(state.agentRuntimeById[id]).cwdMismatch),
      cwdMismatchReason: thread.cwdMismatchReason || asObject(state.agentRuntimeById[id]).cwdMismatchReason || '',
    };
    return thread;
  }

  state.threads = asArray(source.threads).map((item) => normalizeThread(item));
  state.threads.forEach((thread) => ensureThread(thread.id, thread));

  if (!state.activeThreadId && state.threads[0]?.id) {
    state.activeThreadId = state.threads[0].id;
  }

  if (!Object.prototype.hasOwnProperty.call(state.preferences, 'activeThreadId')) {
    state.preferences.activeThreadId = state.activeThreadId || '';
  }
  if (!Object.prototype.hasOwnProperty.call(state.preferences, 'activeCmdThreadId')) {
    state.preferences.activeCmdThreadId = state.activeCmdThreadId || '';
  }

  if (!Object.prototype.hasOwnProperty.call(state.preferences, 'threadPins.chat')) {
    state.preferences['threadPins.chat'] = clone(state.pinnedThreadAtById);
  }
  if (!Object.prototype.hasOwnProperty.call(state.preferences, 'threadArchives.chat')) {
    state.preferences['threadArchives.chat'] = clone(state.archivedThreadAtById);
  }
  if (!Object.prototype.hasOwnProperty.call(state.preferences, 'viewPrefs.chat')) {
    state.preferences['viewPrefs.chat'] = clone(state.viewPrefsChat);
  }
  if (!Object.prototype.hasOwnProperty.call(state.preferences, 'viewPrefs.cmd')) {
    state.preferences['viewPrefs.cmd'] = clone(state.viewPrefsCmd);
  }

  if (typeof window !== 'undefined') {
    window.confirm = (message) => {
      state.callLog.push({ method: 'window/confirm', params: { message: (message || '').toString() } });
      return state.confirmResult;
    };
    window.prompt = (message, defaultValue = '') => {
      state.callLog.push({ method: 'window/prompt', params: { message: (message || '').toString(), defaultValue: (defaultValue || '').toString() } });
      const explicit = state.promptResponses[(message || '').toString()];
      return explicit === undefined ? defaultValue : explicit;
    };
    window.alert = (message) => {
      const text = (message || '').toString();
      state.alerts.push(text);
      state.callLog.push({ method: 'window/alert', params: { message: text } });
    };
  }

  function buildUiState() {
    const activeThreadId = (state.preferences.activeThreadId || state.activeThreadId || '').toString();
    const activeCmdThreadId = (state.preferences.activeCmdThreadId || state.activeCmdThreadId || '').toString();

    const pinned = clone(state.preferences['threadPins.chat'] || state.pinnedThreadAtById) || {};
    const archived = clone(state.preferences['threadArchives.chat'] || state.archivedThreadAtById) || {};
    const viewPrefsChat = clone(state.preferences['viewPrefs.chat'] || state.viewPrefsChat) || {};
    const viewPrefsCmd = clone(state.preferences['viewPrefs.cmd'] || state.viewPrefsCmd) || {};
    return {
      threads: state.threads.map((item) => ({
        id: item.id,
        name: item.name,
        state: (state.statuses[item.id] || item.state || 'idle').toString(),
        provider: (item.provider || asObject(state.agentRuntimeById[item.id]).provider || 'codex').toString(),
      })),
      statuses: clone(state.statuses) || {},
      interruptibleByThread: clone(state.interruptibleByThread) || {},
      timelinesByThread: clone(state.timelinesByThread) || {},
      diffTextByThread: clone(state.diffTextByThread) || {},
      statusHeadersByThread: clone(state.statusHeadersByThread) || {},
      statusDetailsByThread: clone(state.statusDetailsByThread) || {},
      tokenUsageByThread: clone(state.tokenUsageByThread) || {},
      agentMetaById: clone(state.agentMetaById) || {},
      agentRuntimeById: clone(state.agentRuntimeById) || {},
      activityStatsByThread: clone(state.activityStatsByThread) || {},
      alertsByThread: clone(state.alertsByThread) || {},
      activeThreadId,
      activeCmdThreadId,

      'threadPins.chat': pinned,
      'threadArchives.chat': archived,
      'viewPrefs.chat': viewPrefsChat,
      'viewPrefs.cmd': viewPrefsCmd,
    };
  }

  function recordCall(method, params) {
    state.callLog.push({ method, params: clone(params ?? {}) });
  }

  function requireDagKey(params) {
    const dagKey = (params.dagKey || params.dag_key || '').toString().trim();
    if (!dagKey) throw new Error('dagKey is required');
    return dagKey;
  }

  function requireConfiguredDag(map, dagKey, label) {
    if (!Object.prototype.hasOwnProperty.call(map, dagKey)) {
      throw new Error(`${label} not found: ${dagKey}`);
    }
    return map[dagKey];
  }

  function dagRunsForKey(dagKey) {
    const configured = requireConfiguredDag(state.dagRuns, dagKey, 'dag runs');
    if (Array.isArray(configured)) return configured;
    return asArray(asObject(configured).runs);
  }

  function dagRunDetailForKey(runKey) {
    const key = (runKey || '').toString().trim();
    if (!key) throw new Error('runKey is required');
    const configured = requireConfiguredDag(state.dagRunDetails, key, 'dag run');
    const detail = asObject(configured);
    return {
      run: clone(asObject(detail.run)),
      nodes: clone(asArray(detail.nodes)),
    };
  }

  function applyLimit(items, limit) {
    const max = Number(limit);
    if (!Number.isFinite(max) || max <= 0) return items;
    return items.slice(0, max);
  }

  function filterRunsByStatus(items, status) {
    const target = (status || '').toString().trim();
    if (!target) return items;
    return items.filter((item) => {
      const run = asObject(item);
      return (run.status || run.state || '').toString().trim() === target;
    });
  }

  function buildTurnItem(input) {
    const parts = [];
    const attachments = [];
    asArray(input).forEach((entry, index) => {
      const item = asObject(entry);
      const type = (item.type || '').toString();
      if (type === 'text') {
        parts.push((item.text || '').toString());
        return;
      }
      if (type === 'localImage' || type === 'image') {
        attachments.push({
          kind: 'image',
          name: basename(item.path || item.url || `image-${index + 1}.png`) || `image-${index + 1}.png`,
          path: (item.path || '').toString(),
          previewUrl: (item.url || '').toString(),
        });
        return;
      }
      if (type === 'mention') {
        attachments.push({
          kind: 'file',
          name: (item.name || basename(item.path) || `file-${index + 1}`).toString(),
          path: (item.path || '').toString(),
          previewUrl: '',
        });
      }
    });
    const text = parts.join('\n').trim() || '已发送附件';
    state.nextMessageSeq += 1;
    return {
      id: `turn-${state.nextMessageSeq}`,
      kind: 'user',
      text,
      attachments,
      ts: isoClock(),
    };
  }

  function writePreference(key, value) {
    state.preferences[key] = clone(value);
    if (key === 'activeThreadId') state.activeThreadId = (value || '').toString();
    if (key === 'activeCmdThreadId') state.activeCmdThreadId = (value || '').toString();

    if (key === 'threadPins.chat') state.pinnedThreadAtById = clone(asObject(value)) || {};
    if (key === 'threadArchives.chat') state.archivedThreadAtById = clone(asObject(value)) || {};
    if (key === 'viewPrefs.chat') state.viewPrefsChat = clone(asObject(value)) || state.viewPrefsChat;
    if (key === 'viewPrefs.cmd') state.viewPrefsCmd = clone(asObject(value)) || state.viewPrefsCmd;
  }

  globalThis.__AO_E2E_BACKEND_STATE__ = state;
  globalThis.__AO_E2E_BACKEND__ = {
    async byId(methodId, ...args) {
      if (methodId === buildInfoId) {
        return clone(state.buildInfo);
      }
      if (methodId === saveClipboardImageId) {
        recordCall('ui/saveClipboardImage', { hasPayload: Boolean((args[0] || '').toString()) });
        return state.saveClipboardImagePath;
      }
      if (methodId === selectFilesId) {
        recordCall('ui/selectFilesById', {});
        return clone(state.selectFilesResult);
      }
      if (methodId === selectProjectDirId) {
        recordCall('ui/selectProjectDirById', {});
        return state.selectProjectDirResult;
      }
      if (methodId !== callApiId) {
        return null;
      }

      const [method, rawParams = {}] = args;
      const params = asObject(rawParams);
      recordCall(method, params);

      switch ((method || '').toString()) {
        case 'ui/projects/get':
          return { projects: clone(state.projects), active: state.activeProject };
        case 'ui/projects/setActive': {
          const next = normalizePath(params.path || '.') || '.';
          state.activeProject = next;
          return { projects: clone(state.projects), active: state.activeProject };
        }
        case 'ui/projects/add': {
          const next = normalizePath(params.path || '');
          if (next && !state.projects.includes(next)) {
            state.projects.push(next);
          }
          return { projects: clone(state.projects), active: state.activeProject };
        }
        case 'ui/projects/remove': {
          const target = normalizePath(params.path || '');
          state.projects = state.projects.filter((item) => item !== target);
          if (state.activeProject === target) state.activeProject = '.';
          return { projects: clone(state.projects), active: state.activeProject };
        }
        case 'config/read':
          return clone(state.runtimeConfig);
        case 'ui/windowBootstrap/get':
          return { snapshot: null };
        case 'thread/list':
          return { threads: buildUiState().threads };
        case 'ui/sidebar/get':
          return buildUiState();
        case 'ui/state/get':
          return buildUiState();
        case 'ui/dashboard/get':
          return clone(state.dashboard);
        case 'dashboard/dagDetail': {
          const dagKey = requireDagKey(params);
          const detail = requireConfiguredDag(state.dagDetails, dagKey, 'dag detail');
          return clone({
            dag: asObject(detail).dag,
            nodes: asArray(asObject(detail).nodes),
          });
        }
        case 'dashboard/dagRuns': {
          const dagKey = requireDagKey(params);
          const filtered = filterRunsByStatus(dagRunsForKey(dagKey), params.status);
          return { runs: clone(applyLimit(filtered, params.limit)) };
        }
        case 'dashboard/dagRun': {
          return dagRunDetailForKey(params.runKey || params.run_key);
        }
        case 'dashboard/dagStart': {
          const dagKey = requireDagKey(params);
          if (!(params.idempotencyKey || '').toString().trim()) {
            throw new Error('idempotencyKey is required');
          }
          const configured = asObject(requireConfiguredDag(state.dagStart, dagKey, 'dag start'));
          const run = asObject(configured.run);
          if (run.run_key || run.runKey || run.key || run.id) {
            const current = dagRunsForKey(dagKey);
            state.dagRuns[dagKey] = [clone(run), ...current];
            const runKey = (run.run_key || run.runKey || run.key || run.id).toString();
            if (!state.dagRunDetails[runKey]) {
              state.dagRunDetails[runKey] = {
                run: clone(run),
                nodes: clone(asArray(configured.nodes)),
              };
            }
          }
          return clone(asObject(configured.response));
        }
        case 'dashboard/prompts':
        case 'prompt-assets/list':
        case 'prompts/list':
          return { prompts: clone(listPromptsForCwd(params.cwd)) };
        case 'prompts/get': {
          const id = (params.id || '').toString();
          const visiblePrompts = listPromptsForCwd(params.cwd);
          const prompt = visiblePrompts.find((item) => (item?.id || item?.prompt_key || '').toString() === id);
          if (!prompt) throw new Error(`prompt not found: ${id}`);
          return { prompt: clone(prompt) };
        }
        case 'prompts/write': {
          const id = (params.id || params.prompt_key || `prompt/${slugify(params.name || 'new-prompt')}`).toString();
          const scope = (params.scope || '').toString().trim().toLowerCase() === 'global' ? 'global' : 'project';
          const scopeTag = scope === 'global'
            ? 'scope.global'
            : `scope.cwd:${normalizePath(params.cwd || state.activeProject || state.runtimeConfig.cwd || '')}`;
          const userTags = (Array.isArray(params.tags) ? [...params.tags] : asArray(params.tags))
            .map((tag) => (tag || '').toString())
            .filter((tag) => tag && tag !== 'scope.global' && !tag.startsWith('scope.cwd:'));
          const next = {
            id,
            prompt_key: id,
            name: (params.name || id).toString(),
            description: (params.description || '').toString(),
            content: (params.content || params.prompt_text || '').toString(),
            when_to_use: (params.when_to_use || '').toString(),
            agent_key: (params.agentType || params.agent_key || 'main').toString(),
            priority: Number(params.priority) || 0,
            enabled: params.enabled !== false,
            scope,
            tags: [scopeTag, ...userTags],
          };
          const index = state.prompts.findIndex((item) => (item?.id || item?.prompt_key || '').toString() === id);
          if (index >= 0) state.prompts[index] = { ...state.prompts[index], ...next };
          else state.prompts.push(next);
          return { prompt: clone(next) };
        }
        case 'prompts/delete': {
          const id = (params.id || '').toString();
          state.prompts = state.prompts.filter((item) => (item?.id || item?.prompt_key || '').toString() !== id);
          return { ok: true };
        }
        case 'prompt-intents/draft': {
          await sleep(state.promptIntentDelayMs);
          const kind = (params.kind || 'expert').toString();
          const variant = intentVariantFromInput(kind, params.raw_input);
          state.nextPromptIntentSeq += 1;
          const draft = {
            draft_key: `draft-${kind}-${variant}-${state.nextPromptIntentSeq}`,
            kind,
            cwd: normalizePath(params.cwd || state.activeProject || state.runtimeConfig.cwd || ''),
            status: variant === 'blocked' ? 'draft_blocked' : variant,
            card: intentCard(kind, variant, params.raw_input),
            issues: intentIssues(kind, variant),
            confidence: variant === 'blocked' ? 0.52 : 0.91,
          };
          state.promptIntentDrafts[draft.draft_key] = clone(draft);
          return { draft: clone(draft) };
        }
        case 'prompt-intents/dry-run': {
          await sleep(state.promptIntentDelayMs);
          return {
            would_use: true,
            action: 'default_rule',
            target: '旧的默认规则',
            reasons: ['这份草稿会建议先确认风险，再执行保存动作。'],
            candidates: ['旧的默认规则'],
            disclaimer: '仅用于保存前验证，不代表真实模型一定做出相同选择。',
          };
        }
        case 'prompt-intents/commit': {
          await sleep(state.promptIntentDelayMs);
          const draftKey = (params.draft_key || '').toString();
          const draft = asObject(state.promptIntentDrafts[draftKey]);
          if (!draft.draft_key) throw new Error(`intent draft not found: ${draftKey}`);
          const savedPrompt = promptRowFromIntentDraft({
            ...draft,
            scope: params.enable_global && params.confirm_global ? 'global' : 'project',
          });
          state.prompts.push(savedPrompt);
          state.promptIntentCommits.push({ payload: clone(params), savedPrompt: clone(savedPrompt) });
          return {
            prompt: {
              id: savedPrompt.id,
              prompt_key: savedPrompt.prompt_key,
              name: savedPrompt.name,
              enabled: savedPrompt.enabled,
              tags: clone(savedPrompt.tags),
              saved_at: savedPrompt.created_at,
            },
          };
        }
        case 'ui/memory/get':
          return clone(state.memoryCenter);
        case 'ui/memory/entry/get': {
          const { entry } = findMemoryEntry(params.target, params.path);
          if (!entry) throw new Error(`memory entry not found: ${params.path || ''}`);
          return clone({
            target: (params.target || 'private').toString(),
            path: entry.path,
            name: entry.name,
            description: entry.description,
            type: entry.type,
            content: entry.content,
            updatedAt: entry.updatedAt,
          });
        }
        case 'ui/memory/entry/upsert':
          return upsertMemoryEntry(params);
        case 'ui/memory/entry/delete':
          return deleteMemoryEntry(params);
        case 'ui/memory/entry/merge':
          return mergeMemoryEntries(params);
        case 'ui/memory/shared-file/get': {
          const file = findSharedFile(params.path);
          if (!file) throw new Error(`shared file not found: ${params.path || ''}`);
          return clone({
            path: file.path,
            content: file.content || '',
            updatedBy: file.updated_by || file.updatedBy || '',
            updatedAt: file.updated_at || file.updatedAt || '',
          });
        }
        case 'ui/memory/shared-file/delete': {
          const target = (params.path || '').toString().trim();
          if (!target) throw new Error('path is required');
          const before = asArray(state.dashboard.memory).length;
          state.dashboard.memory = asArray(state.dashboard.memory).filter(
            (item) => (item?.path || '').toString().trim() !== target,
          );
          return { deleted: state.dashboard.memory.length < before };
        }
        case 'ui/preferences/get': {
          const key = (params.key || '').toString();
          if (Object.prototype.hasOwnProperty.call(state.preferences, key)) {
            return clone(state.preferences[key]);
          }
          return null;
        }
        case 'ui/preferences/set':
          writePreference((params.key || '').toString(), params.value);
          return { ok: true };
        case 'thread/start': {
          state.nextThreadSeq += 1;
          const id = `thread-e2e-${state.nextThreadSeq}`;
          const provider = (params.modelProvider || state.preferences['settings.provider.active'] || 'codex').toString();
          const cwd = normalizePath(params.cwd || state.activeProject || state.runtimeConfig.cwd || '.');
          const thread = normalizeThread({
            id,
            name: id,
            alias: id,
            provider,
            cwd,
            providerThreadId: `${provider}-provider-${state.nextThreadSeq}`,
            port: 4200 + state.nextThreadSeq,
            statusHeader: '等待指示',
            interruptible: false,
          });
          state.threads.push(thread);
          ensureThread(id, thread);
          return { thread: { id } };
        }
        case 'thread/messages': {
          const threadId = (params.threadId || '').toString();
          const timeline = asArray(state.timelinesByThread[threadId]);
          return {
            total: timeline.length,
            messages: timeline.map((item, index) => ({
              id: index + 1,
              agentId: threadId,
              role: (item?.kind || 'assistant') === 'user' ? 'user' : 'assistant',
              eventType: 'agent_message',
              method: '',
              content: (item?.text || '').toString(),
              createdAt: item?.ts || isoClock(),
            })),
          };
        }
        case 'turn/start': {
          const threadId = (params.threadId || '').toString();
          ensureThread(threadId, { id: threadId, name: threadId });
          const nextItem = buildTurnItem(params.input);
          state.timelinesByThread[threadId] = [...asArray(state.timelinesByThread[threadId]), nextItem];
          state.statuses[threadId] = 'idle';
          state.statusHeadersByThread[threadId] = '已发送';
          if (params.cwd) {
            state.agentRuntimeById[threadId] = {
              ...(asObject(state.agentRuntimeById[threadId])),
              cwd: normalizePath(params.cwd),
            };
          }
          return { ok: true };
        }
        case 'turn/interrupt': {
          const threadId = (params.threadId || '').toString();
          state.interruptibleByThread[threadId] = false;
          state.statuses[threadId] = 'idle';
          state.statusHeadersByThread[threadId] = '已停止';
          return {
            interruptSent: true,
            confirmed: true,
            settled: true,
            mode: 'interrupt_confirmed',
            stateBefore: 'running',
            stateAfter: 'idle',
            waitedMs: 0,
          };
        }
        case 'turn/forceComplete': {
          const threadId = (params.threadId || '').toString();
          state.statuses[threadId] = 'idle';
          state.interruptibleByThread[threadId] = false;
          state.statusHeadersByThread[threadId] = '已强制完成';
          return { confirmed: true };
        }
        case 'thread/recover': {
          const threadId = (params.threadId || '').toString();
          state.statuses[threadId] = 'idle';
          state.interruptibleByThread[threadId] = false;
          state.statusHeadersByThread[threadId] = '已恢复';
          return { recovered: true, mode: 'recovered' };
        }
        case 'thread/name/set': {
          const threadId = (params.threadId || '').toString();
          const nextName = (params.name || '').toString().trim();
          const target = state.threads.find((item) => item.id === threadId);
          if (target && nextName) {
            target.name = nextName;
          }
          state.agentMetaById[threadId] = {
            ...(asObject(state.agentMetaById[threadId])),
            alias: nextName || threadId,
          };
          return { ok: true };
        }
        case 'thread/archive': {
          const threadId = (params.threadId || '').toString();
          state.archivedThreadAtById = {
            ...(asObject(state.preferences['threadArchives.chat'] || state.archivedThreadAtById)),
            [threadId]: Date.now(),
          };
          writePreference('threadArchives.chat', state.archivedThreadAtById);
          return { ok: true };
        }
        case 'thread/unarchive': {
          const threadId = (params.threadId || '').toString();
          const next = { ...(asObject(state.preferences['threadArchives.chat'] || state.archivedThreadAtById)) };
          delete next[threadId];
          state.archivedThreadAtById = next;
          writePreference('threadArchives.chat', next);
          return { ok: true, archiveModified: false };
        }
        case 'thread/delete': {
          const threadId = (params.threadId || '').toString();
          const nextArchive = { ...(asObject(state.preferences['threadArchives.chat'] || state.archivedThreadAtById)) };
          delete nextArchive[threadId];
          state.archivedThreadAtById = nextArchive;
          writePreference('threadArchives.chat', nextArchive);
          state.threads = (state.threads || []).filter((t) => t.id !== threadId);
          return { ok: true };
        }
        case 'ui/selectProjectDir':
          return { path: state.uiSelectProjectDirResult };
        case 'ui/openNewWindow':
          return { ok: true };
        case 'ui/selectProjectDirs':
          return { paths: clone(state.selectProjectDirsResult) };
        case 'ui/selectFiles':
          return { paths: clone(state.selectFilesResult) };
        case 'ui/copyText':
          return { ok: true };
        case 'thread/resolve': {
          const threadId = (params.threadId || '').toString();
          const runtime = asObject(state.agentRuntimeById[threadId]);
          return {
            providerThreadId: (runtime.providerThreadId || '').toString(),
            port: runtime.port ?? null,
          };
        }
        case 'approval/respond':
          return { ok: true };
        case 'skills/match/preview':
          return { matches: clone(asArray(source.skillPreviewMatches)) || [] };
        case 'config/lspPromptHint/read': {
          const overrideHint = (state.preferences['config/lspPromptHint.override'] || '').toString();
          const defaultHint = (state.preferences['config/lspPromptHint.default'] || '默认提示词').toString();
          const effectiveHint = overrideHint || defaultHint;
          return {
            hint: effectiveHint,
            defaultHint,
            overrideHint,
            usingDefault: !overrideHint,
          };
        }
        case 'config/lspPromptHint/write': {
          const overrideHint = (params.hint || '').toString();
          const defaultHint = (state.preferences['config/lspPromptHint.default'] || '默认提示词').toString();
          state.preferences['config/lspPromptHint.override'] = overrideHint;
          return {
            hint: overrideHint || defaultHint,
            defaultHint,
            overrideHint,
            usingDefault: !overrideHint,
          };
        }
        case 'skills/local/listFiles': {
          const dir = normalizePath(params.dir || '');
          const files = clone(asArray(state.skillFilesByDir[dir])) || [];
          return { files };
        }
        case 'skills/local/read': {
          const path = normalizePath(params.path || '');
          const entry = asObject(state.skillFileContents[path]);
          return {
            skill: {
              content: (entry.content || '').toString(),
              summary: (entry.summary || '').toString(),
              summary_source: (entry.summary_source || '').toString(),
            },
          };
        }
        case 'skills/local/write': {
          const path = normalizePath(params.path || '');
          const current = asObject(state.skillFileContents[path]);
          state.skillFileContents[path] = {
            ...current,
            content: (params.content || '').toString(),
          };
          return { ok: true };
        }
        case 'skills/config/write': {
          const name = (params.name || '').toString().trim();
          const content = (params.content || '').toString();
          const parsed = parseSkillContent(content);
          const dir = normalizePath(`/mock-skills/${name}`);
          ensureSkillFile(`${dir}/SKILL.md`, content, {
            summary: parsed.summary,
            summary_source: parsed.summary ? 'frontmatter' : '',
          });
          upsertSkillCard({
            name: parsed.name || name,
            dir,
            description: parsed.description,
            summary: parsed.summary,
            triggerWords: parsed.triggerWords,
            forceWords: parsed.forceWords,
          });
          return { ok: true };
        }
        case 'skills/local/delete': {
          const name = (params.name || '').toString().trim();
          removeSkillCard(name);
          const skillDir = normalizePath(`/mock-skills/${name}`);
          delete state.skillFilesByDir[skillDir];
          Object.keys(state.skillFileContents).forEach((path) => {
            if (normalizePath(path).startsWith(`${skillDir}/`)) {
              delete state.skillFileContents[path];
            }
          });
          return { ok: true };
        }
        case 'skills/local/importDir': {
          if (state.importResult) {
            const payload = clone(state.importResult) || { imported: [], failures: [] };
            const imported = asArray(payload.imported).length > 0 ? asArray(payload.imported) : asArray(payload.skills);
            const normalizedPayload = { ...payload, imported };
            delete normalizedPayload.skills;
            asArray(normalizedPayload.imported).forEach((item) => {
              if (item?.skill_file) {
                const parsedName = (item?.name || basename(dirname(item.skill_file))).toString();
                ensureSkillFile(item.skill_file, `---\nname: "${parsedName}"\nsummary: "${parsedName} 摘要"\n---\n\n## ${parsedName}`,
                  { summary: `${parsedName} 摘要`, summary_source: 'frontmatter' });
                upsertSkillCard({
                  name: parsedName,
                  dir: dirname(item.skill_file),
                  description: `${parsedName} 导入描述`,
                  summary: `${parsedName} 摘要`,
                  triggerWords: [parsedName],
                  forceWords: [],
                });
              }
            });
            return normalizedPayload;
          }
          const importedSkills = asArray(params.paths).map((item) => buildImportedSkill(item));
          return { imported: importedSkills, failures: [] };
        }
        case 'ui/code/open': {
          const path = (params.filePath || params.path || '').toString();
          return clone(state.codeOpenByPath[path] || state.codeOpenByPath[normalizePath(path)] || { ok: false });
        }
        default:
          if ((method || '').toString().startsWith('prompt-intents/')) {
            throw new Error(`Unhandled prompt intent RPC: ${method}`);
          }
          return {};
      }
    },
  };
}

export async function installMockBackend(page, config = {}) {
  await page.addInitScript(browserInstaller, {
    callApiId: CALL_API_METHOD_ID,
    buildInfoId: GET_BUILD_INFO_METHOD_ID,
    saveClipboardImageId: SAVE_CLIPBOARD_IMAGE_METHOD_ID,
    selectFilesId: SELECT_FILES_METHOD_ID,
    selectProjectDirId: SELECT_PROJECT_DIR_METHOD_ID,
    sourceConfig: config,
  });

  await page.route('**/wails/runtime.js', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/javascript',
      body: RUNTIME_MODULE_SOURCE,
    });
  });
}

export async function readBackendState(page) {
  return page.evaluate(() => JSON.parse(JSON.stringify(globalThis.__AO_E2E_BACKEND_STATE__ || {})));
}

export async function readMethodCalls(page, method) {
  return page.evaluate((targetMethod) => {
    const items = globalThis.__AO_E2E_BACKEND_STATE__?.callLog || [];
    return items.filter((item) => item?.method === targetMethod);
  }, method);
}
