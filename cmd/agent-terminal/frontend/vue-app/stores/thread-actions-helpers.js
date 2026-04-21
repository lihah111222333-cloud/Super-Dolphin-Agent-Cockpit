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
    ? existing.findIndex((item) => item?.kind === 'user' && (item?.text || '').trim() === userText)
    : -1;

  if (matchingIndex >= 0) {
    const current = existing[matchingIndex];
    const currentAttachments = Array.isArray(current?.attachments) ? current.attachments : [];
    if (sameOptimisticAttachmentList(currentAttachments, normalizedAttachments)) {
      ctx.logWarn('ui', 'chat.send.optimistic_skip', {
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
    ctx.logWarn('ui', 'chat.send.optimistic_attachments_merged', {
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
  ctx.logWarn('ui', 'chat.send.optimistic_insert', {
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

export function tokenUsageSignature(state, threadId) {
  const usage = state.tokenUsageByThread?.[threadId];
  if (!usage || typeof usage !== 'object') return '';
  const used = Number(usage.usedTokens);
  const limit = Number(usage.contextWindowTokens);
  const percent = Number(usage.usedPercent);
  return [Number.isFinite(used) ? Math.round(used) : '', Number.isFinite(limit) ? Math.round(limit) : '', Number.isFinite(percent) ? percent.toFixed(3) : ''].join('|');
}
function dialogTimelineSignature(state, threadId) {
  const items = Array.isArray(state?.timelinesByThread?.[threadId]) ? state.timelinesByThread[threadId] : [];
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const item = items[index];
    const kind = (item?.kind || '').toString().trim();
    if (kind !== 'assistant' && kind !== 'user') continue;
    return [items.length, index, kind, (item?.id || '').toString().trim(), (item?.ts || '').toString().trim(), (item?.text || '').toString().trim().slice(0, 160)].join('|');
  }
  return `${items.length}|`;
}

async function waitForCompactResponse(ctx, threadId, baselineTimelineSignature) {
  const id = normalizeThreadID(threadId);
  if (!id || typeof ctx.loadMessages !== 'function') return { attempts: 0, changed: false, signature: dialogTimelineSignature(ctx.state, id) };
  let signature = dialogTimelineSignature(ctx.state, id);
  let attempts = 0;
  while (!(signature && signature !== baselineTimelineSignature) && attempts < 3) {
    attempts += 1;
    await ctx.loadMessages(id, 300, { syncRuntime: false });
    signature = dialogTimelineSignature(ctx.state, id);
    if (!(signature && signature !== baselineTimelineSignature) && attempts < 3) await waitMs(120);
  }
  return { attempts, changed: Boolean(signature && signature !== baselineTimelineSignature), signature };
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
  const id = (threadId || '').toString();
  const nextName = (name || '').toString().trim();
  logWarn('ui', 'renameThread.triggered', { threadId: id, name: nextName });
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
    logWarn('ui', 'renameThread.api.success', { res });
    logWarn('ui', 'renameThread.sync.complete');
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
  const model = (config?.model || '').toString();
  const effort = (config?.effort || '').toString();
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

async function resolveDisallowedBuiltinTools(ctx, cwd) {
  try {
    const res = await ctx.callAPI('config/builtinTools/read', ctx.withPreferenceScope({ cwd }));
    if (!res || !Array.isArray(res.tools)) return null;
    return res.tools
      .filter((tool) => tool && tool.enabled === false)
      .map((tool) => (typeof tool.id === 'string' ? tool.id.trim() : ''))
      .filter((id) => id !== '');
  } catch {
    return null;
  }
}

// Per-thread routing metadata captured from thread/start responses.
// Consumers (sidebar badge, live preview, diagnostics) read via
// getThreadRouting(threadId). Kept as a module-scoped Map rather than in
// the whitelisted runtime store because the value is pure UI observation
// and adding it to the store would force THREAD_STORE_RUNTIME_STATE_KEYS
// + syncRuntimeState to grow unnecessarily.
const _routingByThread = new Map();

export function getThreadRouting(threadId) {
  if (!threadId) return null;
  return _routingByThread.get(threadId) || null;
}

export function clearThreadRouting(threadId) {
  if (threadId) _routingByThread.delete(threadId);
}

// Per-thread pending_launch flag. Set from thread/start response when the
// backend took the C1 pending_launch path (no CLI fork yet); cleared once the
// first turn/start succeeds (CLI has been forked) or when the thread is
// stopped/deleted. Kept outside the runtime store because the state is purely
// a UI hint and flipping it does not require the store whitelist to grow.
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

export async function startThread(ctx, cwd = '.', options = {}) {
  const { callAPI, logInfo } = ctx;
  const start = perfNow();
  let modelProvider = '';
  try {
    const pref = await callAPI('ui/preferences/get', ctx.withPreferenceScope({ key: 'settings.provider.active' }));
    if (typeof pref === 'string' && pref.trim()) modelProvider = pref.trim();
  } catch {}
  // p20.3 §4.3：launch payload 可携带 UI 已知的 skill 选择。空数组 / false 不下发，
  // 完全对旧 payload 做 additive 兼容；名称与 send path 对齐（selectedSkills /
  // manualSkillSelection）。backend 的 rpc_types.go 同时兼容 snake_case 别名。
  const payload = { cwd, modelProvider };
  const rawSelected = Array.isArray(options?.selectedSkills) ? options.selectedSkills : [];
  const selectedSkills = rawSelected
    .map((name) => (typeof name === 'string' ? name.trim() : ''))
    .filter((name) => name !== '');
  const manualSkillSelection = options?.manualSkillSelection === true;
  if (selectedSkills.length > 0) payload.selectedSkills = selectedSkills;
  if (manualSkillSelection || selectedSkills.length > 0) payload.manualSkillSelection = manualSkillSelection;
  // Explicit agent_key override. Empty / absent means "let the backend
  // router decide". The router reads prompt_templates and classifies based
  // on user_input; see internal/module/thread/router_resolve.go.
  const agentKeyOverride = typeof options?.agentKey === 'string' ? options.agentKey.trim() : '';
  if (agentKeyOverride) payload.agent_key = agentKeyOverride;
  // First user message (if any) forwarded so the backend router has input
  // to classify. Without this the router always sees empty input at
  // thread/start and falls back to no injection — see
  // internal/module/thread/router_resolve.go resolveRoutedPrompt.
  const launchPrompt = typeof options?.prompt === 'string' ? options.prompt.trim() : '';
  if (launchPrompt) payload.prompt = launchPrompt;
  // C1: opt-in flag forwarded from launchOne when the composer is empty.
  // Backend creates an agent_threads row with pending_launch=true and
  // skips the Claude CLI fork; the spawn happens lazily on the first
  // turn/start via SpawnIfNeeded. See internal/module/thread/spawn.go.
  if (options?.deferSpawn === true) payload.defer_spawn = true;
  const disallowedTools = await resolveDisallowedBuiltinTools(ctx, cwd);
  if (Array.isArray(disallowedTools)) {
    payload.config = { ...(payload.config || {}), disallowed_tools: disallowedTools };
  }
  const res = await callAPI('thread/start', payload);
  const id = res?.thread?.id;
  if (!id) return '';
  // Capture routing metadata the backend surfaced (see
  // internal/module/thread/rpc.go newStartHandler response map).
  const agentKey = (res?.agent_key || res?.agentKey || '').toString().trim();
  const promptVersionId =
    typeof res?.prompt_version_id === 'number' ? res.prompt_version_id
    : typeof res?.promptVersionId === 'number' ? res.promptVersionId
    : null;
  if (agentKey || promptVersionId != null) {
    _routingByThread.set(id, {
      agentKey,
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
  if (!ctx.state.threads.some((t) => t.id === id)) ctx.state.threads = [...ctx.state.threads, { id, name: id, state: 'idle' }];
  _optimisticThreadIds.set(id, Date.now() + OPTIMISTIC_LEAK_GUARD_MS);
  await ctx.syncRuntimeState();
  const focusMode = options?.focusMode === 'cmd' ? 'cmd' : 'chat';
  if (focusMode === 'cmd') saveActiveCmdThread(ctx, id); else saveActiveThread(ctx, id);

  logInfo('thread', 'start.done', {
    thread_id: id,
    focus_mode: focusMode,
    cwd,
    duration_ms: Math.round(perfNow() - start),
    agent_key: agentKey || undefined,
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
  const text = (prompt || '').trim();
  const hasAttachments = attachments.length > 0;
  if (!threadId || (!text && !hasAttachments)) return;
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
  const selectedSkills = Array.isArray(options?.selectedSkills) ? options.selectedSkills.map((item) => (item || '').toString().trim()).filter(Boolean) : [];
  const manualSkillSelection = Boolean(options?.manualSkillSelection);
  const requestPayload = { threadId, input };
  const cwdValue = (options?.cwd || '').toString().trim();
  if (cwdValue) requestPayload.cwd = cwdValue;
  if (selectedSkills.length > 0) requestPayload.selectedSkills = selectedSkills;
  if (manualSkillSelection || selectedSkills.length > 0) requestPayload.manualSkillSelection = manualSkillSelection;
  logInfo('thread', 'send.start', { thread_id: threadId, text_len: text.length, attachments: attachments.length, local_images: localImageCount, inline_images: remoteImageCount, files: fileCount, dropped_attachments: droppedAttachmentCount, selected_skills: selectedSkills.length, manual_skill_selection: manualSkillSelection });
  try {
    const beforeLen = Array.isArray(ctx.state.timelinesByThread?.[threadId]) ? ctx.state.timelinesByThread[threadId].length : 0;
    if (typeof ctx.threadHistoryLoadedAtByThread?.set === 'function') {
      ctx.threadHistoryLoadedAtByThread.set(threadId, Date.now());
    }
    try {
      await callAPI('turn/start', requestPayload);
    } catch (turnError) {
      if (isSessionNotAvailableError(turnError)) {
        logWarn('thread', 'send.auto_recover', { thread_id: threadId, error: turnError });
        await recoverThread(ctx, threadId);
        await callAPI('turn/start', requestPayload);
      } else {
        throw turnError;
      }
    }
    // C1: first successful turn/start means the backend has forked the CLI
    // (SpawnIfNeeded ran for pending threads, eager threads were already
    // running). Clear the pending badge so the sidebar card flips to normal.
    setThreadPendingLaunch(threadId, false);
    // Optimistic UI: insert user message into local timeline immediately so
    // it renders before the backend writes the JSONL / completes the turn.
    const userText = input.filter((i) => i?.type === 'text').map((i) => i.text).join('\n').trim();
    if (userText || attachments.length > 0) upsertOptimisticUserTimelineItem(ctx, threadId, userText, attachments);
    // Note: syncThreadState and loadMessages are NOT called here.
    // They would overwrite the optimistic user message with backend state
    // that doesn't contain user text yet (JSONL not written until turn completes).
    // Timeline refresh is handled by event-driven hydration:
    //   turn/completed → MessagesPage → historyHydrationSignal → loadMessages
    const afterLen = Array.isArray(ctx.state.timelinesByThread?.[threadId]) ? ctx.state.timelinesByThread[threadId].length : 0;
    logWarn('ui', 'chat.send.timeline_diff', { thread_id: threadId, beforeLen, afterLen });
    logInfo('thread', 'send.done', { thread_id: threadId, duration_ms: Math.round(perfNow() - start) });
  } catch (error) {
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
    ctx.setCompactResult(id, 'failed', isTimeout ? '压缩超时：未收到完成信号，请重试。' : '压缩失败，请重试。', { code: isTimeout ? 'compact_timeout' : 'compact_failed' });
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
    return archived
      ? '线程已归档，但部分归档文件处理失败；请检查警告信息。'
      : '线程已取消归档，但部分恢复文件处理失败；请检查警告信息。';
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
