import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
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
    { id: 'tool-1', role: 'assistant', kind: 'tool', title: 'grep', text: 'result', done: true },
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
  });

  it('uses the running summary without opening the disclosure', () => {
    render(<TurnProcessGroup {...baseProps} active />);

    expect(screen.getByTestId('turn-process-group')).not.toHaveAttribute('open');
    expect(screen.getByText('正在执行 · 1 条')).toBeInTheDocument();
  });
});
