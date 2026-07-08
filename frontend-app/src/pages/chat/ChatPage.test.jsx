import React from 'react';
import { describe, expect, it } from 'vitest';
import { TestChatPageWrapper } from './__tests__/chatPageTestSupport.js';

describe('ChatPage module', () => {
  it('exports the chat page component', () => {
    expect(TestChatPageWrapper).toBeTypeOf('function');
  });
});
