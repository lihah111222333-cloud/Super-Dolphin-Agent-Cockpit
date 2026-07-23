
import { firstOptionalPresent } from '../../contractStoreModel.js';
import { firstThreadCopyText } from '../threadCopyPayload.js';
import { isAgentRuntimeId, normalizeBackendThreadId, normalizeThreadId, normalizeThreadIdentity } from '../threadIdentity.js';
import { isInterruptibleTurnSummary, isTerminalActiveTurnStatus, normalizeTurnSummary } from '../threadActivityMetrics.js';
import {
  AGENT_IDENTITY_KEYS,
  ROOT_THREAD_IDENTITY_KEYS,
  THREAD_IDENTITY_KEYS,
  firstValueFromSources,
  normalizePath,
  normalizeProviderName,
  normalizeString,
  objectRecord,
  optionalUiArray,
  optionalUiObject,
  sidebarProjectKey,
} from './clientStoreUtils.js';
import { hasOwn } from './clientStoreThreadModel.js';

function runtimeMapFromPayload(payload = {}) {
  const value = payload.agentRuntimeById || payload.agent_runtime_by_id || optionalUiObject();
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {};
  return value;
}

function runtimeProviderForThread(rawThread, runtimeById = {}) {
  const identity = normalizeThreadIdentity(rawThread);
  const candidates = [
    identity.threadId,
    identity.agentId,
    identity.providerThreadId,
    identity.sessionId,
    rawThread?.id,
    rawThread?.threadId,
    rawThread?.thread_id,
    rawThread?.agentId,
    rawThread?.agent_id,
  ].map(normalizeThreadId).filter(Boolean);
  for (const candidate of candidates) {
    const runtime = runtimeById[candidate];
    if (!runtime || typeof runtime !== 'object' || Array.isArray(runtime)) continue;
    const provider = normalizeString(runtime.provider || runtime.modelProvider || runtime.model_provider);
    if (provider) return provider;
  }
  return '';
}

function runtimeCwdForThread(rawThread, runtimeById = {}) {
  const identity = normalizeThreadIdentity(rawThread);
  const candidates = [
    identity.threadId,
    identity.agentId,
    identity.providerThreadId,
    identity.sessionId,
    rawThread?.id,
    rawThread?.threadId,
    rawThread?.thread_id,
    rawThread?.agentId,
    rawThread?.agent_id,
  ].map(normalizeThreadId).filter(Boolean);
  for (const candidate of candidates) {
    const runtime = runtimeById[candidate];
    if (!runtime || typeof runtime !== 'object' || Array.isArray(runtime)) continue;
    const cwd = normalizePath(runtime.cwd || runtime.CWD || runtime.workdir || runtime.workDir || runtime.work_dir);
    if (cwd) return cwd;
  }
  return '';
}

function snapshotThreadProjectPath(rawThread = {}) {
  const sourceThread = rawThread?.thread && typeof rawThread.thread === 'object' ? rawThread.thread : {};
  const direct = firstThreadCopyText(
    rawThread?.projectPath,
    rawThread?.project_path,
    rawThread?.workspacePath,
    rawThread?.workspace_path,
    rawThread?.rootPath,
    rawThread?.root_path,
    sourceThread?.projectPath,
    sourceThread?.project_path,
    sourceThread?.workspacePath,
    sourceThread?.workspace_path,
    sourceThread?.rootPath,
    sourceThread?.root_path,
  );
  if (direct) return direct;

  for (const key of ['project', 'workspace', 'metadata', 'meta']) {
    const nested = rawThread?.[key];
    if (!nested || typeof nested !== 'object' || Array.isArray(nested)) continue;
    const nestedPath = firstThreadCopyText(nested.path, nested.cwd, nested.root, nested.projectPath, nested.project_path);
    if (nestedPath) return nestedPath;
  }
  return '';
}

