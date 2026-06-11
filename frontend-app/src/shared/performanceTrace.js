import { emitFrontendTraceEvent } from './api/backendApi.js';

export const REACT_RENDER_SLOW_MS = 50;
const FCP_SLOW_MS = 1500;
const LCP_SLOW_MS = 2500;

function metricStatus(durationMs, slowThresholdMs) {
  return durationMs >= slowThresholdMs ? 'slow' : 'ok';
}

function safeDuration(value) {
  const durationMs = Number(value);
  return Number.isFinite(durationMs) && durationMs >= 0 ? durationMs : null;
}

function emitPerformanceMetric({ phase, durationMs, slowThresholdMs, component }) {
  const normalizedDuration = safeDuration(durationMs);
  if (normalizedDuration === null) return false;
  return emitFrontendTraceEvent({
    phase,
    duration_ms: normalizedDuration,
    status: metricStatus(normalizedDuration, slowThresholdMs),
    metadata: { component },
  });
}

export function emitSlowRenderTrace(id, phase, actualDuration, tracePhase = 'frontend.render.slow') {
  const durationMs = safeDuration(actualDuration);
  if (durationMs === null || durationMs < REACT_RENDER_SLOW_MS) return false;
  return emitFrontendTraceEvent({
    phase: tracePhase,
    duration_ms: durationMs,
    status: 'slow',
    metadata: {
      component: id,
      react_phase: phase,
    },
  });
}

function observePerformanceEntries(type, callback) {
  if (typeof PerformanceObserver === 'undefined') return null;
  try {
    const observer = new PerformanceObserver((list) => {
      for (const entry of list.getEntries()) callback(entry);
    });
    observer.observe({ type, buffered: true });
    return observer;
  }
  catch (error) {
    void error;
    return null;
  }
}

export function installFrontendPerformanceObservers() {
  const observers = [];
  const paintObserver = observePerformanceEntries('paint', (entry) => {
    if (entry?.name !== 'first-contentful-paint') return;
    emitPerformanceMetric({
      phase: 'frontend.vitals.fcp',
      durationMs: entry.startTime,
      slowThresholdMs: FCP_SLOW_MS,
      component: 'app',
    });
  });
  if (paintObserver) observers.push(paintObserver);

  const lcpObserver = observePerformanceEntries('largest-contentful-paint', (entry) => {
    emitPerformanceMetric({
      phase: 'frontend.vitals.lcp',
      durationMs: entry.renderTime || entry.loadTime || entry.startTime,
      slowThresholdMs: LCP_SLOW_MS,
      component: 'app',
    });
  });
  if (lcpObserver) observers.push(lcpObserver);

  const longTaskObserver = observePerformanceEntries('longtask', (entry) => {
    emitFrontendTraceEvent({
      phase: 'frontend.longtask',
      duration_ms: safeDuration(entry?.duration) ?? 0,
      status: 'slow',
      metadata: { component: 'main-thread' },
    });
  });
  if (longTaskObserver) observers.push(longTaskObserver);

  return () => {
    for (const observer of observers) observer.disconnect();
  };
}
