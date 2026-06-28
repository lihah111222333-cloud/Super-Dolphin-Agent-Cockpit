// @ts-nocheck
import { normalizeThreadID } from './bridge-event-parser.js';
import { ensureThreadOrderIndex } from './thread-time-utils.js';
import {
  PREF_ACTIVE_THREAD_ID,
  PREF_ACTIVE_CMD_THREAD_ID,

  PREF_PINNED_THREADS_CHAT,
  PREF_ARCHIVED_THREADS_CHAT,
} from './thread-preference.model.js';
import { _optimisticThreadIds, OPTIMISTIC_LEAK_GUARD_MS } from './thread-optimistic.js';
import { resolveBuiltinToolLaunchPolicy } from './builtin-tool-policy.js';
import { withCodexLspToolDefaults } from './codex-lsp-defaults.js';
import { buildCodexIdentityConfig, buildCodexLaunchSandboxPreference } from './codex-sandbox-defaults.js';
import { resolveActiveProviderPreference, resolveProviderConfigPreference, resolveScopedProviderPreference } from './provider-preferences.js';
import { compactFailureResult, dialogTimelineSignature, tokenUsageSignature, waitForCompactResponse } from './thread-compact-helpers.js';
import { maybeHandleStalePromptKey } from './thread-stale-prompt.js';
import { assertThreadCanSendInState, callTurnStartWithSendBlock, clearThreadSendNoticesInState, getThreadSendBlockedNoticeFromState, setThreadSendBlockedNoticeFromError, setThreadSendHoldNoticeFromError } from './thread-send-block.js';
import { touchThreadUpdatedAt } from './thread-actions-timestamps.js';
import { normalizeProviderConfigValue } from '../provider-config-options.js';
import { dropSkillNamesCoveredByRefs } from '../utils/skill-ref-utils.js';
import { getScopedPreferenceCached } from './preferences.js';

export { tokenUsageSignature } from './thread-compact-helpers.js';

function isSessionNotAvailableError(err) {
  const msg = (err?.message || err?.cause?.message || '').toString().toLowerCase();
  return msg.includes('session is not available') || msg.includes('session not found');
}

export function perfNow() {
  return typeof performance !== 'undefined' && typeof performance.now === 'function' ? performance.now() : Date.now();
}

export function waitMs(ms) {
  return new Promise((resolve) => {
    globalThis.setTimeout(resolve, Math.max(0, Number(ms) || 0));
  });
}

function providerPreferenceScope(provider) {
  const value = (provider || '').toString().trim();
  if (!value) return '';
  if (value === 'codex') return 'codex';
  if (value === 'claude' || value.startsWith('claude-')) return 'claude';
  return value;
}

function normalizeOptimisticAttachment(attachment) {
  const kind = (attachment?.kind || '').toString().trim() === 'image' ? 'image' : 'file';
  const path = (attachment?.path || '').toString().trim();
  const previewUrl = (attachment?.previewUrl || '').toString().trim();
  const key = path || previewUrl;
  if (!key) return null;
  const name = (attachment?.name || (path ? path.split(/[\\/]/).pop() : '') || key).toString().trim();
  return {
    kind,
    name,
    path,
    previewUrl,
  };
}

function cloneOptimisticAttachments(attachments) {
  const list = Array.isArray(attachments) ? attachments : [];
  const seen = new Set();
  const out = [];
  for (const item of list) {
    const normalized = normalizeOptimisticAttachment(item);
    if (!normalized) continue;
    const dedupeKey = `${normalized.kind}:${normalized.path || normalized.previewUrl}`;
    if (seen.has(dedupeKey)) continue;
    seen.add(dedupeKey);
    out.push(normalized);
  }
  return out;
}

function sameOptimisticAttachment(left, right) {
  return (left?.kind || '') === (right?.kind || '')
    && (left?.name || '') === (right?.name || '')
    && (left?.path || '') === (right?.path || '')
    && (left?.previewUrl || '') === (right?.previewUrl || '');
}

function sameOptimisticAttachmentList(left, right) {
  if (!Array.isArray(left) && !Array.isArray(right)) return true;
  if (!Array.isArray(left) || !Array.isArray(right)) return false;
  if (left.length !== right.length) return false;
  for (let index = 0; index < left.length; index += 1) {
    if (!sameOptimisticAttachment(left[index], right[index])) return false;
  }
  return true;
}

function freezeOptimisticAttachments(attachments) {
  if (!Array.isArray(attachments) || attachments.length === 0) return undefined;
  return Object.freeze(attachments.map((item) => Object.freeze({ ...item })));
}

function upsertOptimisticUserTimelineItem(ctx, threadId, userText, attachments) {
  const existing = Array.isArray(ctx.state.timelinesByThread?.[threadId]) ? ctx.state.timelinesByThread[threadId] : [];
  const normalizedAttachments = cloneOptimisticAttachments(attachments);
  const frozenAttachments = freezeOptimisticAttachments(normalizedAttachments);
  const matchingIndex = userText
    ? existing.findIndex((item) => item?.kind === 'user' && (item?.text || '').trim() === userText && (
      (item?.id || '').toString().includes('-optimistic-')
      || normalizedAttachments.length > 0
    ))
    : existing.findIndex((item) => item?.kind === 'user' && (item?.id || '').toString().includes('-optimistic-') && sameOptimisticAttachmentList(Array.isArray(item?.attachments) ? item.attachments : [], normalizedAttachments));

  if (matchingIndex >= 0) {
    const current = existing[matchingIndex];
    const currentAttachments = Array.isArray(current?.attachments) ? current.attachments : [];
    if (sameOptimisticAttachmentList(currentAttachments, normalizedAttachments)) {
      if (typeof ctx.logDebug === 'function') ctx.logDebug('ui', 'chat.send.optimistic_skip', {
        thread_id: threadId,
        reason: 'matching_user_message_exists',
        text_preview: userText.slice(0, 80),
      });
      return;
    }
    const nextTimeline = existing.slice();
    const nextItem = {
      ...current,
      text: userText,
    };
    if (frozenAttachments) nextItem.attachments = frozenAttachments;
    else delete nextItem.attachments;
    nextTimeline[matchingIndex] = Object.freeze(nextItem);
    ctx.state.timelinesByThread = { ...ctx.state.timelinesByThread, [threadId]: nextTimeline };
    if (typeof ctx.logDebug === 'function') ctx.logDebug('ui', 'chat.send.optimistic_attachments_merged', {
      thread_id: threadId,
      item_id: (current?.id || '').toString(),
      attachment_count: normalizedAttachments.length,
      text_preview: userText.slice(0, 80),
    });
    return;
  }

  const optimisticItem = {
    id: `${threadId}-optimistic-user-${Date.now()}`,
    kind: 'user',
    text: userText,
    ts: new Date().toISOString(),
  };
  if (frozenAttachments) optimisticItem.attachments = frozenAttachments;
  const frozenItem = Object.freeze(optimisticItem);
  ctx.state.timelinesByThread = { ...ctx.state.timelinesByThread, [threadId]: [...existing, frozenItem] };
  if (typeof ctx.logDebug === 'function') ctx.logDebug('ui', 'chat.send.optimistic_insert', {
    thread_id: threadId,
    item_id: frozenItem.id,
    text_preview: userText.slice(0, 80),
    attachment_count: normalizedAttachments.length,
    timeline_len_before: existing.length,
    timeline_len_after: existing.length + 1,
    existing_user_count: existing.filter((item) => item?.kind === 'user').length,
    existing_optimistic_count: existing.filter((item) => (item?.id || '').toString().includes('-optimistic-')).length,
  });
}

