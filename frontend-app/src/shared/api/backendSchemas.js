import { z } from 'zod';

/** @typedef {Record<string, unknown>} SchemaRecord */

const modelProviderVendorSchema = z.object({}).passthrough();

const workflowMaterialWriteResponseSchema = z.object({ path: z.string() }).strict();

/** @param {unknown} value @returns {string} */
function schemaTextValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

/** @param {...unknown} values @returns {string} */
function schemaFirstText(...values) {
  for (const value of values) {
    const text = schemaTextValue(value);
    if (text) return text;
  }
  return '';
}

/** @param {unknown} value @param {number} fallback @returns {number} */
function schemaNumberValue(value, fallback = 0) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

/** @param {unknown} value @returns {value is SchemaRecord} */
function isSchemaRecord(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

/** @param {unknown} value @returns {SchemaRecord} */
function schemaObjectValue(value) {
  return isSchemaRecord(value) ? value : {};
}

const observabilityEventSchema = z.object({
  ts: z.unknown().optional(),
  traceId: z.unknown().optional(),
  trace_id: z.unknown().optional(),
  spanId: z.unknown().optional(),
  span_id: z.unknown().optional(),
  parentSpanId: z.unknown().optional(),
  parent_span_id: z.unknown().optional(),
  method: z.unknown().optional(),
  phase: z.unknown().optional(),
  kind: z.unknown().optional(),
  status: z.unknown().optional(),
  threadId: z.unknown().optional(),
  thread_id: z.unknown().optional(),
  turnId: z.unknown().optional(),
  turn_id: z.unknown().optional(),
  agentId: z.unknown().optional(),
  agent_id: z.unknown().optional(),
  callId: z.unknown().optional(),
  call_id: z.unknown().optional(),
  toolName: z.unknown().optional(),
  tool_name: z.unknown().optional(),
  clientKind: z.unknown().optional(),
  client_kind: z.unknown().optional(),
  clientRoute: z.unknown().optional(),
  client_route: z.unknown().optional(),
  durationMs: z.unknown().optional(),
  duration_ms: z.unknown().optional(),
  error: z.unknown().optional(),
  code: z.unknown().optional(),
  metadata: z.unknown().optional(),
  stack: z.unknown().optional(),
}).passthrough().transform((event) => ({
  ts: schemaTextValue(event.ts),
  traceId: schemaTextValue(event.traceId ?? event.trace_id),
  spanId: schemaTextValue(event.spanId ?? event.span_id),
  parentSpanId: schemaTextValue(event.parentSpanId ?? event.parent_span_id),
  method: schemaTextValue(event.method),
  phase: schemaTextValue(event.phase),
  kind: schemaTextValue(event.kind),
  status: schemaTextValue(event.status) || 'unknown',
  threadId: schemaTextValue(event.threadId ?? event.thread_id),
  turnId: schemaTextValue(event.turnId ?? event.turn_id),
  agentId: schemaTextValue(event.agentId ?? event.agent_id),
  callId: schemaTextValue(event.callId ?? event.call_id),
  toolName: schemaTextValue(event.toolName ?? event.tool_name),
  clientKind: schemaTextValue(event.clientKind ?? event.client_kind),
  clientRoute: schemaTextValue(event.clientRoute ?? event.client_route),
  durationMs: schemaNumberValue(event.durationMs ?? event.duration_ms, 0),
  error: schemaTextValue(event.error),
  code: event.code || null,
  metadata: event.metadata || null,
  stack: Array.isArray(event.stack) ? event.stack : [],
}));

const observabilityResultSchema = z.object({
  source: z.unknown().optional(),
  truncated: z.unknown().optional(),
  degraded: z.unknown().optional(),
  parseError: z.unknown().optional(),
  parse_error: z.unknown().optional(),
  tailError: z.unknown().optional(),
  tail_error: z.unknown().optional(),
  tailTimedOut: z.unknown().optional(),
  tail_timed_out: z.unknown().optional(),
  tailFilesScanned: z.unknown().optional(),
  tail_files_scanned: z.unknown().optional(),
  totalDurationMs: z.unknown().optional(),
  total_duration_ms: z.unknown().optional(),
  events: z.array(observabilityEventSchema),
}).passthrough().transform((value) => ({
  source: schemaTextValue(value.source),
  truncated: Boolean(value.truncated),
  degraded: Boolean(value.degraded),
  parseError: schemaTextValue(value.parseError ?? value.parse_error),
  tailError: schemaTextValue(value.tailError ?? value.tail_error),
  tailTimedOut: Boolean(value.tailTimedOut ?? value.tail_timed_out),
  tailFilesScanned: schemaNumberValue(value.tailFilesScanned ?? value.tail_files_scanned, 0),
  totalDurationMs: schemaNumberValue(value.totalDurationMs ?? value.total_duration_ms, 0),
  events: value.events,
}));

/** @param {unknown} raw @param {number} index @returns {string} */
function sharedFileItemError(raw, index) {
  if (!isSchemaRecord(raw)) {
    return `shared file item ${index} must be an object`;
  }
  if (!schemaFirstText(raw.path)) return `shared file item ${index} path is required`;
  return '';
}

/** @param {unknown} raw @param {number} index */
function normalizeSharedFileItem(raw, index) {
  const itemError = sharedFileItemError(raw, index);
  if (itemError) throw new Error(itemError);
  const item = /** @type {SchemaRecord} */ (raw);
  const path = schemaFirstText(item.path);
  return {
    id: `${path}:${index}`,
    path,
    content: schemaFirstText(item.content),
    updatedBy: schemaFirstText(item.updated_by, item.updatedBy),
    updatedAt: schemaFirstText(item.updated_at, item.updatedAt),
    createdAt: schemaFirstText(item.created_at, item.createdAt),
  };
}

/** @param {unknown} item @returns {string} */
function finalOutputRefPath(item) {
  if (typeof item === 'string') return schemaTextValue(item);
  if (!isSchemaRecord(item)) return '';
  const sharedfile = schemaObjectValue(item.sharedfile);
  const sharedFile = schemaObjectValue(item.sharedFile);
  const sharedFileSnake = schemaObjectValue(item.shared_file);
  return schemaFirstText(item.path, sharedfile.path, sharedFile.path, sharedFileSnake.path);
}

/** @param {unknown} item @param {number} index @returns {string} */
function finalOutputRefError(item, index) {
  if (typeof item === 'string') {
    return schemaTextValue(item) ? '' : `final output ref ${index} path is required`;
  }
  if (!isSchemaRecord(item)) {
    return `final output ref ${index} must be an object`;
  }
  return finalOutputRefPath(item) ? '' : `final output ref ${index} path is required`;
}

/** @param {unknown} item @param {number} index */
function normalizeFinalOutputRef(item, index) {
  const refError = finalOutputRefError(item, index);
  if (refError) throw new Error(refError);
  if (typeof item === 'string') {
    return { path: schemaTextValue(item), runKey: '', dagKey: '', sourceNodeKey: '' };
  }
  const ref = /** @type {SchemaRecord} */ (item);
  return {
    path: finalOutputRefPath(ref),
    runKey: schemaFirstText(ref.runKey, ref.run_key),
    dagKey: schemaFirstText(ref.dagKey, ref.dag_key),
    sourceNodeKey: schemaFirstText(ref.sourceNodeKey, ref.source_node_key),
  };
}

/** @param {unknown} value @returns {string} */
function finalOutputRefsError(value) {
  if (value === undefined) return '';
  if (!Array.isArray(value)) return 'shared files dashboard finalOutputRefs must be an array';
  for (let index = 0; index < value.length; index += 1) {
    const refError = finalOutputRefError(value[index], index);
    if (refError) return refError;
  }
  return '';
}

/** @param {unknown} value */
function normalizeFinalOutputRefs(value) {
  if (value === undefined) return [];
  const refsError = finalOutputRefsError(value);
  if (refsError) throw new Error(refsError);
  return /** @type {unknown[]} */ (value).map((item, index) => normalizeFinalOutputRef(item, index));
}

/** @param {unknown} value @returns {string} */
function sharedFileRetentionError(value) {
  if (value === undefined) return '';
  if (!isSchemaRecord(value)) {
    return 'shared files dashboard sharedFileRetention must be an object';
  }
  if (!Array.isArray(value.items)) {
    return 'shared files dashboard sharedFileRetention.items must be an array';
  }
  for (let index = 0; index < value.items.length; index += 1) {
    const item = value.items[index];
    if (!isSchemaRecord(item)) {
      return `shared file retention item ${index} must be an object`;
    }
    if (!schemaTextValue(item.path)) return `shared file retention item ${index} path is required`;
  }
  return '';
}

/** @param {unknown} value */
function normalizeSharedFileRetention(value) {
  if (value === undefined) {
    return { items: [], protectedCount: 0, cleanupCandidateCount: 0 };
  }
  const retentionError = sharedFileRetentionError(value);
  if (retentionError) throw new Error(retentionError);
  const retention = /** @type {SchemaRecord & { items: SchemaRecord[] }} */ (value);
  return {
    items: retention.items.map((item) => ({
      path: schemaTextValue(item.path),
      protected: Boolean(item.protected),
      cleanupCandidate: Boolean(item.cleanupCandidate ?? item.cleanup_candidate),
      reason: schemaTextValue(item.reason),
      finalOutput: item.finalOutput || item.final_output || null,
    })),
    protectedCount: schemaNumberValue(retention.protectedCount ?? retention.protected_count, 0),
    cleanupCandidateCount: schemaNumberValue(retention.cleanupCandidateCount ?? retention.cleanup_candidate_count, 0),
  };
}

const sharedFilesDashboardSchema = z.object({
  files: z.array(z.unknown()).optional(),
  finalOutputRefs: z.unknown().optional(),
  sharedFileRetention: z.unknown().optional(),
}).passthrough().superRefine((value, context) => {
  if (!Array.isArray(value.files)) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: ['files'],
      message: 'files must be an array',
    });
    return;
  }
  const files = /** @type {unknown[]} */ (value.files);
  files.forEach((item, index) => {
    const itemError = sharedFileItemError(item, index);
    if (itemError) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
      path: ['files', index],
        message: itemError,
      });
    }
  });
  const refsError = finalOutputRefsError(value.finalOutputRefs);
  if (refsError) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: ['finalOutputRefs'],
      message: refsError,
    });
  }
  const retentionError = sharedFileRetentionError(value.sharedFileRetention);
  if (retentionError) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: ['sharedFileRetention'],
      message: retentionError,
    });
  }
}).transform((value) => {
  const files = value.files;
  if (!files) throw new TypeError('shared files dashboard response files must be an array');
  return {
    files: files.map((item, index) => normalizeSharedFileItem(item, index)),
    finalOutputRefs: normalizeFinalOutputRefs(value.finalOutputRefs),
    retention: normalizeSharedFileRetention(value.sharedFileRetention),
  };
});

