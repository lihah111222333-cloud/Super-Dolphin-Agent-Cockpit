// @ts-check

import {
  isVisibleTimelineItem,
  normalizeTimelineItem,
  sortTimelineChronologically,
} from './timelineRuntime.js';

const IMAGE_PLACEHOLDER_RE = /<image\s[^>]*><\/image>/gi;

function normalizeString(value) {
  return (value || '').toString().trim();
}

function objectRecord(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function firstFieldValue(source, keys = []) {
  const record = objectRecord(source);
  for (const key of keys) {
    const value = record[key];
    if (value !== undefined && value !== null && value !== '') return value;
  }
  return undefined;
}

function firstValueFromSources(sources = []) {
  for (const [source, keys] of sources) {
    const value = firstFieldValue(source, keys);
    if (value !== undefined) return value;
  }
  return undefined;
}

function extractText(value) {
  if (value === null || value === undefined) return '';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return normalizeString(value);
  }
  if (Array.isArray(value)) {
    return value.map((item) => extractText(item)).filter(Boolean).join('\n');
  }
  if (typeof value === 'object') {
    return extractText(value.text || value.content || value.message || value.delta || value.output || value.result || value.answer || value.response);
  }
  return '';
}

export function extractHistoryMetadata(message) {
  const meta = message.metadata || message.meta;
  if (!meta || typeof meta !== 'object') return null;
  try {
    return typeof meta === 'string' ? JSON.parse(meta) : meta;
  }
  catch {
    return null;
  }
}

export function buildHistoryMessageAttachments(message) {
  /*
   * 历史图片来自已保存的 provider input，不是当前输入框附件。
   * 这里只重建预览，发送时仍由 composer slice 生成 input。
   */
  const meta = extractHistoryMetadata(message);
  const inputs = Array.isArray(meta?.input) ? meta.input : [];
  const attachments = [];
  for (const item of inputs) {
    if (!item || typeof item !== 'object') continue;
    if (item.type !== 'image' && item.type !== 'localImage') continue;
    const rawPath = normalizeString(item.path || item.url || item.source);
    if (!rawPath) continue;
    let previewUrl = rawPath;
    if (rawPath.startsWith('/') && !rawPath.startsWith('/clipboard/')) {
      const base = rawPath.split('/').pop() || '';
      if (/\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(base)) {
        previewUrl = `/clipboard/${base}`;
      }
    }
    attachments.push({
      kind: 'image',
      name: normalizeString(item.name) || rawPath.split('/').pop() || rawPath,
      path: rawPath,
      previewUrl,
    });
  }
  return attachments;
}

export function stripHistoryImagePlaceholders(text, hasAttachments) {
  if (!hasAttachments || !text) return text;
  return text.replace(IMAGE_PLACEHOLDER_RE, '').trim();
}

export function normalizeThreadMessageItems(allMessages) {
  /*
   * thread/messages 是已保存的历史页，也要变成同一种 timeline item。
   * 它和实时 patch 共用合并逻辑，分页不会覆盖正在流式输出的内容。
   */
  return sortTimelineChronologically(allMessages.map((message) => {
    const rawText = message.content || message.text || message.message || message.delta || message.output || message.result || message.answer || message.response;
    const historyAttachments = message.role === 'user' ? buildHistoryMessageAttachments(message) : [];
    const text = stripHistoryImagePlaceholders(rawText, historyAttachments.length > 0);
    const normalized = normalizeTimelineItem({
      id: message.id || message.messageId || message.message_id,
      role: message.role,
      kind: message.kind || message.type || message.eventType || message.event_type,
      text,
      createdAt: message.createdAt || message.created_at,
      completedAt: message.completedAt || message.completed_at || message.finishedAt || message.finished_at,
    });
    return historyAttachments.length > 0 ? { ...normalized, attachments: historyAttachments } : normalized;
  }).filter(isVisibleTimelineItem));
}

function dagNodeFallbackText(value) {
  const text = extractText(value);
  if (text) return text;
  if (!value || typeof value !== 'object') return '';
  try {
    return normalizeString(JSON.stringify(value, null, 2));
  }
  catch {
    return '';
  }
}

function dagNodeFallbackPrompt(node) {
  const config = objectRecord(node?.config || node?.raw?.config);
  const exec = objectRecord(config.exec);
  const verifier = objectRecord(exec.verifier);
  const prompt = firstValueFromSources([
    [config, ['first_turn', 'firstTurn', 'prompt', 'instructions', 'message', 'text', 'input']],
    [exec, ['first_turn', 'firstTurn', 'prompt', 'instructions', 'message', 'text', 'input']],
    [verifier, ['first_turn', 'firstTurn', 'prompt', 'instructions', 'message', 'text', 'input']],
  ]);
  return dagNodeFallbackText(prompt);
}

function dagNodeFallbackResult(node) {
  const result = firstValueFromSources([
    [node, ['result', 'output', 'summary', 'message', 'text', 'content']],
    [objectRecord(node?.raw), ['result', 'output', 'summary', 'message', 'text', 'content']],
  ]);
  return dagNodeFallbackText(result);
}

export function dagNodeHistoryFallbackItems(threadId, dagNode) {
  const node = objectRecord(dagNode);
  if (Object.keys(node).length === 0) return [];
  const nodeKey = normalizeString(node.nodeKey || node.node_key || node.id || threadId) || 'dag-node';
  const title = normalizeString(node.title || node.name || nodeKey);
  const startedAt = normalizeString(node.startedAt || node.started_at || node.createdAt || node.created_at) || new Date().toISOString();
  const finishedAt = normalizeString(node.finishedAt || node.finished_at) || startedAt;
  const prompt = dagNodeFallbackPrompt(node);
  const result = dagNodeFallbackResult(node);
  const items = [];
  if (prompt) {
    items.push(normalizeTimelineItem({
      id: `dag-node:${nodeKey}:prompt`,
      role: 'user',
      text: prompt,
      createdAt: startedAt,
      done: true,
    }));
  }
  if (result) {
    items.push(normalizeTimelineItem({
      id: `dag-node:${nodeKey}:result`,
      role: 'assistant',
      text: result,
      title: title ? `DAG 节点结果：${title}` : 'DAG 节点结果',
      createdAt: finishedAt,
      done: true,
    }));
  }
  return sortTimelineChronologically(items.filter(isVisibleTimelineItem));
}

export function threadOpenHistoryFallbackItems(threadId, options = {}) {
  if (normalizeString(options?.source) !== 'dag-node') return [];
  return dagNodeHistoryFallbackItems(threadId, options?.dagNode);
}
