import { expect, it, vi } from 'vitest';
import { runUIAction } from './runUIAction.js';

function sharedFailureOptions() {
  return {
    diagnosticIdFactory: () => 'shared-interrupt',
    healthSink: vi.fn(),
    visibleFailureSink: vi.fn(),
  };
}

it('reports one failure when UI entry points share one rejected action promise', async () => {
  const options = sharedFailureOptions();
  const sharedFailure = Promise.reject(new Error('shared interrupt timeout'));

  expect(runUIAction('thread.interrupt', () => sharedFailure, options)).toBe(sharedFailure);
  expect(runUIAction('thread.interrupt', () => sharedFailure, options)).toBe(sharedFailure);
  await Promise.allSettled([sharedFailure]);
  await Promise.resolve();

  expect(options.healthSink).toHaveBeenCalledTimes(1);
  expect(options.visibleFailureSink).toHaveBeenCalledTimes(1);
});

it('reports one failure when UI entry points share one false action promise', async () => {
  const options = { ...sharedFailureOptions(), rejectFalse: true };
  const sharedFailure = Promise.resolve(false);

  expect(runUIAction('thread.interrupt', () => sharedFailure, options)).toBe(sharedFailure);
  expect(runUIAction('thread.interrupt', () => sharedFailure, options)).toBe(sharedFailure);
  await Promise.allSettled([sharedFailure]);
  await Promise.resolve();

  expect(options.healthSink).toHaveBeenCalledTimes(1);
  expect(options.visibleFailureSink).toHaveBeenCalledTimes(1);
});

it('reports a shared rejected promise once for each distinct UI action', async () => {
  const firstOptions = sharedFailureOptions();
  const secondOptions = sharedFailureOptions();
  const sharedFailure = Promise.reject(new Error('shared provider failure'));

  runUIAction('fixture.first', () => sharedFailure, firstOptions);
  runUIAction('fixture.second', () => sharedFailure, secondOptions);
  await Promise.allSettled([sharedFailure]);
  await Promise.resolve();

  expect(firstOptions.healthSink).toHaveBeenCalledTimes(1);
  expect(firstOptions.visibleFailureSink).toHaveBeenCalledTimes(1);
  expect(secondOptions.healthSink).toHaveBeenCalledTimes(1);
  expect(secondOptions.visibleFailureSink).toHaveBeenCalledTimes(1);
});
