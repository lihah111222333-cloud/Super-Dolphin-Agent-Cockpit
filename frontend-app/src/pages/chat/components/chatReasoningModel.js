import { normalizeMessageText } from './markdownMessageModel.js';

function isReasoningMessage(message) {
  const kind = (message?.kind || '').toString().trim().toLowerCase();
  return kind === 'thinking' || kind === 'reasoning' || kind === 'tool' || kind === 'command' || kind === 'process' || kind === 'plan';
}

function reasoningTitle(message) {
  const kind = (message?.kind || '').toString().trim().toLowerCase();
  const title = (message?.title || '').toString().trim();
  if (title) return title;
  if (kind === 'plan') return '执行计划';
  if (kind === 'tool') return '调用工具';
  if (kind === 'command') return '执行命令';
  return 'AI 思考';
}

function reasoningKindMeta(message = {}) {
  const kind = (message?.kind || '').toString().trim().toLowerCase();
  if (kind === 'tool') return { label: '工具', tone: 'tool' };
  if (kind === 'command') return { label: '命令', tone: 'command' };
  if (kind === 'plan') return { label: '计划', tone: 'plan' };
  if (kind === 'process') return { label: '流程', tone: 'process' };
  return { label: '思考', tone: 'thinking' };
}

function reasoningStepDescription(message = {}) {
  const body = (message?.text || '').toString().trim();
  if (body) return body;
  const meta = reasoningKindMeta(message);
  if (meta.tone === 'plan') return '正在罗列执行计划并同步进度。';
  if (meta.tone === 'tool') return '正在调用工具并等待返回结果。';
  if (meta.tone === 'command') return '正在执行命令并读取输出。';
  if (meta.tone === 'process') return '正在推进任务流程并同步上下文。';
  return 'AI 正在分析上下文、选择工具并整理回答。';
}

function parsePlanItems(text) {
  const statusMarkers = {
    '✅': true,
    '☑': true,
    '✓': true,
    '✔': true,
    '🔄': false,
    '⏳': false,
    '○': false,
    '◯': false,
    '☐': false,
    '❌': false,
  };
  const items = [];
  for (const rawLine of normalizeMessageText(text).split('\n')) {
    const line = rawLine.trim();
    const match = line.match(/^([✅☑✓✔🔄⏳○◯☐❌])?\s*(?:[-*]|\d+[.)])\s*(?:\[([ xX])\]\s*)?(.+)$/u);
    if (!match) continue;
    const label = (match[3] || '').trim();
    if (!label || /^plan$/i.test(label)) continue;
    items.push({
      text: label,
      done: match[1] ? statusMarkers[match[1]] === true : (match[2] || '').toLowerCase() === 'x',
    });
  }
  return items;
}

function positiveTimestampNumber(value) {
  if (!Number.isFinite(value) || value <= 0) return 0;
  return value < 1_000_000_000_000 ? value * 1000 : value;
}

function numericTextTimestampMs(text) {
  if (!/^\d+(?:\.\d+)?$/.test(text)) return 0;
  return positiveTimestampNumber(Number(text));
}

function parsedDateTimestampMs(text) {
  const parsed = Date.parse(text);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function timestampMs(value) {
  if (typeof value === 'number') return positiveTimestampNumber(value);
  const text = (value || '').toString().trim();
  return numericTextTimestampMs(text) || parsedDateTimestampMs(text);
}

function durationLabelFromMs(ms, options = {}) {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  if (totalSeconds <= 0 && !options.showZero) return '';
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes <= 0) return `${seconds}s`;
  return `${minutes}m ${seconds}s`;
}

function syntheticReasoningMessage({ activeTurn, sending, isBusy, fallbackStartTime }) {
  if (!activeTurn && !sending && !isBusy) return null;
  const turnId = activeTurn?.id;
  const id = turnId ? `thinking:${turnId}` : 'thinking-sending';
  const defaultStartTime = fallbackStartTime || new Date().toISOString();
  return {
    id,
    role: 'assistant',
    kind: 'thinking',
    title: '正在处理请求',
    text: '',
    time: activeTurn?.startedAt || defaultStartTime,
    done: false,
  };
}

export {
  durationLabelFromMs,
  isReasoningMessage,
  parsePlanItems,
  reasoningKindMeta,
  reasoningStepDescription,
  reasoningTitle,
  syntheticReasoningMessage,
  timestampMs,
};
