import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';

vi.mock('./composables/useComposerDragDrop.js', () => ({
  useComposerDragDrop: () => ({
    dropActive: false,
    resetDropState: vi.fn(),
    bindNativeFileDrop: vi.fn(),
    unbindNativeFileDrop: vi.fn(),
    onDragEnter: vi.fn(),
    onDragOver: vi.fn(),
    onDragLeave: vi.fn(),
    onDrop: vi.fn(),
  }),
}));

vi.mock('./composables/useComposerInterrupt.js', () => ({
  useComposerInterrupt: (props, emit, controls) => ({
    pauseAcknowledged: false,
    setPauseAcknowledged: vi.fn(),
    interruptPending: false,
    resetInterruptState: vi.fn(),
    isPauseMode: () => false,
    onPrimaryAction: controls.onSend,
    onEscape: vi.fn(),
  }),
}));

vi.mock('./composables/useComposerThreadConfig.js', () => ({
  useComposerThreadConfig: () => ({
    threadConfigOpen: false,
    threadConfigWrapRef: { current: null },
    threadConfigTriggerRef: { current: null },
    threadConfigDropdownStyle: {},
    threadConfigVisible: false,
    threadConfigEditable: false,
    threadConfigInherited: true,
    threadConfigSummaryLabel: '',
    threadConfigInheritModelLabel: '',
    threadConfigInheritEffortLabel: '',
    toggleThreadConfig: vi.fn(),
    onThreadConfigClickOutside: vi.fn(),
    restoreThreadConfig: vi.fn(),
    onModelSelectChange: vi.fn(),
    onEffortSelectChange: vi.fn(),
    threadConfigModelOptions: [],
    threadConfigEffortOptions: [],
  }),
}));

vi.mock('./composables/useComposerTextarea.js', () => ({
  useComposerTextarea: () => ({
    isComposing: false,
    syncComposerInputHeight: vi.fn(),
    setComposerInputRef: vi.fn(),
    onInput: vi.fn(),
    onCompositionStart: vi.fn(),
    onCompositionEnd: vi.fn(),
  }),
}));

import { ComposerBar } from './components/ComposerBar.jsx';
import { useComposerStore, composerStoreVanilla, closeForkDraft } from './stores/composer.js';

function resetComposerState() {
  composerStoreVanilla.setState({
    text: '',
    attachments: [],
    attaching: false,
    forkDraft: {
      active: false,
      sharedFilePaths: [],
      origin: '',
    },
  });
}

function createVueComposerStore() {
  window.__VUE_SETUP_ACTIVE__ = true;
  const store = useComposerStore();
  delete window.__VUE_SETUP_ACTIVE__;
  return store;
}

afterEach(() => {
  cleanup();
  closeForkDraft();
  resetComposerState();
  delete window.__VUE_SETUP_ACTIVE__;
});

describe('Composer React runtime', () => {
  it('treats Vue computed refs as booleans when deciding whether send is enabled', () => {
    const onSend = vi.fn();
    const composer = {
      state: { text: '', attachments: [], attaching: false },
      canSend: { value: false },
      attachByPicker: vi.fn(),
      handlePaste: vi.fn(),
      removeAttachment: vi.fn(),
    };

    render(<ComposerBar composer={composer} onSend={onSend} />);

    const sendButton = screen.getByTestId('composer-send-button');
    expect(sendButton.disabled).toBe(true);

    fireEvent.click(sendButton);
    expect(onSend).not.toHaveBeenCalled();
  });

  it('subscribes to composer store changes when rendered with a Vue-context composer proxy', () => {
    const composer = createVueComposerStore();

    render(<ComposerBar composer={composer} onSend={vi.fn()} />);

    const input = screen.getByTestId('composer-input');
    expect(input.value).toBe('');

    act(() => {
      composerStoreVanilla.setState({ text: 'hello' });
    });

    expect(screen.getByTestId('composer-input').value).toBe('hello');
  });

  it('exposes live fork draft state from the Vue-context composer store', () => {
    const composer = createVueComposerStore();

    composer.openForkDraft({ sharedFilePath: '/tmp/shared.md', origin: 'test' });

    expect(composer.forkDraft.active).toBe(true);
    expect(composer.forkDraft.sharedFilePaths).toEqual(['/tmp/shared.md']);
  });
});