function snapshotThreadCwd(rawThread, runtimeById = {}) {
  const sourceThread = rawThread?.thread && typeof rawThread.thread === 'object' ? rawThread.thread : {};
  return normalizePath(
    firstOptionalPresent(
      rawThread?.cwd,
      rawThread?.CWD,
      rawThread?.workdir,
      rawThread?.workDir,
      rawThread?.work_dir,
      sourceThread?.cwd,
      sourceThread?.CWD,
      sourceThread?.workdir,
      sourceThread?.workDir,
      sourceThread?.work_dir,
      snapshotThreadProjectPath(rawThread),
      runtimeCwdForThread(rawThread, runtimeById),
    ),
  );
}

function threadMatchesCwdScope(rawThread, scopeCwd, runtimeById = {}) {
  const scope = sidebarProjectKey(scopeCwd);
  if (!scope || scope === '.') return true;
  const threadCwd = sidebarProjectKey(snapshotThreadCwd(rawThread, runtimeById));
  return !threadCwd || threadCwd === scope;
}

function threadMatchesIdentifier(thread, value) {
  const id = normalizeThreadId(value);
  return Boolean(id && (
    normalizeThreadId(thread?.id) === id ||
    normalizeThreadId(thread?.agentId) === id ||
    normalizeThreadId(thread?.providerThreadId) === id ||
    normalizeThreadId(thread?.sessionId) === id
  ));
}

function backendThreadIdFromThreads(value, threads = [], options = {}) {
  const id = normalizeThreadId(value);
  if (!id) return '';
  const direct = normalizeBackendThreadId(id);
  const directMatch = direct ? threads.find((thread) => threadMatchesIdentifier(thread, direct)) : null;
  if (directMatch) {
    return directMatch.archived && !options.includeArchived ? '' : normalizeBackendThreadId(directMatch.id);
  }
  const match = threads.find((thread) => threadMatchesIdentifier(thread, id));
  if (match?.archived && !options.includeArchived) return '';
  return normalizeBackendThreadId(match?.id);
}

function backendThreadIdForState(state, value, options = {}) {
  const id = normalizeThreadId(value);
  if (!id) return '';
  const matchedThread = state.threads.find((thread) => threadMatchesIdentifier(thread, id));
  if (matchedThread) return matchedThread.archived && !options.includeArchived ? '' : normalizeBackendThreadId(matchedThread.id);
  if (isAgentRuntimeId(id)) return '';
  return normalizeBackendThreadId(id);
}

function explicitThreadReplacementIds(thread, requestedId) {
  return [
    requestedId,
    thread?.id,
    thread?.agentId,
    thread?.providerThreadId,
    thread?.sessionId,
  ].map(normalizeThreadId).filter(Boolean);
}

function upsertExplicitThread(threads, thread, requestedId) {
  const ids = explicitThreadReplacementIds(thread, requestedId);
  const existingIndex = threads.findIndex((candidate) => ids.some((id) => threadMatchesIdentifier(candidate, id)));
  if (existingIndex < 0) return [...threads, thread];
  return threads.map((candidate, index) => (
    index === existingIndex ? { ...candidate, ...thread } : candidate
  ));
}

function pickThreadScopedEntry(map = {}, threadId = '') {
  return threadId && hasOwn(map, threadId) ? { [threadId]: map[threadId] } : {};
}

function providerForStateThread(state, value) {
  const id = normalizeThreadId(value);
  const matchedThread = id ? state.threads.find((thread) => threadMatchesIdentifier(thread, id)) : null;
  return normalizeProviderName(firstOptionalPresent(matchedThread?.provider, state?.provider));
}

function shouldAutoLoadThreadConfig(state, value) {
  const id = normalizeThreadId(value);
  if (!id) return false;
  if (state.threadConfigByThread?.[id]) return false;
  if (state.threadConfigLoadingByThread?.[id]) return false;
  if (state.threadConfigFailedByThread?.[id]) return false;
  const provider = providerForStateThread(state, id);
  return !(provider === 'codex' && isAgentRuntimeId(id));
}

function threadConfigTargetIdForState(state, value) {
  const id = backendThreadIdForState(state, value);
  if (!id || isAgentRuntimeId(id)) return '';
  return id;
}

function isFailedThreadStatus(value) {
  const status = normalizeString(value).toLowerCase();
  return status === 'failed' || status === 'error' || status.includes('错误') || status.includes('失败');
}

