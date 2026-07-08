// @ts-nocheck

import { RPC_METHODS } from './backendRpcMethods.js';
import {
  assertPlainObject,
  assertNoExtraPayloadFields,
  takePayloadField,
  hasOwn,
  normalizeString,
  normalizeRequiredString,
  requireCwd,
  requireKey,
  cleanObject,
  normalizeOptionalLimit,
} from './backendApiCommon.js';

function observabilityTracePayload(method, params) {
  const payload = assertPlainObject(method, params);
  const traceId = normalizeString(payload.traceId || payload.trace_id);
  if (!traceId) throw new Error(`${method}: traceId is required`);
  return cleanObject({ traceId, limit: normalizeOptionalLimit(method, payload), includeTail: payload.includeTail });
}

function observabilityThreadPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = normalizeString(payload.threadId || payload.thread_id);
  if (!threadId) throw new Error(`${method}: threadId is required`);
  return cleanObject({ threadId, limit: normalizeOptionalLimit(method, payload), includeTail: payload.includeTail });
}

function observabilityListPayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  return cleanObject({ limit: normalizeOptionalLimit(method, payload), component: normalizeString(payload.component) });
}

function observabilityRecentPayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  return cleanObject({
    limit: normalizeOptionalLimit(method, payload),
    status: normalizeString(payload.status),
    component: normalizeString(payload.component),
    method: normalizeString(payload.method),
    traceId: normalizeString(payload.traceId || payload.trace_id),
    threadId: normalizeString(payload.threadId || payload.thread_id),
    agentId: normalizeString(payload.agentId || payload.agent_id),
    keyword: normalizeString(payload.keyword),
    includeTail: payload.includeTail,
  });
}

function threadScopedPayload(method, params) {
  const payload = assertPlainObject(method, params);
  const threadId = resolveThreadIdAliases(method, payload);
  const unused = { ...payload };
  takePayloadField(unused, 'threadId');
  takePayloadField(unused, 'thread_id');
  takePayloadField(unused, 'cwd');
  return { threadId, unused };
}

function resolveThreadIdAliases(method, payload) {
  const camel = hasOwn(payload, 'threadId') ? normalizeString(payload.threadId) : '';
  const snake = hasOwn(payload, 'thread_id') ? normalizeString(payload.thread_id) : '';
  if (camel && snake && camel !== snake) {
    throw new Error(`${method}: conflicting threadId values for threadId and thread_id`);
  }
  const threadId = camel || snake;
  if (!threadId) {
    throw new Error(`${method}: threadId is required`);
  }
  return threadId;
}

function legacyThreadNamePayload(method, params) {
  const { unused, threadId } = threadScopedPayload(method, params);
  const name = takePayloadField(unused, 'name');
  if (!normalizeString(name)) throw new Error(`${method}: name is required`);
  assertNoExtraPayloadFields(method, unused);
  return { threadId, name };
}

function memoryTargetPayload(method, value, field = 'target') {
  const target = normalizeString(value);
  if (target !== 'private' && target !== 'team') {
    throw new Error(`${method}: ${field} must be private or team`);
  }
  return target;
}

function memoryEntryGetPayload(method, params) {
  const payload = requireKey(method, requireCwd(method, params), 'path');
  return {
    ...payload,
    target: memoryTargetPayload(method, payload.target),
  };
}

function memoryEntryUpsertPayload(method, params) {
  const payload = requireCwd(method, params);
  for (const key of ['name', 'description', 'type', 'content']) {
    if (!normalizeString(payload[key])) throw new Error(`${method}: ${key} is required`);
  }
  return cleanObject({
    cwd: payload.cwd,
    target: memoryTargetPayload(method, payload.target),
    existingPath: normalizeString(payload.existingPath),
    name: normalizeString(payload.name),
    description: normalizeString(payload.description),
    type: normalizeString(payload.type),
    content: normalizeRequiredString(method, payload.content, 'content'),
    title: normalizeString(payload.title),
  });
}

function memoryPairPayload(method, params) {
  const payload = requireCwd(method, params);
  for (const key of ['pathA', 'pathB']) {
    if (!normalizeString(payload[key])) throw new Error(`${method}: ${key} is required`);
  }
  return {
    cwd: payload.cwd,
    targetA: memoryTargetPayload(method, payload.targetA, 'targetA'),
    pathA: normalizeString(payload.pathA),
    targetB: memoryTargetPayload(method, payload.targetB, 'targetB'),
    pathB: normalizeString(payload.pathB),
  };
}

