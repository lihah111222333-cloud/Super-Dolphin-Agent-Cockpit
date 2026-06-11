import { normalizedThreadIdentity } from './threadIdentityAdapter.js';

const TURN_STATE_INFO = Object.freeze({
  idle: Object.freeze({ label: '空闲', tone: 'connected', busy: false }),
  starting: Object.freeze({ label: '启动中', tone: 'active', busy: true }),
  preparing: Object.freeze({ label: '准备中', tone: 'active', busy: true }),
  thinking: Object.freeze({ label: '思考中', tone: 'active', busy: true }),
  running: Object.freeze({ label: '运行中', tone: 'active', busy: true }),
  editing: Object.freeze({ label: '编辑中', tone: 'active', busy: true }),
  waiting: Object.freeze({ label: '等待确认', tone: 'warning', busy: true }),
  syncing: Object.freeze({ label: '同步中', tone: 'active', busy: true }),
  responding: Object.freeze({ label: '回复中', tone: 'active', busy: true }),
  force_completing: Object.freeze({ label: '强制完成中', tone: 'active', busy: true }),
  interrupting: Object.freeze({ label: '中断中', tone: 'warning', busy: true }),
  interrupted: Object.freeze({ label: '已中断', tone: 'warning', busy: false }),
  completed: Object.freeze({ label: '已完成', tone: 'done', busy: false }),
  error: Object.freeze({ label: '异常', tone: 'error', busy: false }),
  failed: Object.freeze({ label: '失败', tone: 'error', busy: false }),
  stalled: Object.freeze({ label: '停滞', tone: 'error', busy: false }),
  stopped: Object.freeze({ label: '已停止', tone: 'idle', busy: false }),
  archived: Object.freeze({ label: '已归档', tone: 'idle', busy: false }),
});

const LEGACY_TURN_STATE_ALIASES = Object.freeze({
  工作中: 'running',
  发送中: 'preparing',
  pending: 'starting',
  recovering: 'syncing',
  create: 'idle',
  created: 'idle',
  错误: 'error',
  失败: 'failed',
  空闲: 'idle',
  等待指示: 'idle',
});

function knownProviderKey(value) {
  const normalized = (value || '').toString().trim().toLowerCase();
  return normalized === 'claude' || normalized === 'codex' ? normalized : '';
}

function threadProviderLabel(provider) {
  return knownProviderKey(provider) || 'unknown';
}

function normalizeTurnState(value) {
  const raw = normalizedThreadIdentity(value);
  if (!raw) return '';
  const alias = LEGACY_TURN_STATE_ALIASES[raw] || raw;
  return alias.toLowerCase().replace(/-/g, '_');
}

function threadCardStatusLabel(thread, running) {
  const status = (thread?.status || '').toString().trim();
  const normalized = status.toLowerCase();
  const normalizedState = normalizeTurnState(status);
  const mapped = TURN_STATE_INFO[normalizedState];
  if (!status || normalizedState === 'idle' || normalized === 'idle' || status === '空闲' || status === '等待指示') return '';
  if (mapped?.label) return mapped.label;
  if (running) return '工作中';
  return '';
}

function threadStatusBusy(status) {
  const mapped = TURN_STATE_INFO[normalizeTurnState(status)];
  if (mapped) return mapped.busy;
  const normalized = (status || '').toString().trim().toLowerCase();
  return normalized === '工作中';
}

function threadStatusDotState(status) {
  const normalized = normalizeTurnState(status);
  if (!normalized) return 'idle';
  if (['failed', 'error', 'stalled'].includes(normalized)) return 'error';
  if (['running', 'force_completing'].includes(normalized)) return 'running';
  if (['preparing', 'starting', 'thinking'].includes(normalized)) return 'thinking';
  if (['waiting', 'interrupting', 'interrupted'].includes(normalized)) return 'waiting';
  if (['syncing', 'responding', 'editing'].includes(normalized)) return normalized;
  if (['completed', 'idle', 'stopped', 'archived'].includes(normalized)) return 'idle';
  return 'idle';
}

function threadStatusDotTitle(status, statusLabel) {
  const normalized = normalizeTurnState(status);
  return statusLabel || TURN_STATE_INFO[normalized]?.label || '空闲';
}

function firstStatusText(...values) {
  for (const value of values) {
    const text = normalizedThreadIdentity(value);
    if (text) return text;
  }
  return '';
}

function workStatusForThread({ sending, loading, activeThreadId, activeThread, statusEntry }) {
  if (!activeThreadId) return { busy: false };
  if (loading) return { busy: true };
  const rawState = firstStatusText(
    statusEntry?.state,
    statusEntry?.status,
    activeThread?.state,
    activeThread?.status,
    sending ? 'preparing' : '',
  );
  const normalizedState = normalizeTurnState(rawState);
  const mapped = TURN_STATE_INFO[normalizedState];
  return {
    busy: mapped?.busy ?? Boolean(sending),
  };
}

export {
  threadCardStatusLabel,
  threadProviderLabel,
  threadStatusBusy,
  threadStatusDotState,
  threadStatusDotTitle,
  workStatusForThread,
};
