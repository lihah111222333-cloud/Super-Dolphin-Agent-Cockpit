import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const healthSubscription = vi.hoisted(() => ({
  listeners: new Set(),
  unsubscribe: vi.fn(),
}));

vi.mock('../../shared/diagnostics/frontendHealthStore.js', async (importOriginal) => {
  const actual = await importOriginal();
  return {
    ...actual,
    subscribeFrontendHealth: vi.fn((listener) => {
      healthSubscription.listeners.add(listener);
      const unsubscribe = actual.subscribeFrontendHealth(listener);
      return () => {
        healthSubscription.listeners.delete(listener);
        healthSubscription.unsubscribe();
        unsubscribe();
      };
    }),
  };
});

import {
  frontendHealthStateSnapshot,
  recordFrontendHealth,
  resetFrontendHealthForTest,
  subscribeFrontendHealth,
} from '../../shared/diagnostics/frontendHealthStore.js';
import { FrontendHealthPanel } from './FrontendHealthPanel.jsx';

beforeEach(() => {
  window.localStorage.clear();
  resetFrontendHealthForTest();
  healthSubscription.listeners.clear();
  healthSubscription.unsubscribe.mockClear();
});

it('renders an explicit safe persistence failure state', () => {
  const originalStorage = window.localStorage;
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: {
      getItem: () => null,
      removeItem: () => undefined,
      setItem: () => { throw new Error('raw provider token=secret'); },
    },
  });
  recordFrontendHealth({
    actionId: 'fixture.persistence',
    publicError: {
      code: 'UI_ACTION_FAILED',
      title: '操作未完成',
      message: '操作失败，当前页面状态已保留。',
      diagnosticId: 'diagnostic-visible-persistence',
    },
  });
  expect(frontendHealthStateSnapshot().persistence.status).toBe('failed');

  render(<FrontendHealthPanel />);
  const alert = screen.getByRole('alert');
  expect(alert).toHaveTextContent('Health 持久化异常');
  expect(alert).not.toHaveTextContent('raw provider');
  Object.defineProperty(window, 'localStorage', { configurable: true, value: originalStorage });
});

afterEach(cleanup);

it('unsubscribes from Health updates after StrictMode unmount', () => {
  const { unmount } = render(
    <React.StrictMode>
      <FrontendHealthPanel />
    </React.StrictMode>,
  );

  expect(healthSubscription.listeners.size).toBe(1);
  expect(healthSubscription.unsubscribe).toHaveBeenCalledTimes(1);

  unmount();

  expect(healthSubscription.listeners.size).toBe(0);
  expect(healthSubscription.unsubscribe).toHaveBeenCalledTimes(2);

  const storeEmit = vi.fn();
  const stopObserving = subscribeFrontendHealth(storeEmit);
  recordFrontendHealth({
    actionId: 'fixture.after-unmount',
    publicError: {
      code: 'UI_ACTION_FAILED',
      title: '操作未完成',
      message: '操作失败，当前页面状态已保留。',
      diagnosticId: 'diagnostic-after-unmount',
    },
  });

  expect(storeEmit).toHaveBeenCalledTimes(1);
  expect(healthSubscription.listeners.size).toBe(1);
  stopObserving();
  expect(healthSubscription.listeners.size).toBe(0);
});

it('renders persistent safe Health fields and never exposes a raw error field', () => {
  const rawCause = 'provider payload token=secret /Users/private/path';
  recordFrontendHealth({
    actionId: 'prompt-history.previous',
    publicError: {
      code: 'PROMPT_HISTORY_UNAVAILABLE',
      title: '无法浏览提示历史',
      message: '提示历史暂时不可用，草稿与光标位置已保留。',
      diagnosticId: 'diagnostic-health-panel',
      rawCause,
    },
  });

  render(<FrontendHealthPanel />);
  const panel = screen.getByTestId('frontend-health-panel');
  expect(panel).toHaveTextContent('prompt-history.previous');
  expect(panel).toHaveTextContent('diagnostic-health-panel');
  expect(panel).not.toHaveTextContent(rawCause);
  expect(window.localStorage.getItem('super-dolphin.frontend-health.v1')).not.toContain(rawCause);

  fireEvent.click(screen.getByRole('button', { name: '清空' }));
  expect(panel).toHaveTextContent('当前没有操作失败记录');
});
