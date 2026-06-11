function textValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function adaptObservabilityEvent(event) {
  if (!event || typeof event !== 'object' || Array.isArray(event)) return {};
  return {
    ts: textValue(event.ts),
    traceId: textValue(event.traceId || event.trace_id),
    spanId: textValue(event.spanId || event.span_id),
    parentSpanId: textValue(event.parentSpanId || event.parent_span_id),
    method: textValue(event.method),
    phase: textValue(event.phase),
    kind: textValue(event.kind),
    status: textValue(event.status),
    threadId: textValue(event.threadId || event.thread_id),
    turnId: textValue(event.turnId || event.turn_id),
    agentId: textValue(event.agentId || event.agent_id),
    callId: textValue(event.callId || event.call_id),
    toolName: textValue(event.toolName || event.tool_name),
    clientKind: textValue(event.clientKind || event.client_kind),
    clientRoute: textValue(event.clientRoute || event.client_route),
    durationMs: Number(event.durationMs ?? event.duration_ms) || 0,
    error: textValue(event.error),
    code: event.code || null,
    metadata: event.metadata || null,
    stack: Array.isArray(event.stack) ? event.stack : [],
  };
}

function adaptObservabilityResult(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('observability response must be an object');
  }
  return {
    source: textValue(response.source),
    truncated: Boolean(response.truncated),
    totalDurationMs: Number(response.totalDurationMs ?? response.total_duration_ms) || 0,
    events: Array.isArray(response.events) ? response.events.map(adaptObservabilityEvent) : [],
  };
}

export { adaptObservabilityEvent, adaptObservabilityResult };
