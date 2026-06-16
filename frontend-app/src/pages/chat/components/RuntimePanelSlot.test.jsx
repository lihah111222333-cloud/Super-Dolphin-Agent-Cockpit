import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { RuntimePanelSlot } from './RuntimePanelSlot.jsx';

const threadData = {
  diffText: 'diff --git a/readme.md b/readme.md\n+hello',
  tokenUsage: { usedTokens: 10, contextWindowTokens: 100, usedPercent: 10 },
  activityStats: { commands: 1 },
  warnings: [],
  runtimeResults: [],
};

function renderSlot(overrides = {}) {
  const props = {
    beginResize: vi.fn(),
    codeFileActions: {
      locateCodeFile: vi.fn(),
      openCodeFile: vi.fn(),
      saveCodeFile: vi.fn(),
    },
    closeThreshold: 0,
    formatTime: (value) => value,
    handleKeyDown: vi.fn(),
    maxWidth: 320,
    open: true,
    projectPath: '/repo/app',
    projects: ['/repo/app'],
    renderMarkdownPreview: (content) => <pre>{content}</pre>,
    threadData,
    width: 240,
    ...overrides,
  };
  return { props, ...render(<RuntimePanelSlot {...props} />) };
}

describe('RuntimePanelSlot', () => {
  it('does not render when the right panel is closed', () => {
    renderSlot({ open: false });

    expect(screen.queryByTestId('right-panel-resizer')).toBeNull();
    expect(screen.queryByTestId('runtime-panel')).toBeNull();
  });

  it('renders the right panel shell and wires resize events', () => {
    const { props } = renderSlot();
    const resizer = screen.getByTestId('right-panel-resizer');

    expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
    expect(resizer).toHaveAttribute('aria-valuemin', '0');
    expect(resizer).toHaveAttribute('aria-valuemax', '320');
    expect(resizer).toHaveAttribute('aria-valuenow', '240');

    fireEvent.keyDown(resizer, { key: 'ArrowLeft' });
    fireEvent.pointerDown(resizer, { pointerId: 1, clientX: 0 });

    expect(props.handleKeyDown).toHaveBeenCalledTimes(1);
    expect(props.beginResize).toHaveBeenCalledTimes(1);
  });
});
