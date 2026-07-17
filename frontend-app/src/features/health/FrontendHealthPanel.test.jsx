import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it } from 'vitest';
import {
  recordFrontendHealth,
  resetFrontendHealthForTest,
  retainDiagnosticCause,
} from '../../shared/diagnostics/frontendHealthStore.js';
import { FrontendHealthPanel } from './FrontendHealthPanel.jsx';

beforeEach(() => {
  window.localStorage.clear();
  resetFrontendHealthForTest();
});

afterEach(cleanup);

it('renders persistent safe Health fields and never exposes the retained raw cause', () => {
  const rawCause = 'provider payload token=secret /Users/private/path';
  retainDiagnosticCause('diagnostic-health-panel', new Error(rawCause));
  recordFrontendHealth({
    actionId: 'prompt-history.previous',
    publicError: {
      code: 'PROMPT_HISTORY_UNAVAILABLE',
      title: '无法浏览提示历史',
      message: '提示历史暂时不可用，草稿与光标位置已保留。',
      diagnosticId: 'diagnostic-health-panel',
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