function reusableThreadIdForSend(state, value) {
  const id = normalizeThreadId(value);
  if (!id) return '';
  const matchedThread = state.threads.find((thread) => threadMatchesIdentifier(thread, id));
  if (matchedThread) {
    if (matchedThread.archived || isFailedThreadStatus(matchedThread.status)) return '';
    return normalizeBackendThreadId(matchedThread.id);
  }
  if (isAgentRuntimeId(id)) return '';
  return normalizeBackendThreadId(id);
}

function cwdForExistingThreadSend(state, threadId) {
  const id = normalizeThreadId(threadId);
  if (!id) throw new Error('frontend-app: existing thread id is required before sending');
  const matchedThread = state.threads.find((thread) => (
    threadMatchesIdentifier(thread, id) ||
    threadMatchesIdentifier(thread, state.activeThreadId)
  ));
  if (!matchedThread) {
    throw new Error('frontend-app: reopen the conversation before sending because its authoritative thread record is unavailable');
  }
  const threadCwd = normalizePath(matchedThread?.cwd);
  if (!threadCwd || threadCwd === '.') {
    throw new Error('frontend-app: reopen the conversation before sending because its authoritative workspace is unavailable');
  }
  return threadCwd;
}

function activeProviderLockedThreadId(state) {
  const id = normalizeThreadId(state?.activeThreadId);
  if (!id) return '';
  const matchedThread = (state?.threads || optionalUiArray()).find((thread) => threadMatchesIdentifier(thread, id));
  return normalizeBackendThreadId(matchedThread?.id || id);
}

function activeTurnIdForThread(state, threadId) {
  const id = normalizeBackendThreadId(threadId);
  if (!id) return '';
  const direct = normalizeTurnSummary(state.activeTurnByThread?.[id]);
  if (isInterruptibleTurnSummary(direct)) return direct.id;
  const activeId = normalizeThreadId(state.activeThreadId);
  if (activeId && activeId !== id) {
    const active = normalizeTurnSummary(state.activeTurnByThread?.[activeId]);
    if (isInterruptibleTurnSummary(active) && threadMatchesIdentifier({ id, thread_id: id }, active.threadId || activeId)) return active.id;
  }
  return '';
}

function statusEntryForInterruptTarget(state, threadId, activeId = '') {
  const candidates = [];
  const pushCandidate = (value) => {
    const id = normalizeThreadId(value);
    if (id && !candidates.includes(id)) candidates.push(id);
  };
  const matchedThread = (state?.threads || optionalUiArray()).find((thread) => (
    threadMatchesIdentifier(thread, threadId) || threadMatchesIdentifier(thread, activeId)
  ));
  pushCandidate(threadId);
  pushCandidate(activeId);
  pushCandidate(matchedThread?.id);
  pushCandidate(matchedThread?.agentId);
  pushCandidate(matchedThread?.providerThreadId);
  pushCandidate(matchedThread?.sessionId);
  for (const candidate of candidates) {
    const entry = state?.statuses?.[candidate];
    if (entry && typeof entry === 'object' && !Array.isArray(entry)) {
      return { entry, thread: matchedThread };
    }
  }
  return { entry: null, thread: matchedThread };
}

function threadStatusBlocksInterrupt(state, threadId, activeId = '') {
  const { entry, thread } = statusEntryForInterruptTarget(state, threadId, activeId);
  if (entry?.interruptible === false) return true;
  return isTerminalActiveTurnStatus(firstOptionalPresent(entry?.status, thread?.status));
}

function activeThreadInterruptTarget(state) {
  const activeID = normalizeThreadId(state?.activeThreadId);
  const threadID = backendThreadIdForState(state, activeID) || normalizeBackendThreadId(activeID);
  if (!threadID) return { threadId: '', turnId: '', interruptible: false };
  if (threadStatusBlocksInterrupt(state, threadID, activeID)) {
    return { threadId: threadID, turnId: '', interruptible: false };
  }
  const turnID = activeTurnIdForThread(state, threadID);
  return {
    threadId: threadID,
    turnId: turnID,
    interruptible: Boolean(turnID),
  };
}

