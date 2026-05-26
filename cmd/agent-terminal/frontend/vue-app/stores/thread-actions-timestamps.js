// @ts-nocheck
import { normalizeThreadID } from './bridge-event-parser.js';

export function touchThreadUpdatedAt(ctx, threadId, updatedAt) {
  const id = normalizeThreadID(threadId);
  if (!id || !updatedAt) return;
  const threads = Array.isArray(ctx.state.threads) ? ctx.state.threads : [];
  const index = threads.findIndex((thread) => normalizeThreadID(thread?.id) === id);
  if (index < 0) return;
  const current = threads[index];
  if (current?.updatedAt === updatedAt) return;
  const nextThreads = threads.slice();
  nextThreads[index] = { ...current, updatedAt };
  ctx.state.threads = nextThreads;
}
