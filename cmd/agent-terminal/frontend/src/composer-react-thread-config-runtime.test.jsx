import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { ComposerBar } from './components/ComposerBar.jsx';

vi.mock('./services/api.js', () => ({
  onFilesDropped: () => () => {},
}));

function createComposer() {
  return {
    state: { text: '', attachments: [], attaching: false },
    canSend: { value: false },
    attachByPicker() {},
    handleDrop() {},
    handlePaste() {},
    removeAttachment() {},
    attachByPaths() {
      return 0;
    },
  };
}

afterEach(() => {
  cleanup();
});

describe('ComposerBar React thread config runtime', () => {
  it('unwraps Vue computed values before rendering thread config labels', () => {
    render(
      <ComposerBar
        composer={createComposer()}
        threadId="thread-1"
        threadConfigProvider="codex"
        threadConfigSupportsOverride={true}
        threadConfigMeta={{
          override: {},
          effective: { model: 'gpt-5.5', effort: 'xhigh' },
        }}
      />,
    );

    expect(screen.getByLabelText('线程执行配置').textContent).toContain('GPT-5.5 · xhigh');
  });
});
