import { firstPresentText, requireArrayValue, textValue } from './pages/shared/pageShared.js';
import { APP_BRAND_NAME } from './shared/i18n/appI18n.js';
import { normalizeThreadTimestamp } from './shared/time/threadTimestamp.js';

function projectNameFromPath(projectPath) {
  const value = textValue(projectPath);
  if (!value || value === '未选择项目') return APP_BRAND_NAME;
  const normalized = value.replace(/\\/g, '/').replace(/\/+$/g, '');
  return normalized.split('/').filter(Boolean).pop() || APP_BRAND_NAME;
}

export function projectDirectoryItems(projectPath, projects = [], activeProject = '') {
  const seen = new Set();
  const items = [];
  const add = (value) => {
    const path = textValue(value);
    if (!path || path === '.' || path === '未选择项目' || seen.has(path)) return;
    seen.add(path);
    items.push({ path, name: projectNameFromPath(path) });
  };
  projects.forEach(add);
  add(activeProject);
  add(projectPath);
  return items.length ? items : [{ path: '', name: APP_BRAND_NAME }];
}

export function projectTreeKey(value) {
  return textValue(value).replace(/\\/g, '/').replace(/\/+$/g, '').toLowerCase();
}

export function sidebarActiveProjectPath(activeProject, projectPath) {
  const active = textValue(activeProject);
  return active && active !== '.' ? active : textValue(projectPath);
}

function projectThreadSourceId(thread) {
  return firstPresentText(thread?.id, thread?.threadId, thread?.thread_id, thread?.agentId, thread?.agent_id);
}

function mergeAdditionalProjectThread(thread, mergedById, newThreadIds) {
  const id = projectThreadSourceId(thread);
  if (!id) return;
  if (mergedById.has(id)) {
    mergedById.set(id, { ...mergedById.get(id), ...thread });
    return;
  }
  mergedById.set(id, thread);
  newThreadIds.push(id);
}

export function mergeProjectThreadSources(...sources) {
  const canonical = Array.isArray(sources[0]) ? sources[0] : [];
  const canonicalIds = new Set();
  const mergedById = new Map();
  const ordered = [];

  for (const thread of canonical) {
    const id = projectThreadSourceId(thread);
    if (!id || canonicalIds.has(id)) continue;
    canonicalIds.add(id);
    mergedById.set(id, thread);
    ordered.push(id);
  }

  const newThreadIds = [];
  for (const source of sources.slice(1)) {
    for (const thread of Array.isArray(source) ? source : []) {
      mergeAdditionalProjectThread(thread, mergedById, newThreadIds);
    }
  }

  return [...newThreadIds, ...ordered].map((id) => mergedById.get(id));
}

const AUTOMATION_THREAD_MARKERS = Object.freeze(['automation', 'workflow', 'dag', 'cron', 'task']);
const ARCHIVED_THREAD_STATUS = 'archived';

function threadFieldValue(thread = {}, keys = []) {
  for (const key of keys) {
    const value = textValue(thread[key]);
    if (value) return value;
  }
  return '';
}

function projectThreadArchiveMap(snapshot = {}) {
  for (const candidate of [
    snapshot?.['threadArchives.chat'],
    snapshot?.threadArchivesChat,
    snapshot?.archivedThreadAtById,
    snapshot?.threadArchives?.chat,
    snapshot?.thread_archives?.chat,
  ]) {
    if (candidate && typeof candidate === 'object' && !Array.isArray(candidate)) return candidate;
  }
  return {};
}

