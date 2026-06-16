import { describe, expect, it } from 'vitest';
import {
  THREAD_RAIL_MIN_WIDTH,
  chatLayoutWidthBudget,
  ratioWidth,
  resizerNextWidth,
  rightPanelDefaultWidth,
  rightPanelMaxWidth,
  threadRailTargetWidth,
} from './useChatWorkbenchLayout.js';

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
});
