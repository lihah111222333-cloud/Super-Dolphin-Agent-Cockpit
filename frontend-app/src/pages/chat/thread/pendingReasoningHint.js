import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import { trimmedText } from '../markdown/markdownMessageModel.js';

function isLaunchIntentId(threadId) {
  return /^launch[_-]/i.test(trimmedText(threadId));
}

function pendingHintCanMigrate(hintThreadId, activeThreadId, messages) {
  const launchId = isLaunchIntentId(hintThreadId) ? hintThreadId : activeThreadId;
  if (hintThreadId && !isLaunchIntentId(hintThreadId)) return false;
  if (!isLaunchIntentId(launchId)) return false;
  return messages.some((message) => message?.id === `user-${launchId}`);
}

function usePendingReasoningHint({ activeThreadId, messages }) {
  const generationRef = useRef(0);
  const hintRef = useRef(null);
  const timerRef = useRef(null);
  const [hint, setHint] = useState(null);
  const currentThreadRef = useRef(activeThreadId);
  const updateHint = useCallback((next) => {
    hintRef.current = next;
    setHint(next);
  }, []);
  const clearTimer = useCallback(() => {
    if (timerRef.current !== null) clearTimeout(timerRef.current.id);
    timerRef.current = null;
  }, []);
  const clearCurrent = useCallback((generation) => {
    const current = hintRef.current;
    if (!current || (generation !== undefined && current.generation !== generation)) return;
    if (generation === undefined && current.threadId !== currentThreadRef.current) return;
    clearTimer();
    updateHint(null);
  }, [clearTimer, updateHint]);
  const start = useCallback(() => {
    clearTimer();
    const generation = generationRef.current + 1;
    generationRef.current = generation;
    updateHint({ generation, threadId: currentThreadRef.current });
    timerRef.current = { generation, id: setTimeout(() => clearCurrent(generation), 5000) };
    return generation;
  }, [clearCurrent, clearTimer, updateHint]);
  useLayoutEffect(() => {
    currentThreadRef.current = activeThreadId;
  }, [activeThreadId]);
  useLayoutEffect(() => {
    const current = hintRef.current;
    if (!current || current.threadId === activeThreadId || !pendingHintCanMigrate(current.threadId, activeThreadId, messages)) return;
    updateHint({ ...current, threadId: activeThreadId });
  }, [activeThreadId, messages, updateHint]);
  useLayoutEffect(() => () => clearTimer(), [clearTimer]);
  return { clearCurrent, hintVisible: hint?.threadId === activeThreadId, start };
}

export { pendingHintCanMigrate, usePendingReasoningHint };