export function saveActiveThread(ctx, id) {
  const { logInfo } = ctx;
  const next = id || '';
  if (ctx.state.activeThreadId === next) return;
  const prev = ctx.state.activeThreadId || '';
  ctx.state.activeThreadId = next;
  ctx.markLocalActiveThreadDirty(true);
  ctx.persistPreferenceAndSync(PREF_ACTIVE_THREAD_ID, next, { previous: prev, current: next }, { syncAfterPersist: false });
  logInfo('thread', 'activeChat.switch.request', { previous: prev, current: next, sync_after_persist: false, cwd: ctx.getPreferenceScopeCwd() });
}

export function saveActiveCmdThread(ctx, id) {
  const { logInfo } = ctx;
  const next = id || '';
  if (ctx.state.activeCmdThreadId === next) return;
  const prev = ctx.state.activeCmdThreadId || '';
  ctx.state.activeCmdThreadId = next;
  ctx.markLocalActiveCmdThreadDirty(true);
  ctx.persistPreferenceAndSync(PREF_ACTIVE_CMD_THREAD_ID, next, { previous: prev, current: next }, { syncAfterPersist: false });
  logInfo('thread', 'activeCmd.switch.request', { previous: prev, current: next, sync_after_persist: false, cwd: ctx.getPreferenceScopeCwd() });
}



export async function renameThread(ctx, threadId, name) {
  const { callAPI, logWarn } = ctx;
  const logDebug = typeof ctx.logDebug === 'function' ? ctx.logDebug : () => {};
  const id = (threadId || '').toString();
  const nextName = (name || '').toString().trim();
  logDebug('ui', 'renameThread.triggered', { threadId: id, name: nextName });
  if (!id || !nextName) return;

  if (Array.isArray(ctx.state.threads)) {
    const idx = ctx.state.threads.findIndex(t => t?.id === id);
    if (idx >= 0 && ctx.state.threads[idx].name !== nextName) {
      const nextThreads = ctx.state.threads.slice();
      nextThreads[idx] = { ...nextThreads[idx], name: nextName };
      ctx.state.threads = nextThreads;
    }
  }

  try {
    const res = await callAPI('thread/name/set', { threadId: id, name: nextName });
    logDebug('ui', 'renameThread.api.success', { res });
    logDebug('ui', 'renameThread.sync.complete');
  } catch (error) {
    logWarn('thread', 'rename.remote.failed', { thread_id: id, error });
    throw error;
  }
}

export async function getThreadConfig(ctx, threadId) {
  const { callAPI, logInfo, logWarn } = ctx;
  const id = (threadId || '').toString().trim();
  if (!id) return null;
  const cwd = typeof ctx.getPreferenceScopeCwd === 'function' ? ctx.getPreferenceScopeCwd() : '';
  const start = perfNow();
  logInfo('thread', 'config.get.start', { thread_id: id, cwd });
  try {
    const result = await callAPI('thread/config/get', { threadId: id });
    logInfo('thread', 'config.get.done', {
      thread_id: id,
      cwd,
      provider: (result?.provider || '').toString(),
      supports_thread_override: Boolean(result?.supportsThreadOverride),
      override_model: (result?.override?.model || '').toString(),
      override_effort: (result?.override?.effort || '').toString(),
      effective_model: (result?.effective?.model || '').toString(),
      effective_effort: (result?.effective?.effort || '').toString(),
      duration_ms: Math.round(perfNow() - start),
    });
    return result;
  } catch (error) {
    logWarn('thread', 'config.get.failed', { thread_id: id, cwd, duration_ms: Math.round(perfNow() - start), error });
    throw error;
  }
}

export async function setThreadConfig(ctx, threadId, config = {}) {
  const { callAPI, logInfo, logWarn } = ctx;
  const id = (threadId || '').toString().trim();
  if (!id) return null;
  const cwd = typeof ctx.getPreferenceScopeCwd === 'function' ? ctx.getPreferenceScopeCwd() : '';
  const model = normalizeProviderConfigValue(config?.model);
  const effort = normalizeProviderConfigValue(config?.effort);
  const start = perfNow();
  logInfo('thread', 'config.set.start', { thread_id: id, cwd, requested_model: model, requested_effort: effort });
  try {
    const result = await callAPI('thread/config/set', {
      threadId: id,
      model,
      effort,
    });
    logInfo('thread', 'config.set.done', {
      thread_id: id,
      cwd,
      requested_model: model,
      requested_effort: effort,
      provider: (result?.provider || '').toString(),
      effective_model: (result?.effective?.model || '').toString(),
      effective_effort: (result?.effective?.effort || '').toString(),
      duration_ms: Math.round(perfNow() - start),
    });
    return result;
  } catch (error) {
    logWarn('thread', 'config.set.failed', {
      thread_id: id,
      cwd,
      requested_model: model,
      requested_effort: effort,
      duration_ms: Math.round(perfNow() - start),
      error,
    });
    throw error;
  }
}