function archiveTimestamp(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function projectThreadArchiveTimestamp(thread = {}, archiveMap = {}) {
  for (const key of ['id', 'threadId', 'thread_id', 'agentId', 'agent_id']) {
    const id = textValue(thread[key]);
    const timestamp = id ? archiveTimestamp(archiveMap[id]) : 0;
    if (timestamp > 0) return timestamp;
  }
  return 0;
}

function projectThreadStatusArchived(thread = {}) {
  return ['status', 'state', 'lifecycleStatus', 'lifecycle_status', 'threadStatus', 'thread_status']
    .some((key) => textValue(thread[key]).toLowerCase() === ARCHIVED_THREAD_STATUS);
}

function isProjectThreadArchived(thread = {}) {
  return Boolean(thread?.archived) || Boolean(thread?.isArchived) || Boolean(thread?.archivedAt) || projectThreadStatusArchived(thread);
}

function projectThreadRuntimeMap(snapshot = {}) {
  for (const runtime of [snapshot?.agentRuntimeById, snapshot?.agent_runtime_by_id]) {
    if (runtime && typeof runtime === 'object' && !Array.isArray(runtime)) return runtime;
  }
  return {};
}

function projectThreadRuntimeCwd(thread = {}, runtimeById = {}) {
  for (const key of ['id', 'threadId', 'thread_id', 'agentId', 'agent_id', 'providerThreadId', 'provider_thread_id', 'sessionId', 'session_id', 'sessionUuid', 'session_uuid']) {
    const id = textValue(thread[key]);
    const runtime = id ? runtimeById[id] : null;
    if (!runtime || typeof runtime !== 'object' || Array.isArray(runtime)) continue;
    const cwd = firstPresentText(runtime.cwd, runtime.CWD, runtime.workdir, runtime.workDir, runtime.work_dir);
    if (cwd) return cwd;
  }
  return '';
}

function threadProjectPath(thread = {}) {
  const direct = threadFieldValue(thread, [
    'cwd',
    'projectPath',
    'project_path',
    'workspacePath',
    'workspace_path',
    'rootPath',
    'root_path',
  ]);
  if (direct) return direct;

  for (const key of ['project', 'workspace', 'metadata', 'meta']) {
    const value = thread[key];
    if (!value || typeof value !== 'object') continue;
    const nested = threadFieldValue(value, ['path', 'cwd', 'root', 'projectPath', 'project_path']);
    if (nested) return nested;
  }
  return '';
}

export function sidebarSnapshotThreads(snapshot) {
  const threads = Array.isArray(snapshot?.threads) ? snapshot.threads : [];
  const archiveMap = projectThreadArchiveMap(snapshot);
  const runtimeById = projectThreadRuntimeMap(snapshot);
  return threads.map((thread) => {
    const archivedAt = projectThreadArchiveTimestamp(thread, archiveMap);
    const runtimeCwd = projectThreadRuntimeCwd(thread, runtimeById);
    const updatedAt = normalizeSidebarThreadTimestamp(thread);
    if (!archivedAt && !runtimeCwd && !isProjectThreadArchived(thread) && thread?.updatedAt === updatedAt) return thread;
    return {
      ...thread,
      ...(updatedAt ? { updatedAt } : {}),
      ...(runtimeCwd && !textValue(thread?.cwd) ? { cwd: runtimeCwd } : {}),
      ...(archivedAt || isProjectThreadArchived(thread) ? {
        archived: true,
        archivedAt: thread?.archivedAt || archivedAt || 1,
      } : {}),
    };
  });
}

function normalizeSidebarThreadTimestamp(thread = {}) {
  for (const key of ['updatedAt', 'updated_at', 'createdAt', 'created_at']) {
    const value = thread[key];
    if (value !== undefined && value !== null && value !== '' && value !== 0) {
      return normalizeThreadTimestamp(value, 'sidebar thread updatedAt');
    }
  }
  return '';
}

function isAutomationThread(thread = {}) {
  const metadata = [
    threadFieldValue(thread, ['agentKey', 'agent_key']),
    threadFieldValue(thread, ['dagKey', 'dag_key']),
    threadFieldValue(thread, ['workflowKey', 'workflow_key']),
    threadFieldValue(thread, ['runKey', 'run_key']),
    threadFieldValue(thread, ['taskId', 'task_id']),
    threadFieldValue(thread, ['source', 'origin']),
    threadFieldValue(thread, ['kind', 'type']),
  ].map((value) => value.toLowerCase()).filter(Boolean);
  if (metadata.some((value) => AUTOMATION_THREAD_MARKERS.some((marker) => value.includes(marker)))) return true;

  const label = firstPresentText(thread.name, thread.title);
  return label === 'AI 设计流程' ||
    /^\[AI\s*流程设计师\]/.test(label) ||
    /^\[AI\s*Workflow Designer\]/i.test(label);
}

export function projectThreadItems(threads = [], projectPath = '', activeProjectPath = '', options = {}) {
  const sourceThreads = requireArrayValue(threads, 'project thread list');
  const targetProjectKey = projectTreeKey(projectPath);
  const activeProjectKey = projectTreeKey(activeProjectPath);
  const allowMissingCwdFallback = options.allowMissingCwdFallback !== false;
  if (!targetProjectKey) return [];
  return sourceThreads.filter((thread) => {
    if (!thread || isProjectThreadArchived(thread)) return false;
    if (isAutomationThread(thread)) return false;
    const threadProjectKey = projectTreeKey(threadProjectPath(thread));
    if (threadProjectKey) return threadProjectKey === targetProjectKey;
    if (!allowMissingCwdFallback) return false;
    return targetProjectKey === activeProjectKey;
  });
}

export function taskThreadItems(threads = []) {
  const sourceThreads = requireArrayValue(threads, 'task thread list');
  return sourceThreads.filter((thread) => thread && !thread.archived && !thread.archivedAt && isAutomationThread(thread));
}

export function projectThreadLabel(thread = {}) {
  const id = textValue(thread.id);
  const label = firstPresentText(thread.name, thread.title);
  if (!label || (id && label === id)) return '新对话';
  return label;
}