function skillPersonalType(payload) {
  return normalizeString(payload.personal_type || payload.personalType);
}

function normalizeSkillSummarySuggestion(raw) {
  if (typeof raw === 'string') return normalizeString(raw);
  if (raw && typeof raw === 'object' && !Array.isArray(raw) && hasOwn(raw, 'description')) {
    return normalizeString(raw.description);
  }
  throw new Error(`${RPC_METHODS.SKILLS_SUMMARY_SUGGEST}: description is required`);
}

function skillResolutionPayload(method, params = {}) {
  const payload = assertPlainObject(method, params);
  const conflictID = normalizeString(payload.conflict_id ?? payload.conflictId);
  const action = normalizeString(payload.action);
  if (!conflictID) throw new Error(`${method}: conflict_id is required`);
  if (!action) throw new Error(`${method}: action is required`);
  const entries = [
    ['conflict_id', conflictID],
    ['action', action],
    ['name', payload.name],
    ['scope', payload.scope],
    ['personal_type', payload.personal_type ?? payload.personalType],
    ['provider', payload.provider],
    ['source_provider', payload.source_provider ?? payload.sourceProvider],
    ['source_path_id', payload.source_path_id ?? payload.sourcePathId],
    ['new_name', payload.new_name ?? payload.newName],
    ['keep_source_id', payload.keep_source_id ?? payload.keepSourceID],
    ['merge_content_hash', payload.merge_content_hash ?? payload.mergeContentHash],
    ['disable_policy_target', payload.disable_policy_target ?? payload.disablePolicyTarget],
  ];
  return cleanObject(Object.fromEntries(entries.map(([key, value]) => [key, normalizeString(value)])));
}

function basename(path) {
  const value = normalizeString(path);
  return value.split(/[\\/]/).filter(Boolean).pop() || value;
}

function normalizeAttachmentPath(item) {
  if (typeof item === 'string') return normalizeString(item);
  if (item && typeof item === 'object') return normalizeString(item.path || item.url);
  return '';
}

function normalizeAttachmentInputItem(item) {
  if (item && typeof item === 'object' && normalizeString(item.kind) === 'image') {
    const path = normalizeString(item.path);
    const previewUrl = normalizeString(item.previewUrl || item.url);
    if (path) {
      const payload = { type: 'localImage', path };
      if (previewUrl.toLowerCase().startsWith('data:image/')) payload.url = previewUrl;
      return payload;
    }
    if (previewUrl) return { type: 'image', url: previewUrl };
    return null;
  }

  const path = normalizeAttachmentPath(item);
  if (!path) return null;
  return { type: 'mention', name: basename(path), path };
}

function hasAttachmentInputContent(attachments) {
  return Array.isArray(attachments) && attachments.some((item) => normalizeAttachmentInputItem(item));
}

function normalizeTurnInput(input, attachments = []) {
  const extraItems = Array.isArray(attachments)
    ? attachments.map(normalizeAttachmentInputItem).filter(Boolean)
    : [];

  if (Array.isArray(input)) {
    if (input.length > 0 && extraItems.length > 0) {
      throw new Error(`${RPC_METHODS.TURN_START}: input and attachments cannot both contain content`);
    }
    if (input.length === 0 && extraItems.length === 0) {
      throw new Error(`${RPC_METHODS.TURN_START}: input is required`);
    }
    return { input: [...input, ...extraItems] };
  }

  const text = normalizeString(input);
  if (text && extraItems.length > 0) {
    throw new Error(`${RPC_METHODS.TURN_START}: input and attachments cannot both contain content`);
  }
  if (!text && extraItems.length === 0) {
    throw new Error(`${RPC_METHODS.TURN_START}: input is required`);
  }
  if (extraItems.length > 0) {
    return {
      input: [
        ...(text ? [{ type: 'text', text }] : []),
        ...extraItems,
      ],
    };
  }
  return { prompt: text };
}

export {
  observabilityTracePayload, observabilityThreadPayload, observabilityListPayload, observabilityRecentPayload, threadScopedPayload, resolveThreadIdAliases,
  legacyThreadNamePayload, memoryTargetPayload,
  memoryEntryGetPayload, memoryEntryUpsertPayload, memoryPairPayload, skillPersonalType, normalizeSkillSummarySuggestion, skillResolutionPayload,
  basename, normalizeAttachmentPath, normalizeAttachmentInputItem, hasAttachmentInputContent, normalizeTurnInput,
};
