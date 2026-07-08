import { z } from 'zod';

const memorySectionSchema = z.object({
  entries: z.array(z.unknown()),
}).passthrough();
const modelProviderVendorSchema = z.object({}).passthrough();

const MEMORY_TYPE_INFO = Object.freeze({
  user: { category: 'preference', label: '偏好' },
  feedback: { category: 'preference', label: '偏好' },
  project: { category: 'project', label: '项目' },
  reference: { category: 'project', label: '项目' },
});

function schemaTextValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function schemaFirstText(...values) {
  for (const value of values) {
    const text = schemaTextValue(value);
    if (text) return text;
  }
  return '';
}

function schemaNumberValue(value, fallback = 0) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function schemaObjectValue(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

const sharedFileDetailTextSchema = z.preprocess(
  (value) => (typeof value === 'string' ? value.trim() : ''),
  z.string().min(1, 'path is required'),
);

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

function memorySectionError(section, target) {
  if (!section || typeof section !== 'object' || Array.isArray(section) || !Array.isArray(section.entries)) {
    return `memory ${target} entries must be an array`;
  }
  return '';
}

function memoryEntryError(raw, index, target) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    return `memory ${target} entry ${index} must be an object`;
  }
  const path = schemaTextValue(raw.path);
  if (!path) return `memory ${target} entry ${index} path is required`;
  const type = schemaTextValue(raw.type).toLowerCase();
  if (!MEMORY_TYPE_INFO[type]) return `memory ${target} entry ${index} type is unsupported: ${type || '(empty)'}`;
  const name = schemaFirstText(raw.name, raw.title);
  if (!name) return `memory ${target} entry ${index} name is required`;
  return '';
}

function memoryEntryIssuePath(raw, index, target) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return [target, 'entries', index];
  if (!schemaTextValue(raw.path)) return [target, 'entries', index, 'path'];
  const type = schemaTextValue(raw.type).toLowerCase();
  if (!MEMORY_TYPE_INFO[type]) return [target, 'entries', index, 'type'];
  if (!schemaFirstText(raw.name, raw.title)) return [target, 'entries', index, 'name'];
  return [target, 'entries', index];
}

function validateMemorySection(section, target, context) {
  const sectionError = memorySectionError(section, target);
  if (sectionError) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: [target, 'entries'],
      message: sectionError,
    });
    return;
  }
  section.entries.forEach((raw, index) => {
    const entryError = memoryEntryError(raw, index, target);
    if (entryError) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: memoryEntryIssuePath(raw, index, target),
        message: entryError,
      });
    }
  });
}

function normalizeMemoryEntry(raw, index, target) {
  const entryError = memoryEntryError(raw, index, target);
  if (entryError) throw new Error(entryError);
  const path = schemaTextValue(raw.path);
  const type = schemaTextValue(raw.type).toLowerCase();
  const typeInfo = MEMORY_TYPE_INFO[type];
  const name = schemaFirstText(raw.name, raw.title);
  return {
    id: `${target}:${path}:${index}`,
    target,
    path,
    type,
    category: typeInfo.category,
    tag: typeInfo.label,
    name,
    title: schemaFirstText(raw.title, raw.name),
    description: schemaFirstText(raw.description, raw.summary),
    preview: schemaFirstText(raw.preview, raw.content, raw.text),
    updatedAt: schemaFirstText(raw.updatedAt, raw.updated_at, raw.createdAt, raw.created_at),
    source: schemaTextValue(raw.source),
    raw,
  };
}

function normalizeMemorySection(section, target) {
  const sectionError = memorySectionError(section, target);
  if (sectionError) throw new Error(sectionError);
  return section.entries.map((item, index) => normalizeMemoryEntry(item, index, target));
}

const memorySnapshotSchema = z.object({
  overview: z.unknown().optional(),
  private: memorySectionSchema,
  team: memorySectionSchema,
}).passthrough().superRefine((value, context) => {
  validateMemorySection(value.private, 'private', context);
  validateMemorySection(value.team, 'team', context);
}).transform((value) => ({
  overview: schemaObjectValue(value.overview),
  entries: [
    ...normalizeMemorySection(value.private, 'private'),
    ...normalizeMemorySection(value.team, 'team'),
  ],
}));

