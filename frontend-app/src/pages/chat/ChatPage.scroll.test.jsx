import React from 'react';
import { act, createEvent, fireEvent, render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { TestChatPage, createActiveThreadStore } from './__tests__/chatPageTestSupport.js';

it('adjusts scroll to bottom when a child resource loads and stickiness is enabled', () => {
  const store = createActiveThreadStore([
    { id: 'msg-1', role: 'user', text: '图片加载测试', time: '2026-06-02T08:00:00Z' },
  ]);
  render(<TestChatPage store={store} projectPath="/repo/app" />);

  const timeline = screen.getByTestId('chat-timeline');
  let scrollTop = 500;
  Object.defineProperty(timeline, 'clientHeight', { configurable: true, value: 400 });
  Object.defineProperty(timeline, 'scrollHeight', { configurable: true, value: 1500 });
  Object.defineProperty(timeline, 'scrollTop', {
    configurable: true,
    get: () => scrollTop,
    set: (val) => {
      scrollTop = val;
    },
  });

  const img = document.createElement('img');
  timeline.appendChild(img);

  const loadEvent = createEvent('load', img, {
    bubbles: false,
    cancelable: true,
  });

  act(() => {
    fireEvent(img, loadEvent);
  });

  expect(scrollTop).toBe(1500);
});

it('does not adjust scroll to bottom when a child resource loads but stickiness is disabled', () => {
  const store = createActiveThreadStore([
    { id: 'msg-1', role: 'user', text: '图片加载测试无粘性', time: '2026-06-02T08:00:00Z' },
  ]);
  render(<TestChatPage store={store} projectPath="/repo/app" />);

  const timeline = screen.getByTestId('chat-timeline');
  let scrollTop = 500;
  Object.defineProperty(timeline, 'clientHeight', { configurable: true, value: 400 });
  Object.defineProperty(timeline, 'scrollHeight', { configurable: true, value: 1500 });
  Object.defineProperty(timeline, 'scrollTop', {
    configurable: true,
    get: () => scrollTop,
    set: (val) => {
      scrollTop = val;
    },
  });

  act(() => {
    fireEvent.scroll(timeline);
  });

  const img = document.createElement('img');
  timeline.appendChild(img);

  const loadEvent = createEvent('load', img, {
    bubbles: false,
    cancelable: true,
  });

  act(() => {
    fireEvent(img, loadEvent);
  });

  expect(scrollTop).toBe(500);
});

it('resets scrollTop to 0 when activeThreadId changes to prevent out-of-bounds rendering glitch', () => {
  const store1 = createActiveThreadStore([
    { id: 'msg-1', role: 'user', text: 'Thread 1 message', time: '2026-06-02T08:00:00Z' },
  ], { activeThreadId: 'thread-1' });

  let setScrollTopValue = null;
  const originalScrollTopDesc = Object.getOwnPropertyDescriptor(HTMLDivElement.prototype, 'scrollTop');

  Object.defineProperty(HTMLDivElement.prototype, 'scrollTop', {
    configurable: true,
    get() {
      return 500;
    },
    set(val) {
      setScrollTopValue = val;
    },
  });

  try {
    const { rerender } = render(<TestChatPage store={store1} projectPath="/repo/app" />);

    const store2 = createActiveThreadStore([], {
      activeThreadId: 'thread-2',
      threadStateLoadingByThread: { 'thread-2': true },
      threadTimelineReadyByThread: { 'thread-2': false },
    });

    act(() => {
      rerender(<TestChatPage store={store2} projectPath="/repo/app" />);
    });

    expect(setScrollTopValue).toBe(0);
  }
  finally {
    if (originalScrollTopDesc) {
      Object.defineProperty(HTMLDivElement.prototype, 'scrollTop', originalScrollTopDesc);
    }
    else {
      delete HTMLDivElement.prototype.scrollTop;
    }
  }
});