function backendThreadIdForArchiveState(state, value) {
  const id = normalizeThreadId(value);
  if (!id) return '';
  const matchedThread = state.threads.find((thread) => threadMatchesIdentifier(thread, id));
  if (matchedThread) return normalizeBackendThreadId(matchedThread.id);
  if (isAgentRuntimeId(id)) return '';
  return normalizeBackendThreadId(id);
}

function canonicalizeThreadKey(key, threads = []) {
  return backendThreadIdFromThreads(key, threads, { includeArchived: true }) || normalizeBackendThreadId(key) || normalizeThreadId(key);
}

function canonicalizeActiveTurnByThread(activeTurnByThread = {}, threads = []) {
  const next = {};
  for (const [threadId, turn] of Object.entries(activeTurnByThread || optionalUiArray())) {
    const normalized = normalizeTurnSummary(turn);
    if (!isInterruptibleTurnSummary(normalized)) continue;
    const canonicalThreadId = canonicalizeThreadKey(normalized.threadId || threadId, threads);
    if (canonicalThreadId) next[canonicalThreadId] = { ...normalized, threadId: canonicalThreadId };
  }
  return next;
}

function extractDeltaText(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return value.toString();
  }
  if (Array.isArray(value)) {
    return value.map((item) => extractDeltaText(item)).filter((item) => item !== '').join('\n');
  }
  if (typeof value === 'object') {
    for (const key of ['delta', 'text', 'content', 'message', 'output', 'result', 'answer', 'response']) {
      if (value[key] !== undefined && value[key] !== null) return extractDeltaText(value[key]);
    }
  }
  return '';
}


function runtimeThreadIdentifier(payload = {}) {
  const patch = objectRecord(payload._threadPatch || payload._thread_patch);
  const thread = objectRecord(payload.thread);
  const patchThread = objectRecord(patch.thread);
  const runtime = objectRecord(payload.agentRuntime || payload.agent_runtime);
  const patchRuntime = objectRecord(patch.agentRuntime || patch.agent_runtime);
  return firstValueFromSources([
    [payload, ROOT_THREAD_IDENTITY_KEYS],
    [thread, THREAD_IDENTITY_KEYS],
    [runtime, ['threadId', 'thread_id']],
    [patch, ROOT_THREAD_IDENTITY_KEYS],
    [patchThread, THREAD_IDENTITY_KEYS],
    [patchRuntime, ['threadId', 'thread_id']],
    [payload, AGENT_IDENTITY_KEYS],
    [thread, AGENT_IDENTITY_KEYS],
    [runtime, AGENT_IDENTITY_KEYS],
    [patch, AGENT_IDENTITY_KEYS],
    [patchThread, AGENT_IDENTITY_KEYS],
    [patchRuntime, AGENT_IDENTITY_KEYS],
  ]);
}

function runtimePayloadCwd(payload = {}) {
  return normalizePath(
    payload.cwd ||
    payload.CWD ||
    payload.agentRuntime?.cwd ||
    payload.agent_runtime?.cwd ||
    payload.thread?.cwd ||
    payload._threadPatch?.cwd ||
    payload._threadPatch?.agentRuntime?.cwd ||
    payload._threadPatch?.agent_runtime?.cwd,
  );
}

export {
  activeProviderLockedThreadId,
  activeThreadInterruptTarget,
  activeTurnIdForThread,
  backendThreadIdForArchiveState,
  backendThreadIdForState,
  backendThreadIdFromThreads,
  canonicalizeActiveTurnByThread,
  canonicalizeThreadKey,
  cwdForExistingThreadSend,
  explicitThreadReplacementIds,
  extractDeltaText,
  isFailedThreadStatus,
  pickThreadScopedEntry,
  providerForStateThread,
  reusableThreadIdForSend,
  runtimeCwdForThread,
  runtimeMapFromPayload,
  runtimePayloadCwd,
  runtimeProviderForThread,
  runtimeThreadIdentifier,
  shouldAutoLoadThreadConfig,
  snapshotThreadCwd,
  snapshotThreadProjectPath,
  statusEntryForInterruptTarget,
  threadConfigTargetIdForState,
  threadMatchesCwdScope,
  threadMatchesIdentifier,
  threadStatusBlocksInterrupt,
  upsertExplicitThread,
};
