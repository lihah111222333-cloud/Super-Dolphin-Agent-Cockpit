import { useEffect, useRef, useState } from 'react';
import { REDUCED_MOTION_QUERY, nextStreamingState } from '../model/smoothStreamingModel.js';

function prefersReducedMotion() {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
  return Boolean(window.matchMedia(REDUCED_MOTION_QUERY).matches);
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

      setState((current) => nextStreamingState({ current, latestTarget: targetTextRef.current, streamKey }));

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
