import { createElement, Profiler, StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './styles.css';
import App, { APP_PROFILER_ID } from './App.jsx';
import { initTencentRum } from './shared/monitoring/tencentRum.js';
import { emitFrontendTraceEvent } from './shared/api/backendApi.js';

const REACT_RENDER_SLOW_MS = 50;

/**
 * @param {string} id
 * @param {string} phase
 * @param {number} actualDuration
 */
function emitSlowRenderTrace(id, phase, actualDuration) {
  const durationMs = Number(actualDuration);
  if (!Number.isFinite(durationMs) || durationMs < REACT_RENDER_SLOW_MS) return;
  emitFrontendTraceEvent({
    phase: 'frontend.render.slow',
    duration_ms: durationMs,
    status: 'ok',
    metadata: {
      component: id,
      react_phase: phase,
    },
  });
}

initTencentRum();

createRoot(document.getElementById('root')).render(
  createElement(
    StrictMode,
    null,
    createElement(
      Profiler,
      { id: APP_PROFILER_ID, onRender: emitSlowRenderTrace },
      createElement(App),
    ),
  ),
);
