import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it } from 'vitest';
import {
  frontendHealthStateSnapshot,
  recordFrontendHealth,
  resetFrontendHealthForTest,
} from '../../shared/diagnostics/frontendHealthStore.js';
import { FrontendHealthPanel } from './FrontendHealthPanel.jsx';

beforeEach(() => {
  window.localStorage.clear();
  resetFrontendHealthForTest();
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
