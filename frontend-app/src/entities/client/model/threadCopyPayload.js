import {
  currentIsoTimestamp,
  normalizeOptionalTextField,
  optionalTextField,
  parseRequiredTimestamp,
  utcPartsFromEpochMillis,
} from './contractStoreModel.js';
function normalizeCopyPath(value) {
  const path = normalizeOptionalTextField(value);
  if (!path) return '';
  if (path !== '/' && !/^[a-zA-Z]:[\\/]?$/.test(path)) {
    return path.replace(/[\\/]+$/, '');
  }
  return path;
}

function firstThreadCopyText(...values) {
  for (const value of values) {
    if (value === null || value === undefined || typeof value === 'boolean') continue;
    if (typeof value === 'number' && Number.isFinite(value)) return String(value);
    if (typeof value !== 'string') continue;
    const text = value.trim();
    if (text && text !== '.' && text !== '[object Object]') return text;
  }
  return '';
}

function positiveThreadCopyPort(...values) {
  for (const value of values) {
    const number = Number(value);
    if (Number.isFinite(number) && number > 0) return number;
  }
  return null;
}

function normalizeLogScopeCwd(value) {
  const raw = normalizeCopyPath(value);
  if (!raw || raw === '.') return '';
  return raw;
}

function buildCwdLogPath(cwd) {
  const normalized = normalizeLogScopeCwd(cwd);
  if (!normalized || /^[A-Za-z]:$/.test(normalized) || /^[\\/]+$/.test(normalized)) return null;
  const projectName = normalized.split(/[\\/]/).filter(Boolean).pop() || optionalTextField();
  if (!projectName || projectName === '.' || projectName === '/') return null;
  return `~/.multi-agent/log/${projectName}/`;
}

function copiedAtEpochMillis(value) {
  if (value && typeof value.getTime === 'function') return value.getTime();
  return parseRequiredTimestamp(value, 'thread copy copiedAt');
}

function formatUTC8HumanReadable(value = currentIsoTimestamp('thread copy copiedAt')) {
  let epochMillis;
  try {
    epochMillis = copiedAtEpochMillis(value);
  } catch {
    return optionalTextField();
  }
  const utc8 = utcPartsFromEpochMillis(epochMillis + (8 * 60 * 60 * 1000), 'thread copy copiedAt utc8');
  const year = utc8.year;
  const month = String(utc8.month).padStart(2, '0');
  const day = String(utc8.day).padStart(2, '0');
  const hours = String(utc8.hour).padStart(2, '0');
  const minutes = String(utc8.minute).padStart(2, '0');
  const seconds = String(utc8.second).padStart(2, '0');
  return `${year}-${month}-${day} ${hours}:${minutes}:${seconds} UTC+8`;
}

function buildThreadCopyPayload({
  state,
  threadId,
  thread = {},
  identity = {},
  threadConfig = null,
  defaultProvider = 'codex',
  copiedAt = currentIsoTimestamp('thread copy copiedAt'),
}) {
  const providerThreadId = firstThreadCopyText(
    identity.providerThreadId,
    identity.provider_thread_id,
    thread.providerThreadId,
    thread.provider_thread_id,
  );
  const agentId = firstThreadCopyText(
    identity.agentId,
    identity.agent_id,
    thread.agentId,
    thread.agent_id,
    threadId,
  );
  const provider = firstThreadCopyText(identity.provider, thread.provider, state.provider) || defaultProvider;
  const cwd = firstThreadCopyText(identity.cwd, identity.CWD, thread.cwd, state.activeProject, state.cwd);
  const model = firstThreadCopyText(
    identity.model,
    identity.effective?.model,
    thread.model,
    thread.effective?.model,
    threadConfig?.effective?.model,
    state.providerConfig?.model,
  );
  const effort = firstThreadCopyText(
    identity.effort,
    identity.reasoningEffort,
    identity.reasoning_effort,
    identity.effective?.effort,
    thread.effort,
    thread.effective?.effort,
    threadConfig?.effective?.effort,
    state.providerConfig?.effort,
  );

  return {
    agentId,
    providerThreadId,
    uuid: firstThreadCopyText(identity.uuid, identity.sessionId, identity.session_id, providerThreadId),
    name: firstThreadCopyText(identity.name, thread.name),
    status: firstThreadCopyText(identity.status, thread.status, state.statuses?.[threadId]),
    provider,
    model: model || null,
    effort: effort || null,
    port: positiveThreadCopyPort(identity.port, thread.port),
    cwd: cwd || null,
    'log-path': firstThreadCopyText(
      identity['log-path'],
      identity.logPath,
      identity.log_path,
      thread.logPath,
      thread.log_path,
    ) || buildCwdLogPath(cwd),
    copiedAt: formatUTC8HumanReadable(copiedAt),
  };
}

export {
  buildCwdLogPath,
  buildThreadCopyPayload,
  firstThreadCopyText,
  formatUTC8HumanReadable,
  positiveThreadCopyPort,
};
