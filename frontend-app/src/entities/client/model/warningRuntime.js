const MAX_WARNING_ENTRIES = 300;

export function attachWarningRuntime(runtime, deps) {
  const {
    cleanObject,
    emitFrontendTraceEvent,
    normalizeString,
    normalizeThreadId,
    runtimeThreadIdentifier,
  } = deps;
  const { set } = runtime;

  const warningErrorKey = (fields = {}) => {
    const error = fields?.error;
    if (typeof error === 'string') return error;
    if (error && typeof error === 'object') {
      return normalizeString(error.message || error.code || error.data || JSON.stringify(error));
    }
    return '';
  };

  const warningSignature = (level, event, threadId, fields = {}) => [
    level,
    event,
    threadId,
    normalizeString(fields.method || fields.action || fields.rpcMethod || fields.rpc_method),
    warningErrorKey(fields),
  ].join('|');

  const emitWarningTrace = (level, event, threadId, fields = {}) => {
    const method = normalizeString(event);
    if (!method) return;
    const metadata = cleanObject({
      component: warningTraceComponent(method),
      req_id: fields.req_id ?? fields.reqId,
    });
    emitFrontendTraceEvent(cleanObject({
      phase: 'frontend.warning',
      method,
      trace_id: normalizeString(fields.trace_id || fields.traceId),
      span_id: normalizeString(fields.span_id || fields.spanId),
      parent_span_id: normalizeString(fields.parent_span_id || fields.parentSpanId),
      thread_id: threadId,
      agent_id: normalizeString(fields.agent_id || fields.agentId),
      turn_id: normalizeString(fields.turn_id || fields.turnId),
      call_id: normalizeString(fields.call_id || fields.callId),
      status: warningTraceStatus(level, method),
      error: warningErrorKey(fields),
      metadata: Object.keys(metadata).length > 0 ? metadata : undefined,
    }));
  };

  const addWarning = (level, event, fields = {}) => {
    if (level !== 'warn' && level !== 'error') return;
    const threadId = normalizeThreadId(runtimeThreadIdentifier(fields));
    const signature = warningSignature(level, event, threadId, fields);
    const entry = {
      id: `${event}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
      timestamp: new Date().toISOString(),
      level,
      event,
      threadId,
      fields,
      occurrenceCount: 1,
      signature,
    };
    set((state) => ({
      warningEntries: mergeWarningEntries(state.warningEntries, entry, fields),
    }));
    emitWarningTrace(level, event, threadId, fields);
  };

  Object.assign(runtime, { addWarning });
}

export function warningTraceComponent(event) {
  return String(event || '').trim().split(/[./]/).filter(Boolean)[0] || '';
}

export function warningTraceStatus(level, event) {
  const method = String(event || '').trim().toLowerCase();
  if (level === 'error' || method.endsWith('.failed') || method.endsWith('/failed')) return 'error';
  return 'ok';
}

export function mergeWarningEntries(warningEntries, entry, fields, maxEntries = MAX_WARNING_ENTRIES) {
  const existingIndex = warningEntries.findIndex((item) => item.signature === entry.signature);
  if (existingIndex < 0) return [entry, ...warningEntries].slice(0, maxEntries);
  const existing = warningEntries[existingIndex];
  const updated = {
    ...existing,
    id: entry.id,
    timestamp: entry.timestamp,
    fields,
    occurrenceCount: (Number(existing.occurrenceCount) || 1) + 1,
  };
  return [
    updated,
    ...warningEntries.slice(0, existingIndex),
    ...warningEntries.slice(existingIndex + 1),
  ].slice(0, maxEntries);
}