// Per-thread routing metadata captured from thread/start responses.
const _routingByThread = new Map();

export function getThreadRouting(threadId) {
  if (!threadId) return null;
  return _routingByThread.get(threadId) || null;
}

export function clearThreadRouting(threadId) {
  if (threadId) _routingByThread.delete(threadId);
}

function applyTurnStartRouting(threadId, res) {
  if (!threadId || !res || typeof res !== 'object') return false;
  const agentKey = (res.agent_key || res.agentKey || '').toString().trim();
  const agentTitle = (res.agent_title || res.agentTitle || '').toString().trim();
  const promptKey = (res.prompt_key || res.promptKey || '').toString().trim();
  const promptVersionId =
    typeof res.prompt_version_id === 'number' ? res.prompt_version_id
    : typeof res.promptVersionId === 'number' ? res.promptVersionId
    : null;
  if (!agentKey && !agentTitle && !promptKey && promptVersionId == null) return false;
  const prev = _routingByThread.get(threadId) || {};
  _routingByThread.set(threadId, {
    agentKey: agentKey || prev.agentKey || '',
    agentTitle: agentTitle || prev.agentTitle || '',
    promptKey: promptKey || prev.promptKey || '',
    promptVersionId: promptVersionId != null ? promptVersionId : (prev.promptVersionId ?? null),
    overridden: prev.overridden === true,
  });
  return true;
}

const _pendingLaunchByThread = new Set();

export function getThreadPendingLaunch(threadId) {
  if (!threadId) return false;
  return _pendingLaunchByThread.has(threadId);
}

export function setThreadPendingLaunch(threadId, pending) {
  if (!threadId) return;
  if (pending) _pendingLaunchByThread.add(threadId);
  else _pendingLaunchByThread.delete(threadId);
}

function getStartResponseProvider(res) {
  return (res?.provider || res?.modelProvider || res?.model_provider || '').toString().trim();
}

function normalizeSelectedSkillRefs(rawSelectedSkillRefs) {
  if (!Array.isArray(rawSelectedSkillRefs)) return [];
  return rawSelectedSkillRefs
    .map((item) => {
      const ref = {
        key: (item?.key || '').toString().trim(),
        name: (item?.name || '').toString().trim(),
        scope: (item?.scope || '').toString().trim(),
        personalType: (item?.personalType || item?.personal_type || '').toString().trim(),
        path: (item?.path || '').toString().trim(),
      };
      const source = (item?.source || '').toString().trim();
      if (source) ref.source = source;
      return ref;
    })
    .filter((item) => item.key && item.name);
}

async function resolveLaunchProviderPreference(getPref, cwd) {
  return resolveActiveProviderPreference(getPref, cwd, 'codex');
}