const modelProviderRegistrySchema = z.object({
  activeVendorId: z.unknown().optional(),
  vendors: z.array(modelProviderVendorSchema),
}).passthrough();

/** @param {import('zod').ZodIssue | undefined} issue @returns {string} */
function issuePath(issue) {
  const path = Array.isArray(issue?.path) ? issue.path : [];
  return path.map((part) => part.toString()).join('.');
}

/** @param {string} label @param {import('zod').ZodIssue | undefined} issue @returns {string} */
function formatIssue(label, issue) {
  const path = issuePath(issue);
  if (label === 'observability response') {
    if (path === 'events') return 'observability response events must be an array';
    const eventIndex = issue?.path?.[0] === 'events' && Number.isInteger(issue.path[1]) ? issue.path[1] : null;
    if (eventIndex !== null) return `observability response event[${String(eventIndex)}] must be an object`;
    return 'observability response must be an object';
  }
  if (label === 'shared files dashboard' && path === 'files') {
    return 'shared files dashboard response files must be an array';
  }
  if (label === 'shared files dashboard') {
    if (
      issue?.message?.startsWith('shared file ')
      || issue?.message?.startsWith('shared files dashboard ')
      || issue?.message?.startsWith('final output ref ')
    ) {
      return issue.message;
    }
  }
  if (label === 'model provider registry') {
    if (path === 'vendors') return 'model provider registry vendors must be an array';
    return 'model provider registry response must be an object';
  }
  if (!path && label.endsWith('response')) return `${label} must be an object`;
  return `${label} ${issue?.message ?? 'response is invalid'}`;
}

