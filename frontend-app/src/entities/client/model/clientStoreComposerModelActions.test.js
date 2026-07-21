import { describe, expect, it, vi } from 'vitest';

vi.mock('../../../shared/api/backendApi.js', () => ({
  setPreference: vi.fn(),
  setThreadConfig: vi.fn(),
}));

import { setThreadConfig } from '../../../shared/api/backendApi.js';
import { saveThreadComposerModelConfig } from './helpers/a1/send/clientStoreComposerModelActions.js';

describe('clientStoreComposerModelActions', () => {
  it('keeps the model-save failure visible and records a warning', async () => {
    const failure = new Error('backend unavailable');
    const set = vi.fn();
    const addWarning = vi.fn();
    setThreadConfig.mockRejectedValueOnce(failure);

    await expect(saveThreadComposerModelConfig({
      threadId: 'thread-1',
      threadConfig: {
        provider: 'codex',
        override: { model: '', effort: '' },
      },
      hasModel: true,
      hasEffort: true,
      nextModel: 'gpt-5',
      nextEffort: 'high',
    }, set, addWarning)).rejects.toThrow(failure);

    expect(set).toHaveBeenNthCalledWith(1, { threadConfigSaving: true });
    expect(set).toHaveBeenNthCalledWith(2, {
      threadConfigSaving: false,
      actionNotice: expect.objectContaining({
        message: '线程配置保存失败，请重试。',
        tone: 'error',
      }),
    });
    expect(addWarning).toHaveBeenCalledWith('error', 'thread.config.set.failed', {
      threadId: 'thread-1',
      error: 'action failure; see Health diagnostic ID',
    });
  });
});
