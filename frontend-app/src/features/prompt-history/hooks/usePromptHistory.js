import { useCallback, useEffect, useMemo } from 'react';

import { getPromptHistory } from '../../../shared/api/backendApi.js';
import { createPromptHistoryController } from '../model/promptHistoryController.js';

function fetchPromptHistoryPage(params) {
  return getPromptHistory(params);
}

export function usePromptHistory({
  activeThreadId,
  cwd,
  draft,
  fetchPage = fetchPromptHistoryPage,
  sendMessage,
  setDraft,
  threadLifecycleSignal,
}) {
  if (typeof fetchPage !== 'function') throw new Error('fetchPage is required');
  if (typeof sendMessage !== 'function') throw new Error('sendMessage is required');
  if (typeof setDraft !== 'function') throw new Error('setDraft is required');

  const controller = useMemo(() => {
    // The thread-list identity is a loss-only invalidation signal, not a copied state source.
    void threadLifecycleSignal;
    return createPromptHistoryController({
      fetchPage,
      cwd,
      activeThreadId,
    });
  }, [activeThreadId, cwd, fetchPage, threadLifecycleSignal]);

  useEffect(() => {
    controller.captureDraft(draft);
  }, [controller, draft]);

  useEffect(() => () => controller.dispose(), [controller]);

  const previous = useCallback(async () => {
    const selected = await controller.previous();
    if (typeof selected === 'string') setDraft(selected);
    return selected;
  }, [controller, setDraft]);

  const next = useCallback(() => {
    return controller.next(setDraft);
  }, [controller, setDraft]);

  const send = useCallback(async (...args) => {
    const result = await sendMessage(...args);
    if (result !== false) controller.invalidate();
    return result;
  }, [controller, sendMessage]);

  const invalidate = useCallback(() => controller.invalidate(), [controller]);
  const snapshot = useCallback(() => controller.snapshot(), [controller]);

  return { previous, next, send, invalidate, snapshot };
}
