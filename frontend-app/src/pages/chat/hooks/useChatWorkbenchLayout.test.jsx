import { useRef } from 'react';
import { act, fireEvent, render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  THREAD_RAIL_MIN_WIDTH,
  SPLITTER_WIDTH,
  chatLayoutWidthBudget,
  ratioWidth,
  resizerNextWidth,
  rightPanelDefaultWidth,
  rightPanelMaxWidth,
  threadRailTargetWidth,
  useRuntimeSidePanelLayout,
  useThreadRailLayout,
} from './useChatWorkbenchLayout.js';

const committedColumns = `minmax(0, 1fr) ${SPLITTER_WIDTH}px 240px`;

function firePointer(target, type, { buttons = 0, clientX = 0, pointerId = 1 } = {}) {
  const event = new MouseEvent(type, { bubbles: true, buttons, clientX });
  Object.defineProperty(event, 'pointerId', { configurable: true, value: pointerId });
  fireEvent(target, event);
}

function DragHarness({ rightPanelWidth = 240, setOpen = vi.fn(), setRightPanelWidth = vi.fn(), viewportWidth = 1200 }) {
  const layoutRef = useRef(null);
  const rail = useThreadRailLayout({ layoutRef, rightPanelOpen: true, rightPanelWidth, viewportWidth });
  const panel = useRuntimeSidePanelLayout({
    activeThreadId: null,
    layoutRef,
    open: true,
    railWidth: rail.width,
    rightPanelWidth,
    setOpen,
    setRightPanelWidth,
    store: {},
    viewportWidth,
  });
  return (
    <div ref={layoutRef} data-testid="layout" style={{ gridTemplateColumns: committedColumns }}>
      <output data-testid="rail-width">{rail.width}</output>
      <button data-testid="rail-resizer" onPointerDown={rail.beginResize}>rail</button>
      <button data-testid="panel-resizer" onPointerDown={panel.beginResize}>panel</button>
    </div>
  );
}