function sharedFileItemError(raw, index) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    return `shared file item ${index} must be an object`;
  }
  if (!schemaFirstText(raw.path)) return `shared file item ${index} path is required`;
  return '';
}

function normalizeSharedFileItem(raw, index) {
  const itemError = sharedFileItemError(raw, index);
  if (itemError) throw new Error(itemError);
  const path = schemaFirstText(raw.path);
  return {
    id: `${path}:${index}`,
    path,
    content: schemaFirstText(raw.content),
    updatedBy: schemaFirstText(raw.updated_by, raw.updatedBy),
    updatedAt: schemaFirstText(raw.updated_at, raw.updatedAt),
    createdAt: schemaFirstText(raw.created_at, raw.createdAt),
  };
}

function finalOutputRefPath(item) {
  if (typeof item === 'string') return schemaTextValue(item);
  if (!item || typeof item !== 'object' || Array.isArray(item)) return '';
  return schemaFirstText(item.path, item.sharedfile?.path, item.sharedFile?.path, item.shared_file?.path);
}

function finalOutputRefError(item, index) {
  if (typeof item === 'string') {
    return schemaTextValue(item) ? '' : `final output ref ${index} path is required`;
  }
  if (!item || typeof item !== 'object' || Array.isArray(item)) {
    return `final output ref ${index} must be an object`;
  }
  return finalOutputRefPath(item) ? '' : `final output ref ${index} path is required`;
}

function normalizeFinalOutputRef(item, index) {
  const refError = finalOutputRefError(item, index);
  if (refError) throw new Error(refError);
  if (typeof item === 'string') {
    return { path: schemaTextValue(item), runKey: '', dagKey: '', sourceNodeKey: '' };
  }
  return {
    path: finalOutputRefPath(item),
    runKey: schemaFirstText(item.runKey, item.run_key),
    dagKey: schemaFirstText(item.dagKey, item.dag_key),
    sourceNodeKey: schemaFirstText(item.sourceNodeKey, item.source_node_key),
  };
}

function finalOutputRefsError(value) {
  if (value === undefined) return '';
  if (!Array.isArray(value)) return 'shared files dashboard finalOutputRefs must be an array';
  for (let index = 0; index < value.length; index += 1) {
    const refError = finalOutputRefError(value[index], index);
    if (refError) return refError;
  }
  return '';
}

function normalizeFinalOutputRefs(value) {
  if (value === undefined) return [];
  const refsError = finalOutputRefsError(value);
  if (refsError) throw new Error(refsError);
  return value.map((item, index) => normalizeFinalOutputRef(item, index));
}

function sharedFileRetentionError(value) {
  if (value === undefined) return '';
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return 'shared files dashboard sharedFileRetention must be an object';
  }
  if (!Array.isArray(value.items)) {
    return 'shared files dashboard sharedFileRetention.items must be an array';
  }
  for (let index = 0; index < value.items.length; index += 1) {
    const item = value.items[index];
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      return `shared file retention item ${index} must be an object`;
    }
    if (!schemaTextValue(item.path)) return `shared file retention item ${index} path is required`;
  }
  return '';
}

function normalizeSharedFileRetention(value) {
  if (value === undefined) {
    return { items: [], protectedCount: 0, cleanupCandidateCount: 0 };
  }
  const retentionError = sharedFileRetentionError(value);
  if (retentionError) throw new Error(retentionError);
  return {
    items: value.items.map((item) => ({
      path: schemaTextValue(item.path),
      protected: Boolean(item.protected),
      cleanupCandidate: Boolean(item.cleanupCandidate ?? item.cleanup_candidate),
      reason: schemaTextValue(item.reason),
      finalOutput: item.finalOutput || item.final_output || null,
    })),
    protectedCount: schemaNumberValue(value.protectedCount ?? value.protected_count, 0),
    cleanupCandidateCount: schemaNumberValue(value.cleanupCandidateCount ?? value.cleanup_candidate_count, 0),
  };
}

