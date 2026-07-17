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

it('projects an explicit false result into visible failure and Health', async () => {
  const visibleFailureSink = vi.fn();
  runUIAction('fixture.false-result', () => Promise.resolve(false), {
    diagnosticIdFactory: diagnosticIds('diagnostic-false'),
    rejectFalse: true,
    visibleFailureSink,
  });
  await Promise.resolve();
  await Promise.resolve();

  expect(visibleFailureSink).toHaveBeenCalledTimes(1);
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'fixture.false-result' }),
  ]));
});

it('fails fast when actionId or the retryable action thunk is missing', () => {
  expect(() => runUIAction('', () => {})).toThrow('actionId is required');
  expect(() => runUIAction('fixture.action', Promise.resolve())).toThrow('action must be a function');
  expect(() => runUIAction('fixture.action', () => {}, { healthSink: null })).toThrow('healthSink must be a function');
  expect(() => runUIAction('fixture.action', () => {}, { retryable: 'yes' })).toThrow('retryable must be a boolean');
  expect(() => runUIAction('fixture.action', () => {}, { rejectFalse: 'yes' })).toThrow('rejectFalse must be a boolean');
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

it('terminates async reporting when the id factory and all caller-owned sinks throw', async () => {
  const causes = {
    action: new Error('raw provider token=secret'),
    diagnostic: new Error('raw diagnostic factory failure'),
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
    healthSink: () => { throw causes.health; },
    onError,
    visibleFailureSink: () => { throw causes.visible; },
  });
  await Promise.resolve();
  await Promise.resolve();

  const records = frontendHealthSnapshot();
  expect(records).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'ui-action.diagnostic-id', code: 'DIAGNOSTIC_ID_FACTORY_FAILED' }),
    expect.objectContaining({ actionId: 'frontend-health.record', code: 'HEALTH_SINK_FAILED' }),
    expect.objectContaining({ actionId: 'visible-action-failure.publish', code: 'VISIBLE_FAILURE_SINK_FAILED' }),
    expect.objectContaining({ actionId: 'ui-action.on-error', code: 'ON_ERROR_CALLBACK_FAILED' }),
  ]));
  expect(JSON.stringify(records)).not.toContain('raw ');
  const recordFor = (actionId) => records.find((record) => record.actionId === actionId);
  expect(diagnosticCauseForTest(recordFor('ui-action.diagnostic-id').diagnosticId)).toBe(causes.diagnostic);
  expect(diagnosticCauseForTest(recordFor('frontend-health.record').diagnosticId)).toBe(causes.health);
  expect(diagnosticCauseForTest(recordFor('visible-action-failure.publish').diagnosticId)).toBe(causes.visible);
  expect(diagnosticCauseForTest(recordFor('ui-action.on-error').diagnosticId)).toBe(causes.onError);
  expect(onError).toHaveBeenCalledTimes(1);
});

it('survives throwing health and visible sinks with finite observable Health records', () => {
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
    expect.objectContaining({ actionId: 'frontend-health.record', code: 'HEALTH_SINK_FAILED' }),
  ]));
});

it('does not create an unhandled rejection when async failure reporting uses the finite state', async () => {
  const unhandled = vi.fn();
  window.addEventListener('unhandledrejection', unhandled);
  runUIAction('fixture.no-unhandled', () => Promise.reject(new Error('raw async')), {
    diagnosticIdFactory: () => { throw new Error('raw factory'); },
    healthSink: () => { throw new Error('raw health'); },
    visibleFailureSink: () => { throw new Error('raw visible'); },
    onError: () => { throw new Error('raw onError'); },
  });
  await Promise.resolve();
  await Promise.resolve();
  expect(unhandled).not.toHaveBeenCalled();
  window.removeEventListener('unhandledrejection', unhandled);
});

it('makes each published retry intent one-shot so a successful side effect cannot repeat', async () => {
  const action = vi.fn()
    .mockRejectedValueOnce(new Error('raw first failure'))
    .mockResolvedValueOnce('done');
  const visibleFailureSink = vi.fn();
  runUIAction('fixture.retry-once', action, {
    diagnosticIdFactory: diagnosticIds('retry-once'),
    retryable: true,
    visibleFailureSink,
  });
  await Promise.resolve();
  const retry = visibleFailureSink.mock.calls[0][0].retry;

  await expect(retry()).resolves.toBe('done');
  expect(retry()).toBeUndefined();
  expect(action).toHaveBeenCalledTimes(2);
});
