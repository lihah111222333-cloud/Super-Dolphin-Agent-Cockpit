import React, { useRef, useEffect, useCallback } from 'react';
import * as Vue from '../../lib/vue.esm-browser.prod.js';

const TOOL_TICKER_SCROLL_STEP_PX = 0.45;

export function ToolTickerBar({
  text = '',
  visible = false,
}) {
  const toolTickerViewportRef = useRef(null);
  const toolTickerDirectionRef = useRef(1);
  const toolTickerFrameRef = useRef(0);
  const toolTickerPausedRef = useRef(false);

  const prefersReducedMotion = useCallback(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  }, []);

  const cancelToolTickerFrame = useCallback(() => {
    if (!toolTickerFrameRef.current || typeof cancelAnimationFrame !== 'function') return;
    cancelAnimationFrame(toolTickerFrameRef.current);
    toolTickerFrameRef.current = 0;
  }, []);

  const resetToolTickerViewport = useCallback(() => {
    const viewport = toolTickerViewportRef.current;
    if (!viewport) return;
    viewport.scrollLeft = 0;
    toolTickerDirectionRef.current = 1;
  }, []);

  const resolveToolTickerMaxScroll = useCallback((viewport) => {
    if (!viewport) return 0;
    return Math.max(0, viewport.scrollWidth - viewport.clientWidth);
  }, []);

  const runToolTickerFrame = useCallback(() => {
    toolTickerFrameRef.current = 0;
    if (toolTickerPausedRef.current || prefersReducedMotion() || !visible || !text) return;
    const viewport = toolTickerViewportRef.current;
    if (!viewport) return;
    const maxScroll = resolveToolTickerMaxScroll(viewport);
    if (maxScroll <= 1) {
      viewport.scrollLeft = 0;
      toolTickerDirectionRef.current = 1;
      return;
    }
    let next = viewport.scrollLeft + (TOOL_TICKER_SCROLL_STEP_PX * toolTickerDirectionRef.current);
    if (next >= maxScroll) {
      next = maxScroll;
      toolTickerDirectionRef.current = -1;
    } else if (next <= 0) {
      next = 0;
      toolTickerDirectionRef.current = 1;
    }
    viewport.scrollLeft = next;
    
    // schedule next frame
    if (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function') {
      toolTickerFrameRef.current = window.requestAnimationFrame(runToolTickerFrame);
    }
  }, [visible, text, prefersReducedMotion, resolveToolTickerMaxScroll]);

  const scheduleToolTickerFrame = useCallback(() => {
    cancelToolTickerFrame();
    if (toolTickerPausedRef.current || prefersReducedMotion() || !visible || !text) return;
    if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') return;
    toolTickerFrameRef.current = window.requestAnimationFrame(runToolTickerFrame);
  }, [visible, text, cancelToolTickerFrame, runToolTickerFrame, prefersReducedMotion]);

  const restartToolTicker = useCallback(() => {
    cancelToolTickerFrame();
    toolTickerPausedRef.current = false;
    if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') return;
    window.requestAnimationFrame(() => {
      resetToolTickerViewport();
      scheduleToolTickerFrame();
    });
  }, [cancelToolTickerFrame, resetToolTickerViewport, scheduleToolTickerFrame]);

  const pauseToolTicker = useCallback(() => {
    toolTickerPausedRef.current = true;
    cancelToolTickerFrame();
  }, [cancelToolTickerFrame]);

  const resumeToolTicker = useCallback(() => {
    toolTickerPausedRef.current = false;
    scheduleToolTickerFrame();
  }, [scheduleToolTickerFrame]);

  useEffect(() => {
    const nextVisible = Boolean(visible && text);
    if (!nextVisible) {
      pauseToolTicker();
      resetToolTickerViewport();
    } else {
      restartToolTicker();
    }
    return () => {
      cancelToolTickerFrame();
    };
  }, [visible, text, pauseToolTicker, resetToolTickerViewport, restartToolTicker, cancelToolTickerFrame]);

  return (
    <div
      className="chat-status-tool-ticker"
      title={text}
      onMouseEnter={pauseToolTicker}
      onMouseLeave={resumeToolTicker}
    >
      <div ref={toolTickerViewportRef} className="chat-status-tool-ticker__viewport">
        <div className="chat-status-tool-ticker__track">
          <span className="chat-status-tool-ticker__content">{text}</span>
        </div>
      </div>
    </div>
  );
}

ToolTickerBar.setup = function(props) {
  const toolTickerViewportRef = Vue.ref(null);
  const toolTickerDirectionRef = { current: 1 };
  const toolTickerFrameRef = { current: 0 };
  const toolTickerPausedRef = { current: false };

  function prefersReducedMotion() {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  }
  function cancelToolTickerFrame() {
    if (!toolTickerFrameRef.current || typeof cancelAnimationFrame !== 'function') return;
    cancelAnimationFrame(toolTickerFrameRef.current);
    toolTickerFrameRef.current = 0;
  }
  function resetToolTickerViewport() {
    const viewport = toolTickerViewportRef.value;
    if (!viewport) return;
    viewport.scrollLeft = 0;
    toolTickerDirectionRef.current = 1;
  }
  function resolveToolTickerMaxScroll(viewport) {
    if (!viewport) return 0;
    return Math.max(0, viewport.scrollWidth - viewport.clientWidth);
  }
  function runToolTickerFrame() {
    toolTickerFrameRef.current = 0;
    if (toolTickerPausedRef.current || prefersReducedMotion() || !props.visible || !props.text) return;
    const viewport = toolTickerViewportRef.value;
    if (!viewport) return;
    const maxScroll = resolveToolTickerMaxScroll(viewport);
    if (maxScroll <= 1) {
      viewport.scrollLeft = 0;
      toolTickerDirectionRef.current = 1;
      return;
    }
    let next = viewport.scrollLeft + (TOOL_TICKER_SCROLL_STEP_PX * toolTickerDirectionRef.current);
    if (next >= maxScroll) {
      next = maxScroll;
      toolTickerDirectionRef.current = -1;
    } else if (next <= 0) {
      next = 0;
      toolTickerDirectionRef.current = 1;
    }
    viewport.scrollLeft = next;
    if (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function') {
      toolTickerFrameRef.current = window.requestAnimationFrame(runToolTickerFrame);
    }
  }
  function scheduleToolTickerFrame() {
    cancelToolTickerFrame();
    if (toolTickerPausedRef.current || prefersReducedMotion() || !props.visible || !props.text) return;
    if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') return;
    toolTickerFrameRef.current = window.requestAnimationFrame(runToolTickerFrame);
  }
  function pauseToolTicker() {
    toolTickerPausedRef.current = true;
    cancelToolTickerFrame();
  }
  function resumeToolTicker() {
    toolTickerPausedRef.current = false;
    scheduleToolTickerFrame();
  }

  return {
    toolTickerViewportRef,
    pauseToolTicker,
    resumeToolTicker,
  };
};
