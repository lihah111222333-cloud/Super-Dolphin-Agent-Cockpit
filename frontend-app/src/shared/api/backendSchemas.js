import { z } from 'zod';

const objectSchema = z.object({}).passthrough();
const memorySectionSchema = z.object({
  entries: z.array(z.unknown()),
}).passthrough();
const modelProviderVendorSchema = z.object({}).passthrough();

const sharedFileDetailTextSchema = z.preprocess(
  (value) => (typeof value === 'string' ? value.trim() : ''),
  z.string().min(1, 'path is required'),
);

export const observabilityResultSchema = objectSchema;

export const memorySnapshotSchema = z.object({
  overview: z.unknown().optional(),
  private: memorySectionSchema,
  team: memorySectionSchema,
}).passthrough();

export const sharedFilesDashboardSchema = z.object({
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
  }
});

export const sharedFileDetailResponseSchema = z.object({
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

export const modelProviderRegistrySchema = z.object({
  activeVendorId: z.unknown().optional(),
  vendors: z.array(modelProviderVendorSchema),
}).passthrough();

function issuePath(issue) {
  return issue?.path?.map((part) => part.toString()).join('.') || '';
}

function formatIssue(label, issue) {
  const path = issuePath(issue);
  if (label === 'shared file detail' && path === 'path') {
    return 'shared file detail path is required';
  }
  if (label === 'memory snapshot') {
    if (path === 'private' || path === 'private.entries') return 'memory private entries must be an array';
    if (path === 'team' || path === 'team.entries') return 'memory team entries must be an array';
  }
  if (label === 'shared files dashboard' && path === 'files') {
    return 'shared files dashboard response files must be an array';
  }
  if (label === 'model provider registry') {
    if (path === 'vendors') return 'model provider registry vendors must be an array';
    return 'model provider registry response must be an object';
  }
  if (!path && label.endsWith('response')) return `${label} must be an object`;
  return `${label} ${issue?.message || 'response is invalid'}`;
}

function parseSchema(label, schema, response) {
  const result = schema.safeParse(response);
  if (!result.success) {
    throw new TypeError(formatIssue(label, result.error.issues[0]));
  }
  return result.data;
}

export function parseObservabilityResultResponse(response) {
  return parseSchema('observability response', observabilityResultSchema, response);
}

export function parseMemorySnapshotResponse(response) {
  return parseSchema('memory snapshot', memorySnapshotSchema, response);
}

export function parseSharedFilesDashboardResponse(response) {
  return parseSchema('shared files dashboard', sharedFilesDashboardSchema, response);
}

export function parseSharedFileDetailResponse(response) {
  return parseSchema('shared file detail', sharedFileDetailResponseSchema, response);
}

export function parseModelProviderRegistryResponse(response) {
  return parseSchema('model provider registry', modelProviderRegistrySchema, response);
}
