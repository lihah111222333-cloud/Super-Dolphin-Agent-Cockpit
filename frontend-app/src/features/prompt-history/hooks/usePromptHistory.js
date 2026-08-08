import { useCallback, useEffect, useMemo, useRef } from 'react';

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

  const lifecycleSignalRef = useRef(threadLifecycleSignal);
  const controller = useMemo(() => {
    // 线程身份变化在下方按仅丢失失效处理，确保 EventBridge 更新后
    // 可见重试仍调用实时 controller。
    return createPromptHistoryController({
      fetchPage,
      cwd,
      activeThreadId,
    });
  }, [activeThreadId, cwd, fetchPage]);

  useEffect(() => {
    controller.captureDraft(draft);
  }, [controller, draft]);

  useEffect(() => {
    controller.activate();
    return () => {
      controller.dispose();
    };
  }, [controller]);

  useEffect(() => {
    if (lifecycleSignalRef.current === threadLifecycleSignal) return;
    lifecycleSignalRef.current = threadLifecycleSignal;
    controller.invalidate({ deferPending: true });
  }, [controller, threadLifecycleSignal]);

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
