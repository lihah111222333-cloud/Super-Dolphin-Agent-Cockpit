import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const backend = vi.hoisted(() => ({
  emitFrontendTraceEvent: vi.fn(() => true),
}));

vi.mock('./api/backendApi.js', () => backend);

import { emitSlowRenderTrace, installFrontendPerformanceObservers } from './performanceTrace.js';

const originalPerformanceObserver = globalThis.PerformanceObserver;

class MockPerformanceObserver {
  static instances = [];

  constructor(callback) {
    this.callback = callback;
    this.disconnect = vi.fn();
    MockPerformanceObserver.instances.push(this);
  }

  observe(options) {
    this.options = options;
  }

  emit(entries) {
    this.callback({ getEntries: () => entries });
  }
}

beforeEach(() => {
  backend.emitFrontendTraceEvent.mockClear();
  MockPerformanceObserver.instances = [];
  globalThis.PerformanceObserver = MockPerformanceObserver;
});

afterEach(() => {
  globalThis.PerformanceObserver = originalPerformanceObserver;
});

it('emits slow React render traces without emitting fast renders', () => {
  expect(emitSlowRenderTrace('ChatPage', 'update', 12, 'frontend.render.chat.slow')).toBe(false);
  expect(backend.emitFrontendTraceEvent).not.toHaveBeenCalled();

  expect(emitSlowRenderTrace('ChatPage', 'update', 64, 'frontend.render.chat.slow')).toBe(true);
  expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith({
    phase: 'frontend.render.chat.slow',
    duration_ms: 64,
    status: 'slow',
    metadata: {
      component: 'ChatPage',
      react_phase: 'update',
    },
  });
});

it('installs FCP, LCP, and Long Task observers with sanitized metric events', () => {
  const cleanup = installFrontendPerformanceObservers();
  const observerByType = Object.fromEntries(MockPerformanceObserver.instances.map((observer) => [observer.options.type, observer]));

  observerByType.paint.emit([
    { name: 'first-paint', startTime: 100 },
    { name: 'first-contentful-paint', startTime: 1200 },
  ]);
  observerByType['largest-contentful-paint'].emit([
    { renderTime: 2600, loadTime: 0, startTime: 2600 },
  ]);
  observerByType.longtask.emit([
    { duration: 72 },
  ]);

  expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
    phase: 'frontend.vitals.fcp',
    duration_ms: 1200,
    status: 'ok',
    metadata: { component: 'app' },
  }));
  expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
    phase: 'frontend.vitals.lcp',
    duration_ms: 2600,
    status: 'slow',
    metadata: { component: 'app' },
  }));
  expect(backend.emitFrontendTraceEvent).toHaveBeenCalledWith(expect.objectContaining({
    phase: 'frontend.longtask',
    duration_ms: 72,
    status: 'slow',
    metadata: { component: 'main-thread' },
  }));

  cleanup();
  expect(MockPerformanceObserver.instances.every((observer) => observer.disconnect.mock.calls.length === 1)).toBe(true);
});
