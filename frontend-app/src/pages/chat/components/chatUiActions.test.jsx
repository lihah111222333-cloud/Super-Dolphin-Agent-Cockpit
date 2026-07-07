import { expect, it, vi } from 'vitest';
import { runUIAction } from './chatUiActions.js';

it('reports synchronous chat UI action failures', () => {
  const onError = vi.fn();
  const logger = vi.fn();
  const error = new Error('chat sync boom');

  runUIAction(() => {
    throw error;
  }, { onError, logger });

  expect(onError).toHaveBeenCalledWith(error);
  expect(logger).toHaveBeenCalledWith('[frontend-app] UI action failed', error);
});

it('reports asynchronous chat UI action failures', async () => {
  const onError = vi.fn();
  const logger = vi.fn();
  const error = new Error('chat async boom');

  runUIAction(Promise.reject(error), { onError, logger });
  await Promise.resolve();

  expect(onError).toHaveBeenCalledWith(error);
  expect(logger).toHaveBeenCalledWith('[frontend-app] UI action failed', error);
});
