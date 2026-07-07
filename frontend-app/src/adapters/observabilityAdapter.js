import { parseObservabilityResultResponse } from '../shared/api/backendSchemas.js';

function textValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function observabilityParseFailureEvent(reason, extra = {}) {
  return {
    ts: '',
    traceId: '',
    spanId: '',
    parentSpanId: '',
    method: extra.method || 'observability.event.parse_failed',
    phase: 'observability.parse',
    kind: 'frontend',
    status: 'error',
    threadId: '',
    turnId: '',
    agentId: '',
    callId: '',
    toolName: '',
    clientKind: '',
    clientRoute: '',
    durationMs: 0,
    error: reason,
    code: 'observability.parse_failed',
    metadata: extra.metadata || null,
    stack: [],
  };
}

function adaptObservabilityEvent(event, index = 0) {
  if (!event || typeof event !== 'object' || Array.isArray(event)) {
    return observabilityParseFailureEvent(`event[${index}] must be an object`);
  }
  const status = textValue(event.status);
  return {
    ts: textValue(event.ts),
    traceId: textValue(event.traceId || event.trace_id),
    spanId: textValue(event.spanId || event.span_id),
    parentSpanId: textValue(event.parentSpanId || event.parent_span_id),
    method: textValue(event.method),
    phase: textValue(event.phase),
    kind: textValue(event.kind),
    status: status || 'unknown',
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

function joinParseErrors(existing, next) {
  const parts = [textValue(existing), textValue(next)].filter(Boolean);
  return Array.from(new Set(parts)).join('; ');
}

function adaptObservabilityResult(response) {
  const value = parseObservabilityResultResponse(response);
  let parseError = textValue(value.parseError ?? value.parse_error);
  let events;
  if (!Array.isArray(value.events)) {
    parseError = joinParseErrors(parseError, 'events must be an array');
    events = [observabilityParseFailureEvent('events must be an array', {
      method: 'observability.events.invalid',
      metadata: { receivedType: value.events === null ? 'null' : typeof value.events },
    })];
  } else {
    events = value.events.map((event, index) => {
      const adapted = adaptObservabilityEvent(event, index);
      if (adapted.method === 'observability.event.parse_failed') {
        parseError = joinParseErrors(parseError, adapted.error);
      }
      return adapted;
    });
  }
  return {
    source: textValue(value.source),
    truncated: Boolean(value.truncated),
    degraded: Boolean(value.degraded) || Boolean(parseError),
    parseError,
    tailError: textValue(value.tailError ?? value.tail_error),
    tailTimedOut: Boolean(value.tailTimedOut ?? value.tail_timed_out),
    tailFilesScanned: Number(value.tailFilesScanned ?? value.tail_files_scanned) || 0,
    totalDurationMs: Number(value.totalDurationMs ?? value.total_duration_ms) || 0,
    events,
  };
}

export { adaptObservabilityEvent, adaptObservabilityResult };