export async function startThread(ctx, cwd = '.', options = {}) {
  const { callAPI, logInfo, logWarn } = ctx;
  const start = perfNow();

  const cachedProvider = normalizeProviderConfigValue(getScopedPreferenceCached('settings.provider.active', cwd));

  // Resolve sync overrides first so we can decide which cwd-scoped
  // preference reads are actually needed (no point fetching activePromptKey
  // when the caller already pinned promptKey or chose an agent explicitly).
  const agentKeyOverride = typeof options?.agentKey === 'string' ? options.agentKey.trim() : '';
  const promptKeyOverride = typeof options?.promptKey === 'string' ? options.promptKey.trim() : '';
  const needsActivePromptKey = !promptKeyOverride && !agentKeyOverride;

  // Fetch optional preferences defensively, but keep provider resolution
  // fail-fast: a launch without a trustworthy provider must not silently
  // fall back to another scope.
  const getPref = (req) => callAPI('ui/preferences/get', req).catch(() => undefined);
  const getProviderPref = (req) => callAPI('ui/preferences/get', req);
  const getCodexSandboxPref = (req) => callAPI('ui/preferences/get', req);
  const optionsProviderTrimmed = normalizeProviderConfigValue(options?.modelProvider || options?.model_provider || options?.provider);
  const activePromptKeyPromise = needsActivePromptKey ? getPref({ key: 'settings.activePromptKey', cwd }) : Promise.resolve(undefined);
  const providerPrefPromise = optionsProviderTrimmed
    ? Promise.resolve(optionsProviderTrimmed)
    : cachedProvider ? Promise.resolve(cachedProvider) : resolveLaunchProviderPreference(getProviderPref, cwd);
  const [providerPref, activePromptKey] = await Promise.all([providerPrefPromise, activePromptKeyPromise]);

  const modelProvider = normalizeProviderConfigValue(providerPref);
  if (!modelProvider) {
    throw new Error('startThread: settings.provider.active preference is empty — cannot determine provider. Please select a provider in Settings.');
  }
  const providerScope = providerPreferenceScope(modelProvider);
  const isCodexProvider = providerScope === 'codex';
  const [
    providerModelResolved,
    providerEffortResolved,
    sandboxResolved,
    codexHomeResolved,
    codexInstanceKeyResolved,
    codexModelProviderResolved,
  ] = providerScope ? await Promise.all([
    resolveProviderConfigPreference(getPref, `settings.provider.${providerScope}.model`, cwd),
    resolveProviderConfigPreference(getPref, `settings.provider.${providerScope}.effort`, cwd),
    isCodexProvider ? resolveScopedProviderPreference(getCodexSandboxPref, 'settings.provider.codex.sandbox', cwd) : Promise.resolve({ value: '' }),
    isCodexProvider ? resolveProviderConfigPreference(getPref, 'settings.provider.codex.codexHome', cwd) : Promise.resolve({ value: '' }),
    isCodexProvider ? resolveProviderConfigPreference(getPref, 'settings.provider.codex.codexInstanceKey', cwd) : Promise.resolve({ value: '' }),
    isCodexProvider ? resolveProviderConfigPreference(getPref, 'settings.provider.codex.codexModelProvider', cwd) : Promise.resolve({ value: '' }),
  ]) : [{ value: '' }, { value: '' }, { value: '' }, { value: '' }, { value: '' }, { value: '' }];
  const providerModel = normalizeProviderConfigValue(providerModelResolved.value);
  const providerEffort = normalizeProviderConfigValue(providerEffortResolved.value);
  // p20.3 §4.3：launch payload 可携带 UI 已知的 skill 选择。空数组 / false 不下发，
  // 完全对旧 payload 做 additive 兼容；名称与 send path 对齐（selectedSkills /
  // manualSkillSelection）。backend 的 rpc_types.go 同时兼容 snake_case 别名。
  const payload = { cwd, provider: providerScope, modelProvider };
  if (isCodexProvider) {
    // Codex pool routing is strict by default; always make the identity
    // explicit in thread/start instead of relying on process-level env fallback.
    payload.config = withCodexLspToolDefaults(buildCodexIdentityConfig(codexHomeResolved.value, codexInstanceKeyResolved.value, codexModelProviderResolved.value));
    payload.config.sandbox = buildCodexLaunchSandboxPreference(sandboxResolved.value, cwd);
  }
  // Provider model/effort forwarding: caller override > explicit settings
  // preference > omit. Omitted values are filled by the backend/provider
  // contract; the UI must not mirror packaged model/effort defaults.
  const optionsModelTrimmed = normalizeProviderConfigValue(options?.model);
  const optionsEffortTrimmed = normalizeProviderConfigValue(options?.effort);
  const effectiveModel = optionsModelTrimmed || providerModel || '';
  const effectiveEffort = optionsEffortTrimmed || providerEffort || '';
  if (effectiveModel) payload.model = effectiveModel;
  if (effectiveEffort) payload.effort = effectiveEffort;
  logWarn('thread', 'start.config.trace', {
    cwd,
    model_provider: modelProvider,
    provider_scope: providerScope,
    provider_pref_model: providerModel, provider_pref_effort: providerEffort,
    contract_default_model: '', contract_default_effort: '', model_default_source: 'backend_provider_contract', effort_default_source: 'backend_provider_contract',
    codex_model_provider_pref: normalizeProviderConfigValue(codexModelProviderResolved.value),
    options_model: optionsModelTrimmed,
    options_effort: optionsEffortTrimmed,
    payload_model_provider: (payload.modelProvider || '').toString(),
    payload_model: (payload.model || '').toString(), payload_effort: (payload.effort || '').toString(),
    payload_config_model_provider: (payload.config?.modelProvider || payload.config?.model_provider || '').toString(),
    payload_config_codex_model_provider: (payload.config?.codexModelProvider || '').toString(),
    is_codex_provider: isCodexProvider,
    note: 'diagnostic: provider prefs are observed here; payload forwarding is logged separately by backend',
  });
  const selectedSkillRefs = normalizeSelectedSkillRefs(options?.selectedSkillRefs);
  const rawSelected = Array.isArray(options?.selectedSkills) ? options.selectedSkills : [];
  const selectedSkills = dropSkillNamesCoveredByRefs(rawSelected, selectedSkillRefs);
  const manualSkillSelection = options?.manualSkillSelection === true;
  if (selectedSkills.length > 0) payload.selectedSkills = selectedSkills;
  if (selectedSkillRefs.length > 0) payload.selectedSkillRefs = selectedSkillRefs;
  if (manualSkillSelection || selectedSkills.length > 0 || selectedSkillRefs.length > 0) payload.manualSkillSelection = manualSkillSelection;
  // Explicit agent_key override. Empty / absent means "let the backend
  // router decide". The router reads prompt_templates and classifies based
  // on user_input; see internal/module/thread/router_resolve.go.
  if (agentKeyOverride) payload.agent_key = agentKeyOverride;
  // Prompt_key resolution: explicit caller override > persisted
  // SystemPromptPage "set as launch" pref > fall through to backend default.
  // Backend treats prompt_key as a strict pin (router_resolve.go:
  // pickRoutedTemplate); when it's set the router skips classification.
  if (promptKeyOverride) {
    payload.prompt_key = promptKeyOverride;
  } else if (needsActivePromptKey && typeof activePromptKey === 'string' && activePromptKey.trim()) {
    payload.prompt_key = activePromptKey.trim();
  }
  // First user message (if any) forwarded so the backend router has input
  // to classify. Without this the router always sees empty input at
  // thread/start and falls back to no injection — see
  // internal/module/thread/router_resolve.go resolveRoutedPrompt.
  const launchPrompt = typeof options?.prompt === 'string' ? options.prompt.trim() : '';
  if (launchPrompt) payload.prompt = launchPrompt;
  const launchIntentId = typeof options?.launchIntentId === 'string' ? options.launchIntentId.trim() : '';
  if (launchIntentId) payload.launchIntentId = launchIntentId;
  const startName = typeof options?.name === 'string' ? options.name.trim() : '';
  if (startName) payload.name = startName;
  const baseInstructions = typeof options?.baseInstructions === 'string' ? options.baseInstructions.trim() : '';
  if (baseInstructions) payload.baseInstructions = baseInstructions;
  // C1: opt-in flag for callers that explicitly request a pending_launch row.
  // Backend creates an agent_threads row with pending_launch=true and
  // skips the Claude CLI fork; the spawn happens lazily on the first
  // turn/start via SpawnIfNeeded. See internal/module/thread/spawn.go.
  if (options?.deferSpawn === true) payload.defer_spawn = true;
  const builtinToolPolicy = await resolveBuiltinToolLaunchPolicy(callAPI, cwd);
  if (providerScope === 'claude' && Array.isArray(builtinToolPolicy?.claudeAllowedTools)) {
    payload.config = { ...(payload.config || {}), claude_builtin_tools: builtinToolPolicy.claudeAllowedTools };
  }
  if (isCodexProvider && (builtinToolPolicy?.codexDisabledNativeTools || []).length > 0) {
    payload.config = { ...(payload.config || {}), codexDisabledNativeTools: builtinToolPolicy.codexDisabledNativeTools };
  }

  if (options?.config && typeof options.config === 'object' && !Array.isArray(options.config)) {
    payload.config = { ...(payload.config || {}), ...options.config };
  }
  const finalConfig = (payload.config && typeof payload.config === 'object') ? payload.config : {};
  if (isCodexProvider || finalConfig.modelProvider || finalConfig.model_provider || finalConfig.codexModelProvider) {
    logWarn('thread', 'start.payload.identity_trace', {
      cwd,
      provider_scope: providerScope,
      payload_model_provider: (payload.modelProvider || '').toString(),
      payload_model: (payload.model || '').toString(),
      payload_effort: (payload.effort || '').toString(),
      config_provider: (finalConfig.provider || '').toString(),
      config_model_provider: (finalConfig.modelProvider || finalConfig.model_provider || '').toString(),
      config_codex_model_provider: (finalConfig.codexModelProvider || '').toString(),
      has_config: Object.keys(finalConfig).length > 0, is_codex_provider: isCodexProvider,
    });
  }
  const res = (ctx._lastStartResponse = await callAPI('thread/start', payload));
  const id = res?.thread?.id;
  if (!id) return '';
  // Capture routing metadata the backend surfaced (see
  // internal/module/thread/rpc.go newStartHandler response map).
  const agentKey = (res?.agent_key || res?.agentKey || '').toString().trim();
  const agentTitle = (res?.agent_title || res?.agentTitle || '').toString().trim();
  const promptKey = (res?.prompt_key || res?.promptKey || '').toString().trim();
  let promptVersionId = null;
  if (typeof res?.prompt_version_id === 'number') {
    promptVersionId = res.prompt_version_id;
  } else if (typeof res?.promptVersionId === 'number') {
    promptVersionId = res.promptVersionId;
  }
  const responseProvider = getStartResponseProvider(res);
  const launchCwd = (cwd || '').toString().trim();
  if (responseProvider || (launchCwd && launchCwd !== '.')) {
    const prevRuntime = (ctx.state.agentRuntimeById?.[id] && typeof ctx.state.agentRuntimeById[id] === 'object') ? ctx.state.agentRuntimeById[id] : {};
    const updates = { ...prevRuntime };
    if (launchCwd && launchCwd !== '.') updates.cwd = launchCwd;
    if (responseProvider) updates.provider = responseProvider;
    ctx.state.agentRuntimeById = {
      ...ctx.state.agentRuntimeById,
      [id]: updates,
    };
  }
  if (agentKey || agentTitle || promptKey || promptVersionId != null) {
    _routingByThread.set(id, {
      agentKey,
      agentTitle,
      promptKey,
      promptVersionId,
      overridden: Boolean(agentKeyOverride),
    });
  }
  // C1 pending-launch: backend returns pending_launch=true when it wrote
  // the row but did not fork the CLI. UI renders a "待启动" badge until the
  // first turn/start succeeds (clears the flag) or stopThread removes the
  // thread entirely.
  const pendingLaunch = Boolean(res?.pending_launch ?? res?.pendingLaunch);
  setThreadPendingLaunch(id, pendingLaunch);
  if (!ctx.state.threads.some((t) => t.id === id)) ctx.state.threads = [...ctx.state.threads, { id, name: startName || id, state: 'idle' }];
  const optimisticUserMessage = options?.optimisticUserMessage;
  if (optimisticUserMessage && typeof optimisticUserMessage === 'object') {
    const optimisticText = (optimisticUserMessage.text || '').toString().trim();
    const optimisticAttachments = Array.isArray(optimisticUserMessage.attachments) ? optimisticUserMessage.attachments : [];
    if (optimisticText || optimisticAttachments.length > 0) upsertOptimisticUserTimelineItem(ctx, id, optimisticText, optimisticAttachments);
  }
  _optimisticThreadIds.set(id, Date.now() + OPTIMISTIC_LEAK_GUARD_MS);
  const focusMode = options?.focusMode === 'cmd' ? 'cmd' : 'chat';
  const saveActive = () => { if (!options?.skipSaveActive) { if (focusMode === 'cmd') saveActiveCmdThread(ctx, id); else saveActiveThread(ctx, id); } };
  if (options?.skipInitialRuntimeSync === true) { saveActive(); ctx.syncRuntimeState().catch((error) => logWarn('thread', 'start.initial_sync.background_failed', { thread_id: id, error })); } else { await ctx.syncRuntimeState(); saveActive(); }

  logInfo('thread', 'start.done', {
    thread_id: id,
    focus_mode: focusMode,
    cwd,
    duration_ms: Math.round(perfNow() - start),
    agent_key: agentKey || undefined,
    prompt_key: promptKey || undefined,
    prompt_version_id: promptVersionId ?? undefined,
    agent_key_overridden: agentKeyOverride ? true : undefined,
  });
  return id;
}

