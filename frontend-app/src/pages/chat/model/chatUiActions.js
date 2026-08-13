export { runUIAction } from '../../../shared/ui/runUIAction.js';

export function threadScopedActionOptions(threadId, options = {}) {
  if (threadId === undefined || threadId === null || threadId === '') return options;
  if (typeof threadId !== 'string' || !threadId.trim()) {
    throw new TypeError('threadScopedActionOptions threadId must be empty or a non-empty string');
  }
  return { ...options, threadId: threadId.trim() };
}
