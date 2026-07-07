import { z } from 'zod';

const sharedFileDetailTextSchema = z.preprocess(
  (value) => (typeof value === 'string' ? value.trim() : ''),
  z.string().min(1, 'path is required'),
);

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

function firstIssueMessage(error) {
  const issue = error?.issues?.[0];
  if (!issue) return 'response is invalid';
  return issue.message || 'response is invalid';
}

export function parseSharedFileDetailResponse(response) {
  const result = sharedFileDetailResponseSchema.safeParse(response);
  if (!result.success) {
    throw new TypeError(`shared file detail ${firstIssueMessage(result.error)}`);
  }
  return result.data;
}