export async function stopThread(ctx, threadId, options = {}) {
  const { callAPI, logInfo, logWarn } = ctx;
  if (!threadId) return { confirmed: false, mode: 'no_thread', interruptSent: false, settled: false };
  const start = perfNow();
  const source = (options?.source || '').toString().trim();
  const interruptPayload = source ? { threadId, source } : { threadId };
  _optimisticThreadIds.delete(threadId);
  let interruptSent = false;
  let confirmed = false;
  let mode = 'failed';
  let settled = false;
  logInfo('thread', 'stop.request', { thread_id: threadId, source });
  try {
    const interruptResult = await callAPI('turn/interrupt', interruptPayload);
    interruptSent = Boolean(interruptResult?.interruptSent);
    confirmed = Boolean(interruptResult?.confirmed);
    mode = (interruptResult?.mode || '').toString().trim() || (confirmed ? 'interrupt_confirmed' : 'interrupt_not_confirmed');
    settled = confirmed || mode === 'interrupt_terminal_completed' || mode === 'interrupt_terminal_failed' || mode === 'no_active_turn';
    logInfo('thread', 'stop.interrupt.sent', {
      thread_id: threadId,
      source,
      confirmed,
      mode,
      settled,
      interrupt_sent: interruptSent,
      state_before: (interruptResult?.stateBefore || '').toString(),
      state_after: (interruptResult?.stateAfter || '').toString(),
      waited_ms: Number(interruptResult?.waitedMs || 0),
      duration_ms: Math.round(perfNow() - start),
    });
  } catch (interruptError) {
    logWarn('thread', 'stop.interrupt.failed', { thread_id: threadId, source, error: interruptError, duration_ms: Math.round(perfNow() - start) });
  }
  try {
    await ctx.syncRuntimeState();
  } catch (syncError) {
    logWarn('thread', 'stop.sync.failed', { thread_id: threadId, source, error: syncError, duration_ms: Math.round(perfNow() - start) });
  }
  logInfo('thread', 'stop.done', { thread_id: threadId, source, confirmed, mode, settled, interrupt_sent: interruptSent, duration_ms: Math.round(perfNow() - start) });
  setThreadPendingLaunch(threadId, false);
  clearThreadRouting(threadId);
  return { confirmed, mode, interruptSent, settled };
}


