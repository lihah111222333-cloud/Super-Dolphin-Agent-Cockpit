import { beforeEach, expect, it, vi } from 'vitest';
import {
  diagnosticCauseForTest,
  frontendHealthSnapshot,
  resetFrontendHealthForTest,
} from '../diagnostics/frontendHealthStore.js';
import { runUIAction } from './runUIAction.js';

function diagnosticIds(...ids) {
  let index = 0;
  return () => ids[index++] || `diagnostic-${index}`;
}

beforeEach(() => {
  window.localStorage.clear();
  resetFrontendHealthForTest();
});

it('routes synchronous failures to visible and persistent sinks without console-only reporting', () => {
  const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
  const visibleFailureSink = vi.fn();
  const healthSink = vi.fn();
  const error = new Error('raw provider token=secret');

  runUIAction('fixture.sync', () => { throw error; }, {
    diagnosticIdFactory: diagnosticIds('diagnostic-sync'), healthSink, visibleFailureSink,
  });

  expect(healthSink).toHaveBeenCalledWith(expect.objectContaining({ actionId: 'fixture.sync' }));
  expect(visibleFailureSink).toHaveBeenCalledWith(expect.objectContaining({
    publicError: expect.objectContaining({ diagnosticId: 'diagnostic-sync', message: '操作失败，当前页面状态已保留。' }),
  }));
  expect(JSON.stringify(visibleFailureSink.mock.calls)).not.toContain('raw provider');
  expect(diagnosticCauseForTest('diagnostic-sync')).toBe(error);
  expect(consoleError).not.toHaveBeenCalled();
});

it('routes Promise rejection through the same sinks', async () => {
  const visibleFailureSink = vi.fn();
  const healthSink = vi.fn();
  const error = new Error('async raw cause');

  runUIAction('fixture.async', () => Promise.reject(error), {
    diagnosticIdFactory: diagnosticIds('diagnostic-async'), healthSink, visibleFailureSink,
  });
  await Promise.resolve();

  expect(healthSink).toHaveBeenCalledTimes(1);
  expect(visibleFailureSink).toHaveBeenCalledTimes(1);
  expect(diagnosticCauseForTest('diagnostic-async')).toBe(error);
});

it('fails fast when actionId or the retryable action thunk is missing', () => {
  expect(() => runUIAction('', () => {})).toThrow('actionId is required');
  expect(() => runUIAction('fixture.action', Promise.resolve())).toThrow('action must be a function');
  expect(() => runUIAction('fixture.action', () => {}, { healthSink: null })).toThrow('healthSink must be a function');
  expect(() => runUIAction('fixture.action', () => {}, { retryable: 'yes' })).toThrow('retryable must be a boolean');
});

it('records a visible failure sink exception in Health without recursive reporting', () => {
  const healthSink = vi.fn();
  const visibleSinkError = new Error('visible sink raw failure');

  runUIAction('fixture.visible-sink', () => { throw new Error('action raw failure'); }, {
    diagnosticIdFactory: diagnosticIds('diagnostic-action', 'diagnostic-visible'),
    healthSink,
    visibleFailureSink: () => { throw visibleSinkError; },
  });

  expect(healthSink).toHaveBeenCalledTimes(2);
  expect(healthSink).toHaveBeenLastCalledWith(expect.objectContaining({
    actionId: 'visible-action-failure.publish',
    publicError: expect.objectContaining({ code: 'VISIBLE_FAILURE_SINK_FAILED' }),
  }));
  expect(diagnosticCauseForTest('diagnostic-visible')).toBe(visibleSinkError);
});

it('records an onError callback exception in Health without exposing the raw action cause', () => {
  const healthSink = vi.fn();
  const onErrorCause = new Error('onError raw failure');
  const onError = vi.fn(() => { throw onErrorCause; });

  runUIAction('fixture.on-error', () => { throw new Error('provider raw failure'); }, {
    diagnosticIdFactory: diagnosticIds('diagnostic-action', 'diagnostic-on-error'),
    healthSink,
    onError,
    visibleFailureSink: vi.fn(),
  });

  expect(onError).toHaveBeenCalledWith(expect.objectContaining({ message: '操作失败，当前页面状态已保留。' }));
  expect(healthSink).toHaveBeenCalledTimes(2);
  expect(healthSink).toHaveBeenLastCalledWith(expect.objectContaining({
    actionId: 'ui-action.on-error',
    publicError: expect.objectContaining({ code: 'ON_ERROR_CALLBACK_FAILED' }),
  }));
  expect(diagnosticCauseForTest('diagnostic-on-error')).toBe(onErrorCause);
});

