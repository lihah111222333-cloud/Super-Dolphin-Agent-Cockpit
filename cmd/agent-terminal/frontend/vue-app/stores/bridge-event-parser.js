// @ts-nocheck

export function normalizeThreadID(threadId) {
  return (threadId || '').toString().trim();
}

export function toNormalizedEventString(value) {
  return (value || '').toString().trim().toLowerCase();
}

export function getBridgeEventThreadId(evt) {
  const candidates = [
    evt?.threadId,
    evt?.thread_id,
    evt?.agent_id,
    evt?.params?.threadId,
    evt?.params?.thread_id,
    evt?.params?.agent_id,
    evt?.payload?.threadId,
    evt?.payload?.thread_id,
    evt?.payload?.agent_id,
    evt?.data?.threadId,
    evt?.data?.thread_id,
    evt?.data?.agent_id,
    evt?.item?.threadId,
    evt?.params?.item?.threadId,
    evt?.payload?.item?.threadId,
    evt?.data?.item?.threadId,
  ];
  for (const value of candidates) {
    const id = normalizeThreadID(value);
    if (id) return id;
  }
  return '';
}

export function getBridgeEventMethod(evt) {
  const candidates = [
    evt?.method,
    evt?.params?.method,
    evt?.payload?.method,
    evt?.data?.method,
    evt?.type,
  ];
  for (const value of candidates) {
    const method = (value || '').toString().trim();
    if (method) return method;
  }
  return '';
}

export function getBridgeEventType(evt) {
  const candidates = [
    evt?.payload?.type,
    evt?.params?.type,
    evt?.data?.type,
    evt?.type,
  ];
  for (const value of candidates) {
    const type = (value || '').toString().trim();
    if (type) return type;
  }
  return '';
}

export function getBridgeEventCommand(evt) {
  const candidates = [
    evt?.command,
    evt?.cmd,
    evt?.uiCommand,
    evt?.params?.command,
    evt?.params?.cmd,
    evt?.params?.uiCommand,
    evt?.payload?.command,
    evt?.payload?.cmd,
    evt?.payload?.uiCommand,
    evt?.item?.command,
    evt?.item?.cmd,
    evt?.params?.item?.command,
    evt?.params?.item?.cmd,
    evt?.payload?.item?.command,
    evt?.payload?.item?.cmd,
  ];
  for (const value of candidates) {
    const command = (value || '').toString().trim();
    if (command) return command;
  }
  return '';
}

export function collectBridgeEventItemKinds(evt) {
  const values = [
    evt?.item?.type,
    evt?.item?.kind,
    evt?.params?.item?.type,
    evt?.params?.item?.kind,
    evt?.payload?.item?.type,
    evt?.payload?.item?.kind,
    evt?.data?.item?.type,
    evt?.data?.item?.kind,
    evt?.type,
    evt?.params?.type,
    evt?.payload?.type,
    evt?.data?.type,
  ];
  return values
    .map((value) => (value || '').toString().trim())
    .filter(Boolean);
}

export function isContextCompactionItemKind(value) {
  const normalized = toNormalizedEventString(value)
    .replace(/[_\s-]+/g, '')
    .replace(/[^\w/]/g, '');
  return normalized.includes('contextcompaction') || normalized === 'contextcompacted';
}

export function isCompactCommand(value) {
  const normalized = toNormalizedEventString(value).replace(/\s+/g, '');
  return normalized === '/compact';
}
