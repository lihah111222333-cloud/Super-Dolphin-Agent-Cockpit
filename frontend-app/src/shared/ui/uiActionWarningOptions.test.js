import { expect, it, vi } from 'vitest';
import { uiActionWarningOptions } from './uiActionWarningOptions.js';

it('adds the public error message to the fixed UI-action warning event', () => {
  const addWarning = vi.fn();

  uiActionWarningOptions({ addWarning }).onError({ message: '操作失败，当前页面状态已保留。' });

  expect(addWarning).toHaveBeenCalledWith('error', 'ui.action.failed', {
    error: '操作失败，当前页面状态已保留。',
  });
});

it('does not require a warning-capable store', () => {
  expect(() => uiActionWarningOptions().onError({ message: 'public failure' })).not.toThrow();
});
