import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ExecutionPlan, ReasoningTrace } from './ChatReasoningTrace.jsx';
import {
  durationLabelFromMs,
  isReasoningMessage,
  parsePlanItems,
  syntheticReasoningMessage,
} from './chatReasoningModel.js';

describe('ChatReasoningTrace', () => {
  it('parses mixed markdown plan markers into done and pending items', () => {
    expect(parsePlanItems([
      'Plan',
      '- [x] read files',
      '- [ ] update tests',
      '✅ 3. verify build',
    ].join('\n'))).toEqual([
      { text: 'read files', done: true },
      { text: 'update tests', done: false },
      { text: 'verify build', done: true },
    ]);
  });

  it('renders execution plan progress and item states', () => {
    const { container } = render(
      <ExecutionPlan
        message={{
          kind: 'plan',
          text: '- [x] read files\n- [ ] update tests',
        }}
      />
    );

    expect(screen.getByRole('region', { name: 'AI 执行计划' })).toHaveTextContent('已完成 1/2 项任务');
    expect(screen.getAllByRole('listitem')).toHaveLength(2);
    expect(container.querySelector('[data-plan-status="done"]')).toHaveTextContent('read files');
    expect(container.querySelector('[data-plan-status="pending"]')).toHaveTextContent('update tests');
  });

  it('renders active reasoning status and fallback body text', () => {
    render(<ReasoningTrace message={{ kind: 'tool', done: false, time: '2026-06-15T08:00:00Z' }} active />);

    expect(screen.getByLabelText('AI 思考记录')).toHaveTextContent('正在运行 调用工具');
    expect(screen.getByText('正在调用工具并等待返回结果。')).toBeInTheDocument();
  });
});

describe('chatReasoningModel', () => {
  it('builds synthetic active reasoning messages only while work is pending', () => {
    expect(syntheticReasoningMessage({ activeTurn: null, sending: false, isBusy: false })).toBeNull();
    expect(syntheticReasoningMessage({
      activeTurn: { id: 'turn-1', startedAt: '2026-06-15T08:00:00Z' },
      sending: false,
      isBusy: false,
    })).toEqual(expect.objectContaining({
      done: false,
      id: 'thinking:turn-1',
      kind: 'thinking',
      time: '2026-06-15T08:00:00Z',
    }));
  });

  it('classifies reasoning kinds and formats durations', () => {
    expect(isReasoningMessage({ kind: 'command' })).toBe(true);
    expect(isReasoningMessage({ kind: 'approval' })).toBe(false);
    expect(durationLabelFromMs(65_000)).toBe('1m 5s');
  });
});
