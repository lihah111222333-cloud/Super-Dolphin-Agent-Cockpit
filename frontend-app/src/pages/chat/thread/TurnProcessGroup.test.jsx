import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { TurnProcessGroup } from './TurnProcessGroup.jsx';

const baseProps = {
  active: false,
  activeThreadId: 'thread-1',
  actions: {},
  copy: {
    processComplete: '执行过程',
    processRunning: '正在执行',
    processStepSingle: '条',
    processStepPlural: '条',
  },
  formatTime: () => '08:30',
  messages: [
    { id: 'assistant-1', role: 'assistant', text: 'result', done: true },
  ],
  onScrollIfSticky: () => {},
  smoothStreaming: false,
};

describe('TurnProcessGroup', () => {
  it('is collapsed by default and reveals process messages on demand', () => {
    render(<TurnProcessGroup {...baseProps} />);

    const group = screen.getByTestId('turn-process-group');
    expect(group).not.toHaveAttribute('open');
    expect(screen.getByText('执行过程 · 1 条')).toBeInTheDocument();

    fireEvent.click(group.querySelector('summary'));

    expect(group).toHaveAttribute('open');
    expect(screen.getByText('result')).toBeInTheDocument();
    expect(group.querySelector('.chat-bubble-avatar')).toBeNull();
    expect(within(group).queryByRole('button', { name: /复制/ })).not.toBeInTheDocument();
    expect(within(group).getByText('08:30')).toBeInTheDocument();
  });

  it('uses the running summary without opening the disclosure', () => {
    render(<TurnProcessGroup {...baseProps} active />);

    expect(screen.getByTestId('turn-process-group')).not.toHaveAttribute('open');
    expect(screen.getByText('正在执行 · 1 条')).toBeInTheDocument();
  });

  it('renders every message without a React key warning when provider identities repeat', () => {
    const consoleError = vi.spyOn(console, 'error');
    try {
      render(<TurnProcessGroup {...baseProps} messages={[
        { id: 'tool-duplicated', callId: 'call-duplicated', role: 'assistant', kind: 'tool', title: 'first', text: 'first result', done: true },
        { id: 'tool-duplicated', callId: 'call-duplicated', role: 'assistant', kind: 'tool', title: 'second', text: 'second result', done: true },
      ]} />);

      const group = screen.getByTestId('turn-process-group');
      fireEvent.click(group.querySelector('summary'));

      expect(screen.getByText('执行过程 · 2 条')).toBeInTheDocument();
      const firstResult = screen.getByText('first result');
      const secondResult = screen.getByText('second result');
      expect(firstResult).toBeInTheDocument();
      expect(secondResult).toBeInTheDocument();
      expect(firstResult.compareDocumentPosition(secondResult) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
      expect(consoleError.mock.calls.flat().join(' ')).not.toContain('Encountered two children with the same key');
    } finally {
      consoleError.mockRestore();
    }
  });
});