export async function recoverThread(ctx, threadId) {
  const { callAPI, logInfo, logWarn } = ctx;
  const id = (threadId || '').toString().trim();
  if (!id) return { recovered: false, mode: 'no_thread' };
  const start = perfNow();
  logInfo('thread', 'recover.start', { thread_id: id });
  try {
    const result = await callAPI('thread/recover', { threadId: id });
    clearThreadSendNoticesInState(ctx.state, id);
    await ctx.syncRuntimeState();
    logInfo('thread', 'recover.done', { thread_id: id, recovered: Boolean(result?.recovered), mode: (result?.mode || '').toString(), duration_ms: Math.round(perfNow() - start) });
    return result;
  } catch (error) {
    logWarn('thread', 'recover.failed', { thread_id: id, error, duration_ms: Math.round(perfNow() - start) });
    throw error;
  }
}

export async function sendMessage(ctx, threadId, prompt, attachments = [], options = {}) {
  const { callAPI, logInfo, logWarn } = ctx;
  const logDebug = typeof ctx.logDebug === 'function' ? ctx.logDebug : () => {};
  const text = (prompt || '').trim();
  const hasAttachments = attachments.length > 0;
  if (!threadId || (!text && !hasAttachments)) return;
  assertThreadCanSendInState(ctx.state, threadId);
  const input = [];
  let localImageCount = 0;
  let remoteImageCount = 0;
  let fileCount = 0;
  let droppedAttachmentCount = 0;
  if (text) input.push({ type: 'text', text });
  for (const item of attachments) {
    const path = (item?.path || '').trim();
    const previewUrl = (item?.previewUrl || '').trim();
    const previewLower = previewUrl.toLowerCase();
    if (item?.kind === 'image') {
      if (path) {
        const payload = { type: 'localImage', path };
        if (previewLower.startsWith('data:image/')) payload.url = previewUrl;
        input.push(payload);
        localImageCount += 1;
        continue;
      }
      if (previewUrl) {
        input.push({ type: 'image', url: previewUrl });
        remoteImageCount += 1;
        continue;
      }
      droppedAttachmentCount += 1;
      continue;
    }
    if (!path) {
      droppedAttachmentCount += 1;
      continue;
    }
    input.push({ type: 'mention', name: path.split(/[\/]/).pop() || path, path });
    fileCount += 1;
  }
  if (input.length === 0) {
    logWarn('thread', 'send.skipped.emptyInput', { thread_id: threadId, attachments: attachments.length, dropped_attachments: droppedAttachmentCount });
    return;
  }
  const start = perfNow();
  const selectedSkillRefs = normalizeSelectedSkillRefs(options?.selectedSkillRefs);
  const selectedSkills = dropSkillNamesCoveredByRefs(options?.selectedSkills, selectedSkillRefs);
  const manualSkillSelection = Boolean(options?.manualSkillSelection);
  // fork 继承对话 kickoff：把这条 user prompt 的 text 记到 kickoffByThread，timeline
  // selector 看到匹配 text 的 user 消息会过滤掉，让 agent 视觉上主动开场。后端推回的
  // 真实 timeline 不影响——同一文本永远命中过滤。
  if (options?.kickoff && text) {
    ctx.state.kickoffByThread = { ...ctx.state.kickoffByThread, [threadId]: text };
    // 诊断日志：让 [AO] 能验证 kickoffByThread 真被写入 + text 是 trim 后准确文本
    logInfo('thread', 'send.kickoff_marked', {
      thread_id: threadId,
      text_len: text.length,
      text_preview: text.slice(0, 60),
    });
  }
  const requestPayload = { threadId, input };
  const cwdValue = (options?.cwd || '').toString().trim();
  if (cwdValue) requestPayload.cwd = cwdValue;
  if (selectedSkills.length > 0) requestPayload.selectedSkills = selectedSkills;
  if (selectedSkillRefs.length > 0) requestPayload.selectedSkillRefs = selectedSkillRefs;
  if (manualSkillSelection || selectedSkills.length > 0 || selectedSkillRefs.length > 0) requestPayload.manualSkillSelection = manualSkillSelection;
  logInfo('thread', 'send.start', { thread_id: threadId, text_len: text.length, attachments: attachments.length, local_images: localImageCount, inline_images: remoteImageCount, files: fileCount, dropped_attachments: droppedAttachmentCount, selected_skills: selectedSkills.length, manual_skill_selection: manualSkillSelection });
  let turnStartAccepted = false;
  try {
    const beforeLen = Array.isArray(ctx.state.timelinesByThread?.[threadId]) ? ctx.state.timelinesByThread[threadId].length : 0;
    if (typeof ctx.markHistoryLoaded === 'function') ctx.markHistoryLoaded(threadId);
    // Optimistic UI: insert the user's message into the local timeline BEFORE
    // awaiting turn/start. First-turn of a pending_launch thread spends
    // noticeable time inside turn/start (SpawnIfNeeded routes prompts and
    // forks the provider CLI); rendering only after the RPC returns means the
    // user stares at an empty composer for that entire window and thinks the
    // app hung.
    // The message stays on screen even if turn/start fails terminally — that
    // matches user intent ("I can see what I just typed"), and any error is
    // surfaced via the standard catch path below.
    const userText = input.filter((i) => i?.type === 'text').map((i) => i.text).join('\n').trim();
    if (userText || attachments.length > 0) {
      touchThreadUpdatedAt(ctx, threadId, new Date().toISOString());
      upsertOptimisticUserTimelineItem(ctx, threadId, userText, attachments);
    }
    const turnStartRes = await callTurnStartWithSendBlock(ctx, threadId, requestPayload, isSessionNotAvailableError, recoverThread);
    turnStartAccepted = true;
    // pending-launch threads get their routing decision lazily (SpawnIfNeeded
    // runs inside turn/start, not thread/start), so the backend surfaces it
    // here on the first successful turn. Merge-only: empty fields are ignored
    // so eager-path threads keep the routing that thread/start already set.
    await maybeHandleStalePromptKey(ctx, turnStartRes, cwdValue);
    const routingUpdated = applyTurnStartRouting(threadId, turnStartRes);
    // C1: first successful turn/start means the backend has forked the CLI
    // (SpawnIfNeeded ran for pending threads, eager threads were already
    // running). Clear the pending badge so the sidebar card flips to normal.
    setThreadPendingLaunch(threadId, false);
    // _routingByThread is a module-scoped Map — Vue has no dependency on it,
    // so computed props that already rendered the thread card won't rerun on
    // their own. Poke the whitelisted runtime state so the sidebar recomputes
    // and picks up the fresh routing from routingOf(threadId).
    if (routingUpdated) {
      await ctx.syncRuntimeState();
    }
    // Note: syncThreadState and loadMessages are NOT called here.
    // They would overwrite the optimistic user message with backend state
    // that doesn't contain user text yet (JSONL not written until turn completes).
    // Timeline refresh is handled by event-driven hydration:
    //   turn/completed → MessagesPage → historyHydrationSignal → loadMessages
    const afterLen = Array.isArray(ctx.state.timelinesByThread?.[threadId]) ? ctx.state.timelinesByThread[threadId].length : 0;
    logDebug('ui', 'chat.send.timeline_diff', { thread_id: threadId, beforeLen, afterLen });
    logInfo('thread', 'send.done', { thread_id: threadId, duration_ms: Math.round(perfNow() - start) });
  } catch (error) {
    if (turnStartAccepted) {
      setThreadSendHoldNoticeFromError(ctx.state, threadId, error);
    } else if (!getThreadSendBlockedNoticeFromState(ctx.state, threadId)) {
      setThreadSendBlockedNoticeFromError(ctx.state, threadId, error);
    }
    logWarn('thread', 'send.failed', { thread_id: threadId, error, duration_ms: Math.round(perfNow() - start) });
    throw error;
  }
}

