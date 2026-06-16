import { expect, it, vi } from 'vitest';
import { runUIAction } from './runUIAction.js';

it('reports synchronous UI action failures', () => {
  const onError = vi.fn();
  const logger = vi.fn();
  const error = new Error('sync boom');

  runUIAction(() => {
    throw error;
  }, { onError, logger });

  expect(onError).toHaveBeenCalledWith(error);
  expect(logger).toHaveBeenCalledWith('[frontend-app] UI action failed', error);
});

it('reports asynchronous UI action failures', async () => {
  const onError = vi.fn();
  const logger = vi.fn();
  const error = new Error('async boom');

  runUIAction(Promise.reject(error), { onError, logger });
  await Promise.resolve();

  expect(onError).toHaveBeenCalledWith(error);
  expect(logger).toHaveBeenCalledWith('[frontend-app] UI action failed', error);
});