it('terminates async reporting when every diagnostic sink and the id factory throw', async () => {
  const causes = {
    action: new Error('raw provider token=secret'),
    diagnostic: new Error('raw diagnostic factory failure'),
    emergency: new Error('raw emergency sink failure'),
    health: new Error('raw health sink failure'),
    onError: new Error('raw onError failure'),
    visible: new Error('raw visible sink failure'),
  };
  const onError = vi.fn((publicError) => {
    expect(publicError).toEqual(expect.objectContaining({
      code: 'UI_ACTION_FAILED',
      message: '操作失败，当前页面状态已保留。',
    }));
    throw causes.onError;
  });

  runUIAction('fixture.all-sinks', () => Promise.reject(causes.action), {
    diagnosticIdFactory: () => { throw causes.diagnostic; },
    emergencyHealthSink: () => { throw causes.emergency; },
    healthSink: () => { throw causes.health; },
    onError,
    visibleFailureSink: () => { throw causes.visible; },
  });
  await Promise.resolve();
  await Promise.resolve();

  const records = frontendHealthSnapshot();
  expect(records).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'fixture.all-sinks', code: 'UI_ACTION_FAILED' }),
    expect.objectContaining({ actionId: 'ui-action.diagnostic-id', code: 'DIAGNOSTIC_ID_FACTORY_FAILED' }),
    expect.objectContaining({ actionId: 'frontend-health.record', code: 'HEALTH_SINK_FAILED' }),
    expect.objectContaining({ actionId: 'visible-action-failure.publish', code: 'VISIBLE_FAILURE_SINK_FAILED' }),
    expect.objectContaining({ actionId: 'ui-action.on-error', code: 'ON_ERROR_CALLBACK_FAILED' }),
    expect.objectContaining({ actionId: 'frontend-health.emergency', code: 'EMERGENCY_HEALTH_SINK_FAILED' }),
  ]));
  expect(JSON.stringify(records)).not.toContain('raw ');
  const recordFor = (actionId) => records.find((record) => record.actionId === actionId);
  expect(diagnosticCauseForTest(recordFor('fixture.all-sinks').diagnosticId)).toBe(causes.action);
  expect(diagnosticCauseForTest(recordFor('ui-action.diagnostic-id').diagnosticId)).toBe(causes.diagnostic);
  expect(diagnosticCauseForTest(recordFor('frontend-health.record').diagnosticId)).toBe(causes.health);
  expect(diagnosticCauseForTest(recordFor('visible-action-failure.publish').diagnosticId)).toBe(causes.visible);
  expect(diagnosticCauseForTest(recordFor('ui-action.on-error').diagnosticId)).toBe(causes.onError);
  expect(diagnosticCauseForTest(recordFor('frontend-health.emergency').diagnosticId)).toBe(causes.emergency);
  expect(onError).toHaveBeenCalledTimes(1);
});

it('survives throwing health and visible sinks with finite emergency Health records', () => {
  const healthSinkError = new Error('health sink raw failure');
  const visibleSinkError = new Error('visible sink raw failure');
  const healthSink = vi.fn(() => { throw healthSinkError; });
  const visibleFailureSink = vi.fn(() => { throw visibleSinkError; });

  runUIAction('fixture.recursion', () => { throw new Error('action raw failure'); }, {
    diagnosticIdFactory: diagnosticIds('diagnostic-action', 'diagnostic-health-1', 'diagnostic-visible', 'diagnostic-health-2'),
    healthSink,
    visibleFailureSink,
  });

  expect(healthSink).toHaveBeenCalledTimes(2);
  expect(visibleFailureSink).toHaveBeenCalledTimes(1);
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'fixture.recursion' }),
    expect.objectContaining({ actionId: 'frontend-health.record', code: 'HEALTH_SINK_FAILED' }),
    expect.objectContaining({ actionId: 'visible-action-failure.publish', code: 'VISIBLE_FAILURE_SINK_FAILED' }),
  ]));
});