export async function compactThread(ctx, threadId) {
  const { callAPI, logInfo, logWarn } = ctx;
  const id = normalizeThreadID(threadId);
  if (!id || ctx.compactPendingByThread[id]) return;
  const start = perfNow();
  const signalPromise = ctx.waitForCompactCompletion(id, ctx.COMPACT_COMPLETION_TIMEOUT_MS);
  let baselineSignature = tokenUsageSignature(ctx.state, id);
  let baselineTimelineSignature = dialogTimelineSignature(ctx.state, id);
  let compactCommandSent = false;
  let compactSignalReceived = false;
  let interruptAttempted = false;
  let interruptConfirmed = false;
  let interruptSettled = false;
  let interruptMode = '';
  ctx.compactPendingByThread[id] = true;
  ctx.setCompactResult(id, 'pending', '上下文压缩中…');
  logInfo('thread', 'compact.start', { thread_id: id, token_usage_sig_before: baselineSignature });
  try {
    await ctx.syncRuntimeState();
    baselineSignature = tokenUsageSignature(ctx.state, id) || baselineSignature;
    baselineTimelineSignature = dialogTimelineSignature(ctx.state, id) || baselineTimelineSignature;
    if (ctx.getThreadInterruptible(id)) {
      interruptAttempted = true;
      logInfo('thread', 'compact.interrupt.before', { thread_id: id });
      const interruptResult = await stopThread(ctx, id);
      interruptMode = (interruptResult?.mode || '').toString().trim();
      interruptConfirmed = Boolean(interruptResult?.confirmed);
      interruptSettled = Boolean(interruptResult?.settled || interruptConfirmed || interruptMode === 'no_active_turn');
      logInfo('thread', 'compact.interrupt.result', { thread_id: id, interrupt_confirmed: interruptConfirmed, interrupt_settled: interruptSettled, interrupt_mode: interruptMode });
      if (!interruptSettled) throw new Error('compact_interrupt_not_settled:' + (interruptMode || 'unknown'));
      await waitMs(120);
    }
    await callAPI('thread/compact/start', { threadId: id });
    logInfo('thread', 'compact.command.sent', { thread_id: id, wait_timeout_ms: ctx.COMPACT_COMPLETION_TIMEOUT_MS });
    compactCommandSent = true;
    await signalPromise;
    compactSignalReceived = true;
    await ctx.syncRuntimeState();
    const compactResponse = await waitForCompactResponse(ctx, id, baselineTimelineSignature);
    const afterSignature = tokenUsageSignature(ctx.state, id);
    ctx.setCompactResult(id, 'success', '上下文压缩完成');
    logInfo('thread', 'compact.done', { thread_id: id, compact_signal_received: compactSignalReceived, compact_response_changed: compactResponse.changed, compact_response_attempts: compactResponse.attempts, token_usage_sig_after: afterSignature, token_usage_changed: Boolean(afterSignature && afterSignature !== baselineSignature), interrupt_attempted: interruptAttempted, interrupt_confirmed: interruptConfirmed, interrupt_settled: interruptSettled, interrupt_mode: interruptMode, duration_ms: Math.round(perfNow() - start) });
  } catch (error) {
    const isTimeout = error && typeof error === 'object' && error.code === 'compact_timeout';
    if (!compactCommandSent) {
      ctx.cancelCompactWaiter(id, 'compact_start_failed');
      await signalPromise.catch(() => {});
    }
    const failure = compactFailureResult(isTimeout);
    ctx.setCompactResult(id, 'failed', failure.message, { code: failure.code });
    logWarn('thread', 'compact.failed', { thread_id: id, compact_command_sent: compactCommandSent, compact_signal_received: compactSignalReceived, interrupt_attempted: interruptAttempted, interrupt_confirmed: interruptConfirmed, interrupt_settled: interruptSettled, interrupt_mode: interruptMode, error, duration_ms: Math.round(perfNow() - start) });
    throw error;
  } finally {
    ctx.cancelCompactWaiter(id, 'compact_finished');
    delete ctx.compactPendingByThread[id];
  }
}

