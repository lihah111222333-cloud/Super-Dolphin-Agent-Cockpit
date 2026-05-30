// @ts-nocheck
import { describe, expect, it } from 'vitest';

import {
  THREAD_STORE_UI_LOCAL_STATE_WHITELIST,
  THREAD_STORE_RUNTIME_STATE_KEYS,
  THREAD_STORE_STATE_WHITELIST,
  getUnexpectedThreadStoreStateKeys,
  assertThreadStoreStateWhitelist,
} from './stores/thread-state-whitelist.js';

describe('thread-state-whitelist', () => {
  it('exports the expected whitelist buckets', () => {
    expect(THREAD_STORE_UI_LOCAL_STATE_WHITELIST).toContain('activeThreadId');
    expect(THREAD_STORE_UI_LOCAL_STATE_WHITELIST).toContain('sendBlockedNoticesByThread');
    expect(THREAD_STORE_UI_LOCAL_STATE_WHITELIST).toContain('sendHoldNoticesByThread');
    expect(THREAD_STORE_RUNTIME_STATE_KEYS).toContain('threads');
    expect(THREAD_STORE_STATE_WHITELIST).toContain('activeThreadId');
    expect(THREAD_STORE_STATE_WHITELIST).not.toContain('threads');
  });

  it('detects unexpected root-state keys', () => {
    expect(getUnexpectedThreadStoreStateKeys({ activeThreadId: '', rogue: true })).toEqual(['rogue']);
    expect(getUnexpectedThreadStoreStateKeys({ activeThreadId: '', activeCmdThreadId: '' })).toEqual([]);
  });

  it('throws with context when root state contains non-whitelisted keys', () => {
    expect(() => assertThreadStoreStateWhitelist({ activeThreadId: '', rogue: true }, 'unit-test')).toThrow(
      '[unit-test] unexpected thread store state keys: rogue',
    );
    expect(() => assertThreadStoreStateWhitelist({ activeThreadId: '', activeCmdThreadId: '' }, 'unit-test')).not.toThrow();
  });
});
