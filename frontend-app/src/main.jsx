import { createElement, Profiler, StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './styles.css';
import App, { APP_PROFILER_ID } from './App.jsx';
import { emitSlowRenderTrace, installFrontendPerformanceObservers } from './shared/performanceTrace.js';

function emitAppSlowRenderTrace(id, phase, actualDuration) {
  return emitSlowRenderTrace(id, phase, actualDuration);
}

installFrontendPerformanceObservers();
createRoot(document.getElementById('root')).render(
  createElement(
    StrictMode,
    null,
    createElement(
      Profiler,
      { id: APP_PROFILER_ID, onRender: emitAppSlowRenderTrace },
      createElement(App),
    ),
  ),
);
