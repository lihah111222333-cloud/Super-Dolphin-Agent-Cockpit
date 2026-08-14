import { describe, expect, it } from 'vitest';
import { solveWorkbenchGeometry } from './workbenchGeometry.js';

describe('solveWorkbenchGeometry', () => {
  it.each([
    { width: 500, height: 900 },
    { width: 920, height: 900 },
    { width: 921, height: 900 },
    { width: 340, height: 900 },
    { width: 341, height: 900 },
  ])('returns one immutable snapshot at $width×$height', ({ width, height }) => {
    const geometry = solveWorkbenchGeometry({
      activityHeight: 96,
      rightOpen: true,
      rightPreference: 150,
      railOpen: true,
      railWidth: 340,
      viewportHeight: height,
      viewportWidth: width,
    });

    expect(Object.isFrozen(geometry)).toBe(true);
    expect(Object.isFrozen(geometry.right)).toBe(true);
    expect(Object.isFrozen(geometry.activity)).toBe(true);
    expect(geometry.viewport).toEqual({ height, width });
    const rightColumns = geometry.right.displayed > 0
      ? `${geometry.splitterWidth}px ${geometry.right.displayed}px`
      : '';
    expect(geometry.gridTemplateColumns).toBe(
      `minmax(0, 1fr)${rightColumns ? ` ${rightColumns}` : ''}`,
    );
    expect(geometry.aria.rightNow).toBe(geometry.right.displayed);
    expect(geometry.aria.activityNow).toBe(geometry.activity.displayed);
    expect(geometry.cssVars['--activity-panel-height']).toBe(`${geometry.activity.displayed}px`);
    expect(geometry.composer.rightOffset).toBe(geometry.right.displayed > 0
      ? geometry.right.displayed + geometry.splitterWidth
      : 0);
    expect(geometry.activity.displayed).toBe(112);
  });

  it('clamps display without replacing a durable non-zero preference', () => {
    const narrow = solveWorkbenchGeometry({
      activityHeight: 64,
      rightOpen: true,
      rightPreference: 480.5,
      railOpen: true,
      railWidth: 340,
      viewportHeight: 640,
      viewportWidth: 1024,
    });
    const wide = solveWorkbenchGeometry({
      activityHeight: 64,
      rightOpen: true,
      rightPreference: 480.5,
      railOpen: true,
      railWidth: 340,
      viewportHeight: 640,
      viewportWidth: 1980,
    });

    expect(narrow.right.displayed).toBeLessThan(480.5);
    expect(narrow.right.preference).toBe(480.5);
    expect(wide.right.displayed).toBe(480.5);
    expect(wide.right.preference).toBe(480.5);
  });

  it('uses zero only as the closed sentinel and restores a non-zero default on open', () => {
    const closed = solveWorkbenchGeometry({
      activityHeight: 64,
      rightOpen: false,
      rightPreference: 0,
      railOpen: true,
      railWidth: 340,
      viewportHeight: 640,
      viewportWidth: 1400,
    });
    const opened = solveWorkbenchGeometry({
      activityHeight: 64,
      rightOpen: true,
      rightPreference: 0,
      railOpen: true,
      railWidth: 340,
      viewportHeight: 640,
      viewportWidth: 1400,
    });

    expect(closed.right.displayed).toBe(0);
    expect(opened.right.displayed).toBeGreaterThan(0);
    expect(opened.right.preference).toBe(0);
  });

  it('keeps the durable rail width available while the rail is closed', () => {
    const closed = solveWorkbenchGeometry({
      activityHeight: 64,
      rightOpen: false,
      rightPreference: 0,
      railOpen: false,
      railWidth: 320,
      viewportHeight: 900,
      viewportWidth: 1461,
    });

    expect(closed.rail.displayed).toBe(0);
    expect(closed.cssVars['--workbench-sidebar-width']).toBe('320px');
  });

  it('uses the full main canvas after removing the secondary thread rail', () => {
    const geometry = solveWorkbenchGeometry({
      activityHeight: 64,
      railOpen: true,
      railWidth: 340,
      rightOpen: true,
      rightPreference: 999,
      viewportHeight: 900,
      viewportWidth: 1440,
    });

    expect(geometry).not.toHaveProperty('threadRail');
    expect(geometry.right.max).toBe(440);
    expect(geometry.conversation.min).toBe(440);
    expect(geometry.conversation.width).toBe(654);
    expect(geometry.gridTemplateColumns).toBe('minmax(0, 1fr) 6px 440px');
  });
});
