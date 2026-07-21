import { act, renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useRuntimePanelLayout } from './useRuntimePanelLayout.js';

function startResize(result, inputType) {
  act(() => {
    result.current.beginActivityPanelResize({
      clientY: 400,
      currentTarget: { setPointerCapture: vi.fn() },
      pointerId: 1,
      preventDefault: vi.fn(),
    }, inputType);
  });
}

function renderRuntimePanelLayout() {
  const panel = document.createElement('aside');
  panel.className = 'runtime-panel';
  document.body.append(panel);
  const hook = renderHook(() => useRuntimePanelLayout());
  panel.style.setProperty('--activity-panel-height', `${hook.result.current.activityPanelHeight}px`);
  return { panel, ...hook };
}

function movePointer(clientY) {
  act(() => {
    window.dispatchEvent(new MouseEvent('pointermove', { clientY }));
  });
}

afterEach(() => {
  document.querySelectorAll('.runtime-panel').forEach((panel) => panel.remove());
  vi.restoreAllMocks();
});

describe('useRuntimePanelLayout', () => {
  it('restores the committed geometry and removes active pointer listeners on unmount', () => {
    const removeEventListener = vi.spyOn(window, 'removeEventListener');
    const { panel, result, unmount } = renderRuntimePanelLayout();
    const committedHeight = panel.style.getPropertyValue('--activity-panel-height');

    startResize(result, 'pointer');
    movePointer(300);
    expect(panel.style.getPropertyValue('--activity-panel-height')).not.toBe(committedHeight);
    unmount();
    window.dispatchEvent(new Event('pointerup'));

    expect(panel.style.getPropertyValue('--activity-panel-height')).toBe(committedHeight);
    expect(removeEventListener).toHaveBeenCalledWith('pointermove', expect.any(Function));
    expect(removeEventListener).toHaveBeenCalledWith('pointerup', expect.any(Function));
    expect(removeEventListener).toHaveBeenCalledWith('pointercancel', expect.any(Function));
    expect(removeEventListener).toHaveBeenCalledWith('blur', expect.any(Function));
  });

  it('uses the same idempotent disposer for pointer cancel and a subsequent release', () => {
    const removeEventListener = vi.spyOn(window, 'removeEventListener');
    const { panel, result } = renderRuntimePanelLayout();
    const committedHeight = panel.style.getPropertyValue('--activity-panel-height');

    startResize(result, 'pointer');
    movePointer(300);
    act(() => {
      window.dispatchEvent(new Event('pointercancel'));
      window.dispatchEvent(new Event('pointerup'));
    });

    expect(panel.style.getPropertyValue('--activity-panel-height')).toBe(committedHeight);
    expect(result.current.activityPanelHeight).toBe(Number.parseInt(committedHeight, 10));
    expect(removeEventListener.mock.calls.filter(([eventName]) => eventName === 'pointermove')).toHaveLength(1);
    expect(removeEventListener.mock.calls.filter(([eventName]) => eventName === 'pointerup')).toHaveLength(1);
    expect(removeEventListener.mock.calls.filter(([eventName]) => eventName === 'pointercancel')).toHaveLength(1);
    expect(removeEventListener.mock.calls.filter(([eventName]) => eventName === 'blur')).toHaveLength(1);
  });

  it('restores an old drag before a no-move replacement finishes', () => {
    const { panel, result } = renderRuntimePanelLayout();
    const committedHeight = panel.style.getPropertyValue('--activity-panel-height');

    startResize(result, 'pointer');
    movePointer(300);
    expect(panel.style.getPropertyValue('--activity-panel-height')).not.toBe(committedHeight);

    startResize(result, 'pointer');
    act(() => {
      window.dispatchEvent(new Event('pointerup'));
    });

    expect(panel.style.getPropertyValue('--activity-panel-height')).toBe(committedHeight);
    expect(result.current.activityPanelHeight).toBe(Number.parseInt(committedHeight, 10));
  });

  it('uses the same idempotent disposer for mouse blur and a subsequent release', () => {
    const removeEventListener = vi.spyOn(window, 'removeEventListener');
    const { result } = renderHook(() => useRuntimePanelLayout());

    startResize(result, 'mouse');
    act(() => {
      window.dispatchEvent(new Event('blur'));
      window.dispatchEvent(new Event('mouseup'));
    });

    expect(removeEventListener.mock.calls.filter(([eventName]) => eventName === 'mousemove')).toHaveLength(1);
    expect(removeEventListener.mock.calls.filter(([eventName]) => eventName === 'mouseup')).toHaveLength(1);
    expect(removeEventListener.mock.calls.filter(([eventName]) => eventName === 'blur')).toHaveLength(1);
  });
});