export async function forceCompleteThread(ctx, threadId) {
  const { callAPI, logInfo, logWarn } = ctx;
  const id = (threadId || '').toString().trim();
  if (!id) return;
  const start = perfNow();
  logInfo('thread', 'forceComplete.start', { thread_id: id });
  try {
    const result = await callAPI('turn/forceComplete', { threadId: id });
    await ctx.syncRuntimeState();
    logInfo('thread', 'forceComplete.done', { thread_id: id, confirmed: Boolean(result?.confirmed), duration_ms: Math.round(perfNow() - start) });
    return result;
  } catch (error) {
    logWarn('thread', 'forceComplete.failed', { thread_id: id, error, duration_ms: Math.round(perfNow() - start) });
    throw error;
  }
}

export function getThreadPinnedAt(ctx, threadId) {
  const id = (threadId || '').toString().trim();
  const value = Number(ctx.state.pinnedThreadAtById?.[id]);
  return Number.isFinite(value) && value > 0 ? value : 0;
}

export function getThreadArchivedAt(ctx, threadId) {
  const id = (threadId || '').toString().trim();
  const value = Number(ctx.state.archivedThreadAtById?.[id]);
  return Number.isFinite(value) && value > 0 ? value : 0;
}

export async function setThreadPinned(ctx, threadId, pinned) {
  const id = (threadId || '').toString().trim();
  if (!id) return;
  ensureThreadOrderIndex(id);
  const next = { ...(ctx.state.pinnedThreadAtById || {}) };
  if (pinned) next[id] = Date.now(); else delete next[id];
  ctx.state.pinnedThreadAtById = next;
  const { _optimisticPreferenceMapTaints, OPTIMISTIC_LEAK_GUARD_MS } = await import('./thread-optimistic.js');
  _optimisticPreferenceMapTaints.set('pinnedThreadAtById', Date.now() + OPTIMISTIC_LEAK_GUARD_MS);
  ctx.persistPreferenceAndSync(PREF_PINNED_THREADS_CHAT, next, { thread_id: id, pinned: Boolean(pinned) });
}

function threadArchiveWarningText(response, archived) {
  const warnings = Array.isArray(response?.warnings) ? response.warnings.filter((item) => typeof item === 'string' && item.trim()) : [];
  if (warnings.length > 0) return warnings.join('\n');
  if (response?.partial) {
    return `线程已${archived ? '归档' : '取消归档'}，但部分${archived ? '归档文件处理' : '恢复文件处理'}失败；请检查警告信息。`;
  }
  return '';
}

export async function setThreadArchived(ctx, threadId, archived) {
  const { callAPI, logInfo, logWarn } = ctx;
  const id = (threadId || '').toString().trim();
  if (!id) return;
  const previous = { ...(ctx.state.archivedThreadAtById || {}) };
  const next = { ...previous };
  if (archived) next[id] = Date.now(); else delete next[id];
  ctx.state.archivedThreadAtById = next;
  const { _optimisticPreferenceMapTaints, OPTIMISTIC_LEAK_GUARD_MS } = await import('./thread-optimistic.js');
  _optimisticPreferenceMapTaints.set('archivedThreadAtById', Date.now() + OPTIMISTIC_LEAK_GUARD_MS);
  logInfo('thread', archived ? 'archive.start' : 'unarchive.start', { thread_id: id, cwd: ctx.getPreferenceScopeCwd() });
  try {
    const response = await callAPI(archived ? 'thread/archive' : 'thread/unarchive', { threadId: id });
    ctx.persistPreferenceAndSync(PREF_ARCHIVED_THREADS_CHAT, next, { thread_id: id, archived: Boolean(archived) }, { syncAfterPersist: false });
    await ctx.refreshSidebarState();
    if (archived) {
      if ((ctx.state.activeThreadId || '') === id) saveActiveThread(ctx, '');
      if ((ctx.state.activeCmdThreadId || '') === id) saveActiveCmdThread(ctx, '');
      setThreadPendingLaunch(id, false);
      clearThreadRouting(id);
    }
    const warningText = threadArchiveWarningText(response, archived);
    if (warningText) {
      logWarn('thread', archived ? 'archive.partial_warning' : 'unarchive.partial_warning', {
        thread_id: id,
        warning: warningText,
        partial: Boolean(response?.partial),
        warning_count: Array.isArray(response?.warnings) ? response.warnings.length : 0,
        skipped_count: Number(response?.skippedCount || 0),
      });
      if (typeof window !== 'undefined' && typeof window.alert === 'function') window.alert(warningText);
    }
    if (!archived && response?.archiveModified) {
      const modifiedWarningText = '该线程在归档后有新改动，已尽力恢复；请检查恢复文件。';
      logWarn('thread', 'unarchive.modified_warning', { thread_id: id, warning: modifiedWarningText, modified_files: Array.isArray(response.modifiedFiles) ? response.modifiedFiles.length : 0 });
      if (typeof window !== 'undefined' && typeof window.alert === 'function') window.alert(modifiedWarningText);
    }
    return response;
  } catch (error) {
    ctx.state.archivedThreadAtById = previous;
    logWarn('thread', archived ? 'archive.failed' : 'unarchive.failed', { thread_id: id, error });
    throw error;
  }
}

export function promptRenameThread(ctx, threadId) {
  const { logWarn } = ctx;
  const id = (threadId || '').toString();
  if (!id) return;
  const target = ctx.state.threads.find((item) => item.id === id);
  const current = ctx.displayName(target || { id });
  const next = window.prompt('输入新的 Agent 名称', current);
  if (!next || !next.trim()) return;
  renameThread(ctx, id, next.trim()).catch((error) => {
    logWarn('thread', 'rename.failed', { thread_id: id, error });
  });
}
