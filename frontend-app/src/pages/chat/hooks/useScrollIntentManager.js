import { useCallback, useEffect, useRef } from 'react';
import {
  createScrollIntentState,
  reduceScrollIntent,
  shouldFollowTimeline,
} from '../model/scrollIntentModel.js';
import {
  isTimelineNearBottom,
  requestTimelineBottomScroll,
  scrollTimelineElementToBottom,
} from './timelineScroll.js';

function editableEventTarget(target) {
  if (!(target instanceof Element)) return false;
  return Boolean(target.closest('input, textarea, select, [contenteditable="true"], [role="textbox"]'));
}

function firstTouchY(event) {
  const touch = event.touches?.[0];
  return Number.isFinite(touch?.clientY) ? touch.clientY : null;
}

export function useScrollIntentManager({ activeThreadId, autoScrollKey, timelineContentBlocked }) {
  const timelineRef = useRef(null);
  const intentRef = useRef(createScrollIntentState(activeThreadId));
  const touchStartYRef = useRef(null);
  const frameIdRef = useRef(null);
  const lastAutoScrollKeyRef = useRef('');
  const blockedRef = useRef(timelineContentBlocked);
  const initialThreadRenderRef = useRef(true);

  const transition = useCallback((event) => {
    intentRef.current = reduceScrollIntent(intentRef.current, event);
    return intentRef.current;
  }, []);
  const cancelPendingFrame = useCallback(() => {
    if (frameIdRef.current === null) return;
    window.cancelAnimationFrame(frameIdRef.current);
    frameIdRef.current = null;
  }, []);
  const scrollIfSticky = useCallback((smooth = false, source = 'streaming') => {
    if (!shouldFollowTimeline(intentRef.current, source)) return false;
    scrollTimelineElementToBottom(timelineRef.current, smooth);
    return true;
  }, []);
  const requestStickyBottom = useCallback((smooth, source) => {
    cancelPendingFrame();
    frameIdRef.current = requestTimelineBottomScroll(() => {
      frameIdRef.current = null;
      scrollIfSticky(smooth, source);
    });
  }, [cancelPendingFrame, scrollIfSticky]);
  const scrollToBottom = useCallback((smooth = false) => {
    transition({ type: 'explicit-bottom' });
    cancelPendingFrame();
    scrollTimelineElementToBottom(timelineRef.current, smooth);
  }, [cancelPendingFrame, transition]);
  const markMessageSent = useCallback(() => {
    transition({ type: 'message-sent' });
    requestStickyBottom(true, 'streaming');
  }, [requestStickyBottom, transition]);

  const onTimelineScroll = useCallback((eventOrTimeline) => {
    const timeline = eventOrTimeline?.currentTarget || eventOrTimeline;
    transition({ type: 'scroll-position', nearBottom: isTimelineNearBottom(timeline) });
  }, [transition]);
  const onTimelineWheel = useCallback((event) => {
    transition({
      type: 'wheel',
      ctrlKey: event.ctrlKey,
      deltaX: event.deltaX,
      deltaY: event.deltaY,
    });
  }, [transition]);
  const onTimelineTouchStart = useCallback((event) => {
    touchStartYRef.current = firstTouchY(event);
  }, []);
  const onTimelineTouchMove = useCallback((event) => {
    const currentY = firstTouchY(event);
    const previousY = touchStartYRef.current;
    if (currentY === null || previousY === null) return;
    transition({ type: 'touch', direction: currentY > previousY ? 'up' : 'down' });
    touchStartYRef.current = currentY;
  }, [transition]);
  const onTimelineKeyDown = useCallback((event) => {
    transition({
      type: 'key',
      key: event.key,
      targetEditable: editableEventTarget(event.target),
    });
  }, [transition]);

  useEffect(() => {
    blockedRef.current = timelineContentBlocked;
  }, [timelineContentBlocked]);
  useEffect(() => {
    transition({ type: 'thread-changed', threadId: activeThreadId });
    touchStartYRef.current = null;
    lastAutoScrollKeyRef.current = '';
    initialThreadRenderRef.current = true;
    cancelPendingFrame();
    const timeline = timelineRef.current;
    if (timeline) timeline.scrollTop = 0;
  }, [activeThreadId, cancelPendingFrame, transition]);
  useEffect(() => {
    if (timelineContentBlocked || !activeThreadId) return undefined;
    scrollTimelineElementToBottom(timelineRef.current, false);
    initialThreadRenderRef.current = false;
    const timerId = window.setTimeout(() => {
      scrollIfSticky(false, 'streaming');
    }, 50);
    return () => window.clearTimeout(timerId);
  }, [activeThreadId, scrollIfSticky, timelineContentBlocked]);
  useEffect(() => {
    if (!autoScrollKey) {
      lastAutoScrollKeyRef.current = autoScrollKey;
      return;
    }
    if (lastAutoScrollKeyRef.current === autoScrollKey) return;
    lastAutoScrollKeyRef.current = autoScrollKey;
    if (shouldFollowTimeline(intentRef.current, 'streaming')) requestStickyBottom(false, 'streaming');
  }, [autoScrollKey, requestStickyBottom]);
  useEffect(() => {
    const timeline = timelineRef.current;
    if (!timeline) return undefined;
    const observer = new MutationObserver(() => {
      if (!initialThreadRenderRef.current && !blockedRef.current) scrollIfSticky(false, 'mutation');
    });
    observer.observe(timeline, { childList: true, subtree: true, characterData: true });
    return () => observer.disconnect();
  }, [activeThreadId, scrollIfSticky]);
  useEffect(() => {
    const timeline = timelineRef.current;
    if (!timeline || typeof ResizeObserver !== 'function') return undefined;
    const observer = new ResizeObserver(() => {
      if (!initialThreadRenderRef.current && !blockedRef.current) scrollIfSticky(false, 'resize');
    });
    observer.observe(timeline);
    return () => observer.disconnect();
  }, [activeThreadId, scrollIfSticky]);
  useEffect(() => {
    const timeline = timelineRef.current;
    if (!timeline) return undefined;
    const handleLoad = () => {
      if (!initialThreadRenderRef.current && !blockedRef.current) scrollIfSticky(false, 'load');
    };
    timeline.addEventListener('load', handleLoad, true);
    return () => timeline.removeEventListener('load', handleLoad, true);
  }, [activeThreadId, scrollIfSticky]);
  useEffect(() => () => cancelPendingFrame(), [cancelPendingFrame]);

  return {
    markMessageSent,
    onTimelineKeyDown,
    onTimelineScroll,
    onTimelineTouchMove,
    onTimelineTouchStart,
    onTimelineWheel,
    scrollIfSticky,
    scrollToBottom,
    timelineRef,
  };
}
