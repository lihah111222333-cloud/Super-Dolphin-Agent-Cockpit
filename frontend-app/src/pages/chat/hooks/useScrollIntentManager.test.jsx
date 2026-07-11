import React, { useEffect } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { useScrollIntentManager } from './useScrollIntentManager.js';

function installObserverDouble(name) {
  const original = globalThis[name];
  const instances = [];
  class ObserverDouble {
    constructor(callback) {
      this.callback = callback;
      this.disconnect = vi.fn();
      this.observe = vi.fn();
      instances.push(this);
    }
  }
  globalThis[name] = ObserverDouble;
  return {
    instances,
    restore: () => {
      if (original) globalThis[name] = original;
      else delete globalThis[name];
    },
  };
}

function ScrollIntentHarness({ activeThreadId = 'thread-a', autoScrollKey = 'content-a', onManager }) {
  const {
    markMessageSent,
    onTimelineKeyDown,
    onTimelineScroll,
    onTimelineTouchMove,
    onTimelineTouchStart,
    onTimelineWheel,
    scrollIfSticky,
    timelineRef,
  } = useScrollIntentManager({
    activeThreadId,
    autoScrollKey,
    timelineContentBlocked: false,
  });
  useEffect(() => {
    onManager({ markMessageSent, scrollIfSticky });
  }, [markMessageSent, onManager, scrollIfSticky]);
  return (
    <div
      data-testid="intent-timeline"
      ref={timelineRef}
      onKeyDown={onTimelineKeyDown}
      onScroll={onTimelineScroll}
      onTouchMove={onTimelineTouchMove}
      onTouchStart={onTimelineTouchStart}
      onWheel={onTimelineWheel}
    >
      <input data-testid="editable-target" />
    </div>
  );
}

function observedInstance(instances, target) {
  return instances.find((instance) => instance.observe.mock.calls.some(([observed]) => observed === target));
}

it('cancels its frame, observers, and load listener when the manager unmounts', () => {
  const mutation = installObserverDouble('MutationObserver');
  const resize = installObserverDouble('ResizeObserver');
  const requestAnimationFrameSpy = vi.spyOn(window, 'requestAnimationFrame').mockImplementation(() => 73);
  const cancelAnimationFrameSpy = vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
  let manager;
  let view;
  try {
    view = render(<ScrollIntentHarness onManager={(value) => { manager = value; }} />);
    const timeline = screen.getByTestId('intent-timeline');
    const mutationObserver = observedInstance(mutation.instances, timeline);
    const resizeObserver = observedInstance(resize.instances, timeline);
    expect(mutationObserver).toBeDefined();
    expect(resizeObserver).toBeDefined();

    requestAnimationFrameSpy.mockClear();
    manager.markMessageSent();
    expect(requestAnimationFrameSpy).toHaveBeenCalledTimes(1);

    view.unmount();
    expect(cancelAnimationFrameSpy).toHaveBeenCalledWith(73);
    expect(mutationObserver.disconnect).toHaveBeenCalledTimes(1);
    expect(resizeObserver.disconnect).toHaveBeenCalledTimes(1);
    requestAnimationFrameSpy.mockClear();
    fireEvent.load(timeline);
    expect(requestAnimationFrameSpy).not.toHaveBeenCalled();
  } finally {
    view?.unmount();
    mutation.restore();
    resize.restore();
    requestAnimationFrameSpy.mockRestore();
    cancelAnimationFrameSpy.mockRestore();
  }
});

it('uses one reading intent to gate load, mutation, resize, and streaming corrections', () => {
  const mutation = installObserverDouble('MutationObserver');
  const resize = installObserverDouble('ResizeObserver');
  let manager;
  let view;
  try {
    view = render(<ScrollIntentHarness onManager={(value) => { manager = value; }} />);
    const timeline = screen.getByTestId('intent-timeline');
    let scrollHeight = 1000;
    let scrollTop = 600;
    Object.defineProperty(timeline, 'clientHeight', { configurable: true, value: 400 });
    Object.defineProperty(timeline, 'scrollHeight', { configurable: true, get: () => scrollHeight });
    Object.defineProperty(timeline, 'scrollTop', {
      configurable: true,
      get: () => scrollTop,
      set: (value) => {
        scrollTop = Number(value);
      },
    });
    const mutationObserver = observedInstance(mutation.instances, timeline);
    const resizeObserver = observedInstance(resize.instances, timeline);

    fireEvent.wheel(timeline, { ctrlKey: false, deltaX: 0, deltaY: -40 });
    scrollHeight = 1400;
    mutationObserver.callback([]);
    resizeObserver.callback([]);
    fireEvent.load(timeline);
    manager.scrollIfSticky(false, 'streaming');
    expect(scrollTop).toBe(600);

    fireEvent.keyDown(timeline, { key: 'End' });
    manager.scrollIfSticky(false, 'streaming');
    expect(scrollTop).toBe(1400);
  } finally {
    view?.unmount();
    mutation.restore();
    resize.restore();
  }
});