const sharedFilesDashboardSchema = z.object({
  files: z.array(z.unknown()).optional(),
  memory: z.array(z.unknown()).optional(),
  finalOutputRefs: z.unknown().optional(),
  sharedFileRetention: z.unknown().optional(),
}).passthrough().superRefine((value, context) => {
  if (!Array.isArray(value.files) && !Array.isArray(value.memory)) {
    context.addIssue({
      code: z.ZodIssueCode.custom,
      path: ['files'],
      message: 'files must be an array',
    });
    return;
  }
  const files = Array.isArray(value.files) ? value.files : value.memory;
  files.forEach((item, index) => {
    const itemError = sharedFileItemError(item, index);
    if (itemError) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: [Array.isArray(value.files) ? 'files' : 'memory', index],
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
  const files = Array.isArray(value.files) ? value.files : value.memory;
  return {
    files: files.map((item, index) => normalizeSharedFileItem(item, index)),
    finalOutputRefs: normalizeFinalOutputRefs(value.finalOutputRefs),
    retention: normalizeSharedFileRetention(value.sharedFileRetention),
  };
});

const sharedFileDetailResponseSchema = z.object({
  path: sharedFileDetailTextSchema,
  content: z.preprocess(
    (value) => (value === null || value === undefined ? '' : value),
    z.string(),
  ),
  updated_by: z.string().optional(),
  updatedBy: z.string().optional(),
  updated_at: z.string().optional(),
  updatedAt: z.string().optional(),
  created_at: z.string().optional(),
  createdAt: z.string().optional(),
}).passthrough();

const modelProviderRegistrySchema = z.object({
  activeVendorId: z.unknown().optional(),
  vendors: z.array(modelProviderVendorSchema),
}).passthrough();

function issuePath(issue) {
  const path = Array.isArray(issue?.path) ? issue.path : [];
  return path.map((part) => part.toString()).join('.');
}

function formatIssue(label, issue) {
  const path = issuePath(issue);
  if (label === 'shared file detail' && path === 'path') {
    return 'shared file detail path is required';
  }
  if (label === 'observability response') {
    if (path === 'events') return 'observability response events must be an array';
    const eventIndex = issue?.path?.[0] === 'events' && Number.isInteger(issue.path[1]) ? issue.path[1] : null;
    if (eventIndex !== null) return `observability response event[${eventIndex}] must be an object`;
    return 'observability response must be an object';
  }
  if (label === 'memory snapshot') {
    if (issue?.message?.startsWith('memory ')) return issue.message;
    if (path === 'private' || path === 'private.entries') return 'memory private entries must be an array';
    if (path === 'team' || path === 'team.entries') return 'memory team entries must be an array';
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

function parseSchema(label, schema, response) {
  const result = schema.safeParse(response);
  if (!result.success) {
    throw new TypeError(formatIssue(label, result.error.issues[0]));
  }
  return result.data;
}

function parseObservabilityResultResponse(response) {
  return parseSchema('observability response', observabilityResultSchema, response);
}

function parseMemorySnapshotResponse(response) {
  return parseSchema('memory snapshot', memorySnapshotSchema, response);
}

function parseSharedFilesDashboardResponse(response) {
  return parseSchema('shared files dashboard', sharedFilesDashboardSchema, response);
}

function parseSharedFileDetailResponse(response) {
  return parseSchema('shared file detail', sharedFileDetailResponseSchema, response);
}

function parseModelProviderRegistryResponse(response) {
  return parseSchema('model provider registry', modelProviderRegistrySchema, response);
}

export {
  MEMORY_TYPE_INFO,
  memorySnapshotSchema,
  modelProviderRegistrySchema,
  normalizeMemoryEntry,
  normalizeMemorySection,
  observabilityResultSchema,
  parseMemorySnapshotResponse,
  parseModelProviderRegistryResponse,
  parseObservabilityResultResponse,
  parseSharedFileDetailResponse,
  parseSharedFilesDashboardResponse,
  sharedFileDetailResponseSchema,
  sharedFilesDashboardSchema,
};