describe('useChatWorkbenchLayout geometry', () => {
  it('computes rail and right panel widths from the workbench viewport budget', () => {
    expect(chatLayoutWidthBudget(1200)).toBe(1124);
    expect(ratioWidth(0.2, 1200)).toBe(224);
    expect(threadRailTargetWidth(1200)).toBe(THREAD_RAIL_MIN_WIDTH);
    expect(rightPanelDefaultWidth(1200)).toBe(224);
    expect(rightPanelMaxWidth(1200, THREAD_RAIL_MIN_WIDTH)).toBe(423);
  });

  it('keeps rail min width on narrow viewports', () => {
    expect(chatLayoutWidthBudget(800)).toBe(724);
    expect(threadRailTargetWidth(800)).toBe(THREAD_RAIL_MIN_WIDTH);
    expect(rightPanelDefaultWidth(800)).toBe(144);
    expect(rightPanelMaxWidth(800, THREAD_RAIL_MIN_WIDTH)).toBe(183);
  });

  it('maps keyboard resizer movement by rail and right-panel direction', () => {
    expect(resizerNextWidth({ key: 'ArrowLeft' }, 240, 400, 100, 'rail')).toBe(224);
    expect(resizerNextWidth({ key: 'ArrowRight' }, 240, 400, 100, 'rail')).toBe(256);
    expect(resizerNextWidth({ key: 'ArrowLeft' }, 240, 400, 0, 'right')).toBe(256);
    expect(resizerNextWidth({ key: 'ArrowRight' }, 240, 400, 0, 'right')).toBe(224);
    expect(resizerNextWidth({ key: 'Home' }, 240, 400, 100, 'rail')).toBe(100);
    expect(resizerNextWidth({ key: 'End' }, 240, 400, 100, 'rail')).toBe(400);
    expect(resizerNextWidth({ key: 'ArrowLeft', ctrlKey: true }, 240, 400, 100, 'rail')).toBeNull();
    expect(resizerNextWidth({ key: 'Escape' }, 240, 400, 100, 'rail')).toBeNull();
  });

  it('cancels a prior rail drag without committing, then commits the current drag once', () => {
    const viewportWidth = 1600;
    const railStartWidth = threadRailTargetWidth(viewportWidth);
    const view = render(<DragHarness viewportWidth={viewportWidth} />);
    const resizer = view.getByTestId('rail-resizer');
    act(() => {
      firePointer(resizer, 'pointerdown', { buttons: 1, clientX: 200, pointerId: 1 });
      firePointer(window, 'pointermove', { buttons: 1, clientX: 160, pointerId: 1 });
      firePointer(resizer, 'pointerdown', { buttons: 1, clientX: 200, pointerId: 2 });
    });
    expect(view.getByTestId('rail-width')).toHaveTextContent(String(railStartWidth));
    expect(view.getByTestId('layout').style.gridTemplateColumns).toBe(committedColumns);
    act(() => {
      firePointer(window, 'pointermove', { buttons: 1, clientX: 180, pointerId: 2 });
      firePointer(window, 'pointerup', { pointerId: 2 });
      firePointer(window, 'pointercancel', { pointerId: 2 });
      fireEvent.blur(window);
    });
    expect(view.getByTestId('rail-width')).toHaveTextContent(String(railStartWidth - 20));
  });

  it('restores rail geometry and committed width after pointercancel or blur', () => {
    const viewportWidth = 1600;
    const railStartWidth = threadRailTargetWidth(viewportWidth);
    for (const terminalEvent of ['pointercancel', 'blur']) {
      const view = render(<DragHarness viewportWidth={viewportWidth} />);
      const resizer = view.getByTestId('rail-resizer');
      act(() => {
        firePointer(resizer, 'pointerdown', { buttons: 1, clientX: 200, pointerId: 1 });
        firePointer(window, 'pointermove', { buttons: 1, clientX: 160, pointerId: 1 });
        if (terminalEvent === 'pointercancel') firePointer(window, 'pointercancel', { pointerId: 1 });
        else fireEvent.blur(window);
        firePointer(window, 'pointerup', { pointerId: 1 });
      });
      expect(view.getByTestId('layout').style.gridTemplateColumns).toBe(committedColumns);
      expect(view.getByTestId('rail-width')).toHaveTextContent(String(railStartWidth));
      view.unmount();
    }
  });

  it('commits a completed panel drag only once across repeated terminal events', () => {
    const setRightPanelWidth = vi.fn();
    const view = render(<DragHarness setRightPanelWidth={setRightPanelWidth} />);
    act(() => {
      firePointer(view.getByTestId('panel-resizer'), 'pointerdown', { buttons: 1, clientX: 500, pointerId: 1 });
      firePointer(window, 'pointermove', { buttons: 1, clientX: 550, pointerId: 1 });
      firePointer(window, 'pointerup', { pointerId: 1 });
      firePointer(window, 'pointercancel', { pointerId: 1 });
      fireEvent.blur(window);
    });
    expect(setRightPanelWidth).toHaveBeenCalledTimes(1);
    expect(setRightPanelWidth).toHaveBeenCalledWith(190);
  });

  it('restores panel geometry without a state commit after pointercancel or blur', () => {
    for (const terminalEvent of ['pointercancel', 'blur']) {
      const setOpen = vi.fn();
      const setRightPanelWidth = vi.fn();
      const view = render(<DragHarness setOpen={setOpen} setRightPanelWidth={setRightPanelWidth} />);
      act(() => {
        firePointer(view.getByTestId('panel-resizer'), 'pointerdown', { buttons: 1, clientX: 500, pointerId: 1 });
        firePointer(window, 'pointermove', { buttons: 1, clientX: 550, pointerId: 1 });
      });
      expect(view.getByTestId('layout').style.gridTemplateColumns).toBe(`minmax(0, 1fr) ${SPLITTER_WIDTH}px 190px`);
      act(() => {
        if (terminalEvent === 'pointercancel') firePointer(window, 'pointercancel', { pointerId: 1 });
        else fireEvent.blur(window);
        firePointer(window, 'pointerup', { pointerId: 1 });
      });
      expect(view.getByTestId('layout').style.gridTemplateColumns).toBe(committedColumns);
      expect(setRightPanelWidth).not.toHaveBeenCalled();
      expect(setOpen).not.toHaveBeenCalled();
      view.unmount();
    }
  });

  it('restores panel geometry when a moved drag is replaced by a drag with no movement', () => {
    const setRightPanelWidth = vi.fn();
    const view = render(<DragHarness setRightPanelWidth={setRightPanelWidth} />);
    const resizer = view.getByTestId('panel-resizer');
    act(() => {
      firePointer(resizer, 'pointerdown', { buttons: 1, clientX: 500, pointerId: 1 });
      firePointer(window, 'pointermove', { buttons: 1, clientX: 550, pointerId: 1 });
    });
    expect(view.getByTestId('layout').style.gridTemplateColumns).toBe(`minmax(0, 1fr) ${SPLITTER_WIDTH}px 190px`);
    act(() => {
      firePointer(resizer, 'pointerdown', { buttons: 1, clientX: 500, pointerId: 2 });
      firePointer(window, 'pointerup', { pointerId: 2 });
    });
    expect(view.getByTestId('layout').style.gridTemplateColumns).toBe(committedColumns);
    expect(setRightPanelWidth).toHaveBeenCalledTimes(1);
    expect(setRightPanelWidth).toHaveBeenCalledWith(240);
  });

  it('removes panel drag listeners on unmount without committing a width or open state', () => {
    const setOpen = vi.fn();
    const setRightPanelWidth = vi.fn();
    const view = render(<DragHarness setOpen={setOpen} setRightPanelWidth={setRightPanelWidth} />);
    act(() => {
      firePointer(view.getByTestId('panel-resizer'), 'pointerdown', { buttons: 1, clientX: 500, pointerId: 1 });
      firePointer(window, 'pointermove', { buttons: 1, clientX: 550, pointerId: 1 });
    });
    const layout = view.getByTestId('layout');
    view.unmount();
    expect(layout.style.gridTemplateColumns).toBe(committedColumns);
    act(() => {
      firePointer(window, 'pointerup', { pointerId: 1 });
      firePointer(window, 'pointercancel', { pointerId: 1 });
      fireEvent.blur(window);
    });
    expect(setRightPanelWidth).not.toHaveBeenCalled();
    expect(setOpen).not.toHaveBeenCalled();
  });
});
