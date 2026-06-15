import { describe, expect, it } from 'vitest';
import { chatHeaderFeedbackForStore } from './chatHeaderModel.js';

describe('chatHeaderFeedbackForStore', () => {
  it('prefers explicit action notices', () => {
    expect(chatHeaderFeedbackForStore({
      actionNotice: { message: 'Saved', tone: 'success' },
      bootstrapStatus: 'failed',
      error: 'network',
    })).toEqual({ message: 'Saved', tone: 'success' });
  });

  it('reports backend connection failures', () => {
    expect(chatHeaderFeedbackForStore({ bootstrapStatus: 'failed', error: 'offline' })).toEqual({
      message: '\u8fde\u63a5\u540e\u7aef\u5931\u8d25\uff1aoffline',
      tone: 'error',
    });
  });

  it('returns null when there is no feedback', () => {
    expect(chatHeaderFeedbackForStore({ bootstrapStatus: 'ready' })).toBeNull();
  });
});
