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

  const source = clone(sourceConfig) || {};
  const dashboardSource = asObject(source.dashboard);

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
    },
    memoryCenter: clone(asObject(source.memoryCenter)) || {
      overview: {},
      private: { entries: [] },
      team: { entries: [] },
      agentScopes: [],
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

  function deleteMemoryEntry(params) {
    const section = ensureMemorySection(params.target);
    const normalizedPath = (params.path || '').toString().trim();
    section.entries = section.entries.filter((item) => (item?.path || '').toString().trim() !== normalizedPath);
    if (section.entries.length === 0) {
      section.notice = section.notice || '当前目录下还没有可读的记忆条目。';
    }
    return { deleted: true };
  }

  function ensureAgentScope(scope) {
    const scopeKey = (scope || 'project').toString().trim() || 'project';
    const scopes = asArray(state.memoryCenter.agentScopes);
    let item = scopes.find((entry) => (entry?.scope || '').toString().trim() === scopeKey);
    if (!item) {
      item = { scope: scopeKey, rootPath: '', notice: '', entries: [] };
      scopes.push(item);
      state.memoryCenter.agentScopes = scopes;
    }
    item.entries = clone(asArray(item.entries)) || [];
    return item;
  }

  function getAgentEntry(scope, agentType) {
    const scopeItem = ensureAgentScope(scope);
    const normalizedAgentType = (agentType || '').toString().trim();
    const index = scopeItem.entries.findIndex((item) => (item?.agentType || '').toString().trim() === normalizedAgentType);
    return { scopeItem, index, entry: index >= 0 ? scopeItem.entries[index] : null };
  }

  function deleteAgentEntry(params) {
    const agentType = (params.agentType || '').toString().trim();
    const scope = (params.scope || 'project').toString().trim() || 'project';
    if (!agentType) throw new Error('agentType is required');
    const { scopeItem, index } = getAgentEntry(scope, agentType);
    if (index >= 0) {
      scopeItem.entries.splice(index, 1);
    }
    return { deleted: index >= 0 };
  }

  function saveAgentEntry(params) {
    const normalizedAgentType = (params.agentType || '').toString().trim();
    const content = (params.content || '').toString();
    const scope = (params.scope || 'project').toString().trim() || 'project';
    const { scopeItem, index } = getAgentEntry(scope, normalizedAgentType);
    const next = {
      agentType: normalizedAgentType,
      path: `${normalizedAgentType}/MEMORY.md`,
      content,
      preview: memoryPreview(content),
      updatedAt: isoClock(),
    };
    if (index >= 0) {
      scopeItem.entries[index] = { ...scopeItem.entries[index], ...next };
    } else {
      scopeItem.entries.push(next);
    }
    scopeItem.notice = scopeItem.entries.length === 0 ? scopeItem.notice : '';
    return clone(next);
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
        case 'thread/list':
          return { threads: buildUiState().threads };
        case 'ui/sidebar/get':
          return buildUiState();
        case 'ui/state/get':
          return buildUiState();
        case 'ui/dashboard/get':
          return clone(state.dashboard);
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
        case 'ui/memory/agent/get': {
          const { entry } = getAgentEntry(params.scope, params.agentType);
          if (!entry) {
            return {
              scope: (params.scope || 'project').toString(),
              agentType: (params.agentType || '').toString(),
              path: `${(params.agentType || '').toString().trim()}/MEMORY.md`,
              content: '',
              updatedAt: '',
            };
          }
          return clone({
            scope: (params.scope || 'project').toString(),
            agentType: entry.agentType,
            path: entry.path,
            content: entry.content,
            updatedAt: entry.updatedAt,
          });
        }
        case 'ui/memory/agent/save':
          return clone({
            scope: (params.scope || 'project').toString(),
            ...saveAgentEntry(params),
          });
        case 'ui/memory/agent/delete':
          return clone(deleteAgentEntry(params));
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
        case 'ui/memory/shared-file/promote': {
          const file = findSharedFile(params.sharedPath);
          if (!file) throw new Error(`shared file not found: ${params.sharedPath || ''}`);
          return upsertMemoryEntry({
            ...params,
            content: (params.content || file.content || '').toString(),
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
          const timeline = asArray(state.timelinesByThread[threadId]).filter((item) => {
            const kind = (item?.kind || '').toString().trim();
            return kind === 'assistant' || kind === 'user';
          });
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
          const content = (params.content || '').toString();
          const isMainSkillWrite = path && !path.includes('/');
          if (isMainSkillWrite) {
            const skillName = (params.path || '').toString().trim();
            const parsed = parseSkillContent(content);
            const dir = normalizePath(`/mock-skills/${parsed.name || skillName}`);
            ensureSkillFile(`${dir}/SKILL.md`, content, {
              summary: parsed.summary,
              summary_source: parsed.summary ? 'frontmatter' : '',
            });
            upsertSkillCard({
              name: parsed.name || skillName,
              dir,
              description: parsed.description,
              summary: parsed.summary,
              triggerWords: parsed.triggerWords,
              forceWords: parsed.forceWords,
            });
            return { ok: true, path: `${dir}/SKILL.md` };
          }
          const current = asObject(state.skillFileContents[path]);
          state.skillFileContents[path] = {
            ...current,
            content,
          };
          return { ok: true, path };
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
            const payload = clone(state.importResult) || { skills: [], failures: [] };
            asArray(payload.skills).forEach((item) => {
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
            return payload;
          }
          const importedSkills = asArray(params.paths).map((item) => buildImportedSkill(item));
          return { skills: importedSkills, failures: [] };
        }
        case 'ui/code/open': {
          const path = (params.filePath || params.path || '').toString();
          return clone(state.codeOpenByPath[path] || state.codeOpenByPath[normalizePath(path)] || { ok: false });
        }
        default:
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
