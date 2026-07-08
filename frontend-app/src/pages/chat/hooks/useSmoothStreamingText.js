import { useEffect, useRef, useState } from 'react';

const STREAMING_REVEAL_SHORT_TEXT_CHARS = 16;
const STREAMING_REVEAL_CATCHUP_FRAMES = 80;
const STREAMING_REVEAL_MAX_CHARS_PER_FRAME = 8;
const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)';

function prefersReducedMotion() {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
  return Boolean(window.matchMedia(REDUCED_MOTION_QUERY).matches);
}

function streamingRevealStepSize(remaining) {
  if (remaining <= STREAMING_REVEAL_SHORT_TEXT_CHARS) return 1;
  return Math.max(
    2,
    Math.min(STREAMING_REVEAL_MAX_CHARS_PER_FRAME, Math.ceil(remaining / STREAMING_REVEAL_CATCHUP_FRAMES)),
  );
}

function cancelAnimationFrameRef(frameRef) {
  if (!frameRef.current) return;
  if (typeof window !== 'undefined' && typeof window.cancelAnimationFrame === 'function') {
    window.cancelAnimationFrame(frameRef.current);
  }
  frameRef.current = 0;
}

function useReducedMotionPreference(enabled) {
  const [reduced, setReduced] = useState(() => (enabled ? prefersReducedMotion() : false));
  useEffect(() => {
    if (!enabled) {
      if (reduced) setReduced(false);
      return undefined;
    }
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
      if (reduced) setReduced(false);
      return undefined;
    }
    const query = window.matchMedia(REDUCED_MOTION_QUERY);
    const update = () => setReduced(Boolean(query.matches));
    update();
    query.addEventListener?.('change', update);
    return () => query.removeEventListener?.('change', update);
  }, [enabled, reduced]);
  return enabled && reduced;
}

export function useSmoothStreamingText(text, { enabled = false, streamKey = '' } = {}) {
  const targetText = text === null || text === undefined ? '' : text.toString();
  const [state, setState] = useState(() => ({ streamKey, visibleText: targetText }));
  const frameRef = useRef(0);
  const targetTextRef = useRef(targetText);

  useEffect(() => {
    targetTextRef.current = targetText;
  }, [targetText]);

  const reducedMotion = useReducedMotionPreference(enabled);
  const streamKeyChanged = state.streamKey !== streamKey;
  const passthrough = !enabled || reducedMotion || streamKeyChanged;
  const visibleText = passthrough ? targetText : state.visibleText;

  useEffect(() => () => cancelAnimationFrameRef(frameRef), []);

  useEffect(() => {
    setState({ streamKey, visibleText: targetText });
  }, [streamKey]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (!enabled || reducedMotion) {
      cancelAnimationFrameRef(frameRef);
      return undefined;
    }

    let active = true;

    const tick = () => {
      if (!active) return;

      setState((current) => {
        if (current.streamKey !== streamKey) return current;
        const latestTarget = targetTextRef.current;
        const currentText = current.visibleText;

        if (!latestTarget.startsWith(currentText) || currentText.length > latestTarget.length) {
          return { streamKey, visibleText: latestTarget };
        }

        const remaining = latestTarget.length - currentText.length;
        if (remaining <= 0) return current;

        return {
          streamKey,
          visibleText: latestTarget.slice(0, currentText.length + streamingRevealStepSize(remaining)),
        };
      });

      frameRef.current = window.requestAnimationFrame(tick);
    };

    frameRef.current = window.requestAnimationFrame(tick);

    return () => {
      active = false;
      cancelAnimationFrameRef(frameRef);
    };
  }, [enabled, reducedMotion, streamKey]);

  return visibleText;
}
