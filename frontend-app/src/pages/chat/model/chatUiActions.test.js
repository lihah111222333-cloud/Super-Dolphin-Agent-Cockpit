import { expect, it, vi } from 'vitest';
import { runUIAction } from './chatUiActions.js';

it('reports synchronous chat UI action failures', () => {
  const onError = vi.fn();
  const logger = vi.fn();
  const error = new Error('chat sync boom');

  runUIAction('chat.fixture.sync', () => {
    throw error;
  }, {
    healthSink: logger,
    visibleFailureSink: ({ publicError }) => onError(publicError),
  });

  expect(onError).toHaveBeenCalledWith(expect.objectContaining({ code: 'UI_ACTION_FAILED' }));
  expect(logger).toHaveBeenCalledWith(expect.objectContaining({ actionId: 'chat.fixture.sync' }));
});

it('reports asynchronous chat UI action failures', async () => {
  const onError = vi.fn();
  const logger = vi.fn();
  const error = new Error('chat async boom');

  runUIAction('chat.fixture.async', () => Promise.reject(error), {
    healthSink: logger,
    visibleFailureSink: ({ publicError }) => onError(publicError),
  });
  await Promise.resolve();

  expect(onError).toHaveBeenCalledWith(expect.objectContaining({ code: 'UI_ACTION_FAILED' }));
  expect(logger).toHaveBeenCalledWith(expect.objectContaining({ actionId: 'chat.fixture.async' }));
});
