import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import React, { useState } from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { frontendHealthSnapshot, resetFrontendHealthForTest } from '../../../shared/diagnostics/frontendHealthStore.js';
import { resetVisibleActionFailureForTest } from '../../../shared/ui/actionFailureSink.js';
import { ActionFailureSink } from '../../../shared/ui/actionFailureSink.jsx';
import { ComposerDock } from './ComposerDock.jsx';

function composer() {
  return {
    activePreview: null,
    dropActive: false,
    handleCompositionEnd: vi.fn(),
    handleCompositionStart: vi.fn(),
    handleDragEnter: vi.fn(),
    handleDragLeave: vi.fn(),
    handleDragOver: vi.fn(),
    handleDrop: vi.fn(),
    handlePaste: vi.fn(),
    isComposing: vi.fn(() => false),
    previewAttachmentItem: vi.fn(),
    removeAttachmentItem: vi.fn(),
    setPreviewAttachment: vi.fn(),
  };
}

function store() {
  return {
    composerCapabilities: [],
    forkDraft: { open: false },
    hasInterruptibleThreadAction: vi.fn(() => false),
    interruptActiveThread: vi.fn(),
    provider: 'codex',
    providerConfig: { provider: 'codex', model: 'gpt-5.5', effort: 'xhigh' },
    reconcileComposerCapabilities: vi.fn(),
    removeComposerCapability: vi.fn(),
    threadConfigByThread: {},
    threadConfigLoadingByThread: {},
    threadConfigSaving: false,
    threads: [],
  };
}

function promptPage(text = 'older prompt') {
  return {
    entries: [{ threadId: 'thread-1', messageId: 'message-1', text, createdAt: '2026-07-17T10:00:00Z' }],
    nextCursor: '',
    hasMore: false,
    nonce: 'nonce-1',
  };
}

function renderHarness({ fetchPromptHistory, initialDraft = 'draft kept', sendMessage = vi.fn(), setDraftEffect }) {
  const setDraftSpy = vi.fn();
  const composerValue = composer();
  const storeValue = store();
  function Harness() {
    const [draft, setDraftState] = useState(initialDraft);
    const setDraft = (value) => {
      setDraftSpy(value);
      if (setDraftEffect) setDraftEffect(value);
      setDraftState(value);
    };
    return (
      <>
        <ComposerDock
          attachments={[]}
          composer={composerValue}
          draft={draft}
          fetchPromptHistory={fetchPromptHistory}
          modelThreadId="thread-1"
          projectPath="/repo"
          selectFiles={vi.fn()}
          sendMessage={sendMessage}
          sending={false}
          setDraft={setDraft}
          store={storeValue}
        />
        <ActionFailureSink />
      </>
    );
  }
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={queryClient}><Harness /></QueryClientProvider>);
  return { sendMessage, setDraftSpy };
}

function navigatePrevious(textarea) {
  textarea.setSelectionRange(3, 3);
  fireEvent.keyDown(textarea, { key: 'ArrowUp' });
}

beforeEach(() => {
  window.localStorage.clear();
  resetFrontendHealthForTest();
  resetVisibleActionFailureForTest();
});

afterEach(() => {
  cleanup();
  resetVisibleActionFailureForTest();
});

describe('matrix:FM-21 layer:frontend', () => {
  it('keeps draft cursor and selection stable on rejected prompt history RPC', async () => {
    const rawCause = 'provider raw cause token=secret';
    renderHarness({ fetchPromptHistory: vi.fn().mockRejectedValue(new Error(rawCause)) });
    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    navigatePrevious(textarea);

    const alert = await screen.findByTestId('global-action-failure');
    expect(textarea).toHaveValue('draft kept');
    expect(textarea.selectionStart).toBe(3);
    expect(textarea.selectionEnd).toBe(3);
    expect(alert).toHaveTextContent('提示历史暂时不可用');
    expect(alert).not.toHaveTextContent(rawCause);
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'prompt-history.previous', occurrences: 1 }),
    ]));
    expect(window.localStorage.getItem('super-dolphin.frontend-health.v1')).not.toContain(rawCause);
  });
});

describe('matrix:FM-22 layer:frontend', () => {
  it('keeps draft cursor and selection stable on invalid prompt history response', async () => {
    renderHarness({ fetchPromptHistory: vi.fn().mockResolvedValue({ entries: null, nonce: 'raw-invalid-response' }) });
    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    navigatePrevious(textarea);

    const alert = await screen.findByTestId('global-action-failure');
    expect(textarea).toHaveValue('draft kept');
    expect(textarea.selectionStart).toBe(3);
    expect(textarea.selectionEnd).toBe(3);
    expect(alert).not.toHaveTextContent('raw-invalid-response');
  });
});

it('keeps next navigation committable and retries the same draft intent', async () => {
  let draftFailurePending = true;
  const setDraftEffect = vi.fn((value) => {
    if (value === 'draft kept' && draftFailurePending) {
      draftFailurePending = false;
      throw new Error('raw next failure');
    }
  });
  const { sendMessage } = renderHarness({ fetchPromptHistory: vi.fn().mockResolvedValue(promptPage()), setDraftEffect });
  const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
  navigatePrevious(textarea);
  await waitFor(() => expect(textarea).toHaveValue('older prompt'));
  textarea.setSelectionRange(textarea.value.length, textarea.value.length);
  fireEvent.keyDown(textarea, { key: 'ArrowDown' });

  const alert = await screen.findByTestId('global-action-failure');
  expect(textarea).toHaveValue('older prompt');
  expect(textarea.selectionStart).toBe('older prompt'.length);
  expect(alert).not.toHaveTextContent('raw next failure');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'prompt-history.next' }),
  ]));
  fireEvent.click(screen.getByRole('button', { name: '重试' }));
  await waitFor(() => expect(textarea).toHaveValue('draft kept'));
  expect(sendMessage).not.toHaveBeenCalled();
  expect(alert).not.toBeInTheDocument();
});

it('retries only the failed prompt history intent without sending', async () => {
  const fetchPromptHistory = vi.fn()
    .mockRejectedValueOnce(new Error('raw transient RPC failure'))
    .mockResolvedValueOnce(promptPage('retried prompt'));
  const { sendMessage, setDraftSpy } = renderHarness({ fetchPromptHistory });
  const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
  navigatePrevious(textarea);
  const alert = await screen.findByTestId('global-action-failure');
  fireEvent.click(screen.getByRole('button', { name: '重试' }));

  await waitFor(() => expect(textarea).toHaveValue('retried prompt'));
  expect(fetchPromptHistory).toHaveBeenCalledTimes(2);
  expect(setDraftSpy).toHaveBeenCalledTimes(1);
  expect(sendMessage).not.toHaveBeenCalled();
  expect(alert).not.toBeInTheDocument();
});