/**
 * @template T
 * @param {string} label
 * @param {import('zod').ZodType<T>} schema
 * @param {unknown} response
 * @returns {T}
 */
function parseSchema(label, schema, response) {
  const result = schema.safeParse(response);
  if (!result.success) {
    throw new TypeError(formatIssue(label, result.error.issues[0]));
  }
  return result.data;
}

/** @param {unknown} response */
function parseObservabilityResultResponse(response) {
  return parseSchema('observability response', observabilityResultSchema, response);
}

/** @param {unknown} response */
function parseSharedFilesDashboardResponse(response) {
  return parseSchema('shared files dashboard', sharedFilesDashboardSchema, response);
}

/** @param {unknown} response */
function parseModelProviderRegistryResponse(response) {
  return parseSchema('model provider registry', modelProviderRegistrySchema, response);
}

/**
 * @template T
 * @param {string} label
 * @param {import('zod').ZodType<T>} schema
 * @param {unknown} response
 * @returns {T}
 */
function parseExactSchema(label, schema, response) {
  const result = schema.safeParse(response);
  if (result.success) return result.data;
  const issue = result.error.issues[0];
  const path = issuePath(issue);
  throw new TypeError(`${label}${path ? ` ${path}` : ''} ${issue.message}`);
}

const parseWorkflowMaterialWriteResponse = (/** @type {unknown} */ response) => parseExactSchema('workflow material write', workflowMaterialWriteResponseSchema, response);

export {
  modelProviderRegistrySchema,
  observabilityResultSchema,
  parseModelProviderRegistryResponse,
  parseObservabilityResultResponse,
  parseSharedFilesDashboardResponse,
  parseWorkflowMaterialWriteResponse,
  sharedFilesDashboardSchema,
};
